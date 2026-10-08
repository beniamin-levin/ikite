package imssea

import (
	"math"
	"os"
	"testing"
	"time"
)

func jerusalem(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestParseSeaForecast6October(t *testing.T) {
	tz := jerusalem(t)
	body, err := os.ReadFile("testdata/isr_sea_2026-10-06.xml")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(body, tz)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 6, 0, 1, 0, 0, tz); !f.Issued.Equal(want) {
		t.Fatalf("issued %s", f.Issued)
	}
	if len(f.Regions) != 5 {
		t.Fatalf("%d regions, want 5", len(f.Regions))
	}
	var north *Region
	for i := range f.Regions {
		if f.Regions[i].ID == RegionNorthern {
			north = &f.Regions[i]
		}
	}
	if north == nil || north.Name != "Northern Coast" || len(north.Blocks) != 2 {
		t.Fatalf("northern coast: %+v", north)
	}
	b := north.Blocks[0]
	if !b.From.Equal(time.Date(2026, 10, 6, 8, 0, 0, 0, tz)) || b.DirFrom != 270 || b.DirTo != 360 ||
		b.WindMinKmh != 15 || b.WindMaxKmh != 25 || b.SeaState != 40 || b.WaveMinCm != 30 || b.WaveMaxCm != 60 ||
		b.SeaTempC == nil || *b.SeaTempC != 28 {
		t.Fatalf("day block %+v", b)
	}

	rows := north.Rows("kh", 293410)
	if len(rows) != 24 {
		t.Fatalf("%d hourly rows, want 24 (two 12-hour blocks)", len(rows))
	}
	r := rows[0]
	// 15–25 km/h: 10.8 kt mid, 13.5 kt top; 270–360 → 315°.
	if r.Period.Hour() != 8 || *r.Wind != 10.8 || *r.Gust != 13.5 || *r.WindDir != 315 || r.IDModel != ModelID || r.Model != ModelName {
		t.Fatalf("first row %+v wind %v gust %v dir %v", r, *r.Wind, *r.Gust, *r.WindDir)
	}
	// Night block 135–180 → 158°; its 00:00–07:00 hours fall on the next day.
	last := rows[len(rows)-1]
	if last.Period.Hour() != 7 || last.ForecastDate.Day() != 7 || math.Abs(*last.WindDir-158) > 1 {
		t.Fatalf("last row %s dir %v", last.Period, *last.WindDir)
	}
}

func TestMidDirection(t *testing.T) {
	for _, c := range []struct{ a, b, want float64 }{
		{270, 360, 315}, {315, 225, 270}, {315, 45, 0}, {360, 45, 22.5}, {180, 135, 157.5}, {90, 180, 135},
	} {
		if got := MidDirection(c.a, c.b); math.Abs(got-c.want) > 0.01 && math.Abs(got-c.want) < 359.99 {
			t.Fatalf("MidDirection(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if MidDirection(-1, 90) != -1 {
		t.Fatal("missing direction should stay missing")
	}
}

func TestRegionFor(t *testing.T) {
	for _, c := range []struct {
		name     string
		lat, lon float64
		want     int
	}{
		{"Kiryat Haim", 32.83, 35.07, RegionNorthern},
		{"Bat Galim", 32.832, 34.972, RegionNorthern},
		{"Betzet", 33.078, 35.11, RegionNorthern},
		{"Hadera", 32.47, 34.88, RegionCentral},
		{"Ashkelon", 31.68, 34.55, RegionSouthern},
		{"Sea of Galilee", 32.87, 35.575, RegionGalilee},
		{"Eilat", 29.55, 34.95, RegionEilat},
		{"Tarifa", 36.01, -5.6, 0},
		{"Mykonos", 37.45, 25.33, 0},
	} {
		if got := RegionFor(c.lat, c.lon); got != c.want {
			t.Fatalf("%s: region %d, want %d", c.name, got, c.want)
		}
	}
}
