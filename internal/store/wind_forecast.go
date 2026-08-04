package store

import (
	"database/sql"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func (s *Store) ReplaceWindForecast(windguruID int, forecastDate time.Time, fetchedAt time.Time, rows []models.WindForecastRow) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		DELETE FROM wind_forecast
		WHERE windguru_id = ? AND forecast_date = ? AND id_model < 1000000`,
		windguruID, forecastDate.Format("2006-01-02")); err != nil {
		return err
	}

	for _, r := range rows {
		_, err := tx.Exec(`
			INSERT INTO wind_forecast
				(forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			forecastDate.Format("2006-01-02"), r.Location, windguruID, r.IDModel, r.Model, r.Period,
			r.Wind, r.Gust, r.WindDir, r.Temp, fetchedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) WindForecastAlreadyFetched(windguruID int, forecastDate time.Time) (bool, error) {
	var n int
	err := s.DB.QueryRow(`
		SELECT COUNT(*) FROM wind_forecast
		WHERE windguru_id = ? AND forecast_date = ? AND id_model < 1000000`,
		windguruID, forecastDate.Format("2006-01-02")).Scan(&n)
	return n > 0, err
}

func (s *Store) ReplaceWindForecastModel(windguruID int, forecastDate time.Time, idModel int, fetchedAt time.Time, rows []models.WindForecastRow) error {
	return s.ReplaceWindForecastModelDays(windguruID, idModel, fetchedAt, map[string][]models.WindForecastRow{
		forecastDate.Format("2006-01-02"): rows,
	})
}

// ReplaceWindForecastModelDays replaces openWRF (or any model) rows for one or
// more calendar days. Tomorrow's run overrides overlapping day values because
// each period is stored under its own forecast_date.
func (s *Store) ReplaceWindForecastModelDays(windguruID int, idModel int, fetchedAt time.Time, byDate map[string][]models.WindForecastRow) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for date, rows := range byDate {
		if _, err := tx.Exec(`
			DELETE FROM wind_forecast
			WHERE windguru_id = ? AND forecast_date = ? AND id_model = ?`,
			windguruID, date, idModel); err != nil {
			return err
		}
		for _, r := range rows {
			_, err := tx.Exec(`
				INSERT INTO wind_forecast
					(forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp, fetched_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				date, r.Location, windguruID, idModel, r.Model, r.Period,
				r.Wind, r.Gust, r.WindDir, r.Temp, fetchedAt)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) WindForecastModelAlreadyFetched(windguruID int, forecastDate time.Time, idModel int) (bool, error) {
	var n int
	err := s.DB.QueryRow(`
		SELECT COUNT(*) FROM wind_forecast
		WHERE windguru_id = ? AND forecast_date = ? AND id_model = ?`,
		windguruID, forecastDate.Format("2006-01-02"), idModel).Scan(&n)
	return n > 0, err
}

// ListWindForecastByLocation returns all models for a spot location on a day.
func (s *Store) ListWindForecastByLocation(location string, forecastDate time.Time) ([]models.WindForecastRow, error) {
	date := forecastDate.Format("2006-01-02")
	rows, err := s.DB.Query(`
		SELECT forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp
		FROM wind_forecast
		WHERE location = ? AND forecast_date = ?
		ORDER BY model, id_model, period`, location, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWindForecastRows(rows)
}

// ListWindForecastLocations returns distinct locations that have forecast rows for a day.
func (s *Store) ListWindForecastLocations(forecastDate time.Time) ([]string, error) {
	rows, err := s.DB.Query(`
		SELECT DISTINCT location FROM wind_forecast
		WHERE forecast_date = ?
		ORDER BY location`, forecastDate.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var loc string
		if err := rows.Scan(&loc); err != nil {
			return nil, err
		}
		out = append(out, loc)
	}
	return out, rows.Err()
}

// LatestWindForecastDate returns the most recent forecast_date stored for a spot.
func (s *Store) LatestWindForecastDate(windguruID int) (*time.Time, error) {
	var d sql.NullTime
	err := s.DB.QueryRow(`
		SELECT MAX(forecast_date) FROM wind_forecast WHERE windguru_id = ?`,
		windguruID).Scan(&d)
	if err != nil {
		return nil, err
	}
	if !d.Valid {
		return nil, nil
	}
	t := d.Time
	return &t, nil
}

// ListWindForecast returns hourly forecast rows for a Windguru spot.
// Pass idModel=0 for all models on that day.
func (s *Store) ListWindForecast(windguruID int, forecastDate time.Time, idModel int) ([]models.WindForecastRow, error) {
	date := forecastDate.Format("2006-01-02")
	query := `
		SELECT forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp
		FROM wind_forecast
		WHERE windguru_id = ? AND forecast_date = ?`
	args := []any{windguruID, date}
	if idModel > 0 {
		query += ` AND id_model = ?`
		args = append(args, idModel)
	}
	query += ` ORDER BY id_model, period`

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWindForecastRows(rows)
}

func scanWindForecastRows(rows *sql.Rows) ([]models.WindForecastRow, error) {
	var out []models.WindForecastRow
	for rows.Next() {
		var r models.WindForecastRow
		var wind, gust, dir, temp sql.NullFloat64
		if err := rows.Scan(&r.ForecastDate, &r.Location, &r.WindguruID, &r.IDModel, &r.Model, &r.Period,
			&wind, &gust, &dir, &temp); err != nil {
			return nil, err
		}
		if wind.Valid {
			v := wind.Float64
			r.Wind = &v
		}
		if gust.Valid {
			v := gust.Float64
			r.Gust = &v
		}
		if dir.Valid {
			v := dir.Float64
			r.WindDir = &v
		}
		if temp.Valid {
			v := temp.Float64
			r.Temp = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
