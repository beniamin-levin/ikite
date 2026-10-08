package store

import (
	"database/sql"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func (s *Store) ReplaceWindForecast(windguruID int, forecastDate time.Time, fetchedAt time.Time, rows []models.WindForecastRow) error {
	return s.ReplaceWindForecastModelDays(windguruID, 0, forecastDate, fetchedAt, map[string][]models.WindForecastRow{
		forecastDate.Format("2006-01-02"): rows,
	})
}

// ReplaceAllWindForecast stores a fresh Windguru multi-day fetch. Rows for
// forecast_date >= cutoffDate replace the current outlook for those days; older
// forecast_date rows are left untouched for historical verification.
func (s *Store) ReplaceAllWindForecast(windguruID int, cutoffDate time.Time, fetchedAt time.Time, rows []models.WindForecastRow) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	cutoff := cutoffDate.Format("2006-01-02")
	if _, err := tx.Exec(`
		DELETE FROM wind_forecast
		WHERE windguru_id = ? AND id_model < 1000000 AND forecast_date >= ?`,
		windguruID, cutoff); err != nil {
		return err
	}

	for _, r := range rows {
		date := forecastRowDate(r)
		if date < cutoff {
			continue
		}
		_, err := tx.Exec(`
			INSERT INTO wind_forecast
				(forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			date, r.Location, windguruID, r.IDModel, r.Model, r.Period,
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
	return s.ReplaceWindForecastModelDays(windguruID, idModel, forecastDate, fetchedAt, map[string][]models.WindForecastRow{
		forecastDate.Format("2006-01-02"): rows,
	})
}

// ReplaceWindForecastModelFrom replaces one model's rows from a moment onward,
// keeping everything before it. For sources issued several times a day whose
// later issues cover only the rest of the day (the IMS sea forecast).
func (s *Store) ReplaceWindForecastModelFrom(windguruID, idModel int, from, fetchedAt time.Time, rows []models.WindForecastRow) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM wind_forecast WHERE windguru_id = ? AND id_model = ? AND period >= ?`,
		windguruID, idModel, from); err != nil {
		return err
	}
	for _, r := range rows {
		if r.Period.Before(from) {
			continue
		}
		date := forecastRowDate(r)
		if _, err := tx.Exec(`
			INSERT INTO wind_forecast
				(forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			date, r.Location, windguruID, idModel, r.Model, r.Period, r.Wind, r.Gust, r.WindDir, r.Temp, fetchedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceWindForecastModelDays stores one model's rows for one or more calendar
// days. Only days with forecast_date >= cutoffDate are replaced; older days are
// preserved for historical verification.
func (s *Store) ReplaceWindForecastModelDays(windguruID int, idModel int, cutoffDate time.Time, fetchedAt time.Time, byDate map[string][]models.WindForecastRow) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	cutoff := cutoffDate.Format("2006-01-02")
	for date, rows := range byDate {
		if date < cutoff {
			continue
		}
		deleteQuery := `
			DELETE FROM wind_forecast
			WHERE windguru_id = ? AND forecast_date = ?`
		deleteArgs := []any{windguruID, date}
		if idModel > 0 {
			deleteQuery += ` AND id_model = ?`
			deleteArgs = append(deleteArgs, idModel)
		} else {
			deleteQuery += ` AND id_model < 1000000`
		}
		if _, err := tx.Exec(deleteQuery, deleteArgs...); err != nil {
			return err
		}
		for _, r := range rows {
			rowModel := idModel
			if rowModel == 0 {
				rowModel = r.IDModel
			}
			_, err := tx.Exec(`
				INSERT INTO wind_forecast
					(forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp, fetched_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				date, r.Location, windguruID, rowModel, r.Model, r.Period,
				r.Wind, r.Gust, r.WindDir, r.Temp, fetchedAt)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func forecastRowDate(r models.WindForecastRow) string {
	if !r.ForecastDate.IsZero() {
		return r.ForecastDate.Format("2006-01-02")
	}
	p := r.Period
	day := time.Date(p.Year(), p.Month(), p.Day(), 0, 0, 0, 0, p.Location())
	return day.Format("2006-01-02")
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

// ListWindForecastByLocationFrom returns all forecast rows for a spot from a date onward.
func (s *Store) ListWindForecastByLocationFrom(location string, fromDate time.Time) ([]models.WindForecastRow, error) {
	return s.ListWindForecastByLocationRange(location, fromDate, time.Time{})
}

// ListWindForecastByLocationRange returns forecast rows for a spot between fromDate and toDate (inclusive).
// If toDate is zero, returns all rows from fromDate onward.
func (s *Store) ListWindForecastByLocationRange(location string, fromDate, toDate time.Time) ([]models.WindForecastRow, error) {
	from := fromDate.Format("2006-01-02")
	query := `
		SELECT forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp
		FROM wind_forecast
		WHERE location = ? AND forecast_date >= ?`
	args := []any{location, from}
	if !toDate.IsZero() {
		query += ` AND forecast_date <= ?`
		args = append(args, toDate.Format("2006-01-02"))
	}
	query += ` ORDER BY forecast_date, id_model, period`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWindForecastRows(rows)
}

// MaxWindForecastDate returns the latest forecast_date for a location, or zero if none.
func (s *Store) MaxWindForecastDate(location string) (time.Time, error) {
	var raw sql.NullString
	err := s.DB.QueryRow(`
		SELECT MAX(forecast_date) FROM wind_forecast WHERE location = ?`, location).Scan(&raw)
	if err != nil {
		return time.Time{}, err
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation("2006-01-02", raw.String[:10], time.UTC)
}

// ListHighGustForecasts returns rows with gust >= minGust for given locations and date range.
func (s *Store) ListHighGustForecasts(locations []string, fromDate, toDate time.Time, minGust float64) ([]models.WindForecastRow, error) {
	if len(locations) == 0 {
		return nil, nil
	}
	from := fromDate.Format("2006-01-02")
	to := toDate.Format("2006-01-02")
	query := `
		SELECT forecast_date, location, windguru_id, id_model, model, period, wind, gust, wind_dir, temp
		FROM wind_forecast
		WHERE forecast_date >= ? AND forecast_date <= ? AND gust >= ? AND location IN (`
	args := []any{from, to, minGust}
	for i, loc := range locations {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, loc)
	}
	query += `) ORDER BY location, forecast_date, period, model`

	rows, err := s.DB.Query(query, args...)
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
