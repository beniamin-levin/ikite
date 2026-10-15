package web

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/models"
)

func TestLiveMapPoint(t *testing.T) {
	tz, _ := time.LoadLocation("Asia/Jerusalem")
	lat, lon := 32.83, 35.07
	sp := models.Spot{ID: "kh", Name: "Kiryat Haim", Lat: &lat, Lon: &lon}
	at := time.Date(2026, 10, 15, 13, 5, 0, 0, tz)
	p := liveMapPoint(sp, &models.WindReading{Period: at, Wind: 14.6, Gust: 19.2, WindDir: 275.4}, tz)
	if p.Wind != 15 || p.Gust != 19 || p.Dir != 275 || p.Level != "level3" {
		t.Fatalf("point = %+v", p)
	}
	if p.At != "2026-10-15T13:05:00+03:00" {
		t.Fatalf("at = %s", p.At)
	}
	// A meter that sends no direction stores 0, as in the table.
	if p := liveMapPoint(sp, &models.WindReading{Period: at, Wind: 3}, tz); p.Dir != -1 {
		t.Fatalf("no direction: dir = %v", p.Dir)
	}
	if p := liveMapPoint(sp, nil, tz); p.At != "" || p.Dir != -1 || p.Lat != lat {
		t.Fatalf("no reading: %+v", p)
	}
}

// Every spot without coordinates in its row is placed from meterCoords;
// Windguru station ids double as spot ids for these.
func TestMeterCoordsInRange(t *testing.T) {
	for id, c := range meterCoords {
		if c[0] < -90 || c[0] > 90 || c[1] < -180 || c[1] > 180 || (c[0] == 0 && c[1] == 0) {
			t.Errorf("%s: bad coordinates %v", id, c)
		}
	}
}

func TestLiveMapPageRenders(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	data := map[string]any{"Active": "livemap", "SpotsJSON": `[{"id":"kh"}]`}
	if err := s.render(rec, httptest.NewRequest("GET", "/map", nil), "livemap.html", data); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{"maplibre-gl.min.js", "/static/livemap.js", `href="/map" class="active"`, "Ratings map"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
