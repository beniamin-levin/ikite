package collector

import (
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func fp(v float64) *float64 { return &v }

// Two models, 30 days of kiting hours. Model "a" reads 2 kt high every day
// except the last, when it suddenly reads 10 kt high. A calibration for that last
// day must not know about its own jump — that would be fitting on the answer.
func TestFitBeforeNeverSeesTheDayItPredicts(t *testing.T) {
	var obs []models.ObservedHour
	var fc []models.WindForecastRow
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for d := 0; d < 30; d++ {
		day := start.AddDate(0, 0, d)
		biasA := 2.0
		if d == 29 {
			biasA = 10
		}
		for h := estimateFromHour; h <= estimateToHour; h++ {
			p := day.Add(time.Duration(h) * time.Hour)
			noise := float64(h%3 - 1)
			obs = append(obs, models.ObservedHour{Hour: p, Wind: 12, GustPeak: 18, Samples: 60})
			fc = append(fc,
				models.WindForecastRow{Model: "a", IDModel: 900, Period: p, Wind: fp(12 + biasA + noise), Gust: fp(20)},
				models.WindForecastRow{Model: "b", IDModel: 901, Period: p, Wind: fp(12 - noise), Gust: fp(19)},
			)
		}
		// Half-hourly and night rows must be ignored, not paired.
		fc = append(fc,
			models.WindForecastRow{Model: "a", IDModel: 900, Period: day.Add(12*time.Hour + 30*time.Minute), Wind: fp(99)},
			models.WindForecastRow{Model: "a", IDModel: 900, Period: day.Add(2 * time.Hour), Wind: fp(99)},
		)
	}

	history := pairHistory(obs, groupForecasts(fc))
	last := start.AddDate(0, 0, 29)
	cal := fitBeforeWith(history, last, defaultSetup)

	a := cal.Models["a"]
	if a.Bias < 1.9 || a.Bias > 2.1 {
		t.Fatalf("bias for the last day = %.2f; want ~2 — the day's own +10 must not leak in", a.Bias)
	}
	if a.Hours != 29*(estimateToHour-estimateFromHour+1) {
		t.Fatalf("a trained on %d hours, want %d (29 days, on-the-hour, kiting hours only)",
			a.Hours, 29*(estimateToHour-estimateFromHour+1))
	}

	// Estimating only the last day returns just that day's hours, one per hour.
	pts := estimateHours(cal, groupForecasts(fc), last, last.AddDate(0, 0, 1), time.Now())
	if len(pts) != estimateToHour-estimateFromHour+1 {
		t.Fatalf("got %d points for one day, want %d", len(pts), estimateToHour-estimateFromHour+1)
	}
	for _, p := range pts {
		if p.Period.Format("2006-01-02") != last.Format("2006-01-02") {
			t.Fatalf("point outside the requested day: %s", p.Period)
		}
	}

	// And a calibration for a day before any history has nothing to go on.
	if fitBeforeWith(history, start, defaultSetup).Usable() {
		t.Fatal("no days before the first — nothing to calibrate on")
	}
}

// The archive only fills models ikite did not save for that hour; a saved
// forecast always wins, and archive-only hours are added.
func TestWithArchiveKeepsSavedForecasts(t *testing.T) {
	tz := time.FixedZone("IL", 3*3600)
	h14 := time.Date(2026, 10, 8, 14, 0, 0, 0, tz)
	h15 := h14.Add(time.Hour)
	f := func(v float64) *float64 { return &v }
	saved := groupForecasts([]models.WindForecastRow{
		{IDModel: 3, Model: "gfs", Period: h14, Wind: f(13)},
		{IDModel: 117, Model: "ifs", Period: h14, Wind: f(12)},
	})
	arch := []models.WindForecastRow{
		{Model: "gfs", Period: h14, Wind: f(99)},
		{Model: "gfs", Period: h15, Wind: f(15)},
	}
	got := withArchive(saved, arch)
	byModel := func(k string) map[string]float64 {
		m := map[string]float64{}
		for _, x := range got[k].forecasts {
			m[x.Model] = x.Wind
		}
		return m
	}
	if m := byModel(wallHour(h14)); m["gfs"] != 13 || m["ifs"] != 12 || len(m) != 2 {
		t.Fatalf("14:00 = %v, want saved gfs 13 and ifs 12 only", m)
	}
	if m := byModel(wallHour(h15)); m["gfs"] != 15 {
		t.Fatalf("15:00 = %v, want archive gfs 15", m)
	}
	if len(saved[wallHour(h14)].forecasts) != 2 {
		t.Fatal("withArchive must not modify the saved map")
	}
}

// A day the meter read zero at every hour is a dead sensor and is left out of
// calibration; a light day with some wind is kept.
func TestPairHistorySkipsDeadMeterDays(t *testing.T) {
	tz := time.FixedZone("IL", 3*3600)
	f := func(v float64) *float64 { return &v }
	var obs []models.ObservedHour
	var rows []models.WindForecastRow
	for d := 1; d <= 2; d++ {
		for h := 8; h <= 17; h++ {
			at := time.Date(2025, 3, d, h, 0, 0, 0, tz)
			w := 0.0
			if d == 2 {
				w = 2 // light but alive
			}
			obs = append(obs, models.ObservedHour{Hour: at, Wind: w})
			rows = append(rows, models.WindForecastRow{IDModel: 3, Model: "gfs", Period: at, Wind: f(10)})
		}
	}
	got := pairHistory(obs, groupForecasts(rows))
	for _, h := range got {
		if h.day == "2025-03-01" {
			t.Fatal("dead-meter day 2025-03-01 must be skipped")
		}
	}
	if len(got) != 10 {
		t.Fatalf("kept %d hours, want the 10 of 2025-03-02", len(got))
	}
}
