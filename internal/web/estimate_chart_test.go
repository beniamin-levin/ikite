package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func TestCompass(t *testing.T) {
	cases := map[float64]string{
		0: "N", 11.2: "N", 11.3: "NNE", 348.8: "N", 359.9: "N",
		90: "E", 180: "S", 247.5: "WSW", 270: "W", -1: "",
	}
	for deg, want := range cases {
		if got := compass(deg); got != want {
			t.Errorf("compass(%v) = %q, want %q", deg, got, want)
		}
	}
}

func est(day string, hour int, wind float64) models.WindEstimate {
	p, _ := time.Parse("2006-01-02 15", fmt.Sprintf("%s %02d", day, hour))
	return models.WindEstimate{Period: p, Wind: wind, Gust: wind + 5, Low: wind - 2, High: wind + 2, Dir: 250, Models: 5}
}

func TestBuildExpectedChart(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 3)
	e := []models.WindEstimate{est("2026-10-01", 12, 14), est("2026-10-03", 15, 18)}
	// Oct 2 has no estimate but has a measurement; Oct 4 has nothing at all.
	mh, _ := time.Parse("2006-01-02 15", "2026-10-02 10")
	obs := []models.ObservedHour{{Hour: mh, Wind: 9}}

	c := buildExpectedChart(e, obs, from, to, "en")
	if c == nil {
		t.Fatal("nil chart")
	}
	perDay := chartToHour - chartFromHour + 1
	if len(c.Days) != 3 || len(c.Points) != 3*perDay {
		t.Fatalf("days=%d points=%d; want 3 days (Oct 4 is empty) × %d hours", len(c.Days), len(c.Points), perDay)
	}
	if c.Days[1].First != perDay || c.Days[1].Label != "Fri 2" {
		t.Fatalf("second day = %+v", c.Days[1])
	}
	noon := c.Points[12-chartFromHour]
	if noon.Wind == nil || *noon.Wind != 14 || noon.Dir != "WSW" || noon.Label != "Thu 1 · 12:00" {
		t.Fatalf("Oct 1 12:00 = %+v", noon)
	}
	if p := c.Points[perDay+10-chartFromHour]; p.Measured == nil || *p.Measured != 9 || p.Wind != nil {
		t.Fatalf("Oct 2 10:00 should carry the measurement only: %+v", p)
	}
	if buildExpectedChart(nil, obs, from, to, "en") != nil {
		t.Fatal("no estimate at all — no chart")
	}
}

func TestFindStrongestIgnoresThePast(t *testing.T) {
	e := []models.WindEstimate{
		est("2026-10-01", 9, 25),  // strongest, but already over
		est("2026-10-01", 15, 14), // later today
		est("2026-10-03", 14, 18), // winner
		est("2026-10-12", 14, 30), // beyond the 7-day lookahead
	}
	now := time.Date(2026, 10, 1, 11, 20, 0, 0, time.UTC)
	s := findStrongest(e, now, "en")
	if s == nil || s.When != "Sat 3 14:00" || s.Wind != "18" || s.Low != "16" || s.High != "20" || s.Dir != "WSW" {
		t.Fatalf("strongest = %+v, want Sat 3 14:00 at 18 kt", s)
	}
	if findStrongest(e[:1], now, "en") != nil {
		t.Fatal("only past hours — nothing to headline")
	}
}
