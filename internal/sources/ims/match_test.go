package ims

import (
	"math"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func TestHaversineKmHaifaToRefineries(t *testing.T) {
	// Haifa Port (~26) to Haifa Refineries (~41) — roughly 5–6 km apart.
	d := HaversineKm(32.8300, 35.0700, 32.8512, 35.0616)
	if d < 2 || d > 8 {
		t.Fatalf("distance km: %v", d)
	}
}

func TestNearestStationWithinRadius(t *testing.T) {
	stations := []Station{
		{ID: 1, Name: "Far", Lat: 33.5, Lon: 35.5},
		{ID: 41, Name: "Haifa Refineries", Lat: 32.8512, Lon: 35.0616},
	}
	st, dist := NearestStation(stations, 32.8510, 35.0620, 15)
	if st == nil || st.ID != 41 {
		t.Fatalf("station: %+v", st)
	}
	if dist > 1 {
		t.Fatalf("dist km: %v", dist)
	}
}

func TestNearestStationOutsideRadius(t *testing.T) {
	stations := []Station{
		{ID: 41, Name: "Haifa Refineries", Lat: 32.8512, Lon: 35.0616},
	}
	st, _ := NearestStation(stations, 30.0, 34.8, 15)
	if st != nil {
		t.Fatalf("expected nil, got %+v", st)
	}
}

func TestFilterReadingsSince(t *testing.T) {
	cutoff := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	rows := []models.WindReading{
		{Period: cutoff.Add(-time.Hour)},
		{Period: cutoff},
		{Period: cutoff.Add(time.Minute)},
	}
	out := FilterReadingsSince(rows, cutoff)
	if len(out) != 2 {
		t.Fatalf("rows: %d", len(out))
	}
}

func TestParseStationsJSONArray(t *testing.T) {
	body := []byte(`[
		{"stationId":41,"name":"Haifa Refineries","shortName":"HR","location":{"latitude":32.85,"longitude":35.06}},
		{"stationId":26,"name":"Haifa Port","location":{"lat":32.83,"lon":35.07}}
	]`)
	stations, err := parseStationsJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) != 2 || stations[0].ID != 41 || math.Abs(stations[0].Lat-32.85) > 0.01 {
		t.Fatalf("stations: %+v", stations)
	}
}

func TestParseStationsJSONWrapped(t *testing.T) {
	body := []byte(`{"stations":[{"stationId":10,"name":"Merom Golan","location":{"latitude":33.0,"longitude":35.77}}]}`)
	stations, err := parseStationsJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) != 1 || stations[0].ID != 10 {
		t.Fatalf("stations: %+v", stations)
	}
}
