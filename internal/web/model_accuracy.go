package web

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/sources/windguru"
)

const (
	// Scoring window: kiting hours, over the last accuracyDays days.
	accuracyDays     = 60
	accuracyFromHour = 9
	accuracyToHour   = 18
	// A model needs this many matched hours before its score means anything.
	accuracyMinHours = 24
	// Gust factors are only meaningful once there is real wind: at 2 kt a 5 kt
	// gust is a factor of 2.5 and tells you nothing.
	accuracyRatioMinWind = 8.0
	// Past forecasts only change once a day, so the scores can be cached.
	accuracyCacheTTL = 3 * time.Hour
	// A model with nothing newer than this, relative to the freshest model, has
	// stopped publishing (openWRF did in Sep 2026). Its score is history, not a
	// guide to a forecast you can see today, so it is left out.
	accuracyStaleAfter = 7 * 24 * time.Hour
)

// estimateModelName is how ikite's own blended estimate is scored in the panel,
// alongside the models it is built from.
const estimateModelName = "ikite"

// modelScore is one model's track record against a spot's meter.
type modelScore struct {
	Model     string
	Hours     int
	WindBias  float64 // mean(forecast wind - measured mean wind)
	WindMAE   float64 // mean |forecast wind - measured mean wind|
	GustBias  float64 // mean(forecast gust - measured peak gust)
	HasGust   bool
	GustRatio float64 // forecast gust / forecast wind, windy hours only
	HasRatio  bool
}

type modelAccuracy struct {
	Scores       []modelScore // most accurate wind first
	Days         int          // distinct days that contributed
	ObsGustRatio float64      // measured peak gust / mean wind, windy hours only
	HasObsRatio  bool
	// How often the measured wind fell inside the estimate's likely range.
	EstCoverage      float64
	EstCoverageHours int
}

// estimateCoverage is the share of metered hours whose mean wind landed inside
// the estimate's likely range — the check that the shaded band means what it says.
func estimateCoverage(obs []models.ObservedHour, est []models.WindEstimate) (float64, int) {
	byHour := make(map[string]models.WindEstimate, len(est))
	for _, e := range est {
		byHour[hourKey(e.Period)] = e
	}
	var in, n int
	for _, o := range obs {
		e, ok := byHour[hourKey(o.Hour)]
		if !ok {
			continue
		}
		n++
		if o.Wind >= e.Low && o.Wind <= e.High {
			in++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return float64(in) / float64(n), n
}

// estimateAsForecast lets the panel score the estimate with the same code, on
// the same hours, as every model.
func estimateAsForecast(est []models.WindEstimate) []models.WindForecastRow {
	out := make([]models.WindForecastRow, 0, len(est))
	for _, e := range est {
		w, g := e.Wind, e.Gust
		out = append(out, models.WindForecastRow{Model: estimateModelName, IDModel: -1, Period: e.Period, Wind: &w, Gust: &g})
	}
	return out
}

type accumulator struct {
	last            time.Time
	days            map[string]bool
	n               int
	sumDiff, sumAbs float64
	nGust           int
	sumGust         float64
	nRatio          int
	sumRatio        float64
}

// scoreModels compares each forecast hour against the meter's matching hour.
// It is pure so it can be tested without a database.
func scoreModels(obs []models.ObservedHour, fc []models.WindForecastRow, fromHour, toHour int) modelAccuracy {
	byHour := make(map[string]models.ObservedHour, len(obs))
	for _, o := range obs {
		byHour[hourKey(o.Hour)] = o
	}

	acc := map[string]*accumulator{}
	for _, r := range fc {
		if r.Wind == nil || r.Period.Hour() < fromHour || r.Period.Hour() > toHour {
			continue
		}
		// Meter buckets are centred on the hour. A half-hourly model (openWRF)
		// also has :30 rows; pairing those with the :00 bucket would count them
		// twice and score them against a window 30 minutes off.
		if r.Period.Minute() != 0 || r.Period.Second() != 0 {
			continue
		}
		o, ok := byHour[hourKey(r.Period)]
		if !ok {
			continue
		}
		name := windguru.DisplayModelName(r.IDModel, r.Model)
		a := acc[name]
		if a == nil {
			a = &accumulator{days: map[string]bool{}}
			acc[name] = a
		}
		d := *r.Wind - o.Wind
		if r.Period.After(a.last) {
			a.last = r.Period
		}
		a.n++
		a.sumDiff += d
		a.sumAbs += math.Abs(d)
		if r.Gust != nil && o.GustPeak > 0 {
			a.nGust++
			a.sumGust += *r.Gust - o.GustPeak
		}
		if r.Gust != nil && *r.Wind >= accuracyRatioMinWind {
			a.nRatio++
			a.sumRatio += *r.Gust / *r.Wind
		}
		a.days[r.Period.Format("2006-01-02")] = true
	}

	var newest time.Time
	for _, a := range acc {
		if a.last.After(newest) {
			newest = a.last
		}
	}

	var out modelAccuracy
	days := map[string]bool{}
	for name, a := range acc {
		if a.n < accuracyMinHours || newest.Sub(a.last) > accuracyStaleAfter {
			continue
		}
		for d := range a.days {
			days[d] = true
		}
		s := modelScore{
			Model:    name,
			Hours:    a.n,
			WindBias: a.sumDiff / float64(a.n),
			WindMAE:  a.sumAbs / float64(a.n),
		}
		if a.nGust > 0 {
			s.GustBias, s.HasGust = a.sumGust/float64(a.nGust), true
		}
		if a.nRatio > 0 {
			s.GustRatio, s.HasRatio = a.sumRatio/float64(a.nRatio), true
		}
		out.Scores = append(out.Scores, s)
	}
	sort.Slice(out.Scores, func(i, j int) bool {
		if out.Scores[i].WindMAE != out.Scores[j].WindMAE {
			return out.Scores[i].WindMAE < out.Scores[j].WindMAE
		}
		return out.Scores[i].Model < out.Scores[j].Model
	})
	out.Days = len(days)

	var nr int
	var sr float64
	for _, o := range obs {
		if o.Wind >= accuracyRatioMinWind && o.GustPeak > 0 {
			nr++
			sr += o.GustPeak / o.Wind
		}
	}
	if nr > 0 {
		out.ObsGustRatio, out.HasObsRatio = sr/float64(nr), true
	}
	return out
}

// hourKey compares hours by their wall-clock digits. Forecast periods and meter
// buckets both come from DATETIME columns, and the driver may hand them back in
// different locations; converting with In() would shift one against the other.
func hourKey(t time.Time) string { return t.Format("2006-01-02 15") }

// accuracyRow is a modelScore formatted for the template.
type accuracyRow struct {
	Model      string
	IsEstimate bool
	Best       bool
	WindErr    string
	WindBias   string
	BiasClass  string
	GustBias   string
	GustClass  string
	GustRatio  string
	Hours      int
}

type accuracyView struct {
	Rows         []accuracyRow
	Days         int
	FromHour     int
	ToHour       int
	ObsGustRatio string
	// EstCoverage is set once the estimate has enough scored hours to quote.
	EstCoverage      string
	EstCoverageHours int
}

func buildAccuracyView(a modelAccuracy) *accuracyView {
	// With one model there is nothing to compare, and the panel exists to compare.
	if len(a.Scores) < 2 {
		return nil
	}
	v := &accuracyView{Days: a.Days, FromHour: accuracyFromHour, ToHour: accuracyToHour}
	if a.EstCoverageHours >= accuracyMinHours {
		v.EstCoverage = fmt.Sprintf("%.0f%%", 100*a.EstCoverage)
		v.EstCoverageHours = a.EstCoverageHours
	}
	if a.HasObsRatio {
		v.ObsGustRatio = fmt.Sprintf("%.2f", a.ObsGustRatio)
	}
	for i, s := range a.Scores {
		row := accuracyRow{
			Model:      s.Model,
			IsEstimate: s.Model == estimateModelName,
			Best:       i == 0,
			WindErr:    fmt.Sprintf("±%.1f", s.WindMAE),
			WindBias:   signed(s.WindBias),
			BiasClass:  biasClass(s.WindBias, 1.5),
			GustBias:   "–",
			GustRatio:  "–",
			Hours:      s.Hours,
		}
		if s.HasGust {
			row.GustBias = signed(s.GustBias)
			row.GustClass = biasClass(s.GustBias, 3)
		}
		if s.HasRatio {
			row.GustRatio = fmt.Sprintf("%.2f×", s.GustRatio)
		}
		v.Rows = append(v.Rows, row)
	}
	return v
}

func signed(v float64) string {
	if math.Abs(v) < 0.05 {
		return "0.0"
	}
	return fmt.Sprintf("%+.1f", v)
}

// biasClass flags a bias large enough to matter when reading the forecast.
// It judges the value as displayed (one decimal), so two cells showing the same
// number never get different colours.
func biasClass(v, threshold float64) string {
	v = math.Round(v*10) / 10
	switch {
	case v >= threshold:
		return "acc-over"
	case v <= -threshold:
		return "acc-under"
	}
	return ""
}

type accuracyEntry struct {
	at   time.Time
	view *accuracyView
}

// modelAccuracyFor returns the accuracy panel for a spot, from cache when fresh.
// A nil result means there is nothing worth showing (no meter, too little data).
func (s *Server) modelAccuracyFor(ctx context.Context, spot string, now time.Time) *accuracyView {
	s.accMu.Lock()
	if e, ok := s.accCache[spot]; ok && now.Sub(e.at) < accuracyCacheTTL {
		s.accMu.Unlock()
		return e.view
	}
	s.accMu.Unlock()

	v, err, _ := s.accSF.Do(spot, func() (any, error) {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
		from := today.AddDate(0, 0, -accuracyDays)
		obs, err := s.Store.HourlyObserved(spot, from, today, accuracyFromHour, accuracyToHour)
		if err != nil {
			return nil, err
		}
		var view *accuracyView
		if len(obs) > 0 {
			fc, err := s.Store.ListWindForecastByLocationRange(spot, from, today.AddDate(0, 0, -1))
			if err != nil {
				return nil, err
			}
			est, err := s.Store.ListWindEstimate(spot, from, today)
			if err != nil {
				return nil, err
			}
			acc := scoreModels(obs, append(fc, estimateAsForecast(est)...), accuracyFromHour, accuracyToHour)
			acc.EstCoverage, acc.EstCoverageHours = estimateCoverage(obs, est)
			view = buildAccuracyView(acc)
		}
		s.accMu.Lock()
		if s.accCache == nil {
			s.accCache = map[string]accuracyEntry{}
		}
		s.accCache[spot] = accuracyEntry{at: now, view: view}
		s.accMu.Unlock()
		return view, nil
	})
	if err != nil {
		s.Log.Error("model accuracy", "spot", spot, "err", err)
		return nil
	}
	view, _ := v.(*accuracyView)
	return view
}
