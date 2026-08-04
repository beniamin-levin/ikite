package prediction

import (
	"database/sql"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/store"
)

func hour(h int, wind, gust, dir, temp float64) store.HourlyWindStat {
	return store.HourlyWindStat{
		Hour: h, AvgWind: wind, AvgGust: gust, AvgDir: dir,
		AvgTemp: sql.NullFloat64{Float64: temp, Valid: true}, Count: 10,
	}
}

func TestHourSimilarity(t *testing.T) {
	a := hour(10, 11, 13, 240, 31)
	b := hour(10, 12, 14, 245, 30)
	s := hourSimilarity(a, b)
	if s < 0.7 {
		t.Fatalf("expected high similarity, got %.2f", s)
	}
}

func TestHourSimilarityLow(t *testing.T) {
	a := hour(10, 11, 13, 240, 31)
	b := hour(10, 6, 8, 120, 24)
	s := hourSimilarity(a, b)
	if s > 0.4 {
		t.Fatalf("expected low similarity, got %.2f", s)
	}
}

func TestFindSimilarDays(t *testing.T) {
	today := store.DayProfile{
		Date: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
		Hours: map[int]store.HourlyWindStat{
			8:  hour(8, 9, 11, 210, 31),
			9:  hour(9, 11, 13, 225, 31),
			10: hour(10, 12, 14, 235, 31),
		},
	}
	matchDay := store.DayProfile{
		Date: time.Date(2024, 7, 10, 0, 0, 0, 0, time.UTC),
		Hours: map[int]store.HourlyWindStat{
			8:  hour(8, 9, 11, 215, 30),
			9:  hour(9, 11, 13, 230, 31),
			10: hour(10, 12, 14, 240, 31),
			13: hour(13, 14, 16, 250, 32),
			14: hour(14, 13, 15, 255, 33),
		},
	}
	diffDay := store.DayProfile{
		Date: time.Date(2024, 7, 5, 0, 0, 0, 0, time.UTC),
		Hours: map[int]store.HourlyWindStat{
			8:  hour(8, 4, 5, 90, 24),
			9:  hour(9, 5, 6, 100, 24),
			10: hour(10, 5, 6, 110, 25),
			13: hour(13, 12, 14, 250, 28),
		},
	}
	matches := findSimilarDays(today, []store.DayProfile{matchDay, diffDay}, 10)
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %d", len(matches))
	}
	if matches[0].Day.Date != matchDay.Date {
		t.Fatalf("wrong match day")
	}
}

func TestStrongestFromForecast(t *testing.T) {
	byHour := map[int]hourForecast{
		12: {AvgWind: 12.5},
		13: {AvgWind: 13.2},
		14: {AvgWind: 12.8},
	}
	start, end := strongestFromForecast(byHour)
	if start != 13 || end != 13 {
		t.Fatalf("got %d-%d", start, end)
	}
}

func TestFormatPeak(t *testing.T) {
	got := formatPeak(hourForecast{AvgWind: 13.2, AvgGust: 15.1, MaxGust: 17.8})
	want := "13–14 kt sustained, gusts 15–18 kt"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestDirectionLabel(t *testing.T) {
	got := directionLabel(250)
	if got != "235–265° (WSW)" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatMatchConditionsNoToday(t *testing.T) {
	got := formatMatchConditions(nil, 15, true)
	if got != "no KY readings for today yet; using Jul–Aug climatology baseline" {
		t.Fatalf("got %q", got)
	}
}

func TestDayProfileFromStatsEmpty(t *testing.T) {
	p := dayProfileFromStats(time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC), nil)
	if len(p.Hours) != 0 {
		t.Fatalf("want empty hours, got %d", len(p.Hours))
	}
}
