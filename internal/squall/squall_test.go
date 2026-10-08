package squall

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

var tz = func() *time.Location {
	l, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		panic(err)
	}
	return l
}()

func at(h, m, s int) time.Time { return time.Date(2026, 10, 6, h, m, s, 0, tz) }

func TestGeometryPlacesKiryatHaimOnTile(t *testing.T) {
	g := GeometryFor(KiryatHaim)
	if math.Abs(g.TX-240.3) > 0.5 || math.Abs(g.TY-322.7) > 0.5 {
		t.Fatalf("Kiryat Haim at (%.1f, %.1f), want about (240.3, 322.7)", g.TX, g.TY)
	}
	if math.Abs(g.KmPerPx-0.514) > 0.005 {
		t.Fatalf("km per px %.3f, want ~0.514", g.KmPerPx)
	}
	e, n := g.Km(g.TX-10, g.TY)
	if math.Abs(e+10*g.KmPerPx) > 1e-9 || n != 0 {
		t.Fatalf("10 px west = (%.2f, %.2f) km", e, n)
	}
}

func TestDecodeReadsUniversalBlue(t *testing.T) {
	im := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	im.Set(0, 0, color.NRGBA{0xff, 0xee, 0x00, 0xff}) // 35 dBZ, first yellow
	im.Set(1, 0, color.NRGBA{0xc1, 0x00, 0x00, 0xff}) // 50 dBZ
	im.Set(2, 0, color.NRGBA{0x82, 0x7b, 0x69, 0x49}) // 0 dBZ, translucent drizzle
	im.Set(3, 0, color.NRGBA{0, 0, 0, 0})
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	g, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := []int{g.At(0, 0), g.At(1, 0), g.At(2, 0), g.At(3, 0)}; got[0] != 35 || got[1] != 50 || got[2] != 0 || got[3] != 0 {
		t.Fatalf("decoded %v, want [35 50 0 0]", got)
	}
}

// blob paints a round cell of radius r px: core dBZ in the middle, 28 dBZ rim.
func blob(g *Grid, cx, cy, r float64, core int) {
	for y := int(cy - r - 3); y <= int(cy+r+3); y++ {
		for x := int(cx - r - 3); x <= int(cx+r+3); x++ {
			if x < 0 || y < 0 || x >= g.W || y >= g.H {
				continue
			}
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			switch {
			case d <= r:
				g.DBZ[y*g.W+x] = int8(core)
			case d <= r+3:
				g.DBZ[y*g.W+x] = 28
			}
		}
	}
}

func emptyGrid() *Grid { return &Grid{W: tileSize, H: tileSize, DBZ: make([]int8, tileSize*tileSize)} }

// cellAt paints a storm whose centre is (eastKm, northKm) from Kiryat Haim.
func cellAt(eastKm, northKm float64) *Grid {
	geo := GeometryFor(KiryatHaim)
	g := emptyGrid()
	blob(g, geo.TX+eastKm/geo.KmPerPx, geo.TY-northKm/geo.KmPerPx, 4, 45)
	return g
}

func TestMeasureMotionRecoversShift(t *testing.T) {
	geo := GeometryFor(KiryatHaim)
	// 7 km east and 1 km north in 10 min: 42 km/h toward ENE-ish.
	mo := MeasureMotion(cellAt(-40, -5), cellAt(-33, -4), geo, 10)
	if !mo.OK {
		t.Fatalf("motion not found: %+v", mo)
	}
	if math.Abs(mo.SpeedKmh-42.4) > 4 {
		t.Fatalf("speed %.1f km/h, want ~42", mo.SpeedKmh)
	}
	if math.Abs(mo.HeadingDeg-82) > 8 {
		t.Fatalf("heading %.0f°, want ~82° (moving east)", mo.HeadingDeg)
	}
}

func TestAnalyseProjectsArrival(t *testing.T) {
	geo := GeometryFor(KiryatHaim)
	p := DefaultParams()
	east := Motion{OK: true, VE: 40.0 / 60, SpeedKmh: 40, HeadingDeg: 90}

	// Cell centred 25 km west, radius ~2 km, heading straight for the spot.
	sc := Analyse(cellAt(-25, 0), east, p, geo)
	if sc.Threat == nil {
		t.Fatal("expected a threat")
	}
	// Nearest edge ~23 km out; reaches LeadKm (4) after ~19 km at 0.667 km/min.
	if eta := sc.Threat.ETAMin; eta < 25 || eta > 32 {
		t.Fatalf("ETA %.1f min, want ~28", eta)
	}
	if sc.Threat.MaxDBZ != 45 {
		t.Fatalf("threat dBZ %d", sc.Threat.MaxDBZ)
	}

	// The same cell moving north passes far to the west: no threat.
	north := Motion{OK: true, VN: 40.0 / 60, SpeedKmh: 40}
	if sc := Analyse(cellAt(-25, 0), north, p, geo); sc.Threat != nil {
		t.Fatalf("cell moving north flagged: ETA %.1f", sc.Threat.ETAMin)
	}

	// Moving east but 20 km south: it misses.
	if sc := Analyse(cellAt(-25, -20), east, p, geo); sc.Threat != nil {
		t.Fatalf("cell passing 20 km south flagged: pass %.1f km", sc.Threat.PassKm)
	}

	// Already overhead: arriving now, motion or not.
	if sc := Analyse(cellAt(0, 0), Motion{}, p, geo); sc.Threat == nil || sc.Threat.ETAMin != 0 {
		t.Fatal("overhead cell should be ETA 0")
	}

	// Rain under the threshold is not a threat.
	weak := emptyGrid()
	blob(weak, geo.TX-20/geo.KmPerPx, geo.TY, 4, 30)
	if sc := Analyse(weak, east, p, geo); sc.Threat != nil {
		t.Fatal("30 dBZ rain flagged with a 35 dBZ threshold")
	}
}

// Kiryat Haim on 2026-10-06, as stored.
func khToday() []models.WindReading {
	rows := []struct {
		h, m, s int
		w, g    float64
		d       float64
	}{
		{9, 40, 0, 1, 2, 120}, {9, 45, 0, 2, 3, 81}, {9, 50, 0, 3, 5, 77}, {9, 55, 0, 5, 6, 187},
		{10, 5, 0, 8, 9, 237}, {10, 7, 15, 19, 21, 260}, {10, 10, 0, 17, 19, 285}, {10, 15, 0, 27, 29, 280},
		{10, 20, 0, 20, 21, 287}, {10, 25, 0, 18, 19, 290}, {10, 30, 1, 18, 19, 277}, {10, 35, 0, 18, 19, 287},
		{10, 40, 0, 14, 15, 311}, {10, 45, 0, 6, 7, 316}, {10, 50, 0, 8, 9, 300}, {10, 55, 0, 7, 8, 340},
		{11, 0, 0, 6, 7, 342},
	}
	var out []models.WindReading
	for _, r := range rows {
		out = append(out, models.WindReading{Period: at(r.h, r.m, r.s), Location: "kh", Wind: r.w, Gust: r.g, WindDir: r.d})
	}
	return out
}

func TestFindEventsOn6October(t *testing.T) {
	ev := FindEvents(khToday())
	if len(ev) != 1 {
		t.Fatalf("got %d events, want 1", len(ev))
	}
	e := ev[0]
	if !e.Start.Equal(at(10, 7, 15)) || e.PeakWind != 27 || e.PeakGust != 29 || !e.End.Equal(at(10, 45, 0)) {
		t.Fatalf("event %+v", e)
	}
}

func TestFindEventsIgnoresSeaBreezeBuild(t *testing.T) {
	var rows []models.WindReading
	for i := 0; i <= 60; i += 5 { // 6 → 18 kt over an hour
		rows = append(rows, models.WindReading{Period: at(12, 0, 0).Add(time.Duration(i) * time.Minute), Wind: 6 + float64(i)/5, Gust: 8 + float64(i)/5})
	}
	if ev := FindEvents(rows); len(ev) != 0 {
		t.Fatalf("gradual sea breeze flagged as a squall: %+v", ev)
	}
}

func TestConfirmBatGalimOn6October(t *testing.T) {
	temp := func(v float64) *float64 { return &v }
	rows := []models.WindReading{
		{Period: at(9, 45, 0), Wind: 5.4, Gust: 10.3, Temp: temp(30.5)},
		{Period: at(9, 50, 0), Wind: 4.9, Gust: 9.2, Temp: temp(30.7)},
		{Period: at(9, 55, 0), Wind: 10.3, Gust: 21.1, Temp: temp(24.3), WindDir: 260},
	}
	c := Confirm("bg", "Bat Galim", rows, at(9, 56, 0))
	if c == nil {
		t.Fatal("expected Bat Galim to confirm at 09:55")
	}
	if math.Abs(c.TempDrop-6.4) > 0.01 {
		t.Fatalf("temp drop %.2f, want 6.4", c.TempDrop)
	}
	// Five minutes earlier: calm, nothing to confirm.
	if c := Confirm("bg", "Bat Galim", rows[:2], at(9, 51, 0)); c != nil {
		t.Fatal("calm readings confirmed")
	}
	// Stale: the last reading is 20 min old.
	if c := Confirm("bg", "Bat Galim", rows, at(10, 15, 0)); c != nil {
		t.Fatal("stale reading confirmed")
	}
}

func TestEvaluateCountsLeadAndFalseAlarms(t *testing.T) {
	storm := at(10, 7, 0)
	alerts := []SimAlert{
		{At: at(6, 0, 0)},   // nothing follows: false alarm
		{At: at(9, 40, 0)},  // 27 min ahead
		{At: at(10, 30, 0)}, // during the storm: not a false alarm
		{At: at(13, 0, 0)},  // hours after it: false alarm
	}
	s := Evaluate(alerts, []time.Time{storm})
	if s.Warned20 != 1 || s.FalseAlarms != 2 || math.Abs(s.Leads[0]-27) > 0.01 {
		t.Fatalf("score %+v", s)
	}
}

// syntheticStorm: a cell crossing from 50 km west at 40 km/h, frames every 10
// min, each available 7 min after its scan. The wind hits when the core is
// 3 km out.
func syntheticStorm(geo Geometry) ([]ReplayFrame, time.Time) {
	start := at(8, 0, 0)
	var frames []ReplayFrame
	var hit time.Time
	for i := 0; i <= 9; i++ {
		ft := start.Add(time.Duration(i*10) * time.Minute)
		centre := -50 + 40.0/60*float64(i*10)
		frames = append(frames, ReplayFrame{FrameAt: ft, AvailAt: ft.Add(7 * time.Minute), Grid: cellAt(centre, 0)})
	}
	// Edge (centre + ~2 km) reaches 3 km out when centre = -5 km: 67.5 min in.
	hit = start.Add(67*time.Minute + 30*time.Second)
	LinkMotion(frames, geo)
	return frames, hit
}

func TestReplayWarnsAndTunerMeasuresLead(t *testing.T) {
	geo := GeometryFor(KiryatHaim)
	frames, hit := syntheticStorm(geo)
	p := DefaultParams()

	s := Evaluate(Simulate(frames, p, geo), []time.Time{hit})
	if s.Warned20 != 1 {
		t.Fatalf("default rules did not warn ≥20 min ahead: %+v", s)
	}

	ob := Observe(frames, hit, p, geo)
	if !ob.Storm {
		t.Fatalf("storm not seen: %+v", ob)
	}
	if ob.LeadKm < 1.5 || ob.LeadKm > 4.5 {
		t.Fatalf("lead distance %.1f km, want ~3", ob.LeadKm)
	}
	if ob.SpeedFactor < 0.8 || ob.SpeedFactor > 1.25 {
		t.Fatalf("speed factor %.2f, want ~1 (motion measured correctly)", ob.SpeedFactor)
	}
	if ob.FirstSeen[35] < usefulLeadMin {
		t.Fatalf("first seen only %.0f min ahead", ob.FirstSeen[35])
	}

	res := Tune(frames, nil, []time.Time{hit}, p, []float64{ob.LeadKm}, []float64{ob.SpeedFactor}, geo)
	if res.ScoreAfter.Points < res.ScoreBefore.Points {
		t.Fatalf("tuning made it worse: %v → %v", res.ScoreBefore, res.ScoreAfter)
	}
	if res.ScoreAfter.Warned20 != 1 {
		t.Fatalf("tuned rules lost the warning: %v", res.ScoreAfter)
	}
}

func TestTunerDropsFalseAlarms(t *testing.T) {
	geo := GeometryFor(KiryatHaim)
	// A storm that tracks east 9 km south of the spot (its edge passes ~7 km
	// away) and, as it turned out, brought no squall to the spot.
	var frames []ReplayFrame
	for i := 0; i <= 9; i++ {
		ft := at(8, 0, 0).Add(time.Duration(i*10) * time.Minute)
		frames = append(frames, ReplayFrame{FrameAt: ft, AvailAt: ft.Add(7 * time.Minute),
			Grid: cellAt(-50+40.0/60*float64(i*10), -9)})
	}
	LinkMotion(frames, geo)
	res := Tune(frames, nil, nil, DefaultParams(), nil, nil, geo)
	if res.ScoreBefore.FalseAlarms == 0 {
		t.Fatal("expected the default rules (pass within 8 km) to alert")
	}
	if res.ScoreAfter.FalseAlarms != 0 || !res.Changed {
		t.Fatalf("tuner kept the false alarm: %v (%s)", res.ScoreAfter, Diff(res.Before, res.After))
	}
	if res.After.HitKm >= 8 && res.After.DBZ <= 45 {
		t.Fatalf("expected a tighter rule, got %+v", res.After)
	}
}

func TestParamsSaneClamps(t *testing.T) {
	p := Params{DBZ: 5, HitKm: 100, LeadKm: -3, TriggerMin: 0, SpeedFactor: 9, MinCellPx: 0}.Sane()
	if p.DBZ != 35 || p.HitKm != 20 || p.LeadKm != 0 || p.TriggerMin != 40 || p.SpeedFactor != 2 || p.MinCellPx != 4 {
		t.Fatalf("clamped %+v", p)
	}
	// Lightning: unset means the default, negative means off, and it is bounded.
	if p.LightningKm != 30 {
		t.Fatalf("unset lightning range %v, want 30", p.LightningKm)
	}
	if q := (Params{LightningKm: -5}).Sane(); q.LightningKm != -1 {
		t.Fatalf("off lightning range %v, want -1", q.LightningKm)
	}
	if q := (Params{LightningKm: 500}).Sane(); q.LightningKm != 60 {
		t.Fatalf("huge lightning range %v, want 60", q.LightningKm)
	}
}

func TestCompass(t *testing.T) {
	for deg, want := range map[float64]string{0: "N", 359: "N", 90: "E", 247: "WSW", 280: "W", 315: "NW"} {
		if got := compass(deg); got != want {
			t.Fatalf("compass(%v) = %s, want %s", deg, got, want)
		}
	}
}
