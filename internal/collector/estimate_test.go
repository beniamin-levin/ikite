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
	cal := fitBefore(history, last)

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
	if fitBefore(history, start).Usable() {
		t.Fatal("no days before the first — nothing to calibrate on")
	}
}
