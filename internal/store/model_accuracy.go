package store

import (
	"fmt"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// HourlyObserved folds a meter's readings into centred hourly buckets for
// [from, to), restricted to buckets fromHour..toHour. Buckets with fewer than
// three readings are dropped: a single sample says little about a whole hour.
func (s *Store) HourlyObserved(location string, from, to time.Time, fromHour, toHour int) ([]models.ObservedHour, error) {
	// A bucket for hour H spans H-1:30 .. H:29, so readings from the hour before
	// fromHour are needed to fill the first bucket.
	rows, err := s.DB.Query(`
		SELECT DATE_FORMAT(period + INTERVAL 30 MINUTE, '%Y-%m-%d %H:00:00') AS hr,
		       AVG(wind), MAX(gust), COUNT(*)
		FROM wind_data
		WHERE location = ? AND period >= ? AND period < ?
		  AND HOUR(period) BETWEEN ? AND ?
		GROUP BY hr
		HAVING COUNT(*) >= 3
		ORDER BY hr`,
		location, from.Format("2006-01-02 15:04:05"), to.Format("2006-01-02 15:04:05"),
		fromHour-1, toHour)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.ObservedHour
	for rows.Next() {
		var hr string
		var o models.ObservedHour
		if err := rows.Scan(&hr, &o.Wind, &o.GustPeak, &o.Samples); err != nil {
			return nil, err
		}
		t, err := time.Parse("2006-01-02 15:04:05", hr)
		if err != nil {
			return nil, fmt.Errorf("observed hour %q: %w", hr, err)
		}
		if t.Hour() < fromHour || t.Hour() > toHour {
			continue
		}
		o.Hour = t
		out = append(out, o)
	}
	return out, rows.Err()
}
