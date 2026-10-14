package windguru

import (
	"testing"
	"time"
)

func TestParseStationReadsReadingTime(t *testing.T) {
	// Trimmed from a live Bat Galim (2049) response, 2026-10-02.
	body := []byte(`{"id_station":2049,"weather":{"wind_avg":7,"wind_max":10.8,"wind_min":5.9,` +
		`"wind_direction":234,"temperature":29,"datetime":"2026-10-02 15:18:11 IDT","unixtime":1790943491}}`)
	r, err := parseStation(body)
	if err != nil {
		t.Fatal(err)
	}
	if r.Wind != 5.9 || r.Gust != 10.8 || r.WindDir != 234 {
		t.Fatalf("reading = %+v", r)
	}
	if !r.SourceAt.Equal(time.Unix(1790943491, 0)) {
		t.Fatalf("SourceAt = %v, want the station's own reading time", r.SourceAt)
	}
}

func TestParseStationWithoutReadingTime(t *testing.T) {
	// An offline station (Paros, 2026-10-02) answers without a reading time.
	// That must come back as "no timestamp", never as a fresh reading.
	r, err := parseStation([]byte(`{"id_station":1091,"weather":{"wind_min":0,"wind_max":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !r.SourceAt.IsZero() {
		t.Fatalf("SourceAt = %v, want zero", r.SourceAt)
	}
}

func TestParseStationWithoutLullUsesAverage(t *testing.T) {
	r, err := parseStation([]byte(`{"weather":{"wind_min":null,"wind_avg":8.7,"wind_max":9.7,"wind_direction":268,"unixtime":1791975000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Wind != 8.7 || r.Gust != 9.7 {
		t.Fatalf("wind/gust = %v/%v, want 8.7/9.7", r.Wind, r.Gust)
	}
	r, err = parseStation([]byte(`{"weather":{"wind_min":6.1,"wind_avg":8.7,"wind_max":9.7,"unixtime":1791975000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Wind != 6.1 {
		t.Fatalf("wind = %v, want the lull 6.1", r.Wind)
	}
}
