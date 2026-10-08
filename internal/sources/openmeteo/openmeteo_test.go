package openmeteo

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func TestFetchSpotAIFS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("models") != APIModelAIFS {
			t.Fatalf("model=%q", r.URL.Query().Get("models"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"hourly": {
				"time": ["2026-08-06T10:00","2026-08-06T11:00"],
				"wind_speed_10m": [8.5, 10.2],
				"wind_gusts_10m": [null, 14.0],
				"wind_direction_10m": [270, 280]
			}
		}`))
	}))
	defer srv.Close()

	c := New()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	lat, lon := 32.85, 35.06
	sp := models.Spot{ID: "ky", Lat: &lat, Lon: &lon}
	wg := 373090
	sp.WindguruID = &wg
	loc := time.FixedZone("Asia/Jerusalem", 3*3600)

	rows, err := c.FetchSpot(sp, Models()[0], 2, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[0].Model != ModelAIFS || rows[0].IDModel != IDAIFS {
		t.Fatalf("model meta: %+v", rows[0])
	}
	if rows[0].Wind == nil || *rows[0].Wind != 8.5 {
		t.Fatalf("wind0=%v", rows[0].Wind)
	}
	// Null gust falls back to wind.
	if rows[0].Gust == nil || *rows[0].Gust != 8.5 {
		t.Fatalf("gust0=%v", rows[0].Gust)
	}
	if rows[1].Gust == nil || *rows[1].Gust != 14.0 {
		t.Fatalf("gust1=%v", rows[1].Gust)
	}
}
