package models

import "time"

type WindReading struct {
	Period   time.Time
	Location string
	Wind     float64
	Gust     float64
	WindDir  float64
	Temp     *float64
	Humidity *float64
	Pressure *float64
	// Raw optional source payload (e.g. full IMS channel JSON) for wind_data_log.
	Raw string
	// SourceAt is when the upstream station actually took the reading; zero when
	// the source gives no time or marks the reading stale. Not stored — the
	// collector uses it to refuse stuck data before stamping a reading onto the
	// current table slot.
	SourceAt time.Time
}

type Forecast struct {
	Period   time.Time
	Location string
	ReportHe string
	ReportEn string
}

type HomeWind struct {
	Datetime   time.Time
	Wind       float64
	WindSensor float64
}

// Spot is a dashboard column; id matches wind_data.location.
type Spot struct {
	ID                string
	Name              string
	WindguruStationID *int
	WindguruID        *int
	// WindguruHiresID is an official Windguru spot nearby whose high-resolution
	// models (WRF* 1 km, Zephr-HD, WRF 3/9 km, ICON 7) are free, for spots whose
	// own WindguruID is a custom spot where Windguru keeps those for PRO users.
	WindguruHiresID    *int
	Lat                *float64
	Lon                *float64
	IMSStationID       *int
	SortOrder          int
	Visible            bool
	Collect            bool
	CollectIntervalMin int
	CollectStartHour   int
	CollectEndHour     int
}

// HasCoords reports whether the spot has usable lat/lon for point forecasts.
func (sp Spot) HasCoords() bool {
	return sp.Lat != nil && sp.Lon != nil && (*sp.Lat != 0 || *sp.Lon != 0)
}

// ObservedHour is a spot meter's readings folded into one hour, centred on the
// hour (T-30m .. T+30m) so it lines up with an hourly forecast value at T.
type ObservedHour struct {
	Hour     time.Time // wall-clock hour as stored, in UTC; compare by Format, not In()
	Wind     float64   // mean wind over the hour
	GustPeak float64   // highest gust recorded in the hour
	Samples  int
}

// WindEstimate is ikite's blended best-estimate wind for one spot and hour.
type WindEstimate struct {
	Location  string
	Period    time.Time // wall-clock hour as stored, in UTC; compare by Format
	Wind      float64
	Gust      float64
	Low, High float64 // likely range for the wind
	Dir       float64 // FROM, degrees; -1 when unknown
	Models    int
	IssuedAt  time.Time
}

type WindForecastRow struct {
	ForecastDate time.Time
	Location     string
	WindguruID   int
	IDModel      int
	Model        string
	Period       time.Time
	Wind         *float64
	Gust         *float64
	WindDir      *float64
	Temp         *float64
}

// ValidForecastHours are allowed AI forecast start/stop hours (0–24).
// Stop 24 means through end of day; otherwise stop is exclusive (stop 22 → no run from 22:00).
func ValidForecastHours() []int {
	h := make([]int, 25)
	for i := range h {
		h[i] = i
	}
	return h
}

func NormalizeForecastHour(v, fallback int) int {
	if v >= 0 && v <= 24 {
		return v
	}
	return fallback
}

// ForecastInWindow reports whether the forecast job may run at nowHour.
func ForecastInWindow(startHour, endHour, nowHour int) bool {
	startHour = NormalizeForecastHour(startHour, 8)
	endHour = NormalizeForecastHour(endHour, 22)
	if startHour > endHour && endHour < 24 {
		startHour, endHour = 8, 22
	}
	if nowHour < startHour {
		return false
	}
	if endHour >= 24 {
		return true
	}
	return nowHour < endHour
}

// ValidCollectIntervals are allowed update intervals (minutes).
var ValidCollectIntervals = []int{1, 5, 10, 15, 30}

// ValidCollectHours are allowed start/stop hours (inclusive).
func ValidCollectHours() []int {
	h := make([]int, 0, 17)
	for i := 6; i <= 22; i++ {
		h = append(h, i)
	}
	return h
}

func NormalizeCollectInterval(v int) int {
	for _, n := range ValidCollectIntervals {
		if v == n {
			return v
		}
	}
	return 5
}

func NormalizeCollectHour(v, fallback int) int {
	if v >= 6 && v <= 22 {
		return v
	}
	return fallback
}

// CollectSkipReason returns why collection should not run, without needing the
// last-collect time. Empty string means the spot may collect (interval still applies).
func (sp Spot) CollectSkipReason(now time.Time) string {
	if !sp.Collect {
		return "collect disabled"
	}
	hour := now.Hour()
	if hour < sp.CollectStartHour || hour >= sp.CollectEndHour {
		return "outside hours"
	}
	return ""
}

// collectIntervalSlack absorbs timer jitter and post-fetch delays so a 5‑minute
// systemd poll that fires a few seconds early is not skipped until the next cycle
// (which would create a ~10 minute hole when the timer itself is also 5 minutes).
const collectIntervalSlack = 45 * time.Second

// VisibleCollectGridMin is the shared wall-clock cadence for spots with
// spots.visible=1 in the database (server/admin setting — not client layout prefs).
// Those spots share one index-table minute row (:00, :05, :10, …).
const VisibleCollectGridMin = 5

// ShouldCollectAt reports whether a reading should be collected now.
// Start hour is inclusive; stop hour is exclusive (stop 22 → no collection from 22:00).
// Example: start 8 / stop 22 → 08:00:00 through 21:59:59.
func (sp Spot) ShouldCollectAt(now time.Time, lastCollect time.Time) (bool, string) {
	if reason := sp.CollectSkipReason(now); reason != "" {
		return false, reason
	}
	if sp.CollectIntervalMin > 0 && !lastCollect.IsZero() {
		minWait := time.Duration(sp.CollectIntervalMin)*time.Minute - collectIntervalSlack
		if minWait < 0 {
			minWait = 0
		}
		if now.Sub(lastCollect) < minWait {
			return false, "interval not elapsed"
		}
	}
	return true, ""
}

// ShouldCollectSlot is the schedule gate used by collectors: minute slot first,
// then dedupe by stamped slot period (not raw interval). That lets DB-visible
// spots converge onto the shared :00/:05 grid even after an off-grid reading.
func (sp Spot) ShouldCollectSlot(now time.Time, lastCollect time.Time, peers []Spot) (bool, string) {
	if reason := sp.CollectSkipReason(now); reason != "" {
		return false, reason
	}
	if ok, reason := sp.InCollectMinuteSlot(now, peers); !ok {
		return false, reason
	}
	if lastCollect.IsZero() {
		return true, ""
	}
	slot := CollectSlotPeriod(now, sp, peers)
	if !lastCollect.Truncate(time.Minute).Before(slot) {
		return false, "already collected this slot"
	}
	return true, ""
}

// IsVisibleCollectMinute reports whether now falls on the shared visible-spot grid.
func IsVisibleCollectMinute(now time.Time) bool {
	return now.Minute()%VisibleCollectGridMin == 0
}

// nonVisibleMinuteOffsets are minute offsets within [0, interval) that are not
// reserved for visible spots (multiples of VisibleCollectGridMin).
func nonVisibleMinuteOffsets(interval int) []int {
	interval = NormalizeCollectInterval(interval)
	out := make([]int, 0, interval)
	for m := 0; m < interval; m++ {
		if m%VisibleCollectGridMin != 0 {
			out = append(out, m)
		}
	}
	return out
}

// CollectMinuteOffset returns the minute-of-interval this spot should use.
// Spots with spots.visible=1 always use the shared 5‑minute grid (offset 0).
// Non-visible collect spots are spread across the minutes between those slots.
func CollectMinuteOffset(sp Spot, peers []Spot) int {
	if sp.Visible {
		return 0
	}
	interval := NormalizeCollectInterval(sp.CollectIntervalMin)
	offsets := nonVisibleMinuteOffsets(interval)
	if len(offsets) == 0 {
		// interval < grid (e.g. 1): collect on any non-reserved minute.
		return -1
	}
	idx := 0
	n := 0
	for _, p := range peers {
		if p.Visible || !p.Collect {
			continue
		}
		if NormalizeCollectInterval(p.CollectIntervalMin) != interval {
			continue
		}
		if p.ID == sp.ID {
			idx = n
			break
		}
		n++
	}
	return offsets[idx%len(offsets)]
}

// InCollectMinuteSlot reports whether now matches this spot's scheduled minute.
func (sp Spot) InCollectMinuteSlot(now time.Time, peers []Spot) (bool, string) {
	min := now.Minute()
	if sp.Visible {
		if !IsVisibleCollectMinute(now) {
			return false, "waiting for shared visible minute"
		}
		return true, ""
	}
	if IsVisibleCollectMinute(now) {
		return false, "reserved for visible spots"
	}
	offset := CollectMinuteOffset(sp, peers)
	if offset < 0 {
		return true, ""
	}
	interval := NormalizeCollectInterval(sp.CollectIntervalMin)
	if min%interval != offset {
		return false, "wrong minute slot"
	}
	return true, ""
}

// CollectSlotPeriod stamps a reading onto the scheduled minute (zero seconds)
// so visible spots that finish a few seconds apart share one table row key.
func CollectSlotPeriod(now time.Time, sp Spot, peers []Spot) time.Time {
	now = now.Truncate(time.Minute)
	if sp.Visible {
		block := now.Minute() / VisibleCollectGridMin * VisibleCollectGridMin
		return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), block, 0, 0, now.Location())
	}
	offset := CollectMinuteOffset(sp, peers)
	if offset < 0 {
		return now
	}
	interval := NormalizeCollectInterval(sp.CollectIntervalMin)
	block := now.Minute() / interval * interval
	return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), block+offset, 0, 0, now.Location())
}

func CardinalDirection(angle float64) string {
	directions := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	idx := int((angle/45.0)+0.5) % 8
	if idx < 0 {
		idx += 8
	}
	return directions[idx]
}

// WindCSSClass returns a wind-speed level class aligned with the live table legend:
// 0–6, 6–10, 10–14, 14–18, 18+.
func WindCSSClass(wind float64) string {
	switch {
	case wind >= 18:
		return "level4"
	case wind >= 14:
		return "level3"
	case wind >= 10:
		return "level2"
	case wind >= 6:
		return "level1"
	default:
		return "level0"
	}
}
