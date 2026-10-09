package store

import (
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// ListForecastArchive returns a location's archive hours in [from, to) as
// forecast rows, so they can stand in for forecasts ikite did not save.
func (s *Store) ListForecastArchive(location string, from, to time.Time) ([]models.WindForecastRow, error) {
	rows, err := s.DB.Query(`
		SELECT model, period, wind, gust, wind_dir FROM wind_forecast_archive
		WHERE location = ? AND period >= ? AND period < ?`, location, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.WindForecastRow
	for rows.Next() {
		var (
			model  string
			period time.Time
			wind   float64
			gust   *float64
			dir    *float64
		)
		if err := rows.Scan(&model, &period, &wind, &gust, &dir); err != nil {
			return nil, err
		}
		w := wind
		out = append(out, models.WindForecastRow{
			Location: location, Model: model, Period: period, Wind: &w, Gust: gust, WindDir: dir,
		})
	}
	return out, rows.Err()
}
