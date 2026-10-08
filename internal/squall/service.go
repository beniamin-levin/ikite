package squall

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/notify/telegram"
	"github.com/ben/ikite-go/internal/store"
)

const (
	paramsKey = "squall_params"
	reviewKey = "squall_review_day"
	// spotLoc is the meter the alerts are for.
	spotLoc = "kh"
	// Alerts are for kiting: daylight hours only. Frames are archived and storms
	// recorded around the clock regardless.
	alertFromHour, alertToHour = 6, 20
	// Post-storm reports wait for a civil hour.
	reportFromHour, reportToHour = 7, 22
	// Stop archiving frame images when the disk gets this full.
	minFreeBytes = 2 << 30
	// The replay window used for tuning.
	tuneDays    = 60
	radarMapURL = "https://www.rainviewer.com/map.html?loc=32.83,35.07,9"
)

// Service runs one pass: radar, stations, events, reviews.
type Service struct {
	Store      *store.Store
	Log        *slog.Logger
	Radar      *Client
	TG         *telegram.Client
	ArchiveDir string
	TZ         *time.Location
	Target     Target
	// Translate turns IMS's Hebrew warning details into English; nil leaves
	// them out (messages are English only).
	Translate interface {
		HebrewToEnglish(string) (string, error)
	}
}

func (s *Service) geo() Geometry { return GeometryFor(s.Target) }

// Params loads the current rules.
func (s *Service) Params() Params {
	raw, err := s.Store.GetSetting(paramsKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return DefaultParams()
	}
	p := DefaultParams()
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		s.Log.Warn("squall params unreadable; using defaults", "err", err)
		return DefaultParams()
	}
	return p.Sane()
}

func (s *Service) saveParams(p Params) error {
	b, _ := json.Marshal(p.Sane())
	return s.Store.SetSetting(paramsKey, string(b))
}

// Run does one pass. Each step logs its own failure and the rest still run, so
// a radar outage never silences the station alerts.
func (s *Service) Run(now time.Time) error {
	now = now.In(s.TZ)
	p := s.Params()
	var errs []string
	if err := s.radarStep(now, p); err != nil {
		s.Log.Error("squall radar", "err", err)
		errs = append(errs, "radar: "+err.Error())
	}
	if err := s.lightningStep(now, p); err != nil {
		s.Log.Error("squall lightning", "err", err)
		errs = append(errs, "lightning: "+err.Error())
	}
	if err := s.imsWarningStep(now); err != nil {
		s.Log.Warn("squall ims warnings", "err", err) // a missed IMS fetch is retried next minute
	}
	if n, err := s.archiveIMSRadar(now); err != nil {
		s.Log.Warn("squall ims radar archive", "err", err) // archive only; never fails the run
	} else if n > 0 {
		s.Log.Info("squall ims radar archived", "frames", n)
	}
	if err := s.stationStep(now); err != nil {
		s.Log.Error("squall stations", "err", err)
		errs = append(errs, "stations: "+err.Error())
	}
	if err := s.eventStep(now); err != nil {
		s.Log.Error("squall events", "err", err)
		errs = append(errs, "events: "+err.Error())
	}
	if err := s.dailyReview(now); err != nil {
		s.Log.Error("squall daily review", "err", err)
		errs = append(errs, "review: "+err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// ---- radar ----

func (s *Service) radarStep(now time.Time, p Params) error {
	host, frames, err := s.Radar.Frames()
	if err != nil {
		return err
	}
	last, err := s.Store.LatestSquallFrame()
	if err != nil {
		return err
	}
	var prev *Grid
	var prevAt time.Time
	if last != nil {
		prevAt = last.FrameAt
		prev = s.loadGrid(last.PNGFile)
	}
	geo := s.geo()
	var newest *ReplayFrame
	var newestScan Scan
	for _, f := range frames {
		at := f.Time.In(s.TZ)
		if last != nil && !at.After(last.FrameAt) {
			continue
		}
		body, err := s.Radar.Tile(host, f)
		if err != nil {
			return err
		}
		g, err := Decode(body)
		if err != nil {
			return err
		}
		rel := s.archive(at, body)
		var mo Motion
		if dt := at.Sub(prevAt).Minutes(); prev != nil && dt > 0 && dt <= 20 {
			mo = MeasureMotion(prev, g, geo, dt)
		}
		sc := Analyse(g, mo, p, geo)
		if err := s.Store.InsertSquallFrame(frameRow(at, now, f.Path, rel, sc)); err != nil {
			return err
		}
		prev, prevAt = g, at
		newest = &ReplayFrame{FrameAt: at, AvailAt: now, Grid: g, Motion: mo}
		newestScan = sc
	}
	if newest == nil {
		return nil
	}
	s.Log.Info("squall radar frame", "frame", newest.FrameAt.Format("15:04"), "max_dbz", newestScan.MaxDBZ,
		"cells", len(newestScan.Cells), "motion_ok", newestScan.Motion.OK,
		"speed_kmh", round1(newestScan.Motion.SpeedKmh), "heading", math.Round(newestScan.Motion.HeadingDeg))

	// Only the newest frame can raise an alert, and only while it is fresh: the
	// first run backfills two hours of frames that are history, not news.
	if now.Sub(newest.FrameAt) > 25*time.Minute || !alertHour(now) {
		return nil
	}
	eta, ok := ShouldAlert(newestScan, *newest, p)
	if !ok {
		return nil
	}
	if recent, err := s.sentSince("radar", now.Add(-cooldown)); err != nil || recent {
		return err
	}
	msg := radarMessage(newestScan, eta, now, s.Target) + s.imsThunderNote(now)
	frameAt := newest.FrameAt
	return s.send("radar", now, &frameAt, &eta, msg)
}

func frameRow(at, now time.Time, path, rel string, sc Scan) store.SquallFrame {
	f := store.SquallFrame{FrameAt: at, FetchedAt: now, Path: path, PNGFile: rel,
		MaxDBZ: sc.MaxDBZ, StrongPx: sc.StrongPx, Cells: len(sc.Cells), MotionOK: sc.Motion.OK}
	if sc.Motion.OK {
		f.SpeedKmh, f.HeadingDeg = ptr(round1(sc.Motion.SpeedKmh)), ptr(math.Round(sc.Motion.HeadingDeg))
	}
	if len(sc.Cells) > 0 {
		f.NearestKm, f.NearestBearing = ptr(round1(sc.Cells[0].DistKm)), ptr(math.Round(sc.Cells[0].BearingDeg))
	}
	if t := sc.Threat; t != nil {
		f.ThreatETAMin, f.ThreatPassKm = ptr(round1(t.ETAMin)), ptr(round1(t.PassKm))
		d := t.MaxDBZ
		f.ThreatDBZ = &d
	}
	return f
}

func radarMessage(sc Scan, eta float64, now time.Time, t Target) string {
	c := sc.Threat
	arrive := now.Add(time.Duration(math.Max(eta, 0) * float64(time.Minute)))
	var b strings.Builder
	fmt.Fprintf(&b, "⛈ Squall heading for %s\n", t.Name)
	if eta < 3 {
		b.WriteString("It is arriving now.\n")
	} else {
		fmt.Fprintf(&b, "Expected in ~%.0f min (around %s).\n", eta, arrive.Format("15:04"))
	}
	fmt.Fprintf(&b, "Heavy rain (%d dBZ) %.0f km %s", c.MaxDBZ, c.DistKm, compass(c.BearingDeg))
	if sc.Motion.OK {
		fmt.Fprintf(&b, ", moving %s at %.0f km/h", toward(sc.Motion.HeadingDeg), sc.Motion.SpeedKmh)
	}
	b.WriteString(".\nA second alert follows when Bat Galim or Shavei Tzion feels the gust front.\n")
	b.WriteString("Radar (RainViewer): " + radarMapURL)
	return b.String()
}

// archive stores a RainViewer frame image as YYYY/MM/DD/HHMM.png and returns
// the path relative to the archive dir ("" when it was not stored).
func (s *Service) archive(at time.Time, body []byte) string { return s.archiveAs("", at, body) }

func (s *Service) loadGrid(rel string) *Grid {
	if rel == "" || s.ArchiveDir == "" {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(s.ArchiveDir, rel))
	if err != nil {
		return nil
	}
	g, err := Decode(body)
	if err != nil {
		return nil
	}
	return g
}

func freeBytes(dir string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return st.Bavail * uint64(st.Bsize), true
}

// ---- stations ----

func (s *Service) stationStep(now time.Time) error {
	if !alertHour(now) {
		return nil
	}
	locs := []string{spotLoc}
	for _, u := range Upwind {
		locs = append(locs, u.Loc)
	}
	rows, err := s.Store.ListWindLocationsRange(locs, now.Add(-40*time.Minute), now)
	if err != nil {
		return err
	}
	byLoc := map[string][]models.WindReading{}
	for _, r := range rows {
		byLoc[r.Location] = append(byLoc[r.Location], r)
	}
	// Already blowing at the spot: an "arriving" alert would be late news.
	if kh := byLoc[spotLoc]; len(kh) > 0 && kh[len(kh)-1].Wind >= eventMinKt {
		return nil
	}
	for _, u := range Upwind {
		c := Confirm(u.Loc, u.Name, byLoc[u.Loc], now)
		if c == nil {
			continue
		}
		if recent, err := s.sentSince("station", now.Add(-cooldown)); err != nil || recent {
			return err
		}
		return s.send("station", now, nil, nil, stationMessage(c, s.Target))
	}
	return nil
}

func stationMessage(c *Confirmation, t Target) string {
	var b strings.Builder
	fmt.Fprintf(&b, "💨 Gust front at %s: %.0f kt, gusting %.0f, from %s", c.Name, c.Wind, c.Gust, compass(c.Dir))
	if c.TempDrop >= confirmTempDropC {
		fmt.Fprintf(&b, ", %.1f °C colder", c.TempDrop)
	}
	b.WriteString(".\n")
	if c.Loc == "bg" {
		fmt.Fprintf(&b, "Expected at %s in about 10 minutes.", t.Name)
	} else {
		fmt.Fprintf(&b, "If it is moving south it reaches %s in 10–20 minutes.", t.Name)
	}
	return b.String()
}

// ---- events and post-storm analysis ----

func (s *Service) eventStep(now time.Time) error {
	rows, err := s.Store.ListWindLocationsRange([]string{spotLoc}, now.Add(-6*time.Hour), now)
	if err != nil {
		return err
	}
	for _, e := range FindEvents(rows) {
		ev := store.SquallEvent{Location: spotLoc, StartAt: e.Start, PeakWind: e.PeakWind, PeakGust: e.PeakGust,
			PeakAt: e.PeakAt, Baseline: round1(e.Baseline)}
		if !e.End.IsZero() {
			end := e.End
			ev.EndAt = &end
		}
		if e.DirDeg > 0 {
			ev.DirDeg = ptr(e.DirDeg)
		}
		if err := s.Store.UpsertSquallEvent(ev); err != nil {
			return err
		}
	}
	if h := now.Hour(); h < reportFromHour || h >= reportToHour {
		return nil
	}
	events, err := s.Store.ListSquallEvents(spotLoc, now.AddDate(0, 0, -3), now)
	if err != nil {
		return err
	}
	for _, e := range events {
		if e.AnalyzedAt != nil {
			continue
		}
		if e.EndAt == nil && now.Sub(e.StartAt) < 2*time.Hour {
			continue // still blowing
		}
		if err := s.analyse(now, e); err != nil {
			return fmt.Errorf("analyse %s: %w", e.StartAt.Format("2006-01-02 15:04"), err)
		}
	}
	return nil
}

// replay loads the stored radar frames (with their images) and lightning
// frames for [from, to].
func (s *Service) replay(from, to time.Time) ([]ReplayFrame, []LightningSample, error) {
	rows, err := s.Store.ListSquallFrames(from, to)
	if err != nil {
		return nil, nil, err
	}
	lrows, err := s.Store.ListSquallLightning(from, to)
	if err != nil {
		return nil, nil, err
	}
	samples := make([]LightningSample, 0, len(lrows))
	for _, l := range lrows {
		ls := LightningSample{FrameAt: l.FrameAt, AvailAt: l.FetchedAt, ThreatKm: math.Inf(1)}
		// Backfilled frames were fetched late; credit the usual ~12 min delay.
		if ls.AvailAt.Sub(ls.FrameAt) > 20*time.Minute {
			ls.AvailAt = ls.FrameAt.Add(12 * time.Minute)
			ls.Backfilled = true
		}
		if l.ThreatKm != nil {
			ls.ThreatKm = *l.ThreatKm
		}
		if l.ThreatBearing != nil {
			ls.ThreatBrg = *l.ThreatBearing
		}
		samples = append(samples, ls)
	}
	out := make([]ReplayFrame, 0, len(rows))
	for _, r := range rows {
		f := ReplayFrame{FrameAt: r.FrameAt, AvailAt: r.FetchedAt}
		// Frames older than the backfill were fetched late; never credit them
		// with being available later than ~10 min after the scan.
		if f.AvailAt.Sub(f.FrameAt) > 15*time.Minute {
			f.AvailAt = f.FrameAt.Add(10 * time.Minute)
		}
		if r.MaxDBZ >= motionDBZ {
			f.Grid = s.loadGrid(r.PNGFile)
		}
		out = append(out, f)
	}
	LinkMotion(out, s.geo())
	return out, samples, nil
}

func (s *Service) analyse(now time.Time, e store.SquallEvent) error {
	p := s.Params()
	geo := s.geo()
	frames, lightning, err := s.replay(now.AddDate(0, 0, -tuneDays), now)
	if err != nil {
		return err
	}
	ob := Observe(frames, e.StartAt, p, geo)
	ObserveLightning(&ob, lightning, e.StartAt, p)
	hasLightning := len(lightning) > 0 && lightning[0].FrameAt.Before(e.StartAt.Add(-time.Hour))

	e.Kind = "unknown"
	switch {
	case ob.Storm:
		e.Kind = "storm"
		e.ObsLeadKm = ptr(round1(ob.LeadKm))
		if ob.SpeedFactor > 0 {
			e.ObsSpeedFactor = ptr(round1(ob.SpeedFactor))
		}
	case ob.LightningNearKm <= stormCellKm:
		e.Kind = "storm" // no radar cell on record, but lightning close by: a thunderstorm
	case ob.HasRadar || hasLightning:
		e.Kind = "dry"
	}

	alerts, err := s.Store.ListSquallAlerts(e.StartAt.Add(-alertWindow), e.StartAt, "radar", "lightning", "station")
	if err != nil {
		return err
	}
	leads := map[string]float64{"radar": -1, "lightning": -1, "station": -1}
	for _, a := range alerts {
		if l := e.StartAt.Sub(a.SentAt).Minutes(); l > leads[a.Kind] {
			leads[a.Kind] = l
		}
	}
	if best := math.Max(leads["radar"], math.Max(leads["lightning"], leads["station"])); best >= 0 {
		e.AlertLeadMin = ptr(round1(best))
	}

	// Tune on every storm the archive covers, this one included.
	var tr *TuneResult
	if e.Kind == "storm" && (len(frames) > 0 || len(lightning) > 0) {
		from := now
		if len(frames) > 0 {
			from = frames[0].FrameAt
		}
		if len(lightning) > 0 && lightning[0].FrameAt.Before(from) {
			from = lightning[0].FrameAt
		}
		storms, leadKms, speeds, err := s.stormHistory(now, from, e)
		if err != nil {
			return err
		}
		res := Tune(frames, lightning, storms, p, leadKms, speeds, geo)
		tr = &res
		if res.Changed {
			if err := s.saveParams(res.After); err != nil {
				return err
			}
		}
		before, _ := json.Marshal(res.Before)
		after, _ := json.Marshal(res.After)
		id := e.ID
		if err := s.Store.InsertSquallTuning(now, &id, string(before), string(after),
			res.ScoreBefore.Points, res.ScoreAfter.Points, Diff(res.Before, res.After)); err != nil {
			return err
		}
		one := Evaluate(mergeAlerts(Simulate(frames, res.After, geo), SimulateLightning(lightning, res.After)), []time.Time{e.StartAt})
		if len(one.Leads) == 1 && one.Leads[0] >= 0 {
			e.ReplayLeadMin = ptr(round1(one.Leads[0]))
		}
	}

	e.Report = eventReport(e, ob, leads, tr, p, s.Target)
	e.AnalyzedAt = &now
	if err := s.Store.SaveSquallAnalysis(e); err != nil {
		return err
	}
	return s.send("report", now, nil, nil, e.Report)
}

// stormHistory lists the radar-visible storms since the archive began, with the
// measurements taken from each.
func (s *Service) stormHistory(now, archiveFrom time.Time, cur store.SquallEvent) (starts []time.Time, leadKms, speeds []float64, err error) {
	events, err := s.Store.ListSquallEvents(spotLoc, archiveFrom.Add(2*time.Hour), now)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, ev := range events {
		if ev.ID == cur.ID {
			ev = cur
		}
		if ev.Kind != "storm" {
			continue
		}
		starts = append(starts, ev.StartAt)
		if ev.ObsLeadKm != nil {
			leadKms = append(leadKms, *ev.ObsLeadKm)
		}
		if ev.ObsSpeedFactor != nil {
			speeds = append(speeds, *ev.ObsSpeedFactor)
		}
	}
	return starts, leadKms, speeds, nil
}

func eventReport(e store.SquallEvent, ob Observation, leads map[string]float64, tr *TuneResult, p Params, t Target) string {
	radarLead, lightningLead, stationLead := leads["radar"], leads["lightning"], leads["station"]
	var b strings.Builder
	fmt.Fprintf(&b, "📊 Squall report — %s, %s\n", t.Name, e.StartAt.Format("Mon 2 Jan 15:04"))
	fmt.Fprintf(&b, "Wind %.0f → %.0f kt (gust %.0f)", e.Baseline, e.PeakWind, e.PeakGust)
	if e.DirDeg != nil {
		fmt.Fprintf(&b, " from %s", compass(*e.DirDeg))
	}
	if e.EndAt != nil {
		fmt.Fprintf(&b, ", lasted %.0f min", e.EndAt.Sub(e.StartAt).Minutes())
	}
	b.WriteString(".\n\n")

	b.WriteString("Warning: ")
	switch {
	case radarLead >= usefulLeadMin:
		fmt.Fprintf(&b, "radar alert %.0f min ahead ✅", radarLead)
	case radarLead >= 0:
		fmt.Fprintf(&b, "radar alert only %.0f min ahead ⚠️", radarLead)
	default:
		b.WriteString("no radar alert ❌")
	}
	if lightningLead >= 0 {
		fmt.Fprintf(&b, "; lightning alert %.0f min ahead", lightningLead)
	}
	if stationLead >= 0 {
		fmt.Fprintf(&b, "; station alert %.0f min ahead", stationLead)
	}
	b.WriteString(".\n")

	switch e.Kind {
	case "unknown":
		b.WriteString("Radar and lightning: no frames around the start (archive gap), so nothing to learn from this one.\n")
		return b.String()
	case "dry":
		b.WriteString("No heavy rain or lightning within 30 km when it hit — a dry gust (sea-breeze front or downslope wind). " +
			"Radar and lightning cannot warn of these; only the station alert can. Not used for tuning.\n")
		return b.String()
	}

	if ob.Storm {
		fmt.Fprintf(&b, "Radar: %d dBZ core %.1f km away when the wind hit", ob.CoreDBZ, ob.LeadKm)
		if ob.SpeedKmh > 0 {
			fmt.Fprintf(&b, "; storm moving %s at %.0f km/h", toward(ob.HeadingDeg), ob.SpeedKmh)
		}
		b.WriteString(".\n")
	} else {
		b.WriteString("Radar: no frames with a heavy cell close by on record.\n")
	}
	if !math.IsInf(ob.LightningNearKm, 1) {
		fmt.Fprintf(&b, "Lightning: came within %.0f km on the sea side", ob.LightningNearKm)
		if ob.LightningLead >= 0 {
			fmt.Fprintf(&b, "; inside the %s range %.0f min before it hit", lightningLabel(p.LightningKm), ob.LightningLead)
		}
		b.WriteString(".\n")
	}
	if l, ok := ob.FirstSeen[p.DBZ]; ok && ob.Storm && l >= 0 {
		fmt.Fprintf(&b, "First projected to hit %.0f min before it did (at %d dBZ).\n", l, p.DBZ)
	}
	if ob.SpeedFactor > 0 {
		switch {
		case ob.SpeedFactor > 1.15:
			fmt.Fprintf(&b, "It arrived faster than projected (×%.1f).\n", ob.SpeedFactor)
		case ob.SpeedFactor < 0.87:
			fmt.Fprintf(&b, "It arrived slower than projected (×%.1f).\n", ob.SpeedFactor)
		}
	}

	if tr != nil {
		b.WriteString("\nTuning (replayed over every stored storm and false alarm):\n")
		if tr.Changed {
			fmt.Fprintf(&b, "Changed: %s.\n", Diff(tr.Before, tr.After))
		} else {
			b.WriteString("Rules unchanged — nothing scored better.\n")
		}
		fmt.Fprintf(&b, "Before: %s.\nAfter:  %s.\n", tr.ScoreBefore, tr.ScoreAfter)
		if e.ReplayLeadMin != nil {
			fmt.Fprintf(&b, "With the new rules this storm would have been flagged %.0f min ahead.\n", *e.ReplayLeadMin)
		}
	}

	// What could still be improved.
	var ideas []string
	best, bestDBZ := -1.0, 0
	for _, dbz := range probeDBZ {
		if l := ob.FirstSeen[dbz]; l > best {
			best, bestDBZ = l, dbz
		}
	}
	switch {
	case !ob.Storm:
		// No radar cell to learn from; the lightning range is still tuned.
	case best < 0:
		ideas = append(ideas, "the radar never projected this cell onto the spot — it likely formed or turned toward the bay inside 10 km; only the station alert can catch these")
	case best < usefulLeadMin:
		ideas = append(ideas, fmt.Sprintf("even the most sensitive setting (%d dBZ) saw it only %.0f min ahead — the cell grew close to shore, so radar alone can't give 20 min for storms like this", bestDBZ, best))
	case bestDBZ < p.DBZ && (tr == nil || tr.After.DBZ > bestDBZ):
		ideas = append(ideas, fmt.Sprintf("weaker rain (%d dBZ) showed it %.0f min ahead; lowering the threshold would help if it doesn't add false alarms (the tuner keeps checking)", bestDBZ, best))
	}
	if ob.LightningLead >= usefulLeadMin && lightningLead < 0 && radarLead < 0 && alertHour(e.StartAt) {
		if ob.LightningBackfilled {
			ideas = append(ideas, fmt.Sprintf("lightning wasn't being collected live yet (replayed afterwards) — live, the lightning alert would have come %.0f min ahead", ob.LightningLead))
		} else {
			ideas = append(ideas, fmt.Sprintf("lightning was inside range %.0f min ahead but no lightning alert went out — check the lightning feed was up", ob.LightningLead))
		}
	}
	if math.Max(radarLead, lightningLead) < 0 && e.ReplayLeadMin != nil && *e.ReplayLeadMin >= usefulLeadMin && !alertHour(e.StartAt) {
		ideas = append(ideas, "it hit outside alert hours, so no message was sent")
	}
	if len(ideas) > 0 {
		b.WriteString("\nWhat would improve it: " + strings.Join(ideas, "; ") + ".\n")
	}
	return b.String()
}

// dailyReview, once in the evening, re-tunes when radar alerts fired today
// without a squall following — false alarms are the other half of the score.
func (s *Service) dailyReview(now time.Time) error {
	if now.Hour() != 21 {
		return nil
	}
	day := now.Format("2006-01-02")
	if done, _ := s.Store.GetSetting(reviewKey); done == day {
		return nil
	}
	if err := s.Store.SetSetting(reviewKey, day); err != nil {
		return err
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.TZ)
	alerts, err := s.Store.ListSquallAlerts(midnight, now, "radar", "lightning")
	if err != nil || len(alerts) == 0 {
		return err
	}
	events, err := s.Store.ListSquallEvents(spotLoc, midnight, now)
	if err != nil {
		return err
	}
	var falseAlarms int
	for _, a := range alerts {
		hit := false
		for _, e := range events {
			if !e.StartAt.Before(a.SentAt) && e.StartAt.Sub(a.SentAt) <= falseAlarmWindow {
				hit = true
			}
		}
		if !hit {
			falseAlarms++
		}
	}
	if falseAlarms == 0 {
		return nil
	}

	p := s.Params()
	frames, lightning, err := s.replay(now.AddDate(0, 0, -tuneDays), now)
	if err != nil || (len(frames) == 0 && len(lightning) == 0) {
		return err
	}
	from := now
	if len(frames) > 0 {
		from = frames[0].FrameAt
	}
	if len(lightning) > 0 && lightning[0].FrameAt.Before(from) {
		from = lightning[0].FrameAt
	}
	storms, leadKms, speeds, err := s.stormHistory(now, from, store.SquallEvent{})
	if err != nil {
		return err
	}
	res := Tune(frames, lightning, storms, p, leadKms, speeds, s.geo())
	if res.Changed {
		if err := s.saveParams(res.After); err != nil {
			return err
		}
	}
	before, _ := json.Marshal(res.Before)
	after, _ := json.Marshal(res.After)
	if err := s.Store.InsertSquallTuning(now, nil, string(before), string(after),
		res.ScoreBefore.Points, res.ScoreAfter.Points, "daily false-alarm review: "+Diff(res.Before, res.After)); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🔎 Squall check, %s: %d of today's %d radar/lightning alerts had no squall at %s.\n",
		now.Format("Mon 2 Jan"), falseAlarms, len(alerts), s.Target.Name)
	if res.Changed {
		fmt.Fprintf(&b, "Changed: %s.\n", Diff(res.Before, res.After))
	} else {
		b.WriteString("Rules unchanged — tightening them would have cost a real warning.\n")
	}
	fmt.Fprintf(&b, "Before: %s.\nAfter:  %s.", res.ScoreBefore, res.ScoreAfter)
	return s.send("review", now, nil, nil, b.String())
}

// ---- helpers ----

func (s *Service) sentSince(kind string, since time.Time) (bool, error) {
	alerts, err := s.Store.ListSquallAlerts(since, since.Add(24*time.Hour), kind)
	return len(alerts) > 0, err
}

func (s *Service) send(kind string, now time.Time, frameAt *time.Time, eta *float64, msg string) error {
	if err := s.TG.Send(msg); err != nil {
		return fmt.Errorf("telegram %s: %w", kind, err)
	}
	s.Log.Info("squall alert sent", "kind", kind)
	var e *float64
	if eta != nil {
		e = ptr(round1(*eta))
	}
	return s.Store.InsertSquallAlert(store.SquallAlert{Kind: kind, SentAt: now, FrameAt: frameAt, ETAMin: e, Message: msg})
}

func alertHour(t time.Time) bool { return t.Hour() >= alertFromHour && t.Hour() < alertToHour }

func ptr[T any](v T) *T { return &v }

var points16 = []string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}

func compass(deg float64) string {
	return points16[int(math.Mod(deg+11.25+360, 360)/22.5)%16]
}

// toward phrases a heading: "toward ENE".
func toward(deg float64) string { return "toward " + compass(deg) }

// ---- lightning ----

// lightningStep fetches every published lightning frame not yet stored (up to
// 12 per run) and alerts on the newest when flashes are close on the sea side.
func (s *Service) lightningStep(now time.Time, p Params) error {
	last, err := s.Store.LatestSquallLightning()
	if err != nil {
		return err
	}
	next := now.Add(-time.Hour).Truncate(lightningStep)
	if !last.IsZero() && last.Add(lightningStep).After(next) {
		next = last.Add(lightningStep)
	}
	var newest *LightningScan
	var newestAt time.Time
	for n := 0; n < 12 && !next.After(now.Add(-lightningStep)); n++ {
		at, sc, err := s.fetchLightning(next, now)
		if err == errNotPublished {
			break
		}
		if err != nil {
			return err
		}
		newest, newestAt = &sc, at
		next = next.Add(lightningStep)
	}
	if newest == nil {
		return nil
	}
	km, on := p.lightningOn()
	if !on || newest.ThreatKm > km || now.Sub(newestAt) > 30*time.Minute || !alertHour(now) {
		return nil
	}
	if recent, err := s.sentSince("lightning", now.Add(-cooldown)); err != nil || recent {
		return err
	}
	msg := lightningMessage(*newest, newestAt, s.Target) + s.imsThunderNote(now)
	return s.send("lightning", now, &newestAt, nil, msg)
}

// fetchLightning downloads, archives (frames with flashes only) and records one
// lightning frame.
func (s *Service) fetchLightning(slot, now time.Time) (time.Time, LightningScan, error) {
	at := slot.In(s.TZ)
	body, err := s.Radar.LightningFrame(slot)
	if err != nil {
		return at, LightningScan{}, err
	}
	sc, err := AnalyseLightning(body, s.Target)
	if err != nil {
		return at, sc, err
	}
	rel := ""
	if sc.Px > 0 {
		rel = s.archiveAs("lightning", at, body)
	}
	row := store.SquallLightning{FrameAt: at, FetchedAt: now, PNGFile: rel, Px: sc.Px, Within40Px: sc.Within40Px}
	if !math.IsInf(sc.NearestKm, 1) {
		row.NearestKm, row.NearestBearing = ptr(round1(sc.NearestKm)), ptr(math.Round(sc.NearestBrg))
	}
	if !math.IsInf(sc.ThreatKm, 1) {
		row.ThreatKm, row.ThreatBearing = ptr(round1(sc.ThreatKm)), ptr(math.Round(sc.ThreatBrg))
	}
	if err := s.Store.InsertSquallLightning(row); err != nil {
		return at, sc, err
	}
	if sc.Px > 0 {
		s.Log.Info("squall lightning", "frame", at.Format("15:04"), "px", sc.Px, "nearest_km", row.NearestKm, "threat_km", row.ThreatKm)
	}
	return at, sc, nil
}

// BackfillLightning stores lightning frames for [from, to] (for replaying a
// storm that hit before lightning was being collected).
func (s *Service) BackfillLightning(from, to, now time.Time) (int, error) {
	n, failed := 0, 0
	for t := from.Truncate(lightningStep); !t.After(to); t = t.Add(lightningStep) {
		_, _, err := s.fetchLightning(t, now)
		if err != nil && err != errNotPublished {
			// EUMETSAT answers the odd frame with a 500; one retry, then move on.
			time.Sleep(2 * time.Second)
			_, _, err = s.fetchLightning(t, now)
		}
		switch {
		case err == nil:
			n++
		case err == errNotPublished:
		default:
			failed++
			s.Log.Warn("lightning backfill frame", "frame", t.Format("15:04"), "err", err)
		}
	}
	if failed > 0 {
		return n, fmt.Errorf("%d frames failed", failed)
	}
	return n, nil
}

func lightningMessage(sc LightningScan, at time.Time, t Target) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚡ Lightning %.0f km %s of %s (%s)\n", sc.ThreatKm, compass(sc.ThreatBrg), t.Name, at.Format("15:04"))
	b.WriteString("A thunderstorm is close on the sea side — squall gusts are possible within the hour.\n")
	b.WriteString("A radar or Bat Galim alert follows if a cell heads for the bay.\n")
	b.WriteString("Lightning: EUMETSAT MTG · Radar: " + radarMapURL)
	return b.String()
}

// archiveAs stores an image under <kind>/YYYY/MM/DD/HHMM.png in the archive.
func (s *Service) archiveAs(kind string, at time.Time, body []byte) string {
	if s.ArchiveDir == "" {
		return ""
	}
	if free, ok := freeBytes(s.ArchiveDir); ok && free < minFreeBytes {
		return ""
	}
	rel := filepath.Join(kind, at.Format("2006/01/02"), at.Format("1504")+".png")
	full := filepath.Join(s.ArchiveDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		s.Log.Warn("squall archive", "err", err)
		return ""
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		s.Log.Warn("squall archive", "err", err)
		return ""
	}
	return rel
}

// ---- IMS warnings ----

const imsWarnCheckedKey = "squall_ims_warn_checked"

// imsWarningStep checks IMS warnings every 10 minutes and sends each new one
// covering Haifa Bay once, during alert hours.
func (s *Service) imsWarningStep(now time.Time) error {
	checked, _ := s.Store.GetSetting(imsWarnCheckedKey)
	if t, err := time.ParseInLocation("2006-01-02 15:04", checked, s.TZ); err != nil || now.Sub(t) >= 10*time.Minute {
		if err := s.Store.SetSetting(imsWarnCheckedKey, now.Format("2006-01-02 15:04")); err != nil {
			return err
		}
		body, err := s.Radar.get(imsWarningsURL)
		if err != nil {
			return err
		}
		items, err := ParseIMSWarnings(body, s.TZ)
		if err != nil {
			return err
		}
		for _, it := range items {
			if !it.ValidTo.After(now) {
				continue
			}
			if err := s.Store.SaveIMSWarning(store.IMSWarning{WID: it.WID, TypeID: it.TypeID, SeverityID: it.SeverityID,
				ValidFrom: it.ValidFrom, ValidTo: it.ValidTo, Regions: strings.Join(it.Regions, ","),
				TextEN: it.TextEN, TextHE: it.TextHE}); err != nil {
				return err
			}
		}
	}
	if !alertHour(now) {
		return nil
	}
	ws, err := s.Store.ListIMSWarnings(now)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if w.SentAt != nil {
			continue
		}
		it := IMSWarningItem{WID: w.WID, TypeID: w.TypeID, SeverityID: w.SeverityID, ValidFrom: w.ValidFrom,
			ValidTo: w.ValidTo, TextEN: w.TextEN, TextHE: w.TextHE}
		details := ""
		if s.Translate != nil && strings.TrimSpace(w.TextHE) != "" {
			if en, err := s.Translate.HebrewToEnglish(w.TextHE); err != nil {
				s.Log.Warn("ims warning translate", "wid", w.WID, "err", err)
			} else {
				details = en
			}
		}
		if err := s.send("ims", now, nil, nil, imsWarningMessage(it, details)); err != nil {
			return err
		}
		if err := s.Store.MarkIMSWarningSent(w.WID, now); err != nil {
			return err
		}
	}
	return nil
}

// imsThunderNote is appended to radar and lightning alerts while IMS has a
// thunderstorm or flash warning out for the bay.
func (s *Service) imsThunderNote(now time.Time) string {
	ws, err := s.Store.ListIMSWarnings(now)
	if err != nil {
		return ""
	}
	for _, w := range ws {
		if imsThunderKinds[w.TypeID] && !w.ValidFrom.After(now) {
			return fmt.Sprintf("\nIMS %s warning in force until %s.", imsWarningKinds[w.TypeID], w.ValidTo.Format("15:04"))
		}
	}
	return ""
}
