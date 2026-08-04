package web

import (
	"bytes"
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
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/prediction"
	"github.com/ben/ikite-go/internal/sources/windguru"
	"github.com/ben/ikite-go/internal/store"
	"github.com/ben/ikite-go/internal/wgtimer"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	Cfg   *config.Config
	Store *store.Store
	Log   *slog.Logger
	tmpl  *template.Template
}

func New(cfg *config.Config, st *store.Store, log *slog.Logger) (*Server, error) {
	funcMap := template.FuncMap{
		"round": func(v float64) int { return int(math.Round(v)) },
		"add":   func(a, b float64) float64 { return a + b },
		"inc":   func(i int) int { return i + 1 },
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
				"15233": "Bet", "2752": "Sea", "2256": "Atl", "hp": "Had",
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
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot))))
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
	mux.HandleFunc("GET /about", s.handleAbout)
	mux.HandleFunc("GET /api/spots/collect", s.handleCollectSpotsAPI)
	mux.HandleFunc("GET /api/wind/live", s.handleWindLiveAPI)
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
	return mux
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

type cellData struct {
	Wind    float64
	Gust    float64
	WindDir float64
	Temp    *float64
	CSS     string
	Empty   bool
}

type rowData struct {
	Time  string
	Cells []cellData
}

type indexData struct {
	ReportEN           string
	Headers            []locHeader
	Rows               []rowData
	Period             string
	Active             string
	CollectSpotDefaults template.JS
}

type locHeader struct {
	Key  string
	Name string
	CSS  string
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("p")
	now := time.Now().In(s.Cfg.Timezone)

	var from time.Time
	dateFmt := "15:04"
	switch period {
	case "week":
		from = now.Add(-7 * 24 * time.Hour)
		dateFmt = "02-01 15:04"
	case "day":
		from = now.Add(-2 * time.Hour)
	default:
		from = now.Add(-8 * time.Hour)
		period = ""
	}
	to := now.Add(24 * time.Hour)

	readings, err := s.Store.ListWind(from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	collectSpots, err := s.Store.CollectSpots()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	order := make([]string, 0, len(collectSpots))
	titles := make(map[string]string, len(collectSpots))
	type spotDef struct {
		Key     string `json:"key"`
		Display bool   `json:"display"`
	}
	spotDefs := make([]spotDef, 0, len(collectSpots))
	for _, sp := range collectSpots {
		order = append(order, sp.ID)
		titles[sp.ID] = sp.Name
		spotDefs = append(spotDefs, spotDef{Key: sp.ID, Display: sp.Visible})
	}
	defsJSON, err := json.Marshal(spotDefs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Group by formatted period.
	type key struct {
		formatted string
		period    time.Time
	}
	grouped := map[string]map[string]models.WindReading{}
	var orderKeys []string
	seen := map[string]bool{}

	for _, rd := range readings {
		fk := rd.Period.In(s.Cfg.Timezone).Format(dateFmt)
		if !seen[fk] {
			seen[fk] = true
			orderKeys = append(orderKeys, fk)
		}
		if grouped[fk] == nil {
			grouped[fk] = map[string]models.WindReading{}
		}
		grouped[fk][rd.Location] = rd
	}

	headers := make([]locHeader, 0, len(order))
	for _, loc := range order {
		h := locHeader{Key: loc, Name: titles[loc]}
		for _, fk := range orderKeys {
			if rd, ok := grouped[fk][loc]; ok {
				h.CSS = models.WindCSSClass(math.Round(rd.Wind))
				break
			}
		}
		headers = append(headers, h)
	}

	rows := make([]rowData, 0, len(orderKeys))
	for _, fk := range orderKeys {
		row := rowData{Time: fk, Cells: make([]cellData, 0, len(order))}
		for _, loc := range order {
			rd, ok := grouped[fk][loc]
			if !ok || (rd.Wind == 0 && rd.Gust == 0) {
				row.Cells = append(row.Cells, cellData{Empty: true})
				continue
			}
			row.Cells = append(row.Cells, cellData{
				Wind:    math.Round(rd.Wind),
				Gust:    math.Round(rd.Gust),
				WindDir: math.Round(rd.WindDir) + 180,
				Temp:    rd.Temp,
				CSS:     models.WindCSSClass(math.Round(rd.Wind)),
			})
		}
		rows = append(rows, row)
	}

	var reportEN string
	if f, err := s.Store.LatestForecast("ky"); err == nil && f != nil {
		reportEN = f.ReportEn
	}

	data := indexData{
		ReportEN:           reportEN,
		Headers:            headers,
		Rows:               rows,
		Period:             period,
		Active:             "table",
		CollectSpotDefaults: template.JS(defsJSON),
	}
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		s.Log.Error("render index", "err", err)
	}
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
	if err := s.tmpl.ExecuteTemplate(w, "graph.html", data); err != nil {
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
	for _, mins := range store.ValidAlertTelegramIntervals {
		label := fmt.Sprintf("%d min", mins)
		if mins == 60 {
			label = "1 hour"
		}
		alertIntervals = append(alertIntervals, map[string]any{"Value": mins, "Label": label})
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
	if err := s.tmpl.ExecuteTemplate(w, "settings.html", data); err != nil {
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
	}
	cams := []camOpt{
		{Key: "all", Name: "All"},
		{Key: "kh", Name: "Kiryat Haim"},
		{Key: "ky", Name: "Kiryat Yam"},
		{Key: "bg", Name: "Bat Galim"},
		{Key: "nirvana", Name: "Nirvana"},
		{Key: "mm", Name: "Maagan Michael"},
		{Key: "nahariya", Name: "Nahariya"},
		{Key: "zvulun", Name: "Herzliya Zvulun"},
		{Key: "hilton", Name: "Tel Aviv Hilton"},
		{Key: "kineret", Name: "Kineret"},
	}
	selected := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("cam")))
	valid := false
	for _, c := range cams {
		if c.Key == selected {
			valid = true
			break
		}
	}
	if !valid {
		selected = "kh"
	}
	options := make([]camOpt, len(cams))
	for i, c := range cams {
		c.Active = c.Key == selected
		options[i] = c
	}
	if err := s.tmpl.ExecuteTemplate(w, "camera.html", map[string]any{
		"Active":     "camera",
		"Cam":        selected,
		"CamOptions": options,
		"ShowAll":    selected == "all",
	}); err != nil {
		s.Log.Error("render camera", "err", err)
	}
}

func (s *Server) handlePrefs(w http.ResponseWriter, r *http.Request) {
	if err := s.tmpl.ExecuteTemplate(w, "prefs.html", map[string]any{"Active": "prefs"}); err != nil {
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
	out := make([]spotOut, 0, len(spots))
	for _, sp := range spots {
		out = append(out, spotOut{Key: sp.ID, Name: sp.Name, Display: sp.Visible})
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

func (s *Server) handlePrediction(w http.ResponseWriter, r *http.Request) {
	if err := s.tmpl.ExecuteTemplate(w, "prediction.html", map[string]any{"Active": "prediction"}); err != nil {
		s.Log.Error("render prediction", "err", err)
	}
}

func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(s.Cfg.Timezone)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)

	spots, err := s.Store.ListSpots()
	if err != nil {
		s.Log.Error("forecast spots", "err", err)
		http.Error(w, "failed to load spots", http.StatusInternalServerError)
		return
	}

	selected := strings.TrimSpace(r.URL.Query().Get("spot"))
	if selected == "" {
		selected = "ky"
	}

	type spotOpt struct {
		Key    string
		Name   string
		Active bool
	}
	type fcCell struct {
		Empty   bool
		Wind    float64
		Gust    float64
		WindDir float64
		CSS     string
	}
	type fcRow struct {
		Time string
		Peak bool
		Cells []fcCell
	}

	var options []spotOpt
	selectedOK := false
	for _, sp := range spots {
		if !sp.Visible {
			continue
		}
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
			if options[i].Active {
				selectedOK = true
			}
		}
	}

	spotName := selected
	for _, o := range options {
		if o.Key == selected {
			spotName = o.Name
			break
		}
	}

	rows, err := s.Store.ListWindForecastByLocation(selected, day)
	if err != nil {
		s.Log.Error("forecast load", "spot", selected, "err", err)
		http.Error(w, "failed to load forecast", http.StatusInternalServerError)
		return
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
		if modelOrder[i] == "openWRF" {
			return true
		}
		if modelOrder[j] == "openWRF" {
			return false
		}
		return modelOrder[i] < modelOrder[j]
	})

	byPeriod := map[string]map[string]models.WindForecastRow{}
	var times []string
	timeSeen := map[string]bool{}
	for _, r := range rows {
		t := r.Period.In(s.Cfg.Timezone).Format("15:04")
		if !timeSeen[t] {
			timeSeen[t] = true
			times = append(times, t)
		}
		label := windguru.DisplayModelName(r.IDModel, r.Model)
		if byPeriod[t] == nil {
			byPeriod[t] = map[string]models.WindForecastRow{}
		}
		byPeriod[t][label] = r
	}
	sort.Strings(times)

	peakStartHour, peakEndHour := -1, -1
	if len(modelOrder) > 0 {
		peakModel := modelOrder[0]
		byHour := map[int]float64{}
		for _, t := range times {
			r, ok := byPeriod[t][peakModel]
			if !ok || r.Wind == nil {
				continue
			}
			hr := 0
			if _, err := fmt.Sscanf(t, "%d:", &hr); err != nil {
				continue
			}
			if prev, ok := byHour[hr]; !ok || *r.Wind > prev {
				byHour[hr] = *r.Wind
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
			peakStartHour, peakEndHour = bestHr, bestHr
			if next, ok := byHour[bestHr+1]; ok && next >= best-0.3 {
				peakEndHour = bestHr + 1
			}
		}
	}

	var tableRows []fcRow
	for _, t := range times {
		row := fcRow{Time: t, Cells: make([]fcCell, len(modelOrder))}
		if peakStartHour >= 0 {
			hr := 0
			if _, err := fmt.Sscanf(t, "%d:", &hr); err == nil && hr >= peakStartHour && hr <= peakEndHour {
				row.Peak = true
			}
		}
		for i, m := range modelOrder {
			r, ok := byPeriod[t][m]
			if !ok || r.Wind == nil || r.Gust == nil {
				row.Cells[i] = fcCell{Empty: true}
				continue
			}
			dir := 0.0
			if r.WindDir != nil {
				// Meteorological "from" direction → arrow points where wind goes.
				dir = math.Round(*r.WindDir) + 180
			}
			row.Cells[i] = fcCell{
				Wind:    *r.Wind,
				Gust:    *r.Gust,
				WindDir: dir,
				CSS:     models.WindCSSClass(*r.Wind),
			}
		}
		tableRows = append(tableRows, row)
	}

	data := map[string]any{
		"Active":      "forecast",
		"Date":        day.Format("2006-01-02"),
		"SpotName":    spotName,
		"SpotOptions": options,
		"HasData":     len(tableRows) > 0,
		"Models":      modelOrder,
		"Rows":        tableRows,
	}
	if err := s.tmpl.ExecuteTemplate(w, "forecast.html", data); err != nil {
		s.Log.Error("render forecast", "err", err)
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
	if err := s.tmpl.ExecuteTemplate(w, "spots_rating.html", data); err != nil {
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
	if err := s.tmpl.ExecuteTemplate(w, "spots_map.html", data); err != nil {
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
	if err := s.tmpl.ExecuteTemplate(w, "spot_details.html", data); err != nil {
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
