package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/i18n"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/prediction"
	"github.com/ben/ikite-go/internal/sources/ims"
	"github.com/ben/ikite-go/internal/sources/windguru"
	"github.com/ben/ikite-go/internal/store"
	"github.com/ben/ikite-go/internal/theme"
	"github.com/ben/ikite-go/internal/wgtimer"
)

// aboutRedirectURL is where the old /about page now lives.
const aboutRedirectURL = "https://benlevin.fyi/"

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	Cfg   *config.Config
	Store *store.Store
	Log   *slog.Logger
	tmpl  *template.Template

	windometerLiveMu    sync.RWMutex
	windometerLiveBody  []byte
	windometerLiveAt    time.Time
	windometerLiveSF    singleflight.Group
	windometerLiveFetch func(ctx context.Context, now time.Time) ([]byte, error)

	accMu    sync.Mutex
	accCache map[string]accuracyEntry
	accSF    singleflight.Group
}

func New(cfg *config.Config, st *store.Store, log *slog.Logger) (*Server, error) {
	cssBytes, err := staticFS.ReadFile("static/site.css")
	if err != nil {
		return nil, fmt.Errorf("embed site.css: %w", err)
	}
	// Inline into HTML to remove the render-blocking /static/site.css round-trip
	// (file is small ~5 KiB gzip). Avoids CLS from deferred stylesheet loading.
	siteCSS := template.CSS(cssBytes)

	defaultTr := &i18n.Translator{Lang: i18n.LangEN}
	funcMap := template.FuncMap{
		"T":        defaultTr.T,
		"lang":     func() string { return i18n.LangEN },
		"dir":      func() string { return "ltr" },
		"langURL":  func(tag string) string { return "?lang=" + tag },
		"theme":    func() string { return theme.Current },
		"themeURL": func(tag string) string { return "?theme=" + tag },
		"siteCSS":  func() template.CSS { return siteCSS },
		"i18nJS":   func() template.JS { return template.JS(defaultTr.JSON()) },
		"round":    func(v float64) int { return int(math.Round(v)) },
		"add":      func(a, b float64) float64 { return a + b },
		"inc":      func(i int) int { return i + 1 },
		"subtract": func(a, b int) int {
			if a < b {
				return 0
			}
			return a - b
		},
		"mod": func(a, b int) int { return a % b },
		"temp": func(p *float64) string {
			if p == nil {
				return ""
			}
			return fmt.Sprintf("%.0f", *p)
		},
		"num": func(p *float64) string {
			if p == nil {
				return ""
			}
			return fmt.Sprintf("%.0f", *p)
		},
		"css":  models.WindCSSClass,
		"safe": func(s string) template.HTML { return template.HTML(s) },
		"boldKY": func(name, key string) template.HTML {
			if key == "ky" {
				return template.HTML("<b>" + template.HTMLEscapeString(name) + "</b>")
			}
			return template.HTML(template.HTMLEscapeString(name))
		},
		"shortSpot": func(key, name string) string {
			abbrevs := map[string]string{
				"ky": "KY", "kh": "KH", "bg": "BG", "st": "ST",
				"ky-ims": "AFQ", "kh-ims": "REF", "hp-ims": "HDP",
				"15233": "BET", "2752": "SEA", "2256": "Atl", "hp": "Had",
				"5730": "Mer", "5731": "Zem", "5732": "Avn", "1909": "Dia",
				"3379": "Kin", "1091": "Par", "5500": "Myk", "14905": "Squ",
				"2667": "Tar", "3708": "LA", "4257": "Zan",
			}
			if s, ok := abbrevs[key]; ok {
				return s
			}
			if len(name) <= 6 {
				return name
			}
			return name[:5] + "."
		},
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		Cfg:   cfg,
		Store: st,
		Log:   log,
		tmpl:  tmpl,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	staticRoot, err := fs.Sub(staticFS, "static")
	if err == nil {
		mux.Handle("GET /static/", http.StripPrefix("/static/", staticFileServer(staticRoot)))
	}
	mux.HandleFunc("GET /favicon.ico", serveStaticFile("favicon.ico", "image/x-icon"))
	mux.HandleFunc("GET /apple-touch-icon.png", serveStaticFile("apple-touch-icon.png", "image/png"))
	mux.HandleFunc("GET /apple-touch-icon-precomposed.png", serveStaticFile("apple-touch-icon.png", "image/png"))
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /graph", s.handleGraph)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSettings)
	mux.HandleFunc("POST /settings/spots", s.handleSettingsSpots)
	mux.HandleFunc("POST /settings/spots/add", s.handleSettingsSpotsAdd)
	mux.HandleFunc("POST /settings/forecast", s.handleSettingsForecast)
	mux.HandleFunc("GET /prefs", s.handlePrefs)
	// The About page moved to benlevin.fyi; keep the old URL working for bookmarks,
	// inbound links, and search results.
	mux.HandleFunc("GET /about", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, aboutRedirectURL, http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /api/spots/collect", s.handleCollectSpotsAPI)
	mux.HandleFunc("GET /api/wind/live", s.handleWindLiveAPI)
	mux.HandleFunc("GET /api/wind/recent", s.handleWindRecentAPI)
	mux.HandleFunc("GET /api/wind/table", s.handleWindTableAPI)
	mux.HandleFunc("GET /api/windometer/live", s.handleWindometerLiveAPI)
	mux.HandleFunc("GET /camera", s.handleCamera)
	mux.HandleFunc("GET /prediction", s.handlePrediction)
	mux.HandleFunc("GET /api/prediction", s.handlePredictionAPI)
	mux.HandleFunc("GET /forecast", s.handleForecast)
	mux.HandleFunc("GET /spots/rating", s.handleSpotsRating)
	mux.HandleFunc("GET /spots/map", s.handleSpotsMap)
	mux.HandleFunc("GET /spots/details", s.handleSpotDetails)
	mux.HandleFunc("GET /home", s.handleHome)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return withGzip(withStaticCache(theme.Middleware(i18n.Middleware(mux))))
}

func serveStaticFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		// Prefer revalidation over long immutable cache so icon updates are visible.
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	}
}

type indexSample struct {
	TimeLabel string
	Wind      float64
	Gust      float64
	WindDir   float64
}

type cellData struct {
	Wind     float64
	Gust     float64
	WindDir  float64
	DirFrom  float64
	HasDir   bool
	Temp     *float64
	Pressure *float64
	Humidity *float64
	CSS      string
	Empty    bool
	Samples  []indexSample
	Hot      bool
}

type indexColSpec struct {
	Key    string
	Name   string
	Loc    string
	IsIMS  bool
	Parent string
}

type locHeader struct {
	Key         string
	Name        string
	CSS         string
	DirLabel    string
	IsIMS       bool
	IMSOf       string // parent spot key when IsIMS
	WindguruURL string // https://www.windguru.cz/{station|spot id}; empty if none
}

// windguruSpotURL returns the Windguru station page for a spot.
// Prefer windguru_station_id; fall back to numeric spot id. Forecast-only windguru_id is not a station.
func windguruSpotURL(sp models.Spot) string {
	if sp.WindguruStationID != nil && *sp.WindguruStationID > 0 {
		return fmt.Sprintf("https://www.windguru.cz/station/%d", *sp.WindguruStationID)
	}
	if id, err := strconv.Atoi(sp.ID); err == nil && id > 0 {
		return fmt.Sprintf("https://www.windguru.cz/station/%d", id)
	}
	return ""
}

type rowData struct {
	Time  string
	Cells []cellData
}

type spotCardData struct {
	Key      string
	Name     string
	Wind     float64
	Gust     float64
	DirLabel string
	CSS      string
	Online   bool
	Active   bool
}

type indexData struct {
	Report              string
	ReportStars         string
	ReportBody          string
	Headers             []locHeader
	Rows                []rowData
	SpotCards           []spotCardData
	Updated             string
	LatestPeriod        string
	Period              string
	Active              string
	CollectSpotDefaults template.JS
}

type indexTableView struct {
	Headers      []locHeader
	Rows         []rowData
	Updated      string
	LatestPeriod time.Time
	Period       string
	SpotDefsJSON []byte
}

// indexPeriodSpec describes how one ?p= value is rendered: how far back to
// query, how wide a slot each row covers, how that row is labelled, and how
// many rows may reach the DOM.
//
// Readings land roughly once a minute, so a period only covers its advertised
// span if rows are bucketed — one row per minute would need 1440 rows for a day
// and 10080 for a week.
type indexPeriodSpec struct {
	From    time.Time
	Bucket  time.Duration
	DateFmt string
	Period  string
	MaxRows int
	// MaxSamples caps the per-cell hover detail. Wide slots hold dozens of
	// readings, and every sample is markup in every cell of every row, so the
	// coarser the slot the fewer are worth shipping.
	MaxSamples int
}

// indexPeriodWindow maps the ?p= query value onto a spec. Row caps sit slightly
// above the exact bucket count so a partially aligned first slot cannot trim the
// oldest end of the window.
func indexPeriodWindow(period string, now time.Time) indexPeriodSpec {
	switch period {
	case "week":
		// Rolling 7 days in hourly slots: 168 rows.
		return indexPeriodSpec{
			From:       now.Add(-7 * 24 * time.Hour),
			Bucket:     time.Hour,
			DateFmt:    "02-01 15:04",
			Period:     "week",
			MaxRows:    176,
			MaxSamples: 0,
		}
	case "8h":
		// Rolling 8 hours in 5-minute slots: 96 rows.
		return indexPeriodSpec{
			From:       now.Add(-8 * time.Hour),
			Bucket:     5 * time.Minute,
			DateFmt:    "15:04",
			Period:     "8h",
			MaxRows:    104,
			MaxSamples: 5,
		}
	case "day":
		// The whole day so far, in 10-minute slots: up to 144 rows. Anchored at
		// midnight rather than now-24h so the "15:04" labels stay unique — a
		// rolling window would fold yesterday's 14:30 into today's.
		return indexPeriodSpec{
			From:       time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()),
			Bucket:     10 * time.Minute,
			DateFmt:    "15:04",
			Period:     "day",
			MaxRows:    152,
			MaxSamples: 3,
		}
	default:
		// Default home table: recent window keeps DOM smaller for mobile.
		return indexPeriodSpec{
			From:       now.Add(-4 * time.Hour),
			Bucket:     time.Minute,
			DateFmt:    "15:04",
			Period:     "",
			MaxRows:    18,
			MaxSamples: maxIndexSamples,
		}
	}
}

// indexBucketKey labels the row a reading belongs to, folding readings into
// slots spec.Bucket wide. Truncate aligns to the epoch, which lands on
// wall-clock boundaries for every whole-hour timezone offset.
func indexBucketKey(t time.Time, tz *time.Location, spec indexPeriodSpec) string {
	if tz == nil {
		tz = time.UTC
	}
	t = t.In(tz)
	if spec.Bucket > time.Minute {
		t = t.Truncate(spec.Bucket)
	}
	return t.Format(spec.DateFmt)
}

// imsColumnSpots get a dedicated IMS column next to them in the live table.
var imsColumnSpots = map[string]bool{"ky": true, "kh": true, "hp": true}

// imsColumnName returns the display label for a dedicated IMS table column.
func imsColumnName(parentID, imsLoc string) string {
	switch imsLoc {
	case "ims78":
		return "IMS Afeq"
	case "ims41":
		return "IMS Refineries"
	case "ims46":
		return "IMS Hadera Port"
	default:
		if parentID == "ky" {
			return "KY IMS"
		}
		if parentID == "kh" {
			return "KH IMS"
		}
		return "IMS"
	}
}

// collectLayoutSpots returns wind-table layout keys (real spots + dedicated IMS columns).
func collectLayoutSpots(spots []models.Spot) []struct {
	Key     string
	Name    string
	Display bool
} {
	out := make([]struct {
		Key     string
		Name    string
		Display bool
	}, 0, len(spots)+2)
	for _, sp := range spots {
		out = append(out, struct {
			Key     string
			Name    string
			Display bool
		}{Key: sp.ID, Name: sp.Name, Display: sp.Visible})
		if imsColumnSpots[sp.ID] && sp.IMSStationID != nil {
			imsLoc := ims.LocationKey(*sp.IMSStationID)
			out = append(out, struct {
				Key     string
				Name    string
				Display bool
			}{
				Key:     sp.ID + "-ims",
				Name:    imsColumnName(sp.ID, imsLoc),
				Display: true,
			})
		}
	}
	return out
}

// forecastSpotDefs renders the collect-spot layout in the shape prefs-layout.js
// expects: {key, display}, display seeded from the spot's DB visibility.
func forecastSpotDefs(spots []models.Spot) []struct {
	Key     string `json:"key"`
	Display bool   `json:"display"`
} {
	layout := collectLayoutSpots(spots)
	out := make([]struct {
		Key     string `json:"key"`
		Display bool   `json:"display"`
	}, 0, len(layout))
	for _, sp := range layout {
		out = append(out, struct {
			Key     string `json:"key"`
			Display bool   `json:"display"`
		}{Key: sp.Key, Display: sp.Display})
	}
	return out
}

func (s *Server) buildIndexTableView(period string, now time.Time) (*indexTableView, error) {
	spec := indexPeriodWindow(period, now)
	from, period := spec.From, spec.Period
	to := now.Add(24 * time.Hour)

	readings, err := s.Store.ListWind(from, to)
	if err != nil {
		return nil, err
	}
	collectSpots, err := s.Store.CollectSpots()
	if err != nil {
		return nil, err
	}

	order := make([]string, 0, len(collectSpots))
	titles := make(map[string]string, len(collectSpots))
	wgURLBySpot := make(map[string]string, len(collectSpots))
	imsLocBySpot := make(map[string]string, len(collectSpots))
	type spotDef struct {
		Key     string `json:"key"`
		Display bool   `json:"display"`
	}
	layoutSpots := collectLayoutSpots(collectSpots)
	spotDefs := make([]spotDef, 0, len(layoutSpots))
	for _, sp := range layoutSpots {
		spotDefs = append(spotDefs, spotDef{Key: sp.Key, Display: sp.Display})
	}
	for _, sp := range collectSpots {
		order = append(order, sp.ID)
		titles[sp.ID] = sp.Name
		if u := windguruSpotURL(sp); u != "" {
			wgURLBySpot[sp.ID] = u
		}
		if sp.IMSStationID != nil {
			imsLocBySpot[sp.ID] = ims.LocationKey(*sp.IMSStationID)
		}
	}
	defsJSON, err := json.Marshal(spotDefs)
	if err != nil {
		return nil, err
	}

	grouped := map[string]map[string][]models.WindReading{}
	var orderKeys []string
	seen := map[string]bool{}
	var latestPeriod time.Time
	for _, rd := range readings {
		if rd.Period.After(latestPeriod) {
			latestPeriod = rd.Period
		}
		fk := indexBucketKey(rd.Period, s.Cfg.Timezone, spec)
		if !seen[fk] {
			seen[fk] = true
			orderKeys = append(orderKeys, fk)
		}
		if grouped[fk] == nil {
			grouped[fk] = map[string][]models.WindReading{}
		}
		grouped[fk][rd.Location] = append(grouped[fk][rd.Location], rd)
	}

	cols := make([]indexColSpec, 0, len(order)+2)
	for _, loc := range order {
		cols = append(cols, indexColSpec{Key: loc, Name: titles[loc], Loc: loc})
		if imsColumnSpots[loc] && imsLocBySpot[loc] != "" {
			cols = append(cols, indexColSpec{
				Key: loc + "-ims", Name: imsColumnName(loc, imsLocBySpot[loc]), Loc: imsLocBySpot[loc],
				IsIMS: true, Parent: loc,
			})
		}
	}

	keptCols := cols[:0]
	for _, col := range cols {
		if indexColumnHasData(col.Loc, grouped, orderKeys, s.Cfg.Timezone) {
			keptCols = append(keptCols, col)
		}
	}
	cols = keptCols

	headers := make([]locHeader, 0, len(cols))
	for _, col := range cols {
		h := locHeader{Key: col.Key, Name: col.Name, IsIMS: col.IsIMS, IMSOf: col.Parent}
		if !col.IsIMS {
			h.WindguruURL = wgURLBySpot[col.Key]
		}
		for _, fk := range orderKeys {
			if cell := aggregateIndexCell(grouped[fk][col.Loc], s.Cfg.Timezone, 0); !cell.Empty {
				h.CSS = cell.CSS
				break
			}
		}
		headers = append(headers, h)
	}

	visibleKeys := make(map[string]bool, len(collectSpots))
	for _, sp := range collectSpots {
		if sp.Visible {
			visibleKeys[sp.ID] = true
		}
	}

	rows := make([]rowData, 0, len(orderKeys))
	for _, fk := range orderKeys {
		row := rowData{Time: fk, Cells: make([]cellData, 0, len(cols))}
		for _, col := range cols {
			row.Cells = append(row.Cells, aggregateIndexCell(grouped[fk][col.Loc], s.Cfg.Timezone, spec.MaxSamples))
		}
		// Prefer rows with first-page (DB visible) data so the default table
		// does not paint sparse off-page minutes that JS later hides (CLS).
		if !indexRowHasVisibleData(row, cols, visibleKeys) {
			continue
		}
		rows = append(rows, row)
	}

	// Cap table DOM size for Lighthouse / mobile parse cost. Newest first.
	if len(rows) > spec.MaxRows {
		rows = rows[:spec.MaxRows]
	}

	// Drop columns with no readings in the *displayed* rows (a column may have
	// older data in the window that was capped away — still looks empty).
	headers, rows = pruneEmptyDisplayedColumns(headers, rows)

	updated := now.Format("15:04")
	if len(rows) > 0 {
		updated = rows[0].Time
		if period == "week" && len(updated) > 5 {
			if i := strings.LastIndex(updated, " "); i >= 0 {
				updated = updated[i+1:]
			}
		}
	}

	return &indexTableView{
		Headers:      headers,
		Rows:         rows,
		Updated:      updated,
		LatestPeriod: latestPeriod,
		Period:       period,
		SpotDefsJSON: defsJSON,
	}, nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("p")
	now := time.Now().In(s.Cfg.Timezone)
	view, err := s.buildIndexTableView(period, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var report string
	// The summary describes the Kiryat Yam meter ("Currently 10.7 knots"). When
	// that meter has stopped reporting, the summary is as stale as the reading,
	// so hide it with the meter rather than let it outlive the data.
	kyLatest, kyErr := s.Store.LatestWindPeriod("ky")
	if kyErr != nil {
		s.Log.Warn("ky latest period", "err", kyErr)
	}
	if f, err := s.Store.LatestForecast("ky"); err == nil && f != nil &&
		aiSummaryFresh(f.Period, now, 2*time.Hour) && aiSummaryMeterLive(kyLatest, now) {
		if i18n.Resolve(r) == i18n.LangHE && strings.TrimSpace(f.ReportHe) != "" {
			report = f.ReportHe
		} else {
			report = f.ReportEn
		}
	}
	stars, reportBody := splitWindReport(report)

	latestPeriod := ""
	if !view.LatestPeriod.IsZero() {
		latestPeriod = view.LatestPeriod.UTC().Format(time.RFC3339)
	}
	data := indexData{
		Report:              report,
		ReportStars:         stars,
		ReportBody:          reportBody,
		Headers:             view.Headers,
		Rows:                view.Rows,
		Updated:             view.Updated,
		LatestPeriod:        latestPeriod,
		Period:              view.Period,
		Active:              "table",
		CollectSpotDefaults: template.JS(view.SpotDefsJSON),
	}
	if err := s.render(w, r, "index.html", data); err != nil {
		s.Log.Error("render index", "err", err)
	}
}

func (s *Server) handleWindTableAPI(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("p")
	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	now := time.Now().In(s.Cfg.Timezone)

	latest, err := s.Store.LatestWindPeriodAny()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	latestRFC := ""
	if !latest.IsZero() {
		latestRFC = latest.UTC().Format(time.RFC3339)
	}

	if sinceRaw != "" && latestRFC != "" {
		if since, err := time.Parse(time.RFC3339, sinceRaw); err == nil && !latest.After(since) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"changed":      false,
				"latestPeriod": latestRFC,
			})
			return
		}
	}

	view, err := s.buildIndexTableView(period, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if view.LatestPeriod.IsZero() && latestRFC != "" {
		view.LatestPeriod = latest
	}

	var buf bytes.Buffer
	tr := i18n.FromRequest(r)
	th := theme.Resolve(r)
	tmpl, err := s.tmpl.Clone()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Funcs(template.FuncMap{
		"T":        tr.T,
		"lang":     func() string { return tr.Lang },
		"dir":      func() string { return tr.Dir() },
		"langURL":  func(tag string) string { return i18n.SwitchURL(r, tag) },
		"theme":    func() string { return th },
		"themeURL": func(tag string) string { return theme.SwitchURL(r, tag) },
		"i18nJS":   func() template.JS { return template.JS(tr.JSON()) },
	})
	if err := tmpl.ExecuteTemplate(&buf, "wind_table", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	outLatest := view.LatestPeriod.UTC().Format(time.RFC3339)
	if view.LatestPeriod.IsZero() {
		outLatest = latestRFC
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"changed":      true,
		"latestPeriod": outLatest,
		"updated":      view.Updated,
		"html":         buf.String(),
	})
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("p")
	now := time.Now().In(s.Cfg.Timezone)
	var from time.Time
	switch period {
	case "week":
		from = now.Add(-7 * 24 * time.Hour)
	case "day":
		from = now.Add(-24 * time.Hour)
	default:
		from = now.Add(-8 * time.Hour)
	}
	to := now.Add(24 * time.Hour)

	readings, err := s.Store.ListWind(from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	seriesByID := map[string][][2]float64{}
	for _, rd := range readings {
		ms := float64(rd.Period.UnixMilli())
		seriesByID[rd.Location] = append(seriesByID[rd.Location], [2]float64{ms, rd.Wind})
	}

	names, err := s.Store.SpotNames()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	series := map[string][][2]float64{}
	var locList []string
	for loc, pts := range seriesByID {
		label := names[loc]
		if label == "" {
			label = loc
		}
		locList = append(locList, label)
		series[label] = pts
	}

	data := map[string]any{
		"Series": series,
		"Locs":   locList,
		"Period": period,
		"Active": "graph",
	}
	if err := s.render(w, r, "graph.html", data); err != nil {
		s.Log.Error("render graph", "err", err)
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSettings(w, r) {
		return
	}
	pass := settingsPassFromRequest(r)
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		threshold := r.FormValue("threshold")
		if btn := r.FormValue("threshold_btn"); btn != "" {
			threshold = btn
		}
		if threshold != "" {
			if _, err := strconv.Atoi(threshold); err == nil {
				_ = s.Store.SetSetting("threshold", threshold)
			}
		}
		http.Redirect(w, r, settingsPathWithPass("/settings", pass), http.StatusSeeOther)
		return
	}

	if t := r.URL.Query().Get("t"); t != "" {
		if _, err := strconv.Atoi(t); err == nil {
			_ = s.Store.SetSetting("threshold", t)
		}
	}

	cur, _ := s.Store.GetSetting("threshold")
	if cur == "" {
		cur = "10"
	}
	forecastTelegram, _ := s.Store.GetSetting("forecast_telegram")
	if forecastTelegram == "" {
		forecastTelegram = "yes"
	}
	predictionTelegram, _ := s.Store.GetSetting("prediction_telegram")
	if predictionTelegram == "" {
		predictionTelegram = "yes"
	}
	forecastStart, forecastEnd, _ := s.Store.ForecastSchedule()
	alertSpotLeft, _ := s.Store.AlertTelegramSpotLeft()
	alertSpot, _ := s.Store.AlertTelegramSpot()
	alertSpotRight, _ := s.Store.AlertTelegramSpotRight()
	alertInterval, _ := s.Store.AlertTelegramIntervalMin()
	spotRows, _ := s.settingsSpotRows()
	buttons := make([]int, 0, 13)
	for i := 9; i <= 21; i++ {
		buttons = append(buttons, i)
	}
	alertIntervals := make([]map[string]any, 0, len(store.ValidAlertTelegramIntervals))
	lang := i18n.Resolve(r)
	for _, mins := range store.ValidAlertTelegramIntervals {
		alertIntervals = append(alertIntervals, map[string]any{
			"Value": mins,
			"Label": i18n.IntervalLabel(mins, lang),
		})
	}
	data := map[string]any{
		"Threshold":              cur,
		"ForecastTelegram":       forecastTelegram,
		"PredictionTelegram":     predictionTelegram,
		"AlertTelegramSpotLeft":  alertSpotLeft,
		"AlertTelegramSpot":      alertSpot,
		"AlertTelegramSpotRight": alertSpotRight,
		"AlertTelegramInterval":  alertInterval,
		"AlertIntervals":         alertIntervals,
		"ForecastStartHour":      forecastStart,
		"ForecastEndHour":        forecastEnd,
		"Spots":                  spotRows,
		"Buttons":                buttons,
		"CollectIntervals":       models.ValidCollectIntervals,
		"CollectHours":           models.ValidCollectHours(),
		"ForecastHours":          models.ValidForecastHours(),
		"Pass":                   pass,
		"Active":                 "settings",
	}
	if err := s.render(w, r, "settings.html", data); err != nil {
		s.Log.Error("render settings", "err", err)
	}
}

func (s *Server) handleSettingsSpots(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSettings(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	valid := map[string]bool{}
	spots, err := s.Store.ListSpots()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, sp := range spots {
		valid[sp.ID] = true
	}

	var collectKeys []string
	for _, key := range strings.Split(r.FormValue("collect_spots"), ",") {
		key = strings.TrimSpace(key)
		if key != "" && valid[key] {
			collectKeys = append(collectKeys, key)
		}
	}
	if err := s.Store.SetCollectSpots(collectKeys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if sched := r.FormValue("spot_schedules"); sched != "" {
		var schedules map[string]struct {
			Interval int `json:"interval"`
			Start    int `json:"start"`
			End      int `json:"end"`
		}
		if err := json.Unmarshal([]byte(sched), &schedules); err != nil {
			http.Error(w, "invalid spot schedules", http.StatusBadRequest)
			return
		}
		for id, sc := range schedules {
			if !valid[id] {
				continue
			}
			if err := s.Store.UpdateSpotSchedule(id, sc.Interval, sc.Start, sc.End); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleSettingsSpotsAdd(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSettings(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	wgStr := strings.TrimSpace(r.FormValue("windguru_id"))
	name := strings.TrimSpace(r.FormValue("name"))
	wgID, err := strconv.Atoi(wgStr)
	if err != nil || wgID <= 0 {
		http.Error(w, "invalid windguru station id", http.StatusBadRequest)
		return
	}
	if name == "" {
		http.Error(w, "spot name is required", http.StatusBadRequest)
		return
	}

	spot, err := s.Store.InsertWindguruSpot(name, wgID)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := wgtimer.Queue(s.Cfg.WGTimerQueueDir, wgID); err != nil {
		s.Log.Error("queue wg timer", "station", wgID, "err", err)
		http.Error(w, "spot saved but failed to queue collector timer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":   true,
		"id":   spot.ID,
		"name": spot.Name,
	})
}

func (s *Server) handleSettingsForecast(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeSettings(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	v := r.FormValue("forecast_telegram")
	predV := r.FormValue("prediction_telegram")
	alertSpotLeft := strings.TrimSpace(r.FormValue("alert_telegram_spot_left"))
	alertSpot := strings.TrimSpace(r.FormValue("alert_telegram_spot"))
	alertSpotRight := strings.TrimSpace(r.FormValue("alert_telegram_spot_right"))
	alertIntervalStr := strings.TrimSpace(r.FormValue("alert_telegram_interval_min"))
	if v != "" && v != "yes" && v != "no" {
		http.Error(w, "invalid forecast_telegram value", http.StatusBadRequest)
		return
	}
	if predV != "" && predV != "yes" && predV != "no" {
		http.Error(w, "invalid prediction_telegram value", http.StatusBadRequest)
		return
	}
	if v != "" {
		if err := s.Store.SetSetting("forecast_telegram", v); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if predV != "" {
		if err := s.Store.SetSetting("prediction_telegram", predV); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if alertSpotLeft != "" {
		if _, err := s.Store.SpotByID(alertSpotLeft); err != nil {
			http.Error(w, "invalid alert telegram left spot", http.StatusBadRequest)
			return
		}
		if err := s.Store.SetAlertTelegramSpotLeft(alertSpotLeft); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if alertSpot != "" {
		if _, err := s.Store.SpotByID(alertSpot); err != nil {
			http.Error(w, "invalid alert telegram spot", http.StatusBadRequest)
			return
		}
		if err := s.Store.SetAlertTelegramSpot(alertSpot); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if alertSpotRight != "" {
		if _, err := s.Store.SpotByID(alertSpotRight); err != nil {
			http.Error(w, "invalid alert telegram right spot", http.StatusBadRequest)
			return
		}
		if err := s.Store.SetAlertTelegramSpotRight(alertSpotRight); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if alertIntervalStr != "" {
		n, err := strconv.Atoi(alertIntervalStr)
		if err != nil {
			http.Error(w, "invalid alert telegram interval", http.StatusBadRequest)
			return
		}
		ok := false
		for _, allowed := range store.ValidAlertTelegramIntervals {
			if n == allowed {
				ok = true
				break
			}
		}
		if !ok {
			http.Error(w, "invalid alert telegram interval", http.StatusBadRequest)
			return
		}
		if err := s.Store.SetAlertTelegramIntervalMin(n); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	startStr := strings.TrimSpace(r.FormValue("forecast_start_hour"))
	endStr := strings.TrimSpace(r.FormValue("forecast_end_hour"))
	if v == "" && predV == "" && alertSpotLeft == "" && alertSpot == "" && alertSpotRight == "" && alertIntervalStr == "" && startStr == "" && endStr == "" {
		http.Error(w, "no settings provided", http.StatusBadRequest)
		return
	}
	if startStr != "" || endStr != "" {
		start, end, _ := s.Store.ForecastSchedule()
		if startStr != "" {
			n, err := strconv.Atoi(startStr)
			if err != nil || n < 0 || n > 24 {
				http.Error(w, "invalid forecast start hour", http.StatusBadRequest)
				return
			}
			start = n
		}
		if endStr != "" {
			n, err := strconv.Atoi(endStr)
			if err != nil || n < 0 || n > 24 {
				http.Error(w, "invalid forecast end hour", http.StatusBadRequest)
				return
			}
			end = n
		}
		if err := s.Store.SetForecastSchedule(start, end); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) settingsSpotRows() ([]map[string]any, error) {
	spots, err := s.Store.ListSpots()
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(spots))
	for _, sp := range spots {
		rows = append(rows, map[string]any{
			"Key":              sp.ID,
			"Name":             sp.Name,
			"Visible":          sp.Visible,
			"Collect":          sp.Collect,
			"CollectInterval":  sp.CollectIntervalMin,
			"CollectStartHour": sp.CollectStartHour,
			"CollectEndHour":   sp.CollectEndHour,
		})
	}
	return rows, nil
}

func (s *Server) handleCamera(w http.ResponseWriter, r *http.Request) {
	type camOpt struct {
		Key    string
		Name   string
		Active bool
		Live   bool
	}
	type camCard struct {
		Key        string
		Name       string
		Embed      string
		EmbedLabel string // shown above the first feed when a card carries two
		YouTube    bool
		ExtraEmbed string // optional second feed (e.g. the KY south-west view)
		ExtraLabel string
		Live       bool
		HasWind    bool
	}
	tr := i18n.FromRequest(r)
	catalog := []struct {
		Key, Msg, Embed string
		YouTube, Live   bool
		ExtraEmbed      string
		ExtraMsg        string
		EmbedMsg        string
	}{
		{
			Key: "kh", Msg: "camera.kh",
			Embed:   "https://ipcamlive.com/player/player.php?alias=6469b4554f45e&autoplay=1&skin=white&hidelink=true&disablereportbutton=true&disableautofullscreen=1&disablezoombutton=1&disableframecapture=1&disableuserpause=1",
			YouTube: false,
			Live:    true,
			// The second KH feed (YouTube Lemon5) is dropped: it kept serving a
			// frozen frame. Restore ExtraEmbed/ExtraMsg here when it is live again.
		},
		{
			Key: "ky", Msg: "camera.ky",
			// Both feeds are Surf Cycle's Kiryat Yam cameras, the pair surfo.co.il
			// shows on its weather page. Muted autoplay is a YouTube requirement:
			// an unmuted embed is blocked from starting on its own.
			Embed:      "https://www.youtube.com/embed/9lbGbvFREx0?autoplay=1&mute=1",
			EmbedMsg:   "camera.ky_nw",
			ExtraEmbed: "https://www.youtube.com/embed/xwJTEInXEeE?autoplay=1&mute=1",
			ExtraMsg:   "camera.ky_sw",
			YouTube:    true,
			Live:       true,
			// Previously a single feed, EfU0pofsmew, from the same channel.
		},
		{"bg", "camera.bg", "https://ipcamlive.com/player/player.php?alias=60997a5d22ca9&autoplay=0&skin=white&hidelink=true&disablereportbutton=true&disableautofullscreen=1&disablezoombutton=1&disableframecapture=1", false, true, "", "", ""},
		{"nirvana", "camera.nirvana", "https://ipcamlive.com/player/player.php?alias=60acaa1aeee83&autoplay=0&skin=white&hidelink=true&disablereportbutton=true&disableautofullscreen=1&disablezoombutton=1&disableframecapture=1", false, false, "", "", ""},
		{"mm", "camera.mm", "https://ipcamlive.com/player/player.php?alias=5b17c8292282c&autoplay=0&skin=white&hidelink=true&disablereportbutton=true&disableautofullscreen=1&disablezoombutton=1&disableframecapture=1", false, true, "", "", ""},
		{"nahariya", "camera.nahariya", "https://ipcamlive.com/player/player.php?alias=65f00165e60f4&autoplay=0&skin=white&hidelink=true&disablereportbutton=true&disableautofullscreen=1&disablezoombutton=1&disableframecapture=1", false, true, "", "", ""},
		{"zvulun", "camera.zvulun", "https://ipcamlive.com/player/player.php?alias=634548590c353&autoplay=0&skin=white&hidelink=true&disablereportbutton=true&disableautofullscreen=1&disablezoombutton=1&disableframecapture=1", false, true, "", "", ""},
		{"hilton", "camera.hilton", "https://ipcamlive.com/player/player.php?alias=63454584c4c3c&autoplay=0&skin=white&hidelink=true&disablereportbutton=true&disableautofullscreen=1&disablezoombutton=1&disableframecapture=1", false, true, "", "", ""},
		{"kineret", "camera.kineret", "https://ipcamlive.com/player/player.php?alias=61a4836d911ac&autoplay=0&skin=white&hidelink=true&disablereportbutton=true&disableautofullscreen=1&disablezoombutton=1&disableframecapture=1", false, true, "", "", ""},
	}
	selected := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("cam")))
	if selected == "" {
		selected = "kh"
	}
	valid := selected == "all"
	for _, c := range catalog {
		if c.Key == selected {
			valid = true
			break
		}
	}
	if !valid {
		selected = "kh"
	}

	options := []camOpt{{Key: "all", Name: tr.T("camera.all"), Active: selected == "all", Live: false}}
	for _, c := range catalog {
		options = append(options, camOpt{
			Key: c.Key, Name: tr.T(c.Msg), Active: c.Key == selected, Live: c.Live,
		})
	}

	featuredKey := selected
	if selected == "all" {
		featuredKey = "kh"
	}
	var featured camCard
	for _, c := range catalog {
		if c.Key != featuredKey {
			continue
		}
		featured = camCard{
			Key: c.Key, Name: tr.T(c.Msg), Embed: c.Embed,
			YouTube: c.YouTube, Live: c.Live, HasWind: c.Key == "kh",
			ExtraEmbed: c.ExtraEmbed,
		}
		if c.EmbedMsg != "" {
			featured.EmbedLabel = tr.T(c.EmbedMsg)
		}
		if c.ExtraMsg != "" {
			featured.ExtraLabel = tr.T(c.ExtraMsg)
		}
		break
	}

	if err := s.render(w, r, "camera.html", map[string]any{
		"Active":     "camera",
		"Cam":        selected,
		"CamOptions": options,
		"ShowAll":    selected == "all",
		"Featured":   featured,
	}); err != nil {
		s.Log.Error("render camera", "err", err)
	}
}

func (s *Server) handlePrefs(w http.ResponseWriter, r *http.Request) {
	if err := s.render(w, r, "prefs.html", map[string]any{"Active": "prefs"}); err != nil {
		s.Log.Error("render prefs", "err", err)
	}
}

func (s *Server) handleCollectSpotsAPI(w http.ResponseWriter, r *http.Request) {
	spots, err := s.Store.CollectSpots()
	if err != nil {
		s.Log.Error("collect spots api", "err", err)
		http.Error(w, "failed to load spots", http.StatusInternalServerError)
		return
	}
	type spotOut struct {
		Key     string `json:"key"`
		Name    string `json:"name"`
		Display bool   `json:"display"`
	}
	layout := collectLayoutSpots(spots)
	out := make([]spotOut, 0, len(layout))
	for _, sp := range layout {
		out = append(out, spotOut{Key: sp.Key, Name: sp.Name, Display: sp.Display})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"spots": out}); err != nil {
		s.Log.Error("encode collect spots", "err", err)
	}
}

func (s *Server) handleWindLiveAPI(w http.ResponseWriter, r *http.Request) {
	var keys []string
	if spotParam := strings.TrimSpace(r.URL.Query().Get("spots")); spotParam != "" {
		for _, k := range strings.Split(spotParam, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				keys = append(keys, k)
			}
		}
	} else {
		spots, err := s.Store.CollectSpots()
		if err != nil {
			s.Log.Error("wind live spots", "err", err)
			http.Error(w, "failed to load spots", http.StatusInternalServerError)
			return
		}
		for _, sp := range spots {
			keys = append(keys, sp.ID)
		}
	}

	type spotWind struct {
		Wind   float64 `json:"wind"`
		Gust   float64 `json:"gust"`
		Period string  `json:"period,omitempty"`
	}
	out := make(map[string]spotWind, len(keys))
	for _, key := range keys {
		wind, gust, err := s.Store.LatestWindGust(key)
		if err != nil {
			s.Log.Error("wind live", "spot", key, "err", err)
			continue
		}
		sw := spotWind{Wind: wind, Gust: gust}
		if period, err := s.Store.LatestWindPeriod(key); err == nil && !period.IsZero() {
			sw.Period = period.In(s.Cfg.Timezone).Format(time.RFC3339)
		}
		out[key] = sw
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"spots": out}); err != nil {
		s.Log.Error("encode wind live", "err", err)
	}
}

// handleWindRecentAPI returns newest-first wind samples for a location.
// Supports ?since=RFC3339 — when the latest sample is not newer, returns {"changed":false}.
func (s *Server) handleWindRecentAPI(w http.ResponseWriter, r *http.Request) {
	loc := strings.TrimSpace(r.URL.Query().Get("location"))
	if loc == "" {
		loc = windometerLiveKH
	}
	limit := 120
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}

	latest, err := s.Store.LatestWindPeriod(loc)
	if err != nil {
		s.Log.Error("wind recent latest", "location", loc, "err", err)
		http.Error(w, "failed to load readings", http.StatusInternalServerError)
		return
	}
	latestRFC := ""
	if !latest.IsZero() {
		latestRFC = latest.UTC().Format(time.RFC3339)
	}

	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	if sinceRaw != "" && latestRFC != "" {
		if since, err := time.Parse(time.RFC3339, sinceRaw); err == nil && !latest.After(since) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":           true,
				"changed":      false,
				"location":     loc,
				"latestPeriod": latestRFC,
			})
			return
		}
	}

	rows, err := s.Store.ListWindByLocation(loc, limit)
	if err != nil {
		s.Log.Error("wind recent list", "location", loc, "err", err)
		http.Error(w, "failed to load readings", http.StatusInternalServerError)
		return
	}

	type readingJSON struct {
		Period  string  `json:"period"`
		Time    string  `json:"time"`
		Wind    float64 `json:"wind"`
		Gust    float64 `json:"gust"`
		WindDir float64 `json:"wind_dir"`
		CSS     string  `json:"css"`
	}
	tz := time.Local
	if s.Cfg != nil && s.Cfg.Timezone != nil {
		tz = s.Cfg.Timezone
	}
	out := make([]readingJSON, 0, len(rows))
	for _, rd := range rows {
		p := rd.Period.In(tz)
		out = append(out, readingJSON{
			Period:  p.UTC().Format(time.RFC3339),
			Time:    p.Format("15:04"),
			Wind:    rd.Wind,
			Gust:    rd.Gust,
			WindDir: rd.WindDir,
			CSS:     models.WindCSSClass(rd.Wind),
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":           true,
		"changed":      true,
		"location":     loc,
		"latestPeriod": latestRFC,
		"readings":     out,
	}); err != nil {
		s.Log.Error("encode wind recent", "err", err)
	}
}

func (s *Server) handlePrediction(w http.ResponseWriter, r *http.Request) {
	if err := s.render(w, r, "prediction.html", map[string]any{"Active": "prediction"}); err != nil {
		s.Log.Error("render prediction", "err", err)
	}
}

func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(s.Cfg.Timezone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)

	spots, err := s.Store.ListSpots()
	if err != nil {
		s.Log.Error("forecast spots", "err", err)
		http.Error(w, "failed to load spots", http.StatusInternalServerError)
		return
	}

	explicitSpot := strings.TrimSpace(r.URL.Query().Get("spot"))
	selected := explicitSpot
	if selected == "" {
		if c, err := r.Cookie(forecastSpotCookie); err == nil {
			selected = strings.TrimSpace(c.Value)
		}
	}
	if selected == "" {
		selected = "ky"
	}

	type spotOpt struct {
		Key    string
		Name   string
		Active bool
	}

	// The picker offers every spot that is saved to the DB (settings "Save to DB"),
	// because those are exactly the spots a forecast can exist for. Which of them a
	// given viewer actually sees, and in what order, comes from their /prefs
	// selection — that lives in localStorage, so the filtering happens client-side
	// against CollectSpotDefaults below.
	var options []spotOpt
	var collectSpots []models.Spot
	selectedOK := false
	spotNames := map[string]string{}
	for _, sp := range spots {
		spotNames[sp.ID] = sp.Name
		if !sp.Collect {
			continue
		}
		collectSpots = append(collectSpots, sp)
		active := sp.ID == selected
		if active {
			selectedOK = true
		}
		options = append(options, spotOpt{Key: sp.ID, Name: sp.Name, Active: active})
	}
	if !selectedOK {
		selected = "ky"
		for i := range options {
			options[i].Active = options[i].Key == selected
		}
	} else if explicitSpot != "" {
		// Only a spot the viewer actually chose is remembered — never the fallback,
		// or a stale cookie would keep re-pinning itself.
		http.SetCookie(w, &http.Cookie{
			Name:     forecastSpotCookie,
			Value:    selected,
			Path:     "/",
			MaxAge:   365 * 24 * 3600,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}

	// Same shape as the index page's defaults, including the ky-ims / kh-ims
	// pseudo-columns: prefs are shared between the two pages, and handing the
	// merge a short list would prune those keys out of the saved order.
	spotDefsJSON, err := json.Marshal(forecastSpotDefs(collectSpots))
	if err != nil {
		s.Log.Error("forecast spot defs", "err", err)
		spotDefsJSON = []byte("[]")
	}

	spotName := selected
	if n, ok := spotNames[selected]; ok {
		spotName = n
	}

	maxDate, err := s.Store.MaxWindForecastDate(selected)
	if err != nil {
		s.Log.Error("forecast max date", "spot", selected, "err", err)
		http.Error(w, "failed to load forecast", http.StatusInternalServerError)
		return
	}
	if maxDate.IsZero() {
		maxDate = today
	} else {
		maxDate = time.Date(maxDate.Year(), maxDate.Month(), maxDate.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	}

	fromDate := today
	if v := strings.TrimSpace(r.URL.Query().Get("from")); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, s.Cfg.Timezone); err == nil {
			fromDate = t
		}
	}
	toDate := maxDate
	if v := strings.TrimSpace(r.URL.Query().Get("to")); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, s.Cfg.Timezone); err == nil {
			toDate = t
		}
	} else if !fromDate.Equal(today) {
		toDate = fromDate.AddDate(0, 0, 10)
	}
	if toDate.Before(fromDate) {
		toDate = fromDate
	}

	rows, err := s.Store.ListWindForecastByLocationRange(selected, fromDate, toDate)
	if err != nil {
		s.Log.Error("forecast load", "spot", selected, "err", err)
		http.Error(w, "failed to load forecast", http.StatusInternalServerError)
		return
	}

	actualLocs := forecastActualLocations(selected)
	// For non-bay spots, still attach that spot's configured IMS station when present.
	if selected != "ky" && selected != "kh" {
		if sp, err := s.Store.SpotByID(selected); err == nil && sp.IMSStationID != nil {
			imsLoc := ims.LocationKey(*sp.IMSStationID)
			found := false
			for _, loc := range actualLocs {
				if loc == imsLoc {
					found = true
					break
				}
			}
			if !found {
				actualLocs = append(actualLocs, imsLoc)
			}
		}
	}
	actualLabels := make([]string, len(actualLocs))
	for i, loc := range actualLocs {
		actualLabels[i] = forecastActualLabel(loc, spotNames)
	}

	actualByLoc := map[string][]models.WindReading{}
	if len(actualLocs) > 0 {
		actualFrom := fromDate
		actualTo := toDate.Add(24 * time.Hour)
		readings, err := s.Store.ListWind(actualFrom.Add(-time.Hour), actualTo)
		if err != nil {
			s.Log.Error("forecast actuals", "err", err)
		} else {
			for _, rd := range readings {
				actualByLoc[rd.Location] = append(actualByLoc[rd.Location], rd)
			}
			for loc := range actualByLoc {
				sort.Slice(actualByLoc[loc], func(i, j int) bool {
					return actualByLoc[loc][i].Period.Before(actualByLoc[loc][j].Period)
				})
			}
		}
	}

	modelOrder := []string{}
	modelSeen := map[string]bool{}
	for _, r := range rows {
		label := windguru.DisplayModelName(r.IDModel, r.Model)
		if !modelSeen[label] {
			modelSeen[label] = true
			modelOrder = append(modelOrder, label)
		}
	}
	sort.SliceStable(modelOrder, func(i, j int) bool {
		rank := func(label string) int {
			switch label {
			case "openWRF":
				return 0
			case "skiron":
				return 1
			case "aifs":
				return 2
			case "ukmo":
				return 3
			default:
				return 10
			}
		}
		ri, rj := rank(modelOrder[i]), rank(modelOrder[j])
		if ri != rj {
			return ri < rj
		}
		return modelOrder[i] < modelOrder[j]
	})
	periodsByDay := map[string][]time.Time{}
	periodSeen := map[int64]bool{}
	for _, r := range rows {
		p := r.Period.In(s.Cfg.Timezone)
		if !forecastDisplayPeriod(p) {
			continue
		}
		ts := p.Unix()
		if periodSeen[ts] {
			continue
		}
		periodSeen[ts] = true
		dayKey := p.Format("2006-01-02")
		periodsByDay[dayKey] = append(periodsByDay[dayKey], p)
	}
	for dayKey := range periodsByDay {
		sort.Slice(periodsByDay[dayKey], func(i, j int) bool {
			return periodsByDay[dayKey][i].Before(periodsByDay[dayKey][j])
		})
	}

	byModelPeriod := map[string]map[int64]models.WindForecastRow{}
	for _, r := range rows {
		label := windguru.DisplayModelName(r.IDModel, r.Model)
		ts := r.Period.In(s.Cfg.Timezone).Unix()
		if byModelPeriod[label] == nil {
			byModelPeriod[label] = map[int64]models.WindForecastRow{}
		}
		byModelPeriod[label][ts] = r
	}

	lang := i18n.Resolve(r)
	tr := i18n.FromRequest(r)
	actualTitle := tr.T("forecast.actual_title")
	for i, label := range actualLabels {
		if strings.HasPrefix(actualLocs[i], "ims") {
			// IMS headers already name the station; skip the "(real)" suffix.
			continue
		}
		actualLabels[i] = tr.T("forecast.actual_label", label)
	}
	var dayCards []fcDayCard
	for d := fromDate; !d.After(toDate); d = d.AddDate(0, 0, 1) {
		dayKey := d.Format("2006-01-02")
		showActuals := !d.After(today)
		periods := periodsByDay[dayKey]
		if len(periods) == 0 {
			if !showActuals {
				continue
			}
			// Historical / today with no forecast: still show meter slots.
			periods = forecastHourlySlots(d, s.Cfg.Timezone)
		}

		dayModels := forecastModelsForDay(byModelPeriod, modelOrder, periods)
		dayModelCols := make([]modelCol, 0, len(dayModels))
		for _, label := range dayModels {
			dayModelCols = append(dayModelCols, modelCol{Label: label, Title: forecastModelTitle(label)})
		}

		// Build actual columns. For KY/KH bay views, always show KH + both IMS stations;
		// KY meter column is included only when it has readings that day.
		dayActualLocs := []string(nil)
		dayActualCols := []actualCol(nil)
		if showActuals {
			bayActuals := selected == "ky" || selected == "kh"
			for i, loc := range actualLocs {
				has := false
				for pi := range periods {
					start, end := actualSlotBounds(periods, pi)
					if cell := aggregateActualCell(actualByLoc[loc], start, end, s.Cfg.Timezone); !cell.Empty {
						has = true
						break
					}
				}
				if bayActuals {
					if loc == "ky" && !has {
						continue
					}
				} else if !has {
					continue
				}
				dayActualLocs = append(dayActualLocs, loc)
				title := actualTitle
				if strings.HasPrefix(loc, "ims") {
					title = tr.T("forecast.ims_title")
				}
				dayActualCols = append(dayActualCols, actualCol{
					Label: actualLabels[i],
					Title: title,
				})
			}
		}

		card := fcDayCard{
			DayLabel:   i18n.FormatForecastDay(d, lang),
			Date:       dayKey,
			ActualCols: dayActualCols,
			ModelCols:  dayModelCols,
		}
		peakByModel := forecastDayPeaks(byModelPeriod, dayModels, d, s.Cfg.Timezone)
		for pi, p := range periods {
			row := fcTimeRow{
				TimeLabel: p.Format("15:04"),
				Actuals:   make([]forecastCell, len(dayActualLocs)),
				Cells:     make([]forecastCell, len(dayModels)),
			}
			start, end := actualSlotBounds(periods, pi)
			for i, loc := range dayActualLocs {
				row.Actuals[i] = aggregateActualCell(actualByLoc[loc], start, end, s.Cfg.Timezone)
			}
			hr := p.Hour()
			for i, model := range dayModels {
				peak := peakByModel[model]
				fr, ok := byModelPeriod[model][p.Unix()]
				if !ok || fr.Wind == nil || fr.Gust == nil {
					row.Cells[i] = forecastCell{Empty: true}
					continue
				}
				dir := 0.0
				if fr.WindDir != nil {
					dir = math.Round(*fr.WindDir) + 180
				}
				css := models.WindCSSClass(*fr.Wind)
				if peak[0] >= 0 && hr >= peak[0] && hr <= peak[1] {
					css = "peak"
				}
				row.Cells[i] = forecastCell{
					Wind:    *fr.Wind,
					Gust:    *fr.Gust,
					WindDir: dir,
					CSS:     css,
				}
			}
			card.Rows = append(card.Rows, row)
		}
		dayCards = append(dayCards, card)
	}

	overview := buildForecastOverview(dayCards, lang)

	// ikite's blended estimate for the same range, plus what the meter has
	// measured so far, so the chart shows how the estimate is tracking today.
	var expectedChartJSON template.JS
	var strongest *strongestHour
	if estRows, err := s.Store.ListWindEstimate(selected, fromDate, toDate.AddDate(0, 0, 1)); err != nil {
		s.Log.Error("forecast estimate", "spot", selected, "err", err)
	} else if len(estRows) > 0 {
		var measured []models.ObservedHour
		if fromDate.Before(now) {
			if measured, err = s.Store.HourlyObserved(selected, fromDate, now, chartFromHour, chartToHour); err != nil {
				s.Log.Error("forecast estimate measured", "spot", selected, "err", err)
				measured = nil
			}
		}
		if c := buildExpectedChart(estRows, measured, fromDate, toDate, lang); c != nil {
			if b, err := c.JSON(); err == nil {
				expectedChartJSON = template.JS(b)
			}
		}
		strongest = findStrongest(estRows, now, lang)
	}

	data := map[string]any{
		"Active":              "forecast",
		"Date":                fromDate.Format("2006-01-02") + " – " + toDate.Format("2006-01-02"),
		"FromDate":            fromDate.Format("2006-01-02"),
		"ToDate":              toDate.Format("2006-01-02"),
		"Today":               today.Format("2006-01-02"),
		"MaxDate":             maxDate.Format("2006-01-02"),
		"Spot":                selected,
		"SpotName":            spotName,
		"SpotOptions":         options,
		"CollectSpotDefaults": template.JS(spotDefsJSON),
		"View":                forecastView(r),
		"Accuracy":            s.modelAccuracyFor(r.Context(), selected, now),
		"ExpectedChart":       expectedChartJSON,
		"Strongest":           strongest,
		"HasData":             len(dayCards) > 0,
		"DayCards":            dayCards,
		"Overview":            overview,
		"OverviewHours":       []string{"06:00", "09:00", "12:00", "15:00", "18:00", "21:00"},
	}
	if err := s.render(w, r, "forecast.html", data); err != nil {
		s.Log.Error("render forecast", "err", err)
	}
}

type forecastOverviewDay struct {
	Date     string                 `json:"date"`
	Weekday  string                 `json:"weekday"`
	DayLabel string                 `json:"dayLabel"`
	Rating   float64                `json:"rating"`
	Status   string                 `json:"status"`
	Cells    []forecastOverviewCell `json:"cells"`
}

type forecastOverviewCell struct {
	Empty   bool    `json:"empty"`
	Hour    string  `json:"hour"`
	Wind    float64 `json:"wind"`
	Gust    float64 `json:"gust"`
	DirFrom float64 `json:"dirFrom"`
	WindDir float64 `json:"windDir"` // meteorological "to" for arrow rotation
	CSS     string  `json:"css"`
}

func buildForecastOverview(dayCards []fcDayCard, lang string) []forecastOverviewDay {
	hours := []string{"06:00", "09:00", "12:00", "15:00", "18:00", "21:00"}
	out := make([]forecastOverviewDay, 0, len(dayCards))
	for _, card := range dayCards {
		d, err := time.ParseInLocation("2006-01-02", card.Date, time.UTC)
		if err != nil {
			continue
		}
		byTime := map[string]forecastCell{}
		for _, row := range card.Rows {
			cell := pickOverviewCell(row, card.ModelCols)
			if !cell.Empty {
				byTime[row.TimeLabel] = cell
			}
		}
		cells := make([]forecastOverviewCell, 0, len(hours))
		var peak float64
		for _, h := range hours {
			c := overviewCellAt(byTime, h)
			oc := forecastOverviewCell{Hour: h, Empty: c.Empty}
			if !c.Empty {
				oc.Wind = math.Round(c.Wind)
				oc.Gust = math.Round(c.Gust)
				if c.CSS == "peak" {
					oc.CSS = models.WindCSSClass(c.Wind)
				} else {
					oc.CSS = c.CSS
				}
				if c.WindDir != 0 {
					oc.WindDir = c.WindDir
					// stored as "to"; convert back to from for degree label
					from := math.Mod(c.WindDir-180+360, 360)
					oc.DirFrom = math.Round(from)
				}
				if oc.Wind > peak {
					peak = oc.Wind
				}
			}
			cells = append(cells, oc)
		}
		rating := math.Round(math.Min(10, peak/2)*10) / 10
		status := "weak"
		if rating >= 8 {
			status = "great"
		} else if rating >= 7 {
			status = "good"
		}
		weekday := d.Weekday().String()[:3]
		dayLabel := d.Format("Jan 2")
		if lang == i18n.LangHE {
			names := []string{"א'", "ב'", "ג'", "ד'", "ה'", "ו'", "ש'"}
			weekday = names[int(d.Weekday())]
			dayLabel = d.Format("2/1")
		}
		out = append(out, forecastOverviewDay{
			Date:     card.Date,
			Weekday:  weekday,
			DayLabel: dayLabel,
			Rating:   rating,
			Status:   status,
			Cells:    cells,
		})
	}
	return out
}

func overviewCellAt(byTime map[string]forecastCell, hour string) forecastCell {
	if c, ok := byTime[hour]; ok && !c.Empty {
		return c
	}
	prefix := hour
	if len(hour) >= 2 {
		prefix = hour[:2]
	}
	var bestLabel string
	var best forecastCell
	for label, cell := range byTime {
		if cell.Empty || !strings.HasPrefix(label, prefix) {
			continue
		}
		if bestLabel == "" || label < bestLabel {
			bestLabel = label
			best = cell
		}
	}
	if bestLabel != "" {
		return best
	}
	// 21:00 often missing in model data; fall back to 20:00.
	if hour == "21:00" {
		if c, ok := byTime["20:00"]; ok && !c.Empty {
			return c
		}
		for label, cell := range byTime {
			if !cell.Empty && strings.HasPrefix(label, "20") {
				return cell
			}
		}
	}
	return forecastCell{Empty: true}
}

func pickOverviewCell(row fcTimeRow, models []modelCol) forecastCell {
	prefer := map[string]int{"openwrf": 0, "skiron": 1, "aifs": 2, "gfs": 3, "ifs": 4, "icon13": 5, "gdps": 6}
	bestIdx, bestRank := -1, 100
	for i, m := range models {
		if i >= len(row.Cells) || row.Cells[i].Empty {
			continue
		}
		rank, ok := prefer[strings.ToLower(m.Label)]
		if !ok {
			rank = 50
		}
		if rank < bestRank {
			bestRank, bestIdx = rank, i
		}
	}
	if bestIdx >= 0 {
		return row.Cells[bestIdx]
	}
	for _, c := range row.Actuals {
		if !c.Empty {
			return c
		}
	}
	return forecastCell{Empty: true}
}

type forecastSample struct {
	TimeLabel string
	Wind      float64
	Gust      float64
	WindDir   float64
}

type forecastCell struct {
	Empty   bool
	Wind    float64
	Gust    float64
	WindDir float64
	CSS     string
	Samples []forecastSample
}

type fcTimeRow struct {
	TimeLabel string
	Actuals   []forecastCell
	Cells     []forecastCell
}

// HasData reports whether the row has any actual or model cell with values.
func (r fcTimeRow) HasData() bool {
	for _, c := range r.Actuals {
		if !c.Empty {
			return true
		}
	}
	for _, c := range r.Cells {
		if !c.Empty {
			return true
		}
	}
	return false
}

// IsHourly reports whether the row is on an hour boundary (xx:00).
// Used to densify the 14-day full grid (hide :10/:20/… meter slots).
func (r fcTimeRow) IsHourly() bool {
	label := strings.TrimSpace(r.TimeLabel)
	if len(label) < 5 {
		return true
	}
	return strings.HasSuffix(label, ":00")
}

type modelCol struct {
	Label string
	Title string
}

type actualCol struct {
	Label string
	Title string
}

type fcDayCard struct {
	DayLabel   string
	Date       string
	ActualCols []actualCol
	ModelCols  []modelCol
	Rows       []fcTimeRow
}

const (
	forecastSpotCookie = "ikite_fc_spot"
	// Set by the page's view tabs; read here so the first paint is already in the
	// viewer's view rather than flashing the default and then switching.
	forecastViewCookie  = "ikite_fc_view"
	forecastDefaultView = "full"
)

func forecastView(r *http.Request) string {
	if c, err := r.Cookie(forecastViewCookie); err == nil {
		switch c.Value {
		case "overview", "detailed", "full":
			return c.Value
		}
	}
	return forecastDefaultView
}

func forecastActualLocations(selected string) []string {
	switch selected {
	case "kh":
		// KH meter + both nearby IMS stations.
		return []string{"kh", "ims41", "ims78"}
	case "ky":
		// KY meter (shown only when it has data) + KH meter + both IMS stations.
		return []string{"ky", "kh", "ims41", "ims78"}
	default:
		return []string{selected}
	}
}

func forecastActualLabel(loc string, spotNames map[string]string) string {
	switch loc {
	case "ky":
		return "KY"
	case "kh":
		return "KH"
	case "ims41":
		return "IMS Haifa Refineries"
	case "ims78":
		return "IMS Afeq"
	default:
		if strings.HasPrefix(loc, "ims") {
			return "IMS"
		}
		if n, ok := spotNames[loc]; ok {
			return n
		}
		return strings.ToUpper(loc)
	}
}

// actualSlotBounds returns [start, end) for the real-wind aggregation window of periods[i].
func actualSlotBounds(periods []time.Time, i int) (time.Time, time.Time) {
	start := periods[i]
	if i+1 < len(periods) {
		return start, periods[i+1]
	}
	if i > 0 {
		gap := periods[i].Sub(periods[i-1])
		if gap > 0 {
			return start, start.Add(gap)
		}
	}
	return start, start.Add(30 * time.Minute)
}

// aiSummaryFresh reports whether an AI wind summary is still recent enough to show.
func aiSummaryFresh(period, now time.Time, maxAge time.Duration) bool {
	if period.IsZero() || maxAge <= 0 {
		return false
	}
	age := now.Sub(period)
	return age >= 0 && age <= maxAge
}

// aiSummaryMeterMaxAge is how old the Kiryat Yam meter's newest reading may be
// before the AI summary is hidden. The meter reports every minute; this allows
// for a missed collector run or two without the summary flickering.
const aiSummaryMeterMaxAge = 10 * time.Minute

// aiSummaryMeterLive reports whether the meter behind the AI summary is still
// reporting. Stored periods are wall-clock digits, so compare them as such.
func aiSummaryMeterLive(latest, now time.Time) bool {
	if latest.IsZero() {
		return false
	}
	wall := time.Date(latest.Year(), latest.Month(), latest.Day(), latest.Hour(), latest.Minute(), latest.Second(), 0, now.Location())
	age := now.Sub(wall)
	return age < aiSummaryMeterMaxAge && age > -aiSummaryMeterMaxAge
}

// splitWindReport separates a leading star rating ("★☆☆ | …") from the summary body.
func splitWindReport(report string) (stars, body string) {
	report = strings.TrimSpace(report)
	if report == "" {
		return "", ""
	}
	if i := strings.Index(report, "|"); i >= 0 {
		left := strings.TrimSpace(report[:i])
		right := strings.TrimSpace(report[i+1:])
		if strings.ContainsAny(left, "★☆*") {
			return left, right
		}
	}
	return "", report
}

// indexColumnHasData reports whether a wind_data location has any displayable cell
// in the current index time window.
func indexColumnHasData(location string, grouped map[string]map[string][]models.WindReading, orderKeys []string, tz *time.Location) bool {
	for _, fk := range orderKeys {
		if cell := aggregateIndexCell(grouped[fk][location], tz, 0); !cell.Empty {
			return true
		}
	}
	return false
}

// pruneEmptyDisplayedColumns removes spot columns that are empty across all
// rows that will actually be rendered.
func pruneEmptyDisplayedColumns(headers []locHeader, rows []rowData) ([]locHeader, []rowData) {
	if len(headers) == 0 {
		return headers, rows
	}
	keep := make([]bool, len(headers))
	for _, row := range rows {
		for i, cell := range row.Cells {
			if i < len(keep) && !cell.Empty {
				keep[i] = true
			}
		}
	}
	kept := 0
	for _, ok := range keep {
		if ok {
			kept++
		}
	}
	if kept == len(headers) {
		return headers, rows
	}
	newHeaders := make([]locHeader, 0, kept)
	idxs := make([]int, 0, kept)
	for i, h := range headers {
		if keep[i] {
			newHeaders = append(newHeaders, h)
			idxs = append(idxs, i)
		}
	}
	newRows := make([]rowData, len(rows))
	for ri, row := range rows {
		cells := make([]cellData, 0, len(idxs))
		for _, i := range idxs {
			if i < len(row.Cells) {
				cells = append(cells, row.Cells[i])
			} else {
				cells = append(cells, cellData{Empty: true})
			}
		}
		newRows[ri] = rowData{Time: row.Time, Cells: cells}
	}
	return newHeaders, newRows
}

func indexRowHasData(row rowData) bool {
	for _, cell := range row.Cells {
		if !cell.Empty {
			return true
		}
	}
	return false
}

func indexRowHasVisibleData(row rowData, cols []indexColSpec, visibleKeys map[string]bool) bool {
	if len(visibleKeys) == 0 {
		return indexRowHasData(row)
	}
	for i, cell := range row.Cells {
		if cell.Empty || i >= len(cols) {
			continue
		}
		col := cols[i]
		key := col.Key
		if col.IsIMS && col.Parent != "" {
			key = col.Parent
		}
		if visibleKeys[key] {
			return true
		}
	}
	return false
}

// maxIndexSamples is the per-cell hover-detail cap for the default table.
const maxIndexSamples = 12

// aggregateIndexCell returns min wind and max gust for readings sharing one table time label.
// Wind direction is taken from the reading that produced the max gust.
// Samples lists every reading when more than one exists (for hover detail).
func aggregateIndexCell(readings []models.WindReading, loc *time.Location, maxSamples int) cellData {
	if loc == nil {
		loc = time.UTC
	}
	matched := make([]models.WindReading, 0, len(readings))
	for _, rd := range readings {
		if rd.Wind == 0 && rd.Gust == 0 {
			continue
		}
		matched = append(matched, rd)
	}
	if len(matched) == 0 {
		return cellData{Empty: true}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Period.Before(matched[j].Period)
	})
	minWind := matched[0].Wind
	maxGust := matched[0].Gust
	dir := matched[0].WindDir
	hasDir := matched[0].WindDir != 0
	var temp, pressure, humidity *float64
	for _, rd := range matched {
		if rd.Wind < minWind {
			minWind = rd.Wind
		}
		if rd.Gust > maxGust {
			maxGust = rd.Gust
			if rd.WindDir != 0 {
				dir = rd.WindDir
				hasDir = true
			}
		} else if !hasDir && rd.WindDir != 0 {
			dir = rd.WindDir
			hasDir = true
		}
		if rd.Temp != nil {
			temp = rd.Temp
		}
		if rd.Pressure != nil {
			pressure = rd.Pressure
		}
		if rd.Humidity != nil {
			humidity = rd.Humidity
		}
	}
	wind := math.Round(minWind)
	gust := math.Round(maxGust)
	cell := cellData{
		Wind:     wind,
		Gust:     gust,
		Temp:     temp,
		Pressure: pressure,
		Humidity: humidity,
		CSS:      models.WindCSSClass(wind),
		Hot:      wind >= 14,
		HasDir:   hasDir,
	}
	if hasDir {
		cell.WindDir = math.Round(dir) + 180
		cell.DirFrom = math.Round(dir)
	}
	if len(matched) > 1 && maxSamples > 0 {
		// A coarse slot can hold dozens of readings. Hover detail only needs a
		// readable spread across the slot, and every sample costs DOM bytes in
		// every cell, so thin evenly rather than emitting all of them.
		step := 1
		if len(matched) > maxSamples {
			step = (len(matched) + maxSamples - 1) / maxSamples
		}
		samples := make([]indexSample, 0, (len(matched)+step-1)/step)
		for i, rd := range matched {
			if i%step != 0 {
				continue
			}
			sample := indexSample{
				TimeLabel: rd.Period.In(loc).Format("15:04:05"),
				Wind:      math.Round(rd.Wind),
				Gust:      math.Round(rd.Gust),
			}
			if rd.WindDir != 0 {
				sample.WindDir = math.Round(rd.WindDir) + 180
			}
			samples = append(samples, sample)
		}
		cell.Samples = samples
	}
	return cell
}

// aggregateActualCell returns min wind and max gust among readings in [start, end).
// Wind direction is taken from the reading that produced the max gust.
// Samples lists every reading in the slot (for hover detail).
func aggregateActualCell(readings []models.WindReading, start, end time.Time, loc *time.Location) forecastCell {
	if loc == nil {
		loc = time.UTC
	}
	matched := make([]models.WindReading, 0, 8)
	for _, rd := range readings {
		if rd.Period.Before(start) || !rd.Period.Before(end) {
			continue
		}
		matched = append(matched, rd)
	}
	if len(matched) == 0 {
		return forecastCell{Empty: true}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Period.Before(matched[j].Period)
	})
	minWind := matched[0].Wind
	maxGust := matched[0].Gust
	dir := matched[0].WindDir
	samples := make([]forecastSample, 0, len(matched))
	for _, rd := range matched {
		if rd.Wind < minWind {
			minWind = rd.Wind
		}
		if rd.Gust > maxGust {
			maxGust = rd.Gust
			dir = rd.WindDir
		}
		samples = append(samples, forecastSample{
			TimeLabel: rd.Period.In(loc).Format("15:04"),
			Wind:      math.Round(rd.Wind),
			Gust:      math.Round(rd.Gust),
			WindDir:   math.Round(rd.WindDir) + 180,
		})
	}
	wind := math.Round(minWind)
	gust := math.Round(maxGust)
	return forecastCell{
		Wind:    wind,
		Gust:    gust,
		WindDir: math.Round(dir) + 180,
		CSS:     models.WindCSSClass(wind),
		Samples: samples,
	}
}

func forecastDayPeaks(byModelPeriod map[string]map[int64]models.WindForecastRow, modelOrder []string, day time.Time, loc *time.Location) map[string][2]int {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)
	peakByModel := map[string][2]int{}
	for _, model := range modelOrder {
		byHour := map[int]float64{}
		for ts, fr := range byModelPeriod[model] {
			pt := time.Unix(ts, 0).In(loc)
			if pt.Before(dayStart) || !pt.Before(dayEnd) || fr.Wind == nil {
				continue
			}
			hr := pt.Hour()
			if prev, ok := byHour[hr]; !ok || *fr.Wind > prev {
				byHour[hr] = *fr.Wind
			}
		}
		bestHr, best := -1, -1.0
		for hr := 0; hr < 24; hr++ {
			w, ok := byHour[hr]
			if !ok || w <= best {
				continue
			}
			best = w
			bestHr = hr
		}
		if bestHr >= 0 {
			end := bestHr
			if next, ok := byHour[bestHr+1]; ok && next >= best-0.3 {
				end = bestHr + 1
			}
			peakByModel[model] = [2]int{bestHr, end}
		}
	}
	return peakByModel
}

func forecastDisplayPeriod(p time.Time) bool {
	h, m := p.Hour(), p.Minute()
	if h < 6 || h > 20 {
		return false
	}
	// 20:00 is the latest slot; hide 20:30.
	if h == 20 && m > 0 {
		return false
	}
	return true
}

func forecastHourlySlots(day time.Time, loc *time.Location) []time.Time {
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	out := make([]time.Time, 0, 15)
	for h := 6; h <= 20; h++ {
		out = append(out, day.Add(time.Duration(h)*time.Hour))
	}
	return out
}

// forecastModelsForDay returns models that have at least one wind/gust value among periods.
func forecastModelsForDay(byModelPeriod map[string]map[int64]models.WindForecastRow, modelOrder []string, periods []time.Time) []string {
	out := make([]string, 0, len(modelOrder))
	for _, model := range modelOrder {
		byPeriod := byModelPeriod[model]
		if byPeriod == nil {
			continue
		}
		for _, p := range periods {
			fr, ok := byPeriod[p.Unix()]
			if ok && fr.Wind != nil && fr.Gust != nil {
				out = append(out, model)
				break
			}
		}
	}
	return out
}

func forecastModelTitle(label string) string {
	switch label {
	case "openWRF":
		return "openWRF 1km (local WRF)"
	case "skiron":
		return "OpenSkiron / openWRF 4km (Mediterranean)"
	case "aifs":
		return "ECMWF AIFS 25km (Open-Meteo)"
	case "ukmo":
		return "UK Met Office global 10km (Open-Meteo)"
	case "gfs":
		return "GFS - Global Forecast System (NOAA)"
	case "ims_sea":
		return "IMS official sea forecast (forecaster-issued, 12-hour blocks; range shown as mid–top)"
	case "wrf1":
		return "WRF* 1km Israel (Windguru)"
	case "zephr":
		return "Zephr-HD 2.6km Middle East (Windguru)"
	case "wrf3":
		return "WRF 3km Israel (Windguru)"
	case "wrf9":
		return "WRF 9km (Windguru)"
	case "icon7":
		return "ICON 7km (DWD)"
	case "icon13":
		return "ICON 13km (DWD)"
	case "gdps":
		return "GDPS - Global Deterministic Prediction System (Canada)"
	case "ifs":
		return "IFS - Integrated Forecasting System (ECMWF)"
	default:
		return label
	}
}

func (s *Server) handleSpotsRating(w http.ResponseWriter, r *http.Request) {
	ratings, err := s.Store.ListSpotRatings()
	if err != nil {
		s.Log.Error("spots rating", "err", err)
		http.Error(w, "failed to load ratings", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Active":  "rating",
		"Ratings": ratings,
	}
	if err := s.render(w, r, "spots_rating.html", data); err != nil {
		s.Log.Error("render spots rating", "err", err)
	}
}

func (s *Server) handleSpotsMap(w http.ResponseWriter, r *http.Request) {
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	country := strings.TrimSpace(r.URL.Query().Get("country"))
	points, err := s.Store.ListSpotMapPoints(country, limit)
	if err != nil {
		s.Log.Error("spots map", "err", err)
		http.Error(w, "failed to load map spots", http.StatusInternalServerError)
		return
	}
	s.Log.Info("spots map", "country", country, "limit", limit, "points", len(points))
	countries, err := s.Store.ListSpotRatingCountries()
	if err != nil {
		s.Log.Error("spots countries", "err", err)
		countries = nil
	}
	pointsJSON, err := json.Marshal(points)
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Active":     "map",
		"Limit":      limit,
		"Country":    country,
		"Countries":  countries,
		"PointsJSON": template.JS(pointsJSON),
		"Count":      len(points),
	}
	if err := s.render(w, r, "spots_map.html", data); err != nil {
		s.Log.Error("render spots map", "err", err)
	}
}

func (s *Server) handleSpotDetails(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("spot_id"))
	if err != nil || id <= 0 {
		http.Error(w, "invalid spot_id", http.StatusBadRequest)
		return
	}
	months, name, err := s.Store.SpotMonthRatings(id)
	if err != nil {
		s.Log.Error("spot details", "err", err)
		http.Error(w, "failed to load spot details", http.StatusInternalServerError)
		return
	}
	if name == "" {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{
		"Active": "rating",
		"SpotID": id,
		"Name":   name,
		"Months": months,
	}
	if err := s.render(w, r, "spot_details.html", data); err != nil {
		s.Log.Error("render spot details", "err", err)
	}
}

func (s *Server) handlePredictionAPI(w http.ResponseWriter, r *http.Request) {
	res, err := prediction.ComputeCached(s.Store, time.Now(), s.Cfg.Timezone)
	if err != nil {
		s.Log.Error("prediction compute", "err", err)
		http.Error(w, "prediction failed", http.StatusInternalServerError)
		return
	}
	res = i18n.LocalizePrediction(res, i18n.Resolve(r))
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(res); err != nil {
		s.Log.Error("encode prediction", "err", err)
	}
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	wStr := r.URL.Query().Get("w")
	if wStr == "" {
		http.Error(w, "Fail", http.StatusBadRequest)
		return
	}
	n, err := strconv.Atoi(wStr)
	if err != nil || n == 0 {
		http.Error(w, "Fail", http.StatusBadRequest)
		return
	}
	h := models.HomeWind{
		Datetime:   time.Now().In(s.Cfg.Timezone),
		Wind:       float64(n),
		WindSensor: float64(n),
	}
	if err := s.Store.InsertHomeWind(h); err != nil {
		http.Error(w, fmt.Sprintf("Fail: %v", err), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write([]byte("OK"))
}
