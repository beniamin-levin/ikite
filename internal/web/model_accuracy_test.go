package web

import (
	"math"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func f64(v float64) *float64 { return &v }

func TestScoreModels(t *testing.T) {
	jer, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	// 30 days x hours 9..18 = 300 hours of meter data: 12 kt mean, 20 kt peak gusts.
	var obs []models.ObservedHour
	var fc []models.WindForecastRow
	for d := 0; d < 30; d++ {
		for h := 9; h <= 18; h++ {
			// The meter hour arrives in UTC with wall-clock digits; the forecast
			// period in Jerusalem time. They must still pair up.
			obs = append(obs, models.ObservedHour{
				Hour: time.Date(2026, 9, 1+d, h, 0, 0, 0, time.UTC), Wind: 12, GustPeak: 20, Samples: 60,
			})
			p := time.Date(2026, 9, 1+d, h, 0, 0, 0, jer)
			// "good": spot on for wind, gusts 2 kt high.
			fc = append(fc, models.WindForecastRow{IDModel: 3, Model: "gfs", Period: p, Wind: f64(12), Gust: f64(22)})
			// "hot": 3 kt high on wind and a gust field running at 2.5x.
			fc = append(fc, models.WindForecastRow{IDModel: 1000004, Model: "ukmo", Period: p, Wind: f64(15), Gust: f64(37.5)})
		}
		// Outside kiting hours: must be ignored even though it is badly wrong.
		fc = append(fc, models.WindForecastRow{IDModel: 3, Model: "gfs",
			Period: time.Date(2026, 9, 1+d, 22, 0, 0, 0, jer), Wind: f64(40), Gust: f64(50)})
	}
	// A model with too little history is left out rather than ranked on noise.
	for h := 9; h <= 12; h++ {
		fc = append(fc, models.WindForecastRow{IDModel: 45, Model: "icon13",
			Period: time.Date(2026, 9, 1, h, 0, 0, 0, jer), Wind: f64(12), Gust: f64(20)})
	}

	a := scoreModels(obs, fc, 9, 18)

	if len(a.Scores) != 2 {
		t.Fatalf("want 2 scored models (icon13 below the minimum), got %d: %+v", len(a.Scores), a.Scores)
	}
	if a.Days != 30 {
		t.Fatalf("days = %d, want 30", a.Days)
	}
	best, hot := a.Scores[0], a.Scores[1]
	if best.Model != "gfs" || hot.Model != "ukmo" {
		t.Fatalf("order = %s, %s; want gfs (accurate) before ukmo", best.Model, hot.Model)
	}
	if best.Hours != 300 {
		t.Fatalf("gfs hours = %d, want 300 — the 22:00 rows must not count", best.Hours)
	}
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	if !near(best.WindMAE, 0) || !near(best.WindBias, 0) || !near(best.GustBias, 2) {
		t.Fatalf("gfs = %+v", best)
	}
	if !near(hot.WindBias, 3) || !near(hot.WindMAE, 3) || !near(hot.GustBias, 17.5) || !near(hot.GustRatio, 2.5) {
		t.Fatalf("ukmo = %+v", hot)
	}
	if !a.HasObsRatio || !near(a.ObsGustRatio, 20.0/12.0) {
		t.Fatalf("observed gust ratio = %v (%v)", a.ObsGustRatio, a.HasObsRatio)
	}

	v := buildAccuracyView(a)
	if v == nil || !v.Rows[0].Best || v.Rows[1].Best {
		t.Fatalf("view best flags wrong: %+v", v)
	}
	if v.Rows[1].WindBias != "+3.0" || v.Rows[1].BiasClass != "acc-over" || v.Rows[1].GustRatio != "2.50×" {
		t.Fatalf("ukmo row = %+v", v.Rows[1])
	}
}

func TestBuildAccuracyViewNeedsTwoModels(t *testing.T) {
	if v := buildAccuracyView(modelAccuracy{Scores: []modelScore{{Model: "gfs", Hours: 100}}}); v != nil {
		t.Fatalf("one model is nothing to compare, want nil view, got %+v", v)
	}
	if v := buildAccuracyView(modelAccuracy{}); v != nil {
		t.Fatalf("no data, want nil view")
	}
}

// openWRF publishes half-hourly. Its :30 rows must not be paired with the meter
// bucket centred on :00 — that double-counted them and scored them 30 min off.
func TestScoreModelsIgnoresHalfHourRows(t *testing.T) {
	var obs []models.ObservedHour
	var fc []models.WindForecastRow
	for d := 0; d < 5; d++ {
		for h := 9; h <= 18; h++ {
			obs = append(obs, models.ObservedHour{Hour: time.Date(2026, 9, 1+d, h, 0, 0, 0, time.UTC), Wind: 10, GustPeak: 14})
			fc = append(fc,
				models.WindForecastRow{Model: "openWRF", IDModel: 1000001, Period: time.Date(2026, 9, 1+d, h, 0, 0, 0, time.UTC), Wind: f64(10), Gust: f64(14)},
				// Wildly wrong on purpose: if it leaks in, the error is non-zero.
				models.WindForecastRow{Model: "openWRF", IDModel: 1000001, Period: time.Date(2026, 9, 1+d, h, 30, 0, 0, time.UTC), Wind: f64(30), Gust: f64(40)},
				models.WindForecastRow{Model: "gfs", IDModel: 3, Period: time.Date(2026, 9, 1+d, h, 0, 0, 0, time.UTC), Wind: f64(11), Gust: f64(15)},
			)
		}
	}
	a := scoreModels(obs, fc, 9, 18)
	for _, s := range a.Scores {
		if s.Model == "openWRF" {
			if s.Hours != 50 || s.WindMAE != 0 {
				t.Fatalf("openWRF = %d hours, MAE %.1f; want 50 hours, MAE 0 (only :00 rows)", s.Hours, s.WindMAE)
			}
			return
		}
	}
	t.Fatalf("openWRF missing from %+v", a.Scores)
}

// A model that stopped publishing is history, not a guide to today's forecast.
func TestScoreModelsDropsStaleModels(t *testing.T) {
	var obs []models.ObservedHour
	var fc []models.WindForecastRow
	for d := 0; d < 40; d++ {
		day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d)
		for h := 9; h <= 18; h++ {
			p := day.Add(time.Duration(h) * time.Hour)
			obs = append(obs, models.ObservedHour{Hour: p, Wind: 10, GustPeak: 14})
			fc = append(fc, models.WindForecastRow{Model: "gfs", IDModel: 3, Period: p, Wind: f64(11), Gust: f64(15)})
			if d < 20 { // publishes for the first 20 days only, then goes quiet
				fc = append(fc, models.WindForecastRow{Model: "openWRF", IDModel: 1000001, Period: p, Wind: f64(10), Gust: f64(14)})
			}
			if d >= 36 { // a new model with only its last 4 days, still current
				fc = append(fc, models.WindForecastRow{Model: "icon_eu", IDModel: 1000005, Period: p, Wind: f64(10), Gust: f64(20)})
			}
		}
	}
	a := scoreModels(obs, fc, 9, 18)
	names := map[string]bool{}
	for _, s := range a.Scores {
		names[s.Model] = true
	}
	if names["openWRF"] {
		t.Fatalf("openWRF stopped 20 days ago and should be dropped: %+v", a.Scores)
	}
	if !names["gfs"] || !names["icon_eu"] {
		t.Fatalf("current models missing: %+v", a.Scores)
	}
	if a.Days != 40 {
		t.Fatalf("days = %d, want 40 (from the kept models)", a.Days)
	}
}

// Seen live: aifs at +1.46 displayed as "+1.5" but stayed uncoloured while other
// "+1.5" cells were flagged. The threshold must apply to the displayed value.
func TestBiasClassMatchesDisplayedValue(t *testing.T) {
	for _, v := range []float64{1.46, 1.5, 1.54} {
		if signed(v) != "+1.5" {
			t.Fatalf("signed(%v) = %q, test assumes it displays +1.5", v, signed(v))
		}
		if got := biasClass(v, 1.5); got != "acc-over" {
			t.Fatalf("biasClass(%v) = %q; every value shown as +1.5 must be flagged alike", v, got)
		}
	}
	if got := biasClass(1.44, 1.5); got != "" {
		t.Fatalf("biasClass(1.44) = %q; displays +1.4, below threshold", got)
	}
	if got := biasClass(-1.46, 1.5); got != "acc-under" {
		t.Fatalf("biasClass(-1.46) = %q, want acc-under", got)
	}
}

func TestEstimateScoredLikeAModel(t *testing.T) {
	var obs []models.ObservedHour
	var fc []models.WindForecastRow
	var est []models.WindEstimate
	for d := 0; d < 10; d++ {
		for h := 9; h <= 18; h++ {
			p := time.Date(2026, 9, 1+d, h, 0, 0, 0, time.UTC)
			real := 12.0
			if h%5 == 0 { // 1 hour in 5 falls outside the range
				real = 20
			}
			obs = append(obs, models.ObservedHour{Hour: p, Wind: real, GustPeak: real + 6})
			fc = append(fc, models.WindForecastRow{Model: "gfs", IDModel: 3, Period: p, Wind: f64(15), Gust: f64(20)})
			est = append(est, models.WindEstimate{Period: p, Wind: 12.5, Gust: 18, Low: 10, High: 15})
		}
	}
	cov, n := estimateCoverage(obs, est)
	if n != 100 || cov != 0.8 {
		t.Fatalf("coverage = %.2f over %d hours, want 0.80 over 100", cov, n)
	}

	a := scoreModels(obs, append(fc, estimateAsForecast(est)...), 9, 18)
	a.EstCoverage, a.EstCoverageHours = cov, n
	v := buildAccuracyView(a)
	if v == nil || len(v.Rows) != 2 {
		t.Fatalf("want gfs + estimate rows, got %+v", v)
	}
	if !v.Rows[0].IsEstimate || !v.Rows[0].Best {
		t.Fatalf("the estimate is closer here and should rank first and be flagged: %+v", v.Rows[0])
	}
	if v.Rows[1].IsEstimate {
		t.Fatal("gfs flagged as the estimate")
	}
	if v.EstCoverage != "80%" || v.EstCoverageHours != 100 {
		t.Fatalf("view coverage = %q over %d", v.EstCoverage, v.EstCoverageHours)
	}
}
