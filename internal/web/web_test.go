package web

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/i18n"
	"github.com/ben/ikite-go/internal/models"
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
	if s.tmpl.Lookup("wind_table") == nil {
		t.Fatal("wind_table template missing")
	}
}

// TestTemplateI18nKeysExist guards every template against referencing a message
// key no catalog defines. T falls back to returning the key itself, so a missing
// entry ships straight to the page — that is how the camera sidebar once
// rendered "CAMERA.RECENT_SHORT" as its header.
//
// Only English is asserted: T falls back to English for a Hebrew miss, so an
// untranslated key degrades to English rather than leaking markup.
func TestTemplateI18nKeysExist(t *testing.T) {
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		t.Fatal(err)
	}
	keyRe := regexp.MustCompile(`\bT\s+"([a-zA-Z0-9_.]+)"`)
	en := &i18n.Translator{Lang: i18n.LangEN}
	checked := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := templateFS.ReadFile("templates/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range keyRe.FindAllStringSubmatch(string(b), -1) {
			key := m[1]
			checked++
			if en.T(key) == key {
				t.Errorf("%s: message key %q is missing from the catalog", e.Name(), key)
			}
		}
	}
	if checked == 0 {
		t.Fatal("scanned no message keys - the scanner regexp is broken")
	}
	t.Logf("checked %d template message keys", checked)
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
	if !strings.Contains(body, "forecast-slot-details") || !strings.Contains(body, "Show slot readings on hover") {
		t.Fatal("expected forecast slot-detail pref control")
	}
	if !strings.Contains(body, `lang="en"`) {
		t.Fatal("expected default lang en")
	}
	if strings.Contains(body, `/static/site.css`) {
		t.Fatal("expected site.css to be inlined, not linked")
	}
	if !strings.Contains(body, "@font-face") || !strings.Contains(body, "--nav-h") {
		t.Fatal("expected inlined site.css content in <style>")
	}
}

func TestAboutRedirectsToBenlevin(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("status %d, want %d", rr.Code, http.StatusMovedPermanently)
	}
	if got := rr.Header().Get("Location"); got != aboutRedirectURL {
		t.Fatalf("Location %q, want %q", got, aboutRedirectURL)
	}
}

func TestNavAboutLinksToBenlevin(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/prefs", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, `href="https://benlevin.fyi/"`) {
		t.Fatal("expected nav About link to point at benlevin.fyi")
	}
	if strings.Contains(body, `href="/about"`) {
		t.Fatal("nav still links to the removed /about page")
	}
}

func TestThemeSwitch(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/prefs?theme=light", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rr.Code)
	}
	found := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "ikite_theme" && c.Value == "light" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected ikite_theme=light cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "/prefs", nil)
	req.AddCookie(&http.Cookie{Name: "ikite_theme", Value: "light"})
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(body, `data-theme="light"`) {
		t.Fatal("expected light theme on html")
	}
	if !strings.Contains(body, "theme=current") {
		t.Fatal("expected theme toggle link back to current")
	}
	if !strings.Contains(body, `class="theme-toggle"`) || !strings.Contains(body, `class="theme-icon"`) {
		t.Fatal("expected theme icon toggle")
	}
}

func TestPrefsPageHebrew(t *testing.T) {
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/prefs", nil)
	req.AddCookie(&http.Cookie{Name: "ikite_lang", Value: "he"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(body, `lang="he"`) || !strings.Contains(body, `dir="rtl"`) {
		t.Fatal("expected Hebrew locale")
	}
	if !strings.Contains(body, "העדפות") {
		t.Fatal("expected Hebrew prefs heading")
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
	// The stuck second KH feed was removed; the remaining camera runs full width.
	if strings.Contains(body, "2Le6SDEO3vc") || strings.Contains(body, `id="extraFrame"`) {
		t.Fatal("did not expect the removed second KH camera")
	}
	if strings.Contains(body, `id="camera-stats"`) || strings.Contains(body, `id="stat-wind"`) {
		t.Fatal("did not expect the bottom wind/gust stats strip")
	}
	if !strings.Contains(body, `id="featuredFrame"`) {
		t.Fatal("expected featured camera frame")
	}
	if !strings.Contains(body, `href="/camera?cam=kh" class="active"`) {
		t.Fatal("expected KH picker active")
	}
	if !strings.Contains(body, `id="kh-cam-wind"`) || !strings.Contains(body, `/api/windometer/live`) {
		t.Fatal("expected KH live wind overlay")
	}
	if !strings.Contains(body, `id="kh-cam-history"`) || !strings.Contains(body, `/api/wind/recent`) {
		t.Fatal("expected KH recent wind history overlay")
	}
	if !strings.Contains(body, "camera-card") {
		t.Fatal("expected camera card layout")
	}
	if strings.Contains(body, "other-cameras") || strings.Contains(body, "Nearby cameras") {
		t.Fatal("did not expect nearby cameras strip")
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
	if !strings.Contains(body, "alias=6469b4554f45e") {
		t.Fatal("expected featured Kiryat Haim cam in all view")
	}
	for _, key := range []string{"bg", "kineret", "hilton"} {
		if !strings.Contains(body, `href="/camera?cam=`+key+`"`) {
			t.Fatalf("expected spot picker link for %s", key)
		}
	}
	if strings.Contains(body, "other-cameras") {
		t.Fatal("did not expect nearby cameras strip")
	}
	if !strings.Contains(body, `href="/camera?cam=all" class="active"`) {
		t.Fatal("expected All picker active")
	}
	if strings.Contains(body, "bottom-nav") {
		t.Fatal("did not expect bottom navigation")
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
		"Threshold":              "10",
		"ForecastTelegram":       "yes",
		"PredictionTelegram":     "yes",
		"AlertTelegramSpotLeft":  "15233",
		"AlertTelegramSpot":      "ky",
		"AlertTelegramSpotRight": "bg",
		"AlertTelegramInterval":  5,
		"AlertIntervals":         []map[string]any{{"Value": 5, "Label": "5 min"}},
		"ForecastStartHour":      8,
		"ForecastEndHour":        22,
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

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	if err := s.render(rr, req, "settings.html", data); err != nil {
		t.Fatal(err)
	}
	out := rr.Body.String()
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

func TestCollectLayoutSpotsIncludesIMS(t *testing.T) {
	kyIMS := 78
	khIMS := 41
	spots := []models.Spot{
		{ID: "st", Name: "Shavei Tzion", Visible: true},
		{ID: "ky", Name: "Kiryat Yam", Visible: true, IMSStationID: &kyIMS},
		{ID: "kh", Name: "Kiryat Haim", Visible: false, IMSStationID: &khIMS},
		{ID: "bg", Name: "Bat Galim", Visible: true},
	}
	layout := collectLayoutSpots(spots)
	keys := make([]string, len(layout))
	for i, s := range layout {
		keys[i] = s.Key
	}
	want := "st,ky,ky-ims,kh,kh-ims,bg"
	if got := strings.Join(keys, ","); got != want {
		t.Fatalf("layout keys=%q want %q", got, want)
	}
	if layout[2].Name != "IMS Afeq" || layout[4].Name != "IMS Refineries" {
		t.Fatalf("ims names: %+v %+v", layout[2], layout[4])
	}
	if !layout[2].Display || layout[4].Display != true {
		t.Fatal("ims columns should default to display on")
	}
}

func TestWindguruSpotURL(t *testing.T) {
	st := 15233
	wg := 373090
	cases := []struct {
		sp   models.Spot
		want string
	}{
		{models.Spot{ID: "15233", WindguruStationID: &st}, "https://www.windguru.cz/station/15233"},
		{models.Spot{ID: "ky", WindguruID: &wg}, ""}, // forecast id is not a station
		{models.Spot{ID: "5500"}, "https://www.windguru.cz/station/5500"},
		{models.Spot{ID: "ky"}, ""},
	}
	for _, tc := range cases {
		if got := windguruSpotURL(tc.sp); got != tc.want {
			t.Fatalf("%+v: got %q want %q", tc.sp, got, tc.want)
		}
	}
}

func TestIndexColumnHasData(t *testing.T) {
	tz := time.UTC
	grouped := map[string]map[string][]models.WindReading{
		"12:00": {
			"kh": {{Period: time.Date(2026, 8, 7, 12, 0, 0, 0, tz), Location: "kh", Wind: 10, Gust: 12, WindDir: 270}},
			"ky": {{Period: time.Date(2026, 8, 7, 12, 0, 0, 0, tz), Location: "ky", Wind: 0, Gust: 0}},
		},
	}
	keys := []string{"12:00"}
	if !indexColumnHasData("kh", grouped, keys, tz) {
		t.Fatal("expected kh to have data")
	}
	if indexColumnHasData("ky", grouped, keys, tz) {
		t.Fatal("expected ky with 0/0 to be empty")
	}
	if indexColumnHasData("missing", grouped, keys, tz) {
		t.Fatal("expected missing location empty")
	}
}

func TestAISummaryFresh(t *testing.T) {
	now := time.Date(2026, 8, 7, 16, 0, 0, 0, time.UTC)
	if !aiSummaryFresh(now.Add(-90*time.Minute), now, 2*time.Hour) {
		t.Fatal("expected fresh within 2h")
	}
	if aiSummaryFresh(now.Add(-3*time.Hour), now, 2*time.Hour) {
		t.Fatal("expected stale beyond 2h")
	}
	if aiSummaryFresh(time.Time{}, now, 2*time.Hour) {
		t.Fatal("expected zero period stale")
	}
}

func TestForecastActualLocationsBay(t *testing.T) {
	kh := forecastActualLocations("kh")
	if strings.Join(kh, ",") != "kh,ims41,ims78" {
		t.Fatalf("kh actuals=%v", kh)
	}
	ky := forecastActualLocations("ky")
	if strings.Join(ky, ",") != "ky,kh,ims41,ims78" {
		t.Fatalf("ky actuals=%v", ky)
	}
	if got := forecastActualLabel("ims41", nil); got != "IMS Haifa Refineries" {
		t.Fatalf("ims41 label=%q", got)
	}
	if got := forecastActualLabel("ims78", nil); got != "IMS Afeq" {
		t.Fatalf("ims78 label=%q", got)
	}
}

func TestAISummaryMeterLive(t *testing.T) {
	tz, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 15, 10, 0, 0, tz)
	// Stored periods come back as wall-clock digits, possibly labelled UTC.
	wall := func(h, m int) time.Time { return time.Date(2026, 10, 2, h, m, 0, 0, time.UTC) }
	if !aiSummaryMeterLive(wall(15, 9), now) {
		t.Fatal("a reading one minute old should keep the summary")
	}
	if aiSummaryMeterLive(wall(14, 30), now) {
		t.Fatal("meter stuck since 14:30 should hide the summary at 15:10")
	}
	if aiSummaryMeterLive(time.Time{}, now) {
		t.Fatal("no reading at all should hide the summary")
	}
}
