package web

import (
	"encoding/json"
	"html/template"
	"math"
	"net/http"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// liveMapMaxAge hides a spot whose meter has sent nothing for this long. Spots
// are collected 08:00–22:00, so the evening's last reading still shows (dimmed)
// the next morning until the meter wakes up.
const liveMapMaxAge = 36 * time.Hour

// liveMapSpot is one of our meters on the live wind map.
type liveMapSpot struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Wind  float64 `json:"wind"`
	Gust  float64 `json:"gust"`
	Dir   float64 `json:"dir"` // the direction the wind comes from; -1 unknown
	At    string  `json:"at"`  // RFC3339, Israel time
	Level string  `json:"level"`
}

// liveMapSpots is the latest reading of every collected spot that has a
// place on the map: the same meters and the same merging as the table, so a
// secondary meter joins its spot with the lower wind and the higher gust.
func (s *Server) liveMapSpots(now time.Time) ([]liveMapSpot, error) {
	spots, err := s.Store.CollectSpots()
	if err != nil {
		return nil, err
	}
	secondaries := map[string][]string{}
	for loc, parent := range models.SecondaryMeters {
		secondaries[parent] = append(secondaries[parent], loc)
	}
	out := []liveMapSpot{}
	for _, sp := range withoutSecondaryMeters(spots) {
		if !sp.HasCoords() {
			continue
		}
		rd, err := s.Store.LatestWindReading(sp.ID)
		if err != nil {
			s.Log.Error("live map reading", "spot", sp.ID, "err", err)
			continue
		}
		if rd == nil || now.Sub(rd.Period) > liveMapMaxAge {
			continue
		}
		for _, sec := range secondaries[sp.ID] {
			o, err := s.Store.LatestWindReading(sec)
			if err != nil || o == nil || !o.Period.Equal(rd.Period) || o.Wind == 0 {
				continue
			}
			rd.Wind, rd.Gust = math.Min(rd.Wind, o.Wind), math.Max(rd.Gust, o.Gust)
		}
		out = append(out, liveMapPoint(sp, *rd, s.Cfg.Timezone))
	}
	return out, nil
}

func liveMapPoint(sp models.Spot, rd models.WindReading, tz *time.Location) liveMapSpot {
	wind, gust := math.Round(rd.Wind), math.Round(rd.Gust)
	dir := -1.0
	// As in the table, a direction of exactly 0 is a meter that sent none.
	if rd.WindDir != 0 {
		dir = math.Mod(math.Round(rd.WindDir)+360, 360)
	}
	if tz == nil {
		tz = time.Local
	}
	return liveMapSpot{
		ID: sp.ID, Name: sp.Name, Lat: *sp.Lat, Lon: *sp.Lon,
		Wind: wind, Gust: gust, Dir: dir,
		At:    rd.Period.In(tz).Format(time.RFC3339),
		Level: models.WindCSSClass(wind),
	}
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
