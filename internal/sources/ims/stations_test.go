package ims

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchStations(t *testing.T) {
	const body = `[
		{"stationId":41,"name":"Haifa Refineries","shortName":"HR","location":{"latitude":32.8512,"longitude":35.0616}}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stations" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "ApiToken test-token" {
			t.Fatalf("auth: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New("test-token")
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()

	stations, err := c.FetchStations()
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) != 1 || stations[0].ID != 41 || stations[0].Name != "Haifa Refineries" {
		t.Fatalf("stations: %+v", stations)
	}
	if !stations[0].HasCoords() {
		t.Fatalf("coords: %+v", stations[0])
	}
}
