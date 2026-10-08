package store

import (
	"database/sql"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// ReplaceWindEstimate swaps a spot's estimate rows in [from, to) for rows. A zero
// `to` means open-ended. The daily job replaces today-and-forward; the backfill
// replaces one day at a time.
func (s *Store) ReplaceWindEstimate(location string, from, to time.Time, rows []models.WindEstimate) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	fromS := from.Format("2006-01-02 15:04:05")
	if to.IsZero() {
		_, err = tx.Exec(`DELETE FROM wind_estimate WHERE location = ? AND period >= ?`, location, fromS)
	} else {
		_, err = tx.Exec(`DELETE FROM wind_estimate WHERE location = ? AND period >= ? AND period < ?`,
			location, fromS, to.Format("2006-01-02 15:04:05"))
	}
	if err != nil {
		return err
	}
	for _, r := range rows {
		var dir any
		if r.Dir >= 0 {
			dir = int(r.Dir + 0.5)
		}
		if _, err := tx.Exec(`
			INSERT INTO wind_estimate
				(location, period, wind, gust, wind_low, wind_high, wind_dir, models, issued_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			location, r.Period.Format("2006-01-02 15:04:05"), r.Wind, r.Gust, r.Low, r.High, dir,
			r.Models, r.IssuedAt.Format("2006-01-02 15:04:05")); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListWindEstimate returns a spot's estimate rows with period in [from, to).
func (s *Store) ListWindEstimate(location string, from, to time.Time) ([]models.WindEstimate, error) {
	rows, err := s.DB.Query(`
		SELECT DATE_FORMAT(period, '%Y-%m-%d %H:%i:%s'), wind, gust, wind_low, wind_high, wind_dir, models,
		       DATE_FORMAT(issued_at, '%Y-%m-%d %H:%i:%s')
		FROM wind_estimate
		WHERE location = ? AND period >= ? AND period < ?
		ORDER BY period`,
		location, from.Format("2006-01-02 15:04:05"), to.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.WindEstimate
	for rows.Next() {
		var p, issued string
		var dir sql.NullInt64
		e := models.WindEstimate{Location: location}
		if err := rows.Scan(&p, &e.Wind, &e.Gust, &e.Low, &e.High, &dir, &e.Models, &issued); err != nil {
			return nil, err
		}
		// Wall-clock digits as stored, in UTC — compared by Format, like ObservedHour.
		if e.Period, err = time.Parse("2006-01-02 15:04:05", p); err != nil {
			return nil, err
		}
		if e.IssuedAt, err = time.Parse("2006-01-02 15:04:05", issued); err != nil {
			return nil, err
		}
		e.Dir = -1
		if dir.Valid {
			e.Dir = float64(dir.Int64)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
