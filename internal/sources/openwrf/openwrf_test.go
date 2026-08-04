package openwrf

import (
	"os"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func TestParseForecast(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	text := `WRF 1km Wind for 18z_1km_Kiryat_Yam
Date/Time Wind Dir Gusts
0802-2300 14.8 347 16.3
0803--070 15.4 346 16.8
0803-0000 14.9 350 16.7`

	rows, err := Parse(text, time.Date(2026, 8, 2, 8, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	wantPeriods := []time.Time{
		time.Date(2026, 8, 2, 23, 0, 0, 0, loc),
		time.Date(2026, 8, 2, 23, 30, 0, 0, loc),
		time.Date(2026, 8, 3, 0, 0, 0, 0, loc),
	}
	wantDates := []time.Time{
		time.Date(2026, 8, 2, 0, 0, 0, 0, loc),
		time.Date(2026, 8, 2, 0, 0, 0, 0, loc),
		time.Date(2026, 8, 3, 0, 0, 0, 0, loc),
	}
	for i, want := range wantPeriods {
		if !rows[i].Period.Equal(want) {
			t.Errorf("row %d period = %v, want %v", i, rows[i].Period, want)
		}
		if !rows[i].ForecastDate.Equal(wantDates[i]) {
			t.Errorf("row %d forecast_date = %v, want %v", i, rows[i].ForecastDate, wantDates[i])
		}
		if rows[i].IDModel != ModelID || rows[i].Model != ModelName {
			t.Errorf("row %d model = (%d, %q)", i, rows[i].IDModel, rows[i].Model)
		}
	}
	if got := *rows[1].Wind; got != 15.4 {
		t.Errorf("wind = %v, want 15.4", got)
	}
	if got := *rows[1].WindDir; got != 346 {
		t.Errorf("direction = %v, want 346", got)
	}
	if got := *rows[1].Gust; got != 16.8 {
		t.Errorf("gust = %v, want 16.8", got)
	}
}

func TestParsePeriodAcrossNewYear(t *testing.T) {
	loc := time.UTC
	reference := time.Date(2026, 12, 31, 8, 0, 0, 0, loc)

	period, err := parsePeriod("0101-0030", reference, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2027, 1, 1, 0, 30, 0, 0, loc)
	if !period.Equal(want) {
		t.Fatalf("period = %v, want %v", period, want)
	}
}

func TestParseRejectsMissingRows(t *testing.T) {
	if _, err := Parse("not a forecast", time.Now(), time.UTC); err == nil {
		t.Fatal("expected an error")
	}
}

func TestMatchSpots(t *testing.T) {
	files := []DriveFile{
		{ID: "1", Name: "18z_1km_Kiryat_Yam.pdf"},
		{ID: "2", Name: "18z_1km_Kiryat_Yam_wind_station.pdf"},
		{ID: "3", Name: "18z_1km_Betzet.pdf"},
		{ID: "4", Name: "18z_1km_Bat_Galim_Club.pdf"},
		{ID: "5", Name: "18z_1km_Shavei_Tzion.pdf"},
		{ID: "6", Name: "18z_1km_Atlit_Mivtzar.pdf"},
		{ID: "7", Name: "18z_1km_Kineret_Diamond.pdf"},
		{ID: "8", Name: "18z_1km_Kineret_Migdal.pdf"},
		{ID: "9", Name: "18z_1km_Kineret_Gino.pdf"},
	}
	spots := []models.Spot{
		{ID: "ky", Name: "Kiryat Yam"},
		{ID: "15233", Name: "Betzet"},
		{ID: "bg", Name: "Bat Galim"},
		{ID: "st", Name: "Shavei Tzion"},
		{ID: "2256", Name: "Atlit"},
		{ID: "1909", Name: "Diamond"},
		{ID: "2752", Name: "Sea of G"},
		{ID: "3379", Name: "Kineret"},
		{ID: "hp", Name: "Hadera"},
	}
	got := MatchSpots(spots, files)
	wantPDF := map[string]string{
		"ky":    "18z_1km_Kiryat_Yam.pdf",
		"15233": "18z_1km_Betzet.pdf",
		"bg":    "18z_1km_Bat_Galim_Club.pdf",
		"st":    "18z_1km_Shavei_Tzion.pdf",
		"2256":  "18z_1km_Atlit_Mivtzar.pdf",
		"1909":  "18z_1km_Kineret_Diamond.pdf",
		"2752":  "18z_1km_Kineret_Migdal.pdf",
		"3379":  "18z_1km_Kineret_Gino.pdf",
	}
	if len(got) != len(wantPDF) {
		t.Fatalf("mapped %d spots, want %d: %+v", len(got), len(wantPDF), got)
	}
	for _, m := range got {
		if wantPDF[m.Spot.ID] != m.PDF.Name {
			t.Errorf("spot %s → %s, want %s", m.Spot.ID, m.PDF.Name, wantPDF[m.Spot.ID])
		}
	}
}

func TestFetchLivePDF(t *testing.T) {
	if os.Getenv("OPENWRF_LIVE_TEST") == "" {
		t.Skip("set OPENWRF_LIVE_TEST=1 to download the public forecast")
	}
	loc, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := New("").Fetch(time.Now(), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 70 {
		t.Fatalf("got %d rows, want at least 70", len(rows))
	}
	dates := map[string]bool{}
	for _, r := range rows {
		dates[r.ForecastDate.Format("2006-01-02")] = true
	}
	if len(dates) < 2 {
		t.Fatalf("expected multi-day forecast_date values, got %v", dates)
	}
}
