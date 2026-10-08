package squall

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// After every storm that reaches the spot the archived radar frames are
// replayed against what the meter measured. Two things are measured directly —
// how far ahead of the rain the wind arrived, and whether storms moved faster
// or slower than projected — and the alert thresholds are re-chosen by scoring
// every candidate over all stored storms and false alarms.

const (
	cooldown = 60 * time.Minute
	// An alert counts for a storm if it came at most this long before it…
	alertWindow = 150 * time.Minute
	// …and is a false alarm if no storm followed within this long — or was
	// already under way for less than stormTail (lightning flashing on as a
	// squall passes is the same storm, not a new false alarm).
	falseAlarmWindow = 120 * time.Minute
	stormTail        = 90 * time.Minute
	// usefulLeadMin is the warning needed to drive 5 min and rig for 15.
	usefulLeadMin = 20
	// Beyond this the rain core is too far away to be what caused the gust.
	stormCellKm = 30
)

// ReplayFrame is a stored frame ready to be re-analysed.
type ReplayFrame struct {
	FrameAt, AvailAt time.Time
	Grid             *Grid // nil when the frame was dry
	Motion           Motion
}

// LinkMotion fills each frame's motion from the frame before it.
func LinkMotion(frames []ReplayFrame, geo Geometry) {
	for i := 1; i < len(frames); i++ {
		dt := frames[i].FrameAt.Sub(frames[i-1].FrameAt).Minutes()
		if dt <= 0 || dt > 20 {
			continue
		}
		frames[i].Motion = MeasureMotion(frames[i-1].Grid, frames[i].Grid, geo, dt)
	}
}

// SimAlert is an alert the rules would have sent.
type SimAlert struct {
	At, FrameAt time.Time
	ETAMin      float64 // from At
}

// etaNow is the projected arrival measured from when the frame became available.
func etaNow(sc Scan, f ReplayFrame) (float64, bool) {
	if sc.Threat == nil {
		return 0, false
	}
	return sc.Threat.ETAMin - f.AvailAt.Sub(f.FrameAt).Minutes(), true
}

// ShouldAlert is the radar alert rule, shared by the live service and replays.
func ShouldAlert(sc Scan, f ReplayFrame, p Params) (float64, bool) {
	eta, ok := etaNow(sc, f)
	if !ok {
		return 0, false
	}
	return eta, eta <= p.Sane().TriggerMin && eta >= -5
}

// Simulate replays the radar alert rule over frames.
func Simulate(frames []ReplayFrame, p Params, geo Geometry) []SimAlert {
	var out []SimAlert
	for _, f := range frames {
		if f.Grid == nil {
			continue
		}
		if n := len(out); n > 0 && f.AvailAt.Sub(out[n-1].At) < cooldown {
			continue
		}
		if eta, ok := ShouldAlert(Analyse(f.Grid, f.Motion, p, geo), f, p); ok {
			out = append(out, SimAlert{At: f.AvailAt, FrameAt: f.FrameAt, ETAMin: eta})
		}
	}
	return out
}

// Score summarises how a rule set did over a replay.
type Score struct {
	Points      float64
	Storms      int
	Warned20    int // warned at least usefulLeadMin ahead
	WarnedLate  int // warned, but with less
	FalseAlarms int
	Leads       []float64 // per storm, minutes; -1 when not warned
}

func (s Score) String() string {
	return fmt.Sprintf("%d/%d storms warned ≥%d min ahead, %d late, %d false alarms",
		s.Warned20, s.Storms, usefulLeadMin, s.WarnedLate, s.FalseAlarms)
}

// Evaluate scores alerts against storm start times.
func Evaluate(alerts []SimAlert, storms []time.Time) Score {
	s := Score{Storms: len(storms)}
	used := make([]bool, len(alerts))
	for _, start := range storms {
		lead := -1.0
		for i, a := range alerts {
			if a.At.After(start) || start.Sub(a.At) > alertWindow {
				continue
			}
			used[i] = true
			if l := start.Sub(a.At).Minutes(); l > lead {
				lead = l
			}
		}
		s.Leads = append(s.Leads, lead)
		switch {
		case lead >= usefulLeadMin:
			s.Warned20++
			s.Points += 10 + math.Min(lead-usefulLeadMin, 20)*0.05
		case lead >= 10:
			s.WarnedLate++
			s.Points += 5
		case lead >= 0:
			s.WarnedLate++
			s.Points += 2
		}
	}
	for i, a := range alerts {
		if used[i] {
			continue
		}
		followed := false
		for _, start := range storms {
			ahead := !start.Before(a.At) && start.Sub(a.At) <= falseAlarmWindow
			during := !a.At.Before(start) && a.At.Sub(start) <= stormTail
			if ahead || during {
				followed = true
				break
			}
		}
		if !followed {
			s.FalseAlarms++
			s.Points -= 3
		}
	}
	return s
}

// Observation is what the radar showed around one storm.
type Observation struct {
	HasRadar bool    // frames exist around the start
	Storm    bool    // a heavy cell was close when the wind hit
	LeadKm   float64 // rain core distance when the wind hit
	CoreDBZ  int
	// SpeedFactor is projected / actual time to arrival over the frames before
	// it; 0 when no frame projected it.
	SpeedFactor float64
	SpeedKmh    float64
	HeadingDeg  float64
	// FirstSeen[dbz] is how many minutes before the start a cell of that
	// strength was first projected to reach the spot; -1 if never.
	FirstSeen map[int]float64
	// LightningLead is how early (from when the frame was published) lightning
	// came within the current range on the sea side; -1 if it never did.
	// LightningNearKm is the closest it came in the 2.5 h before.
	LightningLead   float64
	LightningNearKm float64
	// LightningBackfilled: the earliest in-range frame was only fetched later.
	LightningBackfilled bool
}

// ObserveLightning fills the lightning part of an observation.
func ObserveLightning(ob *Observation, samples []LightningSample, start time.Time, p Params) {
	ob.LightningLead, ob.LightningNearKm = -1, math.Inf(1)
	km, on := p.Sane().lightningOn()
	for _, s := range samples {
		before := start.Sub(s.AvailAt)
		if before < 0 || before > alertWindow {
			continue
		}
		if s.ThreatKm < ob.LightningNearKm {
			ob.LightningNearKm = s.ThreatKm
		}
		if on && s.ThreatKm <= km && before.Minutes() > ob.LightningLead {
			ob.LightningLead = before.Minutes()
			ob.LightningBackfilled = s.Backfilled
		}
	}
}

var probeDBZ = []int{25, 30, 35, 40, 45}

// Observe measures one storm against the frames.
func Observe(frames []ReplayFrame, start time.Time, p Params, geo Geometry) Observation {
	p = p.Sane()
	ob := Observation{FirstSeen: map[int]float64{}}
	// The frame nearest the start, within 10 min either side.
	var at *ReplayFrame
	for i := range frames {
		d := frames[i].FrameAt.Sub(start)
		if d < -10*time.Minute || d > 10*time.Minute {
			continue
		}
		if at == nil || absDur(d) < absDur(at.FrameAt.Sub(start)) {
			at = &frames[i]
		}
	}
	if at == nil {
		return ob
	}
	ob.HasRadar = true
	if at.Grid != nil {
		sc := Analyse(at.Grid, at.Motion, p, geo)
		if len(sc.Cells) > 0 && sc.Cells[0].DistKm <= stormCellKm {
			ob.Storm, ob.LeadKm, ob.CoreDBZ = true, sc.Cells[0].DistKm, sc.Cells[0].MaxDBZ
		}
	}
	if !ob.Storm {
		return ob
	}

	pp := p
	pp.LeadKm, pp.SpeedFactor = ob.LeadKm, 1
	var ratios []float64
	for _, dbz := range probeDBZ {
		ob.FirstSeen[dbz] = -1
	}
	for _, f := range frames {
		before := start.Sub(f.FrameAt).Minutes()
		if f.Grid == nil || before < 5 || before > alertWindow.Minutes() {
			continue
		}
		if f.Motion.OK && ob.SpeedKmh == 0 && before <= 30 {
			ob.SpeedKmh, ob.HeadingDeg = f.Motion.SpeedKmh, f.Motion.HeadingDeg
		}
		for _, dbz := range probeDBZ {
			q := pp
			q.DBZ = dbz
			sc := Analyse(f.Grid, f.Motion, q, geo)
			if sc.Threat == nil {
				continue
			}
			lead := start.Sub(f.AvailAt).Minutes()
			if lead > ob.FirstSeen[dbz] {
				ob.FirstSeen[dbz] = lead
			}
			if dbz == p.DBZ && sc.Threat.ETAMin > 0 && before >= 10 && before <= 90 {
				ratios = append(ratios, sc.Threat.ETAMin/before)
			}
		}
	}
	if len(ratios) > 0 {
		ob.SpeedFactor = math.Max(0.5, math.Min(2, median(ratios)))
	}
	return ob
}

// TuneResult is a re-fit of the rules.
type TuneResult struct {
	Before, After           Params
	ScoreBefore, ScoreAfter Score
	Changed                 bool
}

// Tune picks the rule set that scores best over the replay. leadKms and
// speedFactors are the measured values from every storm so far.
func Tune(frames []ReplayFrame, lightning []LightningSample, storms []time.Time, cur Params, leadKms, speedFactors []float64, geo Geometry) TuneResult {
	cur = cur.Sane()
	res := TuneResult{Before: cur, After: cur}
	score := func(p Params) Score {
		return Evaluate(mergeAlerts(Simulate(frames, p, geo), SimulateLightning(lightning, p)), storms)
	}
	res.ScoreBefore = score(cur)
	res.ScoreAfter = res.ScoreBefore

	leads := []float64{cur.LeadKm}
	if len(leadKms) > 0 {
		leads = appendUnique(leads, round1(median(leadKms)))
	}
	speeds := []float64{cur.SpeedFactor}
	if len(speedFactors) > 0 {
		speeds = appendUnique(speeds, round1(median(speedFactors)))
	}
	lightningKms := []float64{cur.LightningKm, -1, 15, 20, 30, 40}
	for _, dbz := range []int{30, 35, 40, 45} {
		for _, hit := range []float64{5, 8, 12} {
			for _, trig := range []float64{30, 40, 50} {
				for _, lead := range leads {
					for _, sp := range speeds {
						p := cur
						p.DBZ, p.HitKm, p.TriggerMin, p.LeadKm, p.SpeedFactor = dbz, hit, trig, lead, sp
						// The radar replay is the slow part: run it once per radar rule set.
						radar := Simulate(frames, p, geo)
						for _, lk := range lightningKms {
							q := p
							q.LightningKm = lk
							q = q.Sane()
							if q == cur {
								continue
							}
							sc := Evaluate(mergeAlerts(radar, SimulateLightning(lightning, q)), storms)
							// Only move for a real improvement: equal scores keep the rules stable.
							if sc.Points > res.ScoreAfter.Points+1e-9 {
								res.After, res.ScoreAfter, res.Changed = q, sc, true
							}
						}
					}
				}
			}
		}
	}
	// Measured physics is adopted even when the score is flat: with few storms
	// on record it is better evidence than the defaults.
	if !res.Changed && (len(leadKms) > 0 || len(speedFactors) > 0) {
		p := res.After
		p.LeadKm, p.SpeedFactor = leads[len(leads)-1], speeds[len(speeds)-1]
		if p != cur {
			if sc := score(p); sc.Points >= res.ScoreBefore.Points-1e-9 {
				res.After, res.ScoreAfter, res.Changed = p, sc, true
			}
		}
	}
	return res
}

// mergeAlerts combines alert streams in time order.
func mergeAlerts(a, b []SimAlert) []SimAlert {
	out := append(append([]SimAlert(nil), a...), b...)
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Diff describes what changed between two rule sets.
func Diff(a, b Params) string {
	var parts []string
	if a.DBZ != b.DBZ {
		parts = append(parts, fmt.Sprintf("heavy-rain threshold %d → %d dBZ", a.DBZ, b.DBZ))
	}
	if a.LeadKm != b.LeadKm {
		parts = append(parts, fmt.Sprintf("gust front ahead of rain %.1f → %.1f km", a.LeadKm, b.LeadKm))
	}
	if a.SpeedFactor != b.SpeedFactor {
		parts = append(parts, fmt.Sprintf("storm speed ×%.1f → ×%.1f", a.SpeedFactor, b.SpeedFactor))
	}
	if a.HitKm != b.HitKm {
		parts = append(parts, fmt.Sprintf("track must pass within %.0f → %.0f km", a.HitKm, b.HitKm))
	}
	if a.TriggerMin != b.TriggerMin {
		parts = append(parts, fmt.Sprintf("warn at ETA %.0f → %.0f min", a.TriggerMin, b.TriggerMin))
	}
	if a.LightningKm != b.LightningKm {
		parts = append(parts, fmt.Sprintf("lightning range %s → %s", lightningLabel(a.LightningKm), lightningLabel(b.LightningKm)))
	}
	if len(parts) == 0 {
		return "no change"
	}
	return strings.Join(parts, "; ")
}

func lightningLabel(km float64) string {
	if km < 0 {
		return "off"
	}
	return fmt.Sprintf("%.0f km", km)
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if n := len(s); n%2 == 1 {
		return s[n/2]
	} else {
		return (s[n/2-1] + s[n/2]) / 2
	}
}

func appendUnique(xs []float64, v float64) []float64 {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
