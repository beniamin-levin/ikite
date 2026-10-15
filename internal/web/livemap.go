package web

import (
	"encoding/json"
	"html/template"
	"math"
	"net/http"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// liveMapMaxAge is how old a reading may be and still show. Spots are
// collected 08:00–22:00, so the evening's last reading still shows (dimmed)
// the next morning until the meter wakes up; an older one shows as no reading.
const liveMapMaxAge = 36 * time.Hour

// meterCoords places the spots whose row has no coordinates, from each
// Windguru station's own position (its iapi station payload, 2026-10-15).
var meterCoords = map[string][2]float64{
	"2256":  {32.71216, 34.94514},     // Atlit fortress
	"2259":  {32.488972, 34.889338},   // Freegull Sea Sports, Sdot Yam beach
	"5500":  {37.426337, 25.322609},   // Mykonos, Korfos beach
	"3708":  {37.73502, -0.89178},     // Los Alcázares
	"2667":  {36.02441, -5.613491},    // Tarifa, Campo de Fútbol
	"14905": {49.706989, -123.126636}, // Squamish, BC
	"1091":  {37.03195, 25.098238},    // Paros, Pounda
	"3379":  {32.88, 35.6504},         // Beit Zida (IMS), Kinneret
	"4257":  {52.37978, 4.529579},     // Zandvoort
}

// liveMapSpot is one spot on the live wind map. At is empty when the spot has
// no reading within liveMapMaxAge.
type liveMapSpot struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Wind   float64 `json:"wind"`
	Gust   float64 `json:"gust"`
	Dir    float64 `json:"dir"`          // the direction the wind comes from; -1 unknown
	At     string  `json:"at,omitempty"` // RFC3339, Israel time
	Level  string  `json:"level"`
	Camera string  `json:"camera,omitempty"` // the spot's camera page, when it has one
}

// liveMapSpots is every spot in the spots table with its latest reading. Each
// meter sits at its own place, so a secondary meter (Freegull at Sdot Yam) is
// a pin of its own here rather than merged into its spot as in the table.
func (s *Server) liveMapSpots(now time.Time) ([]liveMapSpot, error) {
	spots, err := s.Store.ListSpots()
	if err != nil {
		return nil, err
	}
	cams := map[string]bool{}
	for _, c := range cameraCatalog {
		cams[c.Key] = true
	}
	out := []liveMapSpot{}
	for _, sp := range spots {
		if !sp.HasCoords() {
			c, ok := meterCoords[sp.ID]
			if !ok {
				continue
			}
			sp.Lat, sp.Lon = &c[0], &c[1]
		}
		rd, err := s.Store.LatestWindReading(sp.ID)
		if err != nil {
			s.Log.Error("live map reading", "spot", sp.ID, "err", err)
			rd = nil
		}
		if rd != nil && now.Sub(rd.Period) > liveMapMaxAge {
			rd = nil
		}
		p := liveMapPoint(sp, rd, s.Cfg.Timezone)
		if cams[sp.ID] {
			p.Camera = "/camera?cam=" + sp.ID
		}
		out = append(out, p)
	}
	return out, nil
}

func liveMapPoint(sp models.Spot, rd *models.WindReading, tz *time.Location) liveMapSpot {
	p := liveMapSpot{ID: sp.ID, Name: sp.Name, Lat: *sp.Lat, Lon: *sp.Lon, Dir: -1}
	if rd == nil {
		return p
	}
	p.Wind, p.Gust = math.Round(rd.Wind), math.Round(rd.Gust)
	// As in the table, a direction of exactly 0 is a meter that sent none.
	if rd.WindDir != 0 {
		p.Dir = math.Mod(math.Round(rd.WindDir)+360, 360)
	}
	if tz == nil {
		tz = time.Local
	}
	p.At = rd.Period.In(tz).Format(time.RFC3339)
	p.Level = models.WindCSSClass(p.Wind)
	return p
}

func (s *Server) handleLiveMap(w http.ResponseWriter, r *http.Request) {
	spots, err := s.liveMapSpots(time.Now())
	if err != nil {
		s.Log.Error("live map", "err", err)
		http.Error(w, "failed to load spots", http.StatusInternalServerError)
		return
	}
	js, err := json.Marshal(spots)
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Active":    "livemap",
		"SpotsJSON": template.JS(js),
	}
	if err := s.render(w, r, "livemap.html", data); err != nil {
		s.Log.Error("render live map", "err", err)
	}
}

func (s *Server) handleLiveMapAPI(w http.ResponseWriter, r *http.Request) {
	spots, err := s.liveMapSpots(time.Now())
	if err != nil {
		s.Log.Error("live map api", "err", err)
		http.Error(w, "failed to load spots", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(map[string]any{"spots": spots}); err != nil {
		s.Log.Error("encode live map", "err", err)
	}
}
