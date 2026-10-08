package store

import (
	"time"

	"github.com/ben/ikite-go/internal/sources/imssea"
)

// SaveIMSSeaForecast stores every block of an IMS sea forecast issue. Saving
// the same issue again refreshes it.
func (s *Store) SaveIMSSeaForecast(f *imssea.Forecast, fetchedAt time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range f.Regions {
		for _, b := range r.Blocks {
			var dirFrom, dirTo, state any
			if b.DirFrom >= 0 {
				dirFrom, dirTo = int(b.DirFrom), int(b.DirTo)
			}
			if b.SeaState >= 0 {
				state = b.SeaState
			}
			if _, err := tx.Exec(`
				INSERT INTO ims_sea_forecast (issued_at, region_id, region_name, valid_from, valid_to, dir_from, dir_to,
					wind_min_kmh, wind_max_kmh, sea_state, wave_min_cm, wave_max_cm, sea_temp_c, fetched_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON DUPLICATE KEY UPDATE valid_to = VALUES(valid_to), dir_from = VALUES(dir_from), dir_to = VALUES(dir_to),
					wind_min_kmh = VALUES(wind_min_kmh), wind_max_kmh = VALUES(wind_max_kmh), sea_state = VALUES(sea_state),
					wave_min_cm = VALUES(wave_min_cm), wave_max_cm = VALUES(wave_max_cm), sea_temp_c = VALUES(sea_temp_c),
					fetched_at = VALUES(fetched_at)`,
				f.Issued, r.ID, r.Name, b.From, b.To, dirFrom, dirTo, b.WindMinKmh, b.WindMaxKmh, state,
				b.WaveMinCm, b.WaveMaxCm, b.SeaTempC, fetchedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
