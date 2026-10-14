package collector

import (
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/estimate"
	"github.com/ben/ikite-go/internal/models"
)

func TestStuckZeroHoursExcludesOnlyWindyRuns(t *testing.T) {
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	fc := map[string]*hourForecasts{}
	var obs []models.ObservedHour
	for h := 9; h <= 18; h++ {
		at := day.Add(time.Duration(h) * time.Hour)
		fc[wallHour(at)] = &hourForecasts{period: at, forecasts: []estimate.Forecast{{Model: "a", Wind: 12}, {Model: "b", Wind: 14}}}
		wind := 10.0
		if h >= 12 && h <= 14 { // 3 h at zero: stuck
			wind = 0
		}
		if h == 17 { // a single calm hour: kept
			wind = 0
		}
		obs = append(obs, models.ObservedHour{Hour: at, Wind: wind})
	}
	stuck := stuckZeroHours(obs, fc)
	for h := 9; h <= 18; h++ {
		k := wallHour(day.Add(time.Duration(h) * time.Hour))
		want := h >= 12 && h <= 14
		if stuck[k] != want {
			t.Errorf("hour %d stuck=%v, want %v", h, stuck[k], want)
		}
	}
	if got := len(pairHistory(obs, fc)); got != 7 {
		t.Errorf("pairHistory kept %d hours, want 7", got)
	}
}
