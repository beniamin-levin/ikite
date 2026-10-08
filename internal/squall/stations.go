package squall

import (
	"math"
	"sort"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// Upwind stations whose readings show a gust front before it reaches Kiryat
// Haim: Bat Galim (SW, 8 km — 12 min ahead on 2026-10-06) and Shavei Tzion
// (N, 17 km — ahead when a storm comes from the north-west).
var Upwind = []struct{ Loc, Name string }{{"bg", "Bat Galim"}, {"st", "Shavei Tzion"}}

const (
	// A gust front at a station: gusts at least this strong…
	confirmGustKt = 18.0
	// …together with rain-cooled air (on 2026-10-06 Bat Galim fell 30.7 → 24.3 °C
	// in five minutes) or a sudden jump in the wind.
	confirmTempDropC = 3.0
	confirmJumpKt    = 10.0
	confirmWindow    = 30 * time.Minute
	confirmMaxAge    = 8 * time.Minute
)

// Confirmation is a station showing a gust front.
type Confirmation struct {
	Loc, Name string
	At        time.Time
	Wind      float64
	Gust      float64
	Dir       float64
	TempDrop  float64 // °C below the warmest reading in the last 30 min; 0 if unknown
	Jump      float64 // kt above the calmest reading in the last 30 min
}

// Confirm checks one station's recent readings (any order).
func Confirm(loc, name string, rows []models.WindReading, now time.Time) *Confirmation {
	if len(rows) == 0 {
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Period.Before(rows[j].Period) })
	last := rows[len(rows)-1]
	if now.Sub(last.Period) > confirmMaxAge || last.Gust < confirmGustKt {
		return nil
	}
	maxTemp, minWind := math.Inf(-1), math.Inf(1)
	for _, r := range rows[:len(rows)-1] {
		if last.Period.Sub(r.Period) > confirmWindow {
			continue
		}
		if r.Temp != nil && *r.Temp > maxTemp {
			maxTemp = *r.Temp
		}
		if r.Wind < minWind {
			minWind = r.Wind
		}
	}
	c := &Confirmation{Loc: loc, Name: name, At: last.Period, Wind: last.Wind, Gust: last.Gust, Dir: last.WindDir}
	if last.Temp != nil && !math.IsInf(maxTemp, -1) {
		c.TempDrop = maxTemp - *last.Temp
	}
	if !math.IsInf(minWind, 1) {
		c.Jump = last.Wind - minWind
	}
	if c.TempDrop >= confirmTempDropC || c.Jump >= confirmJumpKt {
		return c
	}
	return nil
}
