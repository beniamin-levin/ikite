package squall

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/store"
)

// flashPNG paints a flash (3×3 px) at a lat/lon on the lightning window.
func flashPNG(t *testing.T, pts ...[2]float64) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, lightningPxW, lightningPxH))
	for _, p := range pts {
		x := int((p[1] - lightningW) / (lightningE - lightningW) * lightningPxW)
		y := int((lightningN - p[0]) / (lightningN - lightningS) * lightningPxH)
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				im.Set(x+dx, y+dy, color.NRGBA{254, 243, 173, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnalyseLightningSeaSideOnly(t *testing.T) {
	// 25 km due west of Kiryat Haim (over the sea) and 15 km east (inland).
	west := [2]float64{KiryatHaim.Lat, KiryatHaim.Lon - 25/(111.32*math.Cos(KiryatHaim.Lat*math.Pi/180))}
	east := [2]float64{KiryatHaim.Lat, KiryatHaim.Lon + 15/(111.32*math.Cos(KiryatHaim.Lat*math.Pi/180))}
	sc, err := AnalyseLightning(flashPNG(t, west, east), KiryatHaim)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(sc.NearestKm-15) > 1.5 || math.Abs(sc.NearestBrg-90) > 8 {
		t.Fatalf("nearest %.1f km at %.0f°, want ~15 km E", sc.NearestKm, sc.NearestBrg)
	}
	if math.Abs(sc.ThreatKm-25) > 1.5 || math.Abs(sc.ThreatBrg-270) > 8 {
		t.Fatalf("threat %.1f km at %.0f°, want ~25 km W (the inland flash is not a threat)", sc.ThreatKm, sc.ThreatBrg)
	}

	// Inland only: no threat.
	sc, _ = AnalyseLightning(flashPNG(t, east), KiryatHaim)
	if !math.IsInf(sc.ThreatKm, 1) {
		t.Fatalf("inland flash 15 km E flagged at %.1f km", sc.ThreatKm)
	}
	// Very close, any side: a threat.
	near := [2]float64{KiryatHaim.Lat, KiryatHaim.Lon + 8/(111.32*math.Cos(KiryatHaim.Lat*math.Pi/180))}
	if sc, _ = AnalyseLightning(flashPNG(t, near), KiryatHaim); sc.ThreatKm > 9 {
		t.Fatalf("flash 8 km E not a threat: %.1f km", sc.ThreatKm)
	}
}

// What EUMETSAT showed around Kiryat Haim on 2026-10-06 (sea-side distance by
// frame, UTC), before the 10:07 squall.
func lightningOn6October() []LightningSample {
	km := map[string]float64{"05:55": 25.3, "06:00": 22.7, "06:15": 20.4, "06:20": 22.4, "06:25": 22.4,
		"06:30": 22.4, "06:35": 22.4, "06:40": 8.2, "06:45": 7.3, "06:50": 11.4, "06:55": 11.4, "07:00": 11.4}
	var out []LightningSample
	for t := time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC); t.Before(time.Date(2026, 10, 6, 7, 30, 0, 0, time.UTC)); t = t.Add(5 * time.Minute) {
		d, ok := km[t.Format("15:04")]
		if !ok {
			d = math.Inf(1)
		}
		out = append(out, LightningSample{FrameAt: t.In(tz), AvailAt: t.Add(12 * time.Minute).In(tz), ThreatKm: d})
	}
	return out
}

func TestLightningWouldHaveWarnedOn6October(t *testing.T) {
	storm := at(10, 7, 15)
	alerts := SimulateLightning(lightningOn6October(), DefaultParams())
	s := Evaluate(alerts, []time.Time{storm})
	if s.Warned20 != 1 {
		t.Fatalf("no ≥20 min lightning warning: %+v", s)
	}
	// First frame inside 30 km: 05:55 UTC, published ~09:07 local: ~60 min ahead.
	if s.Leads[0] < 55 || s.Leads[0] > 65 {
		t.Fatalf("lead %.0f min, want ~60", s.Leads[0])
	}
	var ob Observation
	ObserveLightning(&ob, lightningOn6October(), storm, DefaultParams())
	if ob.LightningLead < 55 || ob.LightningNearKm > 7.5 {
		t.Fatalf("observed %+v", ob)
	}
}

func TestTunerSwitchesLightningOffWhenItOnlyCriesWolf(t *testing.T) {
	// The same lightning, but nothing came of it at the spot.
	res := Tune(nil, lightningOn6October(), nil, DefaultParams(), nil, nil, GeometryFor(KiryatHaim))
	if res.ScoreBefore.FalseAlarms == 0 {
		t.Fatal("expected the default lightning rule to alert")
	}
	if res.ScoreAfter.FalseAlarms >= res.ScoreBefore.FalseAlarms {
		t.Fatalf("tuner kept the false alarms: %v (%s)", res.ScoreAfter, Diff(res.Before, res.After))
	}
}

func TestParseIMSWarningsKeepsBayWarnings(t *testing.T) {
	body, err := os.ReadFile("testdata/ims_warnings_2026-10-06.json")
	if err != nil {
		t.Fatal(err)
	}
	items, err := ParseIMSWarnings(body, tz)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d bay warnings, want 1", len(items))
	}
	w := items[0]
	if w.WID != 143654 || w.TypeID != 51 || !w.ValidFrom.Equal(at(12, 0, 0)) || !w.ValidTo.Equal(at(15, 0, 0)) {
		t.Fatalf("warning %+v", w)
	}
	if len(w.Regions) == 0 || w.TextEN == "" || w.TextHE == "" {
		t.Fatalf("warning missing regions or text: %+v", w)
	}
	msg := imsWarningMessage(w, "Impressive rain fell in the north in the last two hours.")
	if want := "Valid Tue 12:00 – Tue 15:00."; !bytes.Contains([]byte(msg), []byte(want)) {
		t.Fatalf("message lacks %q:\n%s", want, msg)
	}
	if !bytes.Contains([]byte(msg), []byte("Details (auto-translated): Impressive rain")) {
		t.Fatalf("translated details missing:\n%s", msg)
	}
	// English only: no Hebrew, even when the translation failed or came back untranslated.
	for _, details := range []string{"", w.TextHE} {
		if m := imsWarningMessage(w, details); hasHebrew(m) {
			t.Fatalf("message contains Hebrew (details %q):\n%s", details, m)
		}
	}
}

func TestParseIMSWarningsEmpty(t *testing.T) {
	// What IMS sends once nothing is in force.
	items, err := ParseIMSWarnings([]byte(`{"data":{"full_warnings_data":[],"distinct_warnings":[]},"method":"GET"}`), tz)
	if err != nil || len(items) != 0 {
		t.Fatalf("empty warnings: %v, %d items", err, len(items))
	}
}

// IMS re-issues a warning under a new id when it rewords it: only the first of
// each kind/severity/window is sent; a changed window or severity is news.
func TestSplitIMSRepeats(t *testing.T) {
	tz := time.FixedZone("IL", 3*3600)
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, tz) }
	sent := at(8, 19)
	w := func(wid int64, sev, fromD, fromH, toD, toH int, s *time.Time) store.IMSWarning {
		return store.IMSWarning{WID: wid, TypeID: 3, SeverityID: sev, ValidFrom: at(fromD, fromH), ValidTo: at(toD, toH), SentAt: s}
	}
	ws := []store.IMSWarning{
		w(1, 3, 8, 10, 8, 22, &sent), // already sent
		w(2, 3, 8, 10, 8, 22, nil),   // reworded repeat of 1
		w(3, 3, 8, 22, 9, 10, nil),   // new window: send
		w(4, 3, 8, 22, 9, 10, nil),   // repeat of 3
		w(5, 3, 8, 22, 9, 10, nil),   // repeat of 3
		w(6, 4, 8, 22, 9, 10, nil),   // upgraded to orange: send
	}
	send, rep := splitIMSRepeats(ws)
	ids := func(xs []store.IMSWarning) (out []int64) {
		for _, x := range xs {
			out = append(out, x.WID)
		}
		return
	}
	if got := ids(send); len(got) != 2 || got[0] != 3 || got[1] != 6 {
		t.Fatalf("send %v, want [3 6]", got)
	}
	if got := ids(rep); len(got) != 3 || got[0] != 2 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("repeats %v, want [2 4 5]", got)
	}
}
