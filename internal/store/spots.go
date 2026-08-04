package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/ben/ikite-go/internal/models"
)

const spotSelectSQL = `
	SELECT id, name, windguru_station_id, windguru_id, sort_order, visible, collect,
	       collect_interval_min, collect_start_hour, collect_end_hour
	FROM spots`

func (s *Store) ListSpots() ([]models.Spot, error) {
	rows, err := s.DB.Query(spotSelectSQL + `
		ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Spot
	for rows.Next() {
		sp, err := scanSpot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Store) SpotNames() (map[string]string, error) {
	spots, err := s.ListSpots()
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(spots))
	for _, sp := range spots {
		names[sp.ID] = sp.Name
	}
	return names, nil
}

func (s *Store) SpotByID(id string) (*models.Spot, error) {
	row := s.DB.QueryRow(spotSelectSQL+`
		WHERE id = ?`, id)
	sp, err := scanSpot(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("unknown spot %q", id)
	}
	if err != nil {
		return nil, err
	}
	return &sp, nil
}

func (s *Store) SpotsWithWindguruForecast() ([]models.Spot, error) {
	rows, err := s.DB.Query(spotSelectSQL + `
		WHERE windguru_id IS NOT NULL
		ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Spot
	for rows.Next() {
		sp, err := scanSpot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Store) SpotByWindguruID(stationID int) (*models.Spot, error) {
	row := s.DB.QueryRow(spotSelectSQL+`
		WHERE windguru_station_id = ?`, stationID)
	sp, err := scanSpot(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("unknown windguru station %d", stationID)
	}
	if err != nil {
		return nil, err
	}
	return &sp, nil
}

func (s *Store) InsertWindguruSpot(name string, wgStationID int) (*models.Spot, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("spot name is required")
	}
	if len(name) > 100 {
		return nil, fmt.Errorf("spot name too long")
	}
	if wgStationID <= 0 {
		return nil, fmt.Errorf("invalid windguru station id")
	}

	id := strconv.Itoa(wgStationID)
	if len(id) > 20 {
		return nil, fmt.Errorf("windguru station id too long")
	}

	var exists int
	err := s.DB.QueryRow(`SELECT 1 FROM spots WHERE id = ? OR windguru_station_id = ? LIMIT 1`, id, wgStationID).Scan(&exists)
	if err == nil {
		return nil, fmt.Errorf("spot already exists for windguru station %d", wgStationID)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	var sortOrder int
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) + 10 FROM spots`).Scan(&sortOrder); err != nil {
		return nil, err
	}

	_, err = s.DB.Exec(`
		INSERT INTO spots (id, name, windguru_station_id, sort_order, visible, collect)
		VALUES (?, ?, ?, ?, 0, 1)`,
		id, name, wgStationID, sortOrder)
	if err != nil {
		return nil, err
	}

	row := s.DB.QueryRow(spotSelectSQL+`
		WHERE id = ?`, id)
	sp, err := scanSpot(row)
	if err != nil {
		return nil, err
	}
	return &sp, nil
}

func (s *Store) UpdateSpotSchedule(id string, interval, startHour, endHour int) error {
	interval = models.NormalizeCollectInterval(interval)
	startHour = models.NormalizeCollectHour(startHour, 8)
	endHour = models.NormalizeCollectHour(endHour, 22)
	if startHour > endHour {
		startHour, endHour = 8, 22
	}
	_, err := s.DB.Exec(`
		UPDATE spots
		SET collect_interval_min = ?, collect_start_hour = ?, collect_end_hour = ?
		WHERE id = ?`, interval, startHour, endHour, id)
	return err
}

func (s *Store) VisibleSpots() ([]string, error) {
	rows, err := s.DB.Query(`
		SELECT id FROM spots
		WHERE visible = 1
		ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CollectSpots returns spots with Save to DB enabled, in display order.
func (s *Store) CollectSpots() ([]models.Spot, error) {
	rows, err := s.DB.Query(spotSelectSQL + `
		WHERE collect = 1
		ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Spot
	for rows.Next() {
		sp, err := scanSpot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Store) VisibleSpotSet() (map[string]bool, error) {
	spots, err := s.ListSpots()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(spots))
	for _, sp := range spots {
		set[sp.ID] = sp.Visible
	}
	return set, nil
}

func (s *Store) SpotOrder() ([]string, error) {
	spots, err := s.ListSpots()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(spots))
	for _, sp := range spots {
		out = append(out, sp.ID)
	}
	return out, nil
}

func (s *Store) SetVisibleSpots(keys []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`UPDATE spots SET visible = 0`); err != nil {
		return err
	}
	for _, id := range keys {
		if _, err := tx.Exec(`UPDATE spots SET visible = 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetSpotOrder(keys []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	seen := map[string]bool{}
	order := 0
	for _, id := range keys {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		order += 10
		if _, err := tx.Exec(`UPDATE spots SET sort_order = ? WHERE id = ?`, order, id); err != nil {
			return err
		}
	}

	rows, err := tx.Query(`SELECT id FROM spots ORDER BY sort_order, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if seen[id] {
			continue
		}
		order += 10
		if _, err := tx.Exec(`UPDATE spots SET sort_order = ? WHERE id = ?`, order, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetCollectSpots(keys []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`UPDATE spots SET collect = 0`); err != nil {
		return err
	}
	for _, id := range keys {
		if _, err := tx.Exec(`UPDATE spots SET collect = 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SpotCollectEnabled(id string) (bool, error) {
	var collect int
	err := s.DB.QueryRow(`SELECT collect FROM spots WHERE id = ?`, id).Scan(&collect)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return collect != 0, nil
}

// ApplyLegacySpotSettings migrates spot_order / visible_spots settings into spots table.
func (s *Store) ApplyLegacySpotSettings() error {
	orderVal, err := s.GetSetting("spot_order")
	if err != nil {
		return err
	}
	visibleVal, err := s.GetSetting("visible_spots")
	if err != nil {
		return err
	}
	if orderVal == "" && visibleVal == "" {
		return nil
	}
	if orderVal != "" {
		if err := s.SetSpotOrder(splitKeys(orderVal)); err != nil {
			return err
		}
		_ = s.SetSetting("spot_order", "")
	}
	if visibleVal != "" {
		if err := s.SetVisibleSpots(splitKeys(visibleVal)); err != nil {
			return err
		}
		_ = s.SetSetting("visible_spots", "")
	}
	return nil
}

type spotScanner interface {
	Scan(dest ...any) error
}

func scanSpot(row spotScanner) (models.Spot, error) {
	var sp models.Spot
	var wg, wgSpot sql.NullInt64
	var visible, collect int
	if err := row.Scan(&sp.ID, &sp.Name, &wg, &wgSpot, &sp.SortOrder, &visible, &collect,
		&sp.CollectIntervalMin, &sp.CollectStartHour, &sp.CollectEndHour); err != nil {
		return sp, err
	}
	if wg.Valid {
		id := int(wg.Int64)
		sp.WindguruStationID = &id
	}
	if wgSpot.Valid {
		id := int(wgSpot.Int64)
		sp.WindguruID = &id
	}
	sp.Visible = visible != 0
	sp.Collect = collect != 0
	sp.CollectIntervalMin = models.NormalizeCollectInterval(sp.CollectIntervalMin)
	sp.CollectStartHour = models.NormalizeCollectHour(sp.CollectStartHour, 8)
	sp.CollectEndHour = models.NormalizeCollectHour(sp.CollectEndHour, 22)
	return sp, nil
}
