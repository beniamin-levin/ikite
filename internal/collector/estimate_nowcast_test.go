package collector

import (
	"math"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/estimate"
	"github.com/ben/ikite-go/internal/models"
)

func TestLastCompleteHour(t *testing.T) {
	tz := time.FixedZone("IDT", 3*3600)
	for _, c := range []struct{ now, want string }{
		{"11:35", "11:00"}, // bucket 11 (10:30–11:29) is in
		{"11:10", "10:00"},
		{"09:30", "09:00"},
	} {
		n, _ := time.ParseInLocation("15:04", c.now, tz)
		if got := lastCompleteHour(n).Format("15:04"); got != c.want {
			t.Errorf("at %s: %s, want %s", c.now, got, c.want)
		}
	}
}

func TestMeterMissAndNowcastPoint(t *testing.T) {
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return day.Add(time.Duration(h) * time.Hour) }
	obs := map[string]models.ObservedHour{wallHour(at(10)): {Hour: at(10), Wind: 14}}
	base := map[string]estimate.Point{
		wallHour(at(10)): {Wind: 11},
		wallHour(at(11)): {Wind: 12, Gust: 16, Low: 9, High: 15},
	}
	miss, ok := meterMiss(obs, base, at(10), 1)
	if !ok || miss != 3 {
		t.Fatalf("miss %v %v, want +3", miss, ok)
	}
	if _, ok := meterMiss(obs, base, at(11), 1); ok {
		t.Fatal("no reading for the last hour must give no nowcast")
	}
	np := nowcastParams{alpha: 0.75, tau: 5, hours: 1}
	p := nowcastPoint(base[wallHour(at(11))], miss, 1, np)
	if math.Abs(p.Wind-14.25) > 1e-9 || math.Abs(p.High-17.25) > 1e-9 || math.Abs(p.Gust-18.25) > 1e-9 {
		t.Fatalf("lead 1: %+v, want wind 14.25", p)
	}
	far := nowcastPoint(base[wallHour(at(11))], miss, 8, np)
	if !(far.Wind > 12 && far.Wind < p.Wind) {
		t.Fatalf("the correction must fade with lead: %+v", far)
	}
	calm := nowcastPoint(estimate.Point{Wind: 2, Low: 0, High: 5}, -6, 1, np)
	if calm.Wind != 0 || calm.Low != 0 || calm.High < calm.Wind {
		t.Fatalf("nothing below zero: %+v", calm)
	}
}
