package collector

import (
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func TestFilterOpenWRFDays(t *testing.T) {
	loc := time.FixedZone("IST", 3*3600)
	today := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	tomorrow := today.AddDate(0, 0, 1)
	dayAfter := today.AddDate(0, 0, 2)

	byDate := map[string][]models.WindForecastRow{
		today.Format("2006-01-02"):    {{ForecastDate: today}},
		tomorrow.Format("2006-01-02"): {{ForecastDate: tomorrow}},
		dayAfter.Format("2006-01-02"): {{ForecastDate: dayAfter}},
	}

	got := filterOpenWRFDays(byDate, today, tomorrow)
	if len(got) != 2 {
		t.Fatalf("got %d days, want 2", len(got))
	}
	if _, ok := got[dayAfter.Format("2006-01-02")]; ok {
		t.Fatal("day after tomorrow should be excluded")
	}
}

func TestIsraelSpotIDs(t *testing.T) {
	spots := []models.Spot{
		{ID: "ky", Name: "Kiryat Yam"},
		{ID: "1091", Name: "Paros"},
		{ID: "bg", Name: "Bat Galim"},
		{ID: "4257", Name: "Zandvoort"},
	}
	got := israelSpotIDs(spots)
	if len(got) != 2 || got[0] != "ky" || got[1] != "bg" {
		t.Fatalf("got %v, want only the 2 Israel spots (ky, bg)", got)
	}
}

func TestForecastGustSendWindowOpen(t *testing.T) {
	loc := time.FixedZone("IST", 3*3600)
	cases := []struct {
		h, m int
		want bool
	}{
		{7, 0, false},
		{8, 0, false},
		{8, 9, false},
		{8, 10, true},
		{8, 11, true},
		{12, 0, true},
	}
	for _, tc := range cases {
		now := time.Date(2026, 8, 10, tc.h, tc.m, 0, 0, loc)
		if got := forecastGustSendWindowOpen(now); got != tc.want {
			t.Fatalf("%02d:%02d got %v want %v", tc.h, tc.m, got, tc.want)
		}
	}
}

func TestForecastGustFingerprintStable(t *testing.T) {
	loc := time.FixedZone("IST", 3*3600)
	gust := 26.0
	wind := 20.0
	rows := []models.WindForecastRow{
		{
			Location:     "ky",
			ForecastDate: time.Date(2026, 8, 4, 0, 0, 0, 0, loc),
			IDModel:      3,
			Model:        "gfs",
			Period:       time.Date(2026, 8, 4, 12, 0, 0, 0, loc),
			Wind:         &wind,
			Gust:         &gust,
		},
	}
	a := forecastGustFingerprint(rows)
	b := forecastGustFingerprint(rows)
	if a != b {
		t.Fatalf("fingerprints differ: %s vs %s", a, b)
	}
}
