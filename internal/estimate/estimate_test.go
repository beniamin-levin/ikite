package estimate

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// history builds n hours where the meter reads `real` and each model forecasts
// real+bias, with the given per-hour noise pattern (repeated).
func history(n int, real float64, models map[string]struct {
	bias  float64
	noise []float64
}) []Past {
	var out []Past
	for i := 0; i < n; i++ {
		p := Past{Hour: 6 + i%16, Wind: real, GustPeak: real * 1.5}
		for name, m := range models {
			e := m.bias + m.noise[i%len(m.noise)]
			p.Forecasts = append(p.Forecasts, Forecast{Model: name, Wind: real + e, Gust: real*1.5 + 2*e, Dir: -1})
		}
		out = append(out, p)
	}
	return out
}

func TestFitRemovesBiasAndTrustsTheSteadyModel(t *testing.T) {
	h := history(100, 12, map[string]struct {
		bias  float64
		noise []float64
	}{
		"steady": {bias: +3, noise: []float64{-0.5, 0.5}}, // always 3 kt high, but consistent
		"noisy":  {bias: 0, noise: []float64{-4, 4}},      // right on average, all over the place
	})
	c := Fit(h)
	if !c.Usable() {
		t.Fatal("two models with 100 hours each should be usable")
	}
	s, n := c.Models["steady"], c.Models["noisy"]
	if !near(s.Bias, 3, 1e-9) || !near(n.Bias, 0, 1e-9) {
		t.Fatalf("bias steady=%v noisy=%v", s.Bias, n.Bias)
	}
	if !(s.Var < n.Var) {
		t.Fatalf("steady should be trusted more: var %v vs %v", s.Var, n.Var)
	}

	// The steady model says 18: corrected to 15. The noisy one says 10. The
	// blend should sit close to the steady model's corrected value.
	p, ok := c.Estimate(12, []Forecast{{Model: "steady", Wind: 18, Dir: -1}, {Model: "noisy", Wind: 10, Dir: -1}})
	if !ok {
		t.Fatal("estimate not produced")
	}
	wSteady, wNoisy := 1/s.Var, 1/n.Var
	want := (wSteady*15 + wNoisy*10) / (wSteady + wNoisy)
	if !near(p.Wind, want, 1e-9) || p.Wind < 14 {
		t.Fatalf("wind = %.2f, want %.2f (weighted toward the steady model)", p.Wind, want)
	}
	if p.Models != 2 || !(p.Low < p.Wind && p.Wind < p.High) {
		t.Fatalf("point = %+v", p)
	}
}

func TestNeedsTwoCalibratedModels(t *testing.T) {
	h := history(100, 10, map[string]struct {
		bias  float64
		noise []float64
	}{"only": {bias: 1, noise: []float64{0}}})
	if Fit(h).Usable() {
		t.Fatal("one model is not a blend")
	}

	// Two models, but one has too little history to be trusted.
	h = history(100, 10, map[string]struct {
		bias  float64
		noise []float64
	}{"a": {bias: 1, noise: []float64{0}}, "b": {bias: 0, noise: []float64{1, -1}}})
	for i := MinHours - 1; i < len(h); i++ {
		h[i].Forecasts = h[i].Forecasts[:1]
		if h[i].Forecasts[0].Model != "a" {
			h[i].Forecasts[0].Model = "a"
		}
	}
	c := Fit(h)
	if m, ok := c.Models["b"]; ok {
		t.Fatalf("model b has only %d hours (< %d) and must be left out", m.Hours, MinHours)
	}
	if c.Usable() {
		t.Fatal("with b left out only one model remains — not usable")
	}

	// An hour where only one calibrated model forecasts cannot be blended.
	full := Fit(history(100, 10, map[string]struct {
		bias  float64
		noise []float64
	}{"a": {bias: 1, noise: []float64{0.5, -0.5}}, "b": {bias: 0, noise: []float64{1, -1}}}))
	if _, ok := full.Estimate(12, []Forecast{{Model: "a", Wind: 12, Dir: -1}, {Model: "unknown", Wind: 30, Dir: -1}}); ok {
		t.Fatal("only one calibrated model present — no estimate expected")
	}
	// The same model twice is still one vote.
	if _, ok := full.Estimate(12, []Forecast{{Model: "a", Wind: 12, Dir: -1}, {Model: "a", Wind: 12, Dir: -1}}); ok {
		t.Fatal("a duplicated model must not count as two")
	}
}

func TestRangeWidensWhenModelsDisagree(t *testing.T) {
	c := Fit(history(100, 12, map[string]struct {
		bias  float64
		noise []float64
	}{"a": {bias: 0, noise: []float64{1, -1}}, "b": {bias: 0, noise: []float64{1, -1}}}))
	agree, _ := c.Estimate(12, []Forecast{{Model: "a", Wind: 12, Dir: -1}, {Model: "b", Wind: 12, Dir: -1}})
	split, _ := c.Estimate(12, []Forecast{{Model: "a", Wind: 6, Dir: -1}, {Model: "b", Wind: 18, Dir: -1}})
	if !(split.High-split.Low > agree.High-agree.Low) {
		t.Fatalf("range should widen with disagreement: agree %.1f, split %.1f", agree.High-agree.Low, split.High-split.Low)
	}
	if agree.High-agree.Low < 2*minHalfRange-1e-9 {
		t.Fatalf("range narrower than the floor: %.2f", agree.High-agree.Low)
	}
	calm, _ := c.Estimate(12, []Forecast{{Model: "a", Wind: 0.5, Dir: -1}, {Model: "b", Wind: 0.5, Dir: -1}})
	if calm.Low < 0 {
		t.Fatalf("negative wind in range: %+v", calm)
	}
}

func TestDirectionAveragesAcrossNorth(t *testing.T) {
	c := Fit(history(100, 12, map[string]struct {
		bias  float64
		noise []float64
	}{"a": {bias: 0, noise: []float64{1, -1}}, "b": {bias: 0, noise: []float64{1, -1}}}))
	// 350° and 10° are 20° apart around north. A plain average says 180 — due
	// south, the opposite direction.
	p, _ := c.Estimate(12, []Forecast{{Model: "a", Wind: 12, Dir: 350}, {Model: "b", Wind: 12, Dir: 10}})
	if !(p.Dir < 1 || p.Dir > 359) {
		t.Fatalf("dir = %.1f, want ~0 (north)", p.Dir)
	}
	none, _ := c.Estimate(12, []Forecast{{Model: "a", Wind: 12, Dir: -1}, {Model: "b", Wind: 12, Dir: -1}})
	if none.Dir >= 0 {
		t.Fatalf("no model gave a direction, want -1, got %.1f", none.Dir)
	}
}

func TestGustCorrectedAndNeverBelowWind(t *testing.T) {
	// Models whose gusts run hot by a steady amount — the UKMO pattern.
	var h []Past
	for i := 0; i < 100; i++ {
		e := float64(i%3 - 1)
		h = append(h, Past{Hour: 12, Wind: 12, GustPeak: 18, Forecasts: []Forecast{
			{Model: "a", Wind: 12 + e, Gust: 26 + e, Dir: -1},
			{Model: "b", Wind: 12 - e, Gust: 24 - e, Dir: -1},
		}})
	}
	c := Fit(h)
	p, _ := c.Estimate(12, []Forecast{{Model: "a", Wind: 12, Gust: 26, Dir: -1}, {Model: "b", Wind: 12, Gust: 24, Dir: -1}})
	if !near(p.Gust, 18, 1e-6) {
		t.Fatalf("gust = %.2f, want the measured 18 once the +8/+6 bias is removed", p.Gust)
	}
	q, _ := c.Estimate(12, []Forecast{{Model: "a", Wind: 30, Gust: 26, Dir: -1}, {Model: "b", Wind: 30, Gust: 24, Dir: -1}})
	if q.Gust < q.Wind {
		t.Fatalf("gust %.1f below wind %.1f", q.Gust, q.Wind)
	}
}

// The sea-breeze pattern: a model that is right in the morning but 4 kt high at
// the midday peak must be corrected by 4 at 14:00 and by nothing at 08:00.
func TestBiasDependsOnTimeOfDay(t *testing.T) {
	var h []Past
	for d := 0; d < 20; d++ {
		for hour := 6; hour <= 21; hour++ {
			bias := 0.0
			if hour >= 11 && hour <= 16 {
				bias = 4
			}
			// Alternating by day, so the noise cancels within every block.
			e := float64(1 - 2*(d%2))
			h = append(h, Past{Hour: hour, Wind: 12, Forecasts: []Forecast{
				{Model: "a", Wind: 12 + bias + e, Dir: -1},
				{Model: "b", Wind: 12 + bias - e, Dir: -1},
			}})
		}
	}
	c := Fit(h)
	a := c.Models["a"]
	if !a.Block[1].OK || !near(a.Block[1].Bias, 4, 1e-9) || !near(a.Block[0].Bias, 0, 1e-9) {
		t.Fatalf("blocks = %+v", a.Block)
	}
	fs := []Forecast{{Model: "a", Wind: 16, Dir: -1}, {Model: "b", Wind: 16, Dir: -1}}
	mid, _ := c.Estimate(14, fs)
	morning, _ := c.Estimate(8, fs)
	if !near(mid.Wind, 12, 1e-9) {
		t.Fatalf("14:00 estimate = %.2f, want 12 (16 minus the midday +4)", mid.Wind)
	}
	if !near(morning.Wind, 16, 1e-9) {
		t.Fatalf("08:00 estimate = %.2f, want 16 (no morning bias)", morning.Wind)
	}
}

// A block without enough history falls back to the whole-day bias rather than
// trusting a handful of hours.
func TestSparseBlockFallsBackToWholeDay(t *testing.T) {
	var h []Past
	for i := 0; i < 60; i++ {
		hour := 12 // all history at midday...
		if i < MinHours-1 {
			hour = 7 // ...except a few morning hours, fewer than MinHours
		}
		e := float64(i%3 - 1)
		h = append(h, Past{Hour: hour, Wind: 10, Forecasts: []Forecast{
			{Model: "a", Wind: 13 + e, Dir: -1}, {Model: "b", Wind: 13 - e, Dir: -1},
		}})
	}
	c := Fit(h)
	if c.Models["a"].Block[0].OK {
		t.Fatalf("morning block has %d hours, below MinHours — must not be used", MinHours-1)
	}
	bias, _ := c.Models["a"].at(7)
	if !near(bias, c.Models["a"].Bias, 1e-9) {
		t.Fatalf("morning bias %.2f should fall back to whole-day %.2f", bias, c.Models["a"].Bias)
	}
}

// Gusts are corrected by time of day too: a model whose gusts run 6 kt hot at
// midday but are right in the evening (the Kiryat Yam pattern).
func TestGustBiasDependsOnTimeOfDay(t *testing.T) {
	var h []Past
	for d := 0; d < 20; d++ {
		for hour := 6; hour <= 21; hour++ {
			hot := 0.0
			if hour >= 11 && hour <= 16 {
				hot = 6
			}
			e := float64(1 - 2*(d%2))
			h = append(h, Past{Hour: hour, Wind: 12, GustPeak: 18, Forecasts: []Forecast{
				{Model: "a", Wind: 12 + e, Gust: 18 + hot + e, Dir: -1},
				{Model: "b", Wind: 12 - e, Gust: 18 + hot - e, Dir: -1},
			}})
		}
	}
	c := Fit(h)
	fs := []Forecast{{Model: "a", Wind: 12, Gust: 24, Dir: -1}, {Model: "b", Wind: 12, Gust: 24, Dir: -1}}
	mid, _ := c.Estimate(14, fs)
	eve, _ := c.Estimate(19, fs)
	if !near(mid.Gust, 18, 1e-9) {
		t.Fatalf("14:00 gust = %.2f, want 18 (24 minus the midday +6)", mid.Gust)
	}
	if !near(eve.Gust, 24, 1e-9) {
		t.Fatalf("19:00 gust = %.2f, want 24 (no evening bias)", eve.Gust)
	}
}
