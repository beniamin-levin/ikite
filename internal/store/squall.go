package store

import (
	"database/sql"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// SquallFrame is one stored radar frame; see migrations/022_squall.sql.
type SquallFrame struct {
	FrameAt        time.Time
	FetchedAt      time.Time
	Path           string
	PNGFile        string
	MaxDBZ         int
	StrongPx       int
	Cells          int
	MotionOK       bool
	SpeedKmh       *float64
	HeadingDeg     *float64
	NearestKm      *float64
	NearestBearing *float64
	ThreatETAMin   *float64
	ThreatPassKm   *float64
	ThreatDBZ      *int
}

func (s *Store) InsertSquallFrame(f SquallFrame) error {
	_, err := s.DB.Exec(`
		INSERT INTO squall_frame (frame_at, fetched_at, path, png_file, max_dbz, strong_px, cells, motion_ok,
			speed_kmh, heading_deg, nearest_km, nearest_bearing, threat_eta_min, threat_pass_km, threat_dbz)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE fetched_at = VALUES(fetched_at), png_file = VALUES(png_file),
			max_dbz = VALUES(max_dbz), strong_px = VALUES(strong_px), cells = VALUES(cells),
			motion_ok = VALUES(motion_ok), speed_kmh = VALUES(speed_kmh), heading_deg = VALUES(heading_deg),
			nearest_km = VALUES(nearest_km), nearest_bearing = VALUES(nearest_bearing),
			threat_eta_min = VALUES(threat_eta_min), threat_pass_km = VALUES(threat_pass_km),
			threat_dbz = VALUES(threat_dbz)`,
		f.FrameAt, f.FetchedAt, f.Path, f.PNGFile, f.MaxDBZ, f.StrongPx, f.Cells, f.MotionOK,
		f.SpeedKmh, f.HeadingDeg, f.NearestKm, f.NearestBearing, f.ThreatETAMin, f.ThreatPassKm, f.ThreatDBZ)
	return err
}

// LatestSquallFrame returns the newest stored frame, or nil.
func (s *Store) LatestSquallFrame() (*SquallFrame, error) {
	rows, err := s.listSquallFrames(`ORDER BY frame_at DESC LIMIT 1`)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// ListSquallFrames returns frames in [from, to], oldest first.
func (s *Store) ListSquallFrames(from, to time.Time) ([]SquallFrame, error) {
	return s.listSquallFrames(`WHERE frame_at >= ? AND frame_at <= ? ORDER BY frame_at`, from, to)
}

func (s *Store) listSquallFrames(tail string, args ...any) ([]SquallFrame, error) {
	rows, err := s.DB.Query(`
		SELECT frame_at, fetched_at, path, png_file, max_dbz, strong_px, cells, motion_ok,
			speed_kmh, heading_deg, nearest_km, nearest_bearing, threat_eta_min, threat_pass_km, threat_dbz
		FROM squall_frame `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquallFrame
	for rows.Next() {
		var f SquallFrame
		var speed, heading, near, bearing, eta, pass sql.NullFloat64
		var tdbz sql.NullInt64
		if err := rows.Scan(&f.FrameAt, &f.FetchedAt, &f.Path, &f.PNGFile, &f.MaxDBZ, &f.StrongPx, &f.Cells,
			&f.MotionOK, &speed, &heading, &near, &bearing, &eta, &pass, &tdbz); err != nil {
			return nil, err
		}
		f.SpeedKmh, f.HeadingDeg, f.NearestKm = nullF(speed), nullF(heading), nullF(near)
		f.NearestBearing, f.ThreatETAMin, f.ThreatPassKm = nullF(bearing), nullF(eta), nullF(pass)
		if tdbz.Valid {
			v := int(tdbz.Int64)
			f.ThreatDBZ = &v
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func nullF(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

// SquallAlert is one message the squall service sent.
type SquallAlert struct {
	ID      int64
	Kind    string
	SentAt  time.Time
	FrameAt *time.Time
	ETAMin  *float64
	Message string
}

func (s *Store) InsertSquallAlert(a SquallAlert) error {
	_, err := s.DB.Exec(`INSERT INTO squall_alert (kind, sent_at, frame_at, eta_min, message) VALUES (?, ?, ?, ?, ?)`,
		a.Kind, a.SentAt, a.FrameAt, a.ETAMin, a.Message)
	return err
}

// ListSquallAlerts returns alerts of the given kinds sent in [from, to], oldest first.
func (s *Store) ListSquallAlerts(from, to time.Time, kinds ...string) ([]SquallAlert, error) {
	q := `SELECT id, kind, sent_at, frame_at, eta_min, message FROM squall_alert WHERE sent_at >= ? AND sent_at <= ?`
	args := []any{from, to}
	if len(kinds) > 0 {
		q += ` AND kind IN (?` + strings.Repeat(`, ?`, len(kinds)-1) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	rows, err := s.DB.Query(q+` ORDER BY sent_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquallAlert
	for rows.Next() {
		var a SquallAlert
		var frame sql.NullTime
		var eta sql.NullFloat64
		if err := rows.Scan(&a.ID, &a.Kind, &a.SentAt, &frame, &eta, &a.Message); err != nil {
			return nil, err
		}
		if frame.Valid {
			t := frame.Time
			a.FrameAt = &t
		}
		a.ETAMin = nullF(eta)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SquallEvent is a squall measured at the spot.
type SquallEvent struct {
	ID             int64
	Location       string
	StartAt        time.Time
	EndAt          *time.Time
	PeakWind       float64
	PeakGust       float64
	PeakAt         time.Time
	DirDeg         *float64
	Baseline       float64
	Kind           string
	AlertLeadMin   *float64
	ReplayLeadMin  *float64
	ObsLeadKm      *float64
	ObsSpeedFactor *float64
	AnalyzedAt     *time.Time
	Report         string
}

// UpsertSquallEvent records an event, refreshing its peak and end while it is
// still being measured. Analysis fields are left alone.
func (s *Store) UpsertSquallEvent(e SquallEvent) error {
	_, err := s.DB.Exec(`
		INSERT INTO squall_event (location, start_at, end_at, peak_wind, peak_gust, peak_at, dir_deg, baseline)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE end_at = VALUES(end_at), peak_wind = VALUES(peak_wind),
			peak_gust = VALUES(peak_gust), peak_at = VALUES(peak_at), dir_deg = VALUES(dir_deg)`,
		e.Location, e.StartAt, e.EndAt, e.PeakWind, e.PeakGust, e.PeakAt, e.DirDeg, e.Baseline)
	return err
}

// SaveSquallAnalysis stores what the post-storm replay found.
func (s *Store) SaveSquallAnalysis(e SquallEvent) error {
	_, err := s.DB.Exec(`
		UPDATE squall_event SET kind = ?, alert_lead_min = ?, replay_lead_min = ?, obs_lead_km = ?,
			obs_speed_factor = ?, analyzed_at = ?, report = ?
		WHERE id = ?`,
		e.Kind, e.AlertLeadMin, e.ReplayLeadMin, e.ObsLeadKm, e.ObsSpeedFactor, e.AnalyzedAt, e.Report, e.ID)
	return err
}

// ListSquallEvents returns events for a location starting in [from, to], oldest first.
func (s *Store) ListSquallEvents(location string, from, to time.Time) ([]SquallEvent, error) {
	rows, err := s.DB.Query(`
		SELECT id, location, start_at, end_at, peak_wind, peak_gust, peak_at, dir_deg, baseline,
			COALESCE(kind, ''), alert_lead_min, replay_lead_min, obs_lead_km, obs_speed_factor,
			analyzed_at, COALESCE(report, '')
		FROM squall_event WHERE location = ? AND start_at >= ? AND start_at <= ? ORDER BY start_at`,
		location, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquallEvent
	for rows.Next() {
		var e SquallEvent
		var end, analyzed sql.NullTime
		var dir, alertLead, replayLead, leadKm, speed sql.NullFloat64
		if err := rows.Scan(&e.ID, &e.Location, &e.StartAt, &end, &e.PeakWind, &e.PeakGust, &e.PeakAt, &dir,
			&e.Baseline, &e.Kind, &alertLead, &replayLead, &leadKm, &speed, &analyzed, &e.Report); err != nil {
			return nil, err
		}
		if end.Valid {
			t := end.Time
			e.EndAt = &t
		}
		if analyzed.Valid {
			t := analyzed.Time
			e.AnalyzedAt = &t
		}
		e.DirDeg, e.AlertLeadMin, e.ReplayLeadMin = nullF(dir), nullF(alertLead), nullF(replayLead)
		e.ObsLeadKm, e.ObsSpeedFactor = nullF(leadKm), nullF(speed)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) InsertSquallTuning(tunedAt time.Time, eventID *int64, before, after string, scoreBefore, scoreAfter float64, notes string) error {
	_, err := s.DB.Exec(`
		INSERT INTO squall_tuning (tuned_at, event_id, params_before, params_after, score_before, score_after, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, tunedAt, eventID, before, after, scoreBefore, scoreAfter, notes)
	return err
}

// ListWindLocationsRange returns readings for the given locations in [from, to], oldest first.
func (s *Store) ListWindLocationsRange(locations []string, from, to time.Time) ([]models.WindReading, error) {
	if len(locations) == 0 {
		return nil, nil
	}
	args := []any{from, to}
	for _, l := range locations {
		args = append(args, l)
	}
	rows, err := s.DB.Query(`
		SELECT period, location, wind, gust, COALESCE(wind_dir, 0), temp
		FROM wind_data WHERE period >= ? AND period <= ? AND location IN (?`+strings.Repeat(`, ?`, len(locations)-1)+`)
		ORDER BY period`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.WindReading
	for rows.Next() {
		var r models.WindReading
		var temp sql.NullFloat64
		if err := rows.Scan(&r.Period, &r.Location, &r.Wind, &r.Gust, &r.WindDir, &temp); err != nil {
			return nil, err
		}
		if temp.Valid {
			t := temp.Float64
			r.Temp = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// IMSRadarFrame is an archived IMS rain-radar frame.
type IMSRadarFrame struct {
	FrameAt, FetchedAt time.Time
	Source, PNGFile    string
	Bytes              int
}

func (s *Store) InsertIMSRadarFrame(f IMSRadarFrame) error {
	_, err := s.DB.Exec(`INSERT IGNORE INTO squall_ims_frame (frame_at, fetched_at, source, png_file, bytes) VALUES (?, ?, ?, ?, ?)`,
		f.FrameAt, f.FetchedAt, f.Source, f.PNGFile, f.Bytes)
	return err
}

// LatestIMSRadarFrame returns the newest archived IMS frame, or nil.
func (s *Store) LatestIMSRadarFrame() (*IMSRadarFrame, error) {
	var f IMSRadarFrame
	err := s.DB.QueryRow(`SELECT frame_at, fetched_at, source, png_file, bytes FROM squall_ims_frame ORDER BY frame_at DESC LIMIT 1`).
		Scan(&f.FrameAt, &f.FetchedAt, &f.Source, &f.PNGFile, &f.Bytes)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// SquallLightning is one stored lightning frame.
type SquallLightning struct {
	FrameAt, FetchedAt time.Time
	PNGFile            string
	Px, Within40Px     int
	NearestKm          *float64
	NearestBearing     *float64
	ThreatKm           *float64
	ThreatBearing      *float64
}

func (s *Store) InsertSquallLightning(l SquallLightning) error {
	_, err := s.DB.Exec(`
		INSERT INTO squall_lightning (frame_at, fetched_at, png_file, px, within40_px, nearest_km, nearest_bearing, threat_km, threat_bearing)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE fetched_at = VALUES(fetched_at), png_file = VALUES(png_file), px = VALUES(px),
			within40_px = VALUES(within40_px), nearest_km = VALUES(nearest_km), nearest_bearing = VALUES(nearest_bearing),
			threat_km = VALUES(threat_km), threat_bearing = VALUES(threat_bearing)`,
		l.FrameAt, l.FetchedAt, l.PNGFile, l.Px, l.Within40Px, l.NearestKm, l.NearestBearing, l.ThreatKm, l.ThreatBearing)
	return err
}

// ListSquallLightning returns lightning frames in [from, to], oldest first.
func (s *Store) ListSquallLightning(from, to time.Time) ([]SquallLightning, error) {
	rows, err := s.DB.Query(`
		SELECT frame_at, fetched_at, png_file, px, within40_px, nearest_km, nearest_bearing, threat_km, threat_bearing
		FROM squall_lightning WHERE frame_at >= ? AND frame_at <= ? ORDER BY frame_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquallLightning
	for rows.Next() {
		var l SquallLightning
		var nk, nb, tk, tb sql.NullFloat64
		if err := rows.Scan(&l.FrameAt, &l.FetchedAt, &l.PNGFile, &l.Px, &l.Within40Px, &nk, &nb, &tk, &tb); err != nil {
			return nil, err
		}
		l.NearestKm, l.NearestBearing, l.ThreatKm, l.ThreatBearing = nullF(nk), nullF(nb), nullF(tk), nullF(tb)
		out = append(out, l)
	}
	return out, rows.Err()
}

// LatestSquallLightning returns the newest stored lightning frame time, or zero.
func (s *Store) LatestSquallLightning() (time.Time, error) {
	var t sql.NullTime
	if err := s.DB.QueryRow(`SELECT MAX(frame_at) FROM squall_lightning`).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if !t.Valid {
		return time.Time{}, nil
	}
	return t.Time, nil
}

// IMSWarning is an IMS official warning covering the spot.
type IMSWarning struct {
	WID                     int64
	TypeID, SeverityID      int
	ValidFrom, ValidTo      time.Time
	Regions, TextEN, TextHE string
	SentAt                  *time.Time
}

// SaveIMSWarning records a warning the first time it is seen; later sightings
// leave it (and whether it was sent) alone.
func (s *Store) SaveIMSWarning(w IMSWarning) error {
	_, err := s.DB.Exec(`
		INSERT IGNORE INTO squall_ims_warning (wid, warning_type_id, severity_id, valid_from, valid_to, regions, text_en, text_he)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		w.WID, w.TypeID, w.SeverityID, w.ValidFrom, w.ValidTo, w.Regions, w.TextEN, w.TextHE)
	return err
}

func (s *Store) MarkIMSWarningSent(wid int64, at time.Time) error {
	_, err := s.DB.Exec(`UPDATE squall_ims_warning SET sent_at = ? WHERE wid = ?`, at, wid)
	return err
}

// ListIMSWarnings returns warnings still valid at t, oldest first.
func (s *Store) ListIMSWarnings(t time.Time) ([]IMSWarning, error) {
	rows, err := s.DB.Query(`
		SELECT wid, warning_type_id, severity_id, valid_from, valid_to, regions, text_en, text_he, sent_at
		FROM squall_ims_warning WHERE valid_to > ? ORDER BY valid_from, wid`, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IMSWarning
	for rows.Next() {
		var w IMSWarning
		var sent sql.NullTime
		if err := rows.Scan(&w.WID, &w.TypeID, &w.SeverityID, &w.ValidFrom, &w.ValidTo, &w.Regions, &w.TextEN, &w.TextHE, &sent); err != nil {
			return nil, err
		}
		if sent.Valid {
			t := sent.Time
			w.SentAt = &t
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
