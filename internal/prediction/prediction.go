package prediction

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ben/ikite-go/internal/sources/openwrf"
	"github.com/ben/ikite-go/internal/store"
)

const (
	goodWindKt       = 10.0
	minSimilarity    = 0.55
	minCompareHours  = 2
	compareStartHour = 6
	baselineWeight   = 0.35
)

// Result is a same-day thermal wind forecast for KY (Kiryat Yam).
type Result struct {
	Location        string         `json:"location"`
	Date            string         `json:"date"`
	GeneratedAt     time.Time      `json:"generated_at"`
	StrongestWindow string         `json:"strongest_window"`
	ExpectedPeak    string         `json:"expected_peak"`
	Direction       string         `json:"direction"`
	GoodWindow      string         `json:"good_window"`
	WindDown        string         `json:"wind_down"`
	Source          string         `json:"source,omitempty"`
	Conditions      string         `json:"conditions,omitempty"`
	Current         string         `json:"current,omitempty"`
	BasedOnReadings int            `json:"based_on_readings"`
	SimilarDays     int            `json:"similar_days"`
	History         *HistoryScore  `json:"history,omitempty"`
	OpenWRF         *ModelForecast `json:"openwrf,omitempty"`
}

// ModelForecast summarizes a numeric model forecast used on the prediction page.
type ModelForecast struct {
	Model           string           `json:"model"`
	StrongestWindow string           `json:"strongest_window"`
	ExpectedPeak    string           `json:"expected_peak"`
	Direction       string           `json:"direction"`
	GoodWindow      string           `json:"good_window"`
	WindDown        string           `json:"wind_down"`
	Hours           []ModelHourPoint `json:"hours,omitempty"`
}

type ModelHourPoint struct {
	Time    string  `json:"time"`
	Wind    float64 `json:"wind"`
	Gust    float64 `json:"gust"`
	WindDir float64 `json:"wind_dir"`
}

// Compute matches today's readings against similar historical Jul–Aug days.
func Compute(st *store.Store, now time.Time, loc *time.Location) (*Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return compute(ctx, st, now.In(loc), loc)
}

func compute(ctx context.Context, st *store.Store, now time.Time, loc *time.Location) (*Result, error) {
	_ = ctx // reserved for future store context wiring
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	todayRows, err := st.KyDayHourlyStats(now)
	if err != nil {
		return nil, err
	}
	today := dayProfileFromStats(dayStart, todayRows)
	history, err := st.KySummerDayProfiles(dayStart)
	if err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return nil, fmt.Errorf("prediction: no Jul–Aug history available")
	}
	baseline := hourlyMap(store.BaselineHourlyStats(history))

	matches := findSimilarDays(today, history, now.Hour())
	fc := buildForecast(matches, baseline, today, now.Hour())

	res := &Result{
		Location:        "ky",
		Date:            now.Format("2006-01-02"),
		GeneratedAt:     now,
		StrongestWindow: formatWindow(fc.peakStart, fc.peakEnd),
		ExpectedPeak:    formatPeak(fc.peak),
		Direction:       directionLabel(fc.peak.AvgDir),
		GoodWindow:      formatWindow(fc.goodStart, fc.goodEnd),
		WindDown:        fmt.Sprintf("~%02d:00", fc.windDown),
		Source:          "history",
		BasedOnReadings: store.TotalReadings(history),
		SimilarDays:     len(matches),
		Conditions:      formatMatchConditions(matches, now.Hour(), len(today.Hours) == 0),
	}

	if ow, err := openWRFForecast(st, dayStart, loc); err == nil && ow != nil {
		res.OpenWRF = ow
		// Prefer openWRF for the main prediction cards when available.
		res.StrongestWindow = ow.StrongestWindow
		res.ExpectedPeak = ow.ExpectedPeak
		res.Direction = ow.Direction
		res.GoodWindow = ow.GoodWindow
		res.WindDown = ow.WindDown
		res.Source = openwrf.ModelName
		if res.Conditions != "" {
			res.Conditions = openwrf.ModelName + " model forecast; " + res.Conditions
		} else {
			res.Conditions = openwrf.ModelName + " model forecast"
		}
	}

	if hist, err := evaluateHistory(st, dayStart, loc); err == nil {
		res.History = hist
	}

	if now.Hour() >= 11 {
		_ = savePrediction(st, res, fc)
	}

	wind, gust, _, temp, humidity, pressure, err := st.KyLatestReading()
	if err == nil && wind > 0 {
		res.Current = formatCurrent(wind, gust, temp, humidity, pressure)
	}
	return res, nil
}

func openWRFForecast(st *store.Store, dayStart time.Time, loc *time.Location) (*ModelForecast, error) {
	rows, err := st.ListWindForecastByLocation("ky", dayStart)
	if err != nil {
		return nil, err
	}
	byHour := map[int]hourForecast{}
	var points []ModelHourPoint
	for _, r := range rows {
		if r.IDModel != openwrf.ModelID && r.Model != openwrf.ModelName {
			continue
		}
		if r.Wind == nil || r.Gust == nil || r.WindDir == nil {
			continue
		}
		p := r.Period.In(loc)
		points = append(points, ModelHourPoint{
			Time:    p.Format("15:04"),
			Wind:    *r.Wind,
			Gust:    *r.Gust,
			WindDir: *r.WindDir,
		})

		hr := p.Hour()
		cur := hourForecast{
			AvgWind: *r.Wind,
			AvgGust: *r.Gust,
			MaxGust: *r.Gust,
			AvgDir:  *r.WindDir,
			Weight:  1,
		}
		if prev, ok := byHour[hr]; ok {
			// Keep the stronger half-hour sample for peak detection.
			if cur.AvgWind > prev.AvgWind {
				cur.MaxGust = math.Max(prev.MaxGust, cur.MaxGust)
				byHour[hr] = cur
			} else {
				prev.MaxGust = math.Max(prev.MaxGust, cur.MaxGust)
				byHour[hr] = prev
			}
			continue
		}
		byHour[hr] = cur
	}
	if len(byHour) == 0 {
		return nil, nil
	}
	peakStart, peakEnd := strongestFromForecast(byHour)
	goodStart, goodEnd := goodFromForecast(byHour, goodWindKt)
	windDown := fadeFromForecast(byHour, goodWindKt)
	peak := statsForForecast(byHour, peakStart, peakEnd)
	return &ModelForecast{
		Model:           openwrf.ModelName,
		StrongestWindow: formatWindow(peakStart, peakEnd),
		ExpectedPeak:    formatPeak(peak),
		Direction:       directionLabel(peak.AvgDir),
		GoodWindow:      formatWindow(goodStart, goodEnd),
		WindDown:        fmt.Sprintf("~%02d:00", windDown),
		Hours:           points,
	}, nil
}

func dayProfileFromStats(date time.Time, rows []store.HourlyWindStat) store.DayProfile {
	p := store.DayProfile{Date: date, Hours: map[int]store.HourlyWindStat{}}
	for _, h := range rows {
		p.Hours[h.Hour] = h
	}
	return p
}

type dayMatch struct {
	Day   store.DayProfile
	Score float64
}

type hourForecast struct {
	AvgWind float64
	AvgGust float64
	MaxGust float64
	AvgDir  float64
	Weight  float64
}

type forecastBundle struct {
	peak      hourForecast
	peakStart int
	peakEnd   int
	goodStart int
	goodEnd   int
	windDown  int
}

func findSimilarDays(today store.DayProfile, history []store.DayProfile, nowHour int) []dayMatch {
	var matches []dayMatch
	for _, day := range history {
		if !dayHadGoodWind(day) {
			continue
		}
		score, compared := daySimilarity(today, day, nowHour)
		if compared < minCompareHours || score < minSimilarity {
			continue
		}
		matches = append(matches, dayMatch{Day: day, Score: score})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	return matches
}

func dayHadGoodWind(day store.DayProfile) bool {
	for hr := 9; hr <= 18; hr++ {
		if h, ok := day.Hours[hr]; ok && h.AvgWind >= goodWindKt {
			return true
		}
	}
	return false
}

func daySimilarity(today, hist store.DayProfile, nowHour int) (score float64, compared int) {
	var sum float64
	for hr := compareStartHour; hr <= nowHour; hr++ {
		t, okT := today.Hours[hr]
		h, okH := hist.Hours[hr]
		if !okT || !okH || t.Count == 0 || h.Count == 0 {
			continue
		}
		if !t.AvgTemp.Valid || !h.AvgTemp.Valid {
			continue
		}
		sum += hourSimilarity(t, h)
		compared++
	}
	if compared == 0 {
		return 0, 0
	}
	return sum / float64(compared), compared
}

func hourSimilarity(a, b store.HourlyWindStat) float64 {
	ws := closeness(a.AvgWind, b.AvgWind, 3.0)
	gs := closeness(a.AvgGust, b.AvgGust, 4.0)
	ds := 1 - dirDiffDeg(a.AvgDir, b.AvgDir)/90.0
	if ds < 0 {
		ds = 0
	}
	ts := closeness(a.AvgTemp.Float64, b.AvgTemp.Float64, 3.0)
	return 0.25*ws + 0.20*gs + 0.30*ds + 0.25*ts
}

func closeness(a, b, tol float64) float64 {
	d := math.Abs(a - b)
	if d >= tol {
		return 0
	}
	return 1 - d/tol
}

func dirDiffDeg(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > 180 {
		d = 360 - d
	}
	return d
}

func hourlyMap(rows []store.HourlyWindStat) map[int]hourForecast {
	out := make(map[int]hourForecast, len(rows))
	for _, h := range rows {
		out[h.Hour] = hourForecast{
			AvgWind: h.AvgWind,
			AvgGust: h.AvgGust,
			MaxGust: h.MaxGust,
			AvgDir:  h.AvgDir,
			Weight:  1,
		}
	}
	return out
}

func buildForecast(matches []dayMatch, baseline map[int]hourForecast, today store.DayProfile, nowHour int) forecastBundle {
	byHour := map[int]hourForecast{}

	for hr := nowHour + 1; hr <= 19; hr++ {
		var sum hourForecast
		for _, m := range matches {
			h, ok := m.Day.Hours[hr]
			if !ok {
				continue
			}
			w := m.Score
			sum.AvgWind += h.AvgWind * w
			sum.AvgGust += h.AvgGust * w
			sum.MaxGust = math.Max(sum.MaxGust, h.MaxGust)
			sum.AvgDir += h.AvgDir * w
			sum.Weight += w
		}
		if b, ok := baseline[hr]; ok {
			bw := baselineWeight
			if sum.Weight == 0 {
				bw = 1
			}
			sum.AvgWind += b.AvgWind * bw
			sum.AvgGust += b.AvgGust * bw
			sum.MaxGust = math.Max(sum.MaxGust, b.MaxGust)
			sum.AvgDir += b.AvgDir * bw
			sum.Weight += bw
		}
		if sum.Weight > 0 {
			sum.AvgWind /= sum.Weight
			sum.AvgGust /= sum.Weight
			sum.AvgDir /= sum.Weight
			byHour[hr] = sum
		}
	}

	// If no future hours yet, use full-day baseline + match peaks.
	if len(byHour) == 0 {
		for hr := 9; hr <= 19; hr++ {
			if b, ok := baseline[hr]; ok {
				byHour[hr] = b
			}
		}
	}

	peakStart, peakEnd := strongestFromForecast(byHour)
	goodStart, goodEnd := goodFromForecast(byHour, goodWindKt)
	for hr := compareStartHour; hr <= nowHour; hr++ {
		if h, ok := today.Hours[hr]; ok && h.AvgWind >= goodWindKt {
			if goodStart < 0 || hr < goodStart {
				goodStart = hr
			}
			if hr > goodEnd {
				goodEnd = hr
			}
		}
	}
	windDown := fadeFromForecast(byHour, goodWindKt)

	peak := statsForForecast(byHour, peakStart, peakEnd)
	if len(matches) > 0 {
		// Pull peak stats from top matches' actual outcomes (weighted).
		var pw, pg, pd, wSum float64
		var maxGust float64
		limit := len(matches)
		if limit > 8 {
			limit = 8
		}
		for _, m := range matches[:limit] {
			hr, wind, gust := dayPeak(m.Day)
			if wind < goodWindKt {
				continue
			}
			h := m.Day.Hours[hr]
			w := m.Score
			pw += wind * w
			pg += gust * w
			pd += h.AvgDir * w
			maxGust = math.Max(maxGust, h.MaxGust)
			wSum += w
		}
		if wSum > 0 {
			peak.AvgWind = pw / wSum
			peak.AvgGust = pg / wSum
			peak.AvgDir = pd / wSum
			peak.MaxGust = math.Max(peak.MaxGust, maxGust)
		}
	}

	return forecastBundle{
		peak:      peak,
		peakStart: peakStart,
		peakEnd:   peakEnd,
		goodStart: goodStart,
		goodEnd:   goodEnd,
		windDown:  windDown,
	}
}

func dayPeak(day store.DayProfile) (hour int, wind, gust float64) {
	best := -1.0
	for hr := 9; hr <= 18; hr++ {
		h, ok := day.Hours[hr]
		if !ok || h.AvgWind <= best {
			continue
		}
		best = h.AvgWind
		hour = hr
		wind = h.AvgWind
		gust = h.AvgGust
	}
	return hour, wind, gust
}

func strongestFromForecast(byHour map[int]hourForecast) (start, end int) {
	bestHr := 13
	best := -1.0
	for hr := 9; hr <= 18; hr++ {
		h, ok := byHour[hr]
		if !ok || h.AvgWind <= best {
			continue
		}
		best = h.AvgWind
		bestHr = hr
	}
	end = bestHr
	if next, ok := byHour[bestHr+1]; ok && bestHr < 18 && next.AvgWind >= best-0.3 {
		end = bestHr + 1
	}
	return bestHr, end
}

func goodFromForecast(byHour map[int]hourForecast, threshold float64) (start, end int) {
	start, end = -1, -1
	for hr := 8; hr <= 19; hr++ {
		h, ok := byHour[hr]
		if !ok || h.AvgWind < threshold {
			continue
		}
		if start < 0 {
			start = hr
		}
		end = hr
	}
	if start < 0 {
		return 11, 16
	}
	return start, end
}

func fadeFromForecast(byHour map[int]hourForecast, threshold float64) int {
	_, goodEnd := goodFromForecast(byHour, threshold)
	if goodEnd > 0 && goodEnd < 19 {
		return goodEnd + 1
	}
	return 17
}

func statsForForecast(byHour map[int]hourForecast, start, end int) hourForecast {
	var sum hourForecast
	n := 0
	for hr := start; hr <= end; hr++ {
		h, ok := byHour[hr]
		if !ok {
			continue
		}
		sum.AvgWind += h.AvgWind
		sum.AvgGust += h.AvgGust
		sum.AvgDir += h.AvgDir
		sum.MaxGust = math.Max(sum.MaxGust, h.MaxGust)
		n++
	}
	if n > 0 {
		sum.AvgWind /= float64(n)
		sum.AvgGust /= float64(n)
		sum.AvgDir /= float64(n)
	}
	return sum
}

func formatWindow(start, end int) string {
	if end > start {
		return fmt.Sprintf("%02d:00 – %02d:30", start, end)
	}
	return fmt.Sprintf("%02d:00 – %02d:30", start, start)
}

func formatPeak(s hourForecast) string {
	wLo := int(math.Floor(s.AvgWind))
	wHi := int(math.Ceil(s.AvgWind))
	if wHi <= wLo {
		wHi = wLo + 1
	}
	gLo := int(math.Round(s.AvgGust))
	gHi := int(math.Round(s.MaxGust))
	if gHi <= gLo {
		gHi = gLo + 1
	}
	return fmt.Sprintf("%d–%d kt sustained, gusts %d–%d kt", wLo, wHi, gLo, gHi)
}

func formatMatchConditions(matches []dayMatch, nowHour int, noTodayData bool) string {
	if noTodayData {
		return "no KY readings for today yet; using Jul–Aug climatology baseline"
	}
	if len(matches) == 0 {
		return fmt.Sprintf("no close Jul–Aug matches yet (comparing hours %d–%d); using summer baseline", compareStartHour, nowHour)
	}
	limit := len(matches)
	if limit > 3 {
		limit = 3
	}
	parts := []string{fmt.Sprintf("%d similar good-wind summer days (hours %d–%d)", len(matches), compareStartHour, nowHour)}
	for i := 0; i < limit; i++ {
		m := matches[i]
		hr, w, g := dayPeak(m.Day)
		parts = append(parts, fmt.Sprintf("%s %.0f%% match peaked %02d:00 at %.0f/%.0f kt",
			m.Day.Date.Format("02 Jan"), m.Score*100, hr, w, g))
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += "; " + parts[i]
	}
	return out
}

func formatCurrent(wind, gust float64, temp, humidity, pressure sql.NullFloat64) string {
	s := fmt.Sprintf("%.0f kt, gusts %.0f kt", wind, gust)
	if temp.Valid {
		s += fmt.Sprintf(", %.0f°C", temp.Float64)
	}
	if humidity.Valid {
		s += fmt.Sprintf(", %.0f%% humidity", humidity.Float64)
	}
	if pressure.Valid {
		s += fmt.Sprintf(", %.0f hPa", pressure.Float64)
	}
	return s
}

func directionLabel(avgDir float64) string {
	lo := int(math.Round(avgDir)) - 15
	hi := int(math.Round(avgDir)) + 15
	return fmt.Sprintf("%d–%d° (%s)", lo, hi, compass(avgDir))
}

func compass(deg float64) string {
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	switch {
	case deg >= 225 && deg < 270:
		return "WSW"
	case deg >= 180 && deg < 225:
		return "SW"
	case deg >= 270 && deg < 315:
		return "W"
	case deg >= 135 && deg < 180:
		return "SE"
	default:
		return "W-SW"
	}
}
