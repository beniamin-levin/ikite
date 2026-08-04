package store

import (
	"database/sql"
	"strings"
)

// SpotRatingAggregate is a Windguru archive score summed across months.
type SpotRatingAggregate struct {
	SpotID      int
	SpotName    string
	CountryName string
	Score       int
}

// SpotMapPoint is a rated spot with coordinates for the map.
type SpotMapPoint struct {
	SpotID   int     `json:"spot_id"`
	SpotName string  `json:"spot_name"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Score    int     `json:"score"`
	Color    string  `json:"color"`
}

// SpotMonthRating is one month of Windguru archive stats for a spot.
type SpotMonthRating struct {
	Month     int
	Score     int
	Bft8Plus  int
	Bft7      int
	Bft6      int
	Bft5      int
	Bft4      int
	Bft3      int
	Bft2Minus int
}

// ListSpotRatings returns spots ordered by total archive score.
func (s *Store) ListSpotRatings() ([]SpotRatingAggregate, error) {
	rows, err := s.DB.Query(`
		SELECT spot_id, spot_name, country_name, SUM(score) AS score
		FROM wind_wg_raiting
		GROUP BY spot_id, spot_name, country_name
		ORDER BY score DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SpotRatingAggregate
	for rows.Next() {
		var r SpotRatingAggregate
		if err := rows.Scan(&r.SpotID, &r.SpotName, &r.CountryName, &r.Score); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListSpotRatingCountries returns distinct country names for the map filter.
// The source data mixes some spot names into the country column; keep only
// rows that look like real countries (no digits / device brand hints).
func (s *Store) ListSpotRatingCountries() ([]string, error) {
	rows, err := s.DB.Query(`
		SELECT DISTINCT country_name
		FROM wind_wg_raiting
		WHERE country_name <> ''
			AND country_name NOT REGEXP '[0-9]'
			AND country_name NOT LIKE '%,%'
		ORDER BY country_name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		if looksLikeSpotName(c) {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// looksLikeSpotName reports whether a country_name value is actually a spot
// (e.g. "Kiryat Yam", "Décollage …", "Windbird 1316").
func looksLikeSpotName(s string) bool {
	lower := strings.ToLower(s)
	for _, kw := range []string{
		"windbird", "pioupiou", "openwindmap", "arduino", "balise", "station",
		"décollo", "decollo", "déco", "parapente", "plage", "beach", "strand",
		"kite", "surf", "windsurf", "sup klub", "ulm", "aéro", "aero",
		"noname", "test", "spot", "club", "marina",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ListSpotMapPoints returns rated spots with lat/lon for the map.
func (s *Store) ListSpotMapPoints(country string, limit int) ([]SpotMapPoint, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}

	q := `
		SELECT w.spot_id, w.spot_name, sd.lat, sd.lon,
			ROUND(SUM(w.` + "`4 Bft`" + `) * 0.3 + SUM(w.` + "`5 Bft`" + `) + SUM(w.` + "`6 Bft`" + `) * 2
				+ SUM(w.` + "`7 Bft`" + `) * 3 + SUM(w.` + "`8+ Bft`" + `) * 4) AS score
		FROM wind_wg_raiting w
		INNER JOIN spots_details sd ON w.spot_id = sd.spot_id
		WHERE (sd.lat <> 0 OR sd.lon <> 0)`
	args := []any{}
	if country != "" {
		q += ` AND w.country_name = ?`
		args = append(args, country)
	}
	q += `
		GROUP BY w.spot_id, w.spot_name, sd.lat, sd.lon
		ORDER BY score DESC
		LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SpotMapPoint
	for rows.Next() {
		var p SpotMapPoint
		var score sql.NullFloat64
		if err := rows.Scan(&p.SpotID, &p.SpotName, &p.Lat, &p.Lon, &score); err != nil {
			return nil, err
		}
		if score.Valid {
			p.Score = int(score.Float64)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	n := len(out)
	colors := []string{"#e10eff", "#ff14a2", "#ff4927", "#f1f100", "#00ff00"}
	for i := range out {
		bucket := 4
		if n > 0 {
			for j := 0; j < 5; j++ {
				if i+1 > (n/5)*j {
					bucket = j
				}
			}
		}
		out[i].Color = colors[bucket]
	}
	return out, nil
}

// SpotMonthRatings returns per-month archive bars for one Windguru spot.
func (s *Store) SpotMonthRatings(spotID int) ([]SpotMonthRating, string, error) {
	rows, err := s.DB.Query(`
		SELECT month, score, `+"`8+ Bft`"+`, `+"`7 Bft`"+`, `+"`6 Bft`"+`, `+"`5 Bft`"+`,
			`+"`4 Bft`"+`, `+"`3 Bft`"+`, `+"`2- Bft`"+`, spot_name
		FROM wind_wg_raiting
		WHERE spot_id = ?
		ORDER BY month ASC`, spotID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []SpotMonthRating
	var name string
	for rows.Next() {
		var r SpotMonthRating
		var spotName string
		if err := rows.Scan(&r.Month, &r.Score, &r.Bft8Plus, &r.Bft7, &r.Bft6, &r.Bft5, &r.Bft4, &r.Bft3, &r.Bft2Minus, &spotName); err != nil {
			return nil, "", err
		}
		if name == "" {
			name = spotName
		}
		out = append(out, r)
	}
	return out, name, rows.Err()
}
