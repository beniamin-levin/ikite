package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ben/ikite-go/internal/config"
)

func TestTemplatesParse(t *testing.T) {
	cfg := &config.Config{}
	s, err := New(cfg, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if s.tmpl.Lookup("index.html") == nil {
		t.Fatal("index.html missing")
	}
	if s.tmpl.Lookup("graph.html") == nil {
		t.Fatal("graph.html missing")
	}
	if s.tmpl.Lookup("settings.html") == nil {
		t.Fatal("settings.html missing")
	}
	if s.tmpl.Lookup("camera.html") == nil {
		t.Fatal("camera.html missing")
	}
	if s.tmpl.Lookup("prediction.html") == nil {
		t.Fatal("prediction.html missing")
	}
	if s.tmpl.Lookup("prefs.html") == nil {
		t.Fatal("prefs.html missing")
	}
	if s.tmpl.Lookup("about.html") == nil {
		t.Fatal("about.html missing")
	}
	if s.tmpl.Lookup("spots_rating.html") == nil {
		t.Fatal("spots_rating.html missing")
	}
	if s.tmpl.Lookup("spots_map.html") == nil {
		t.Fatal("spots_map.html missing")
	}
	if s.tmpl.Lookup("spot_details.html") == nil {
		t.Fatal("spot_details.html missing")
	}
	if s.tmpl.Lookup("nav") == nil {
		t.Fatal("nav template missing")
	}
}

func TestPrefsPage(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/prefs", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(body, "Preferences") || !strings.Contains(body, "prefs-layout.js") {
		t.Fatal("expected prefs page content")
	}
}

func TestHealthz(t *testing.T) {
	cfg := &config.Config{}
	s, err := New(cfg, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestCameraDefaultKH(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/camera", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(body, "alias=6469b4554f45e") {
		t.Fatal("expected Kiryat Haim cam by default")
	}
	if strings.Contains(body, "alias=60997a5d22ca9") {
		t.Fatal("did not expect Bat Galim when defaulting to KH")
	}
	if !strings.Contains(body, `href="/camera?cam=kh" class="active"`) {
		t.Fatal("expected KH picker active")
	}
}

func TestCameraAll(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/camera?cam=all", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	for _, alias := range []string{"6469b4554f45e", "60997a5d22ca9", "61a4836d911ac"} {
		if !strings.Contains(body, "alias="+alias) {
			t.Fatalf("expected cam alias %s in all view", alias)
		}
	}
	if !strings.Contains(body, `href="/camera?cam=all" class="active"`) {
		t.Fatal("expected All picker active")
	}
}

// The settings page sends the pass with every fetch. If the template emits it
// with extra quote characters the server rejects the request and the UI shows
// "Save failed".
func TestSettingsTemplatePassLiteral(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	const pass = "8a1d8a4c-0dd2-4824-80dd-8360dd32c375"
	data := map[string]any{
		"Threshold":             "10",
		"ForecastTelegram":      "yes",
		"PredictionTelegram":    "yes",
		"AlertTelegramSpotLeft": "15233",
		"AlertTelegramSpot":     "ky",
		"AlertTelegramSpotRight": "bg",
		"AlertTelegramInterval": 5,
		"AlertIntervals":        []map[string]any{{"Value": 5, "Label": "5 min"}},
		"ForecastStartHour":     8,
		"ForecastEndHour":       22,
		"Spots": []map[string]any{{
			"Key": "ky", "Name": "Kiryat Yam", "Visible": true, "Collect": true,
			"CollectInterval": 5, "CollectStartHour": 8, "CollectEndHour": 22,
		}},
		"Buttons":          []int{10},
		"CollectIntervals": []int{5},
		"CollectHours":     []int{8, 22},
		"ForecastHours":    []int{8, 22},
		"Pass":             pass,
		"Active":           "settings",
	}

	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "settings.html", data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if want := `const settingsPass = "` + pass + `";`; !strings.Contains(out, want) {
		t.Fatalf("settings pass literal not found; want %q", want)
	}
	if strings.Contains(out, `\"`+pass) {
		t.Fatal("settings pass literal is double-quoted")
	}
}

func TestFaviconRoutes(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	for _, path := range []string{"/favicon.ico", "/apple-touch-icon.png", "/static/favicon.svg", "/static/favicon-16.png", "/static/favicon-32.png"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, rr.Code)
		}
		if rr.Body.Len() == 0 {
			t.Fatalf("%s: empty body", path)
		}
	}
}
