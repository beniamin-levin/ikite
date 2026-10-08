package ims

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchStationRangeHaifaRefineriesChannels(t *testing.T) {
	const body = `{"stationId":41,"data":[{"datetime":"2026-08-06T14:10:00+03:00","channels":[
		{"id":2,"name":"ws60mmax","value":9.4,"status":1,"valid":true},
		{"id":4,"name":"Ws60m","value":6.0,"status":1,"valid":true},
		{"id":5,"name":"Wd60m","value":264.0,"status":1,"valid":true},
		{"id":7,"name":"TD","value":31.2,"status":1,"valid":true},
		{"id":8,"name":"RH","value":64.0,"status":1,"valid":true},
		{"id":15,"name":"BP","value":1004.5,"status":1,"valid":true}
	]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "ApiToken test-token" {
			t.Fatalf("auth: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New("test-token")
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()

	from := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	to := from.Add(6 * time.Hour)
	rows, err := c.FetchStationRange(41, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: %d", len(rows))
	}
	// 6.0 m/s ≈ 11.66 kt; 9.4 m/s ≈ 18.27 kt
	if rows[0].Wind < 11 || rows[0].Wind > 12 {
		t.Fatalf("wind kt: %v", rows[0].Wind)
	}
	if rows[0].Gust < 18 || rows[0].Gust > 19 {
		t.Fatalf("gust kt: %v", rows[0].Gust)
	}
	if rows[0].WindDir != 264 {
		t.Fatalf("dir: %v", rows[0].WindDir)
	}
	if rows[0].Location != "ims41" {
		t.Fatalf("location: %s", rows[0].Location)
	}
	if rows[0].Temp == nil || *rows[0].Temp != 31.2 {
		t.Fatalf("temp: %v", rows[0].Temp)
	}
	if rows[0].Humidity == nil || *rows[0].Humidity != 64 {
		t.Fatalf("humidity: %v", rows[0].Humidity)
	}
	if rows[0].Pressure == nil || *rows[0].Pressure != 1004.5 {
		t.Fatalf("pressure: %v", rows[0].Pressure)
	}
	if rows[0].Raw == "" || !strings.Contains(rows[0].Raw, "Wd60m") {
		t.Fatalf("raw payload missing: %q", rows[0].Raw)
	}
}

func TestClassifyChannel(t *testing.T) {
	cases := map[string]string{
		"WS": "wind", "Ws60m": "wind", "WSmax": "gust", "ws60mmax": "gust",
		"WD": "dir", "Wd60m": "dir", "TD": "temp", "RH": "humidity", "BP": "pressure",
		"WS1mm": "", "Ws10mm": "",
	}
	for name, want := range cases {
		if got := classifyChannel(name); got != want {
			t.Fatalf("%s: got %q want %q", name, got, want)
		}
	}
}
