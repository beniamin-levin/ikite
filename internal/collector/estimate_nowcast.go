package collector

import (
	"fmt"
	"io"
	"math"
	"time"

	"github.com/ben/ikite-go/internal/estimate"
	"github.com/ben/ikite-go/internal/models"
)

// The morning estimate is fixed by 09:30, but by midday the meter already shows
// whether the day is running above or below it. On 2026-10-09 Hadera was at
// 14 kt by 10:00 against an estimate of 11 and ended at 22 against 17. The
// nowcast re-issues the rest of today every hour: it takes how far the meter
// has been from the estimate over the last complete hours and carries that
// miss forward, fading with lead time.
//
// It is written to its own table (wind_nowcast) so wind_estimate keeps the
// morning issue that the accuracy panel scores.

// nowcastParams: the correction for an hour `lead` hours after the last
// complete meter hour is alpha × miss × exp(−(lead−1)/tau); miss is the mean of
// (meter − estimate) over the last `hours` complete hours, newest weighted double.
type nowcastParams struct {
	alpha, tau float64
	hours      int
}

// defaultNowcast was chosen by `estimate -nowcast-backtest 30` on 14 Oct 2026
// (11 spots, 13,980 scored hours, issues 09:00–18:00): error 1.76 → 1.55 kt,
// 1.78 → 1.16 for the next hour, and 2.47 → 2.08 on windy hours. Using the full
// miss or never fading it did worse (1.85 kt); averaging 2–3 hours was a hair worse.
var defaultNowcast = nowcastParams{alpha: 0.75, tau: 5, hours: 1}

// Hours the nowcast runs for: the meter must have a complete hour of the day
// behind it, and after 18:00 there is little day left to correct.
const (
	nowcastFirstHour = 9
	nowcastLastHour  = 18
)

// meterMiss is the weighted mean of (meter − estimate) over the `hours`
// complete hours ending at last. ok is false when the meter has no reading for
// the last hour (silent meter: nothing to correct with).
func meterMiss(obs map[string]models.ObservedHour, base map[string]estimate.Point, last time.Time, hours int) (float64, bool) {
	var num, den float64
	for i := 0; i < hours; i++ {
		t := last.Add(-time.Duration(i) * time.Hour)
		o, ok := obs[wallHour(t)]
		p, okp := base[wallHour(t)]
		if !ok || !okp {
			if i == 0 {
				return 0, false
			}
			continue
		}
		w := 1.0
		if i == 0 {
			w = 2
		}
		num += w * (o.Wind - p.Wind)
		den += w
	}
	if den == 0 {
		return 0, false
	}
	return num / den, true
}

// nowcastPoint shifts a base estimate by the faded miss. The range and the
// gust move with it; nothing goes below zero.
func nowcastPoint(p estimate.Point, miss, lead float64, np nowcastParams) estimate.Point {
	d := np.alpha * miss * math.Exp(-(lead-1)/np.tau)
	out := p
	out.Wind = math.Max(p.Wind+d, 0)
	out.Low = math.Max(p.Low+d, 0)
	out.High = math.Max(p.High+d, out.Wind)
	out.Gust = math.Max(p.Gust+d, out.Wind)
	return out
}

// lastCompleteHour is the newest meter hour fully in: the bucket for hour H
// spans H-1:30..H:29 (see HourlyObserved).
func lastCompleteHour(now time.Time) time.Time {
	t := now.Add(-30 * time.Minute)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

// Nowcast re-issues the rest of today for every spot from the meter's latest
// complete hours. Spots whose meter is silent or dead today get no nowcast, so
// the site falls back to the morning estimate.
func (s *EstimateService) Nowcast(now time.Time) error {
	now = now.In(s.Cfg.Timezone)
	last := lastCompleteHour(now)
	if last.Hour() < nowcastFirstHour || last.Hour() > nowcastLastHour {
		s.Log.Info("nowcast skipped", "reason", "outside hours", "last_hour", last.Format("15:04"))
		return nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	spots, err := s.Store.CollectSpots()
	if err != nil {
		return fmt.Errorf("collect spots: %w", err)
	}
	var done, skipped, failed int
	for _, sp := range spots {
		n, err := s.nowcastSpot(sp.ID, today, last, now)
		switch {
		case err != nil:
			failed++
			s.Log.Error("nowcast failed", "spot", sp.ID, "err", err)
		case n == 0:
			skipped++
		default:
			done++
		}
	}
	s.Log.Info("nowcast done", "spots", done, "skipped", skipped, "failed", failed)
	if failed > 0 && done == 0 {
		return fmt.Errorf("nowcast: all %d attempted spots failed", failed)
	}
	return nil
}

func (s *EstimateService) nowcastSpot(spot string, today, last, now time.Time) (int, error) {
	setup := defaultSetup
	histFrom := today.AddDate(0, 0, -setup.historyDays)
	tomorrow := today.AddDate(0, 0, 1)
	obs, err := s.Store.HourlyObserved(spot, histFrom, now, estimateFromHour, estimateToHour)
	if err != nil {
		return 0, fmt.Errorf("observed: %w", err)
	}
	past, err := s.Store.ListWindForecastByLocationRange(spot, histFrom, today)
	if err != nil {
		return 0, fmt.Errorf("forecasts: %w", err)
	}
	byHour := groupForecasts(past)
	cal := fitBeforeWith(pairHistory(obs, byHour), today, setup)

	var rows []models.WindEstimate
	defer func() {
		// Always replace today's rows, so a spot that drops out (silent meter)
		// stops showing a stale nowcast.
		if err := s.Store.ReplaceWindNowcast(spot, today, tomorrow, rows); err != nil {
			s.Log.Error("nowcast save", "spot", spot, "err", err)
		}
	}()
	if !cal.Usable() {
		return 0, nil
	}
	todayS := today.Format("2006-01-02")
	var todayObs []models.ObservedHour
	obsBy := map[string]models.ObservedHour{}
	for _, o := range obs {
		if o.Hour.Format("2006-01-02") == todayS && !o.Hour.After(wallClock(last)) {
			todayObs = append(todayObs, o)
			obsBy[wallHour(o.Hour)] = o
		}
	}
	if deadMeterDays(todayObs)[todayS] {
		s.Log.Info("nowcast skipped", "spot", spot, "reason", "meter reads zero all day")
		return 0, nil
	}
	base := map[string]estimate.Point{}
	for k, h := range byHour {
		if h.period.Format("2006-01-02") != todayS {
			continue
		}
		if p, ok := cal.Estimate(h.period.Hour(), h.forecasts); ok {
			base[k] = p
		}
	}
	miss, ok := meterMiss(obsBy, base, wallClock(last), defaultNowcast.hours)
	if !ok {
		s.Log.Info("nowcast skipped", "spot", spot, "reason", "no meter reading for the last hour")
		return 0, nil
	}
	for k, p := range base {
		t := byHour[k].period
		lead := float64(t.Hour() - last.Hour())
		if lead < 1 {
			continue
		}
		q := nowcastPoint(p, miss, lead, defaultNowcast)
		rows = append(rows, models.WindEstimate{
			Period: t, Wind: q.Wind, Gust: q.Gust, Low: q.Low, High: q.High,
			Dir: q.Dir, Models: q.Models, IssuedAt: now,
		})
	}
	s.Log.Info("nowcast saved", "spot", spot, "hours", len(rows), "miss_kt", fmt.Sprintf("%+.1f", miss))
	return len(rows), nil
}

// wallClock re-reads t's wall-clock digits as UTC, the way stored DATETIMEs
// come back from the store (see ObservedHour), so the two compare directly.
func wallClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// NowcastBacktest replays the hourly nowcast over the last `days` days: for
// every spot, day and issue hour, the morning estimate (calibrated only on the
// days before) is corrected from the meter hours up to the issue, then scored
// on the hours after it. Nothing is written.
func (s *EstimateService) NowcastBacktest(w io.Writer, now time.Time, days int) error {
	now = now.In(s.Cfg.Timezone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	spots, err := s.Store.CollectSpots()
	if err != nil {
		return fmt.Errorf("collect spots: %w", err)
	}
	var cands []nowcastParams
	cands = append(cands, nowcastParams{alpha: 0, tau: 1, hours: 1}) // the morning estimate
	for _, h := range []int{1, 2, 3} {
		for _, a := range []float64{0.5, 0.75, 1} {
			for _, t := range []float64{1, 2, 3, 5, 1000} {
				cands = append(cands, nowcastParams{alpha: a, tau: t, hours: h})
			}
		}
	}
	type score struct {
		n                 [3]int // lead 1, 2–3, 4+
		abs               [3]float64
		nAll, nStrong     int
		absAll, absStrong float64
		errStrong         float64
	}
	scores := make([]score, len(cands))
	leadBucket := func(l int) int {
		switch {
		case l <= 1:
			return 0
		case l <= 3:
			return 1
		}
		return 2
	}
	for _, sp := range spots {
		histFrom := today.AddDate(0, 0, -(defaultSetup.historyDays + days))
		obs, err := s.Store.HourlyObserved(sp.ID, histFrom, today, estimateFromHour, estimateToHour)
		if err != nil {
			return fmt.Errorf("%s observed: %w", sp.ID, err)
		}
		past, err := s.Store.ListWindForecastByLocationRange(sp.ID, histFrom, today)
		if err != nil {
			return fmt.Errorf("%s forecasts: %w", sp.ID, err)
		}
		byHour := groupForecasts(past)
		history := pairHistory(obs, byHour)
		obsBy := map[string]models.ObservedHour{}
		dead := deadMeterDays(obs)
		for _, o := range obs {
			if !dead[o.Hour.Format("2006-01-02")] {
				obsBy[wallHour(o.Hour)] = o
			}
		}
		for d := days; d >= 1; d-- {
			day := today.AddDate(0, 0, -d)
			cal := fitBeforeWith(history, day, defaultSetup)
			if !cal.Usable() {
				continue
			}
			dayS := day.Format("2006-01-02")
			base := map[string]estimate.Point{}
			for k, h := range byHour {
				if h.period.Format("2006-01-02") == dayS {
					if p, ok := cal.Estimate(h.period.Hour(), h.forecasts); ok {
						base[k] = p
					}
				}
			}
			if len(base) == 0 {
				continue
			}
			wd := wallClock(day)
			for issue := nowcastFirstHour; issue <= nowcastLastHour; issue++ {
				last := wd.Add(time.Duration(issue) * time.Hour)
				for i, c := range cands {
					miss, ok := meterMiss(obsBy, base, last, c.hours)
					if !ok {
						continue
					}
					for t := issue + 1; t <= 19; t++ {
						k := wallHour(wd.Add(time.Duration(t) * time.Hour))
						o, ok1 := obsBy[k]
						p, ok2 := base[k]
						if !ok1 || !ok2 {
							continue
						}
						q := nowcastPoint(p, miss, float64(t-issue), c)
						e := q.Wind - o.Wind
						b := leadBucket(t - issue)
						sc := &scores[i]
						sc.n[b]++
						sc.abs[b] += math.Abs(e)
						sc.nAll++
						sc.absAll += math.Abs(e)
						if p.Wind >= 11 || o.Wind >= 12 {
							sc.nStrong++
							sc.absStrong += math.Abs(e)
							sc.errStrong += e
						}
					}
				}
			}
		}
	}
	div := func(x float64, n int) float64 {
		if n == 0 {
			return math.NaN()
		}
		return x / float64(n)
	}
	for i, c := range cands {
		sc := scores[i]
		name := fmt.Sprintf("alpha %.2f tau %4.0f h, last %d h", c.alpha, c.tau, c.hours)
		if c.alpha == 0 {
			name = "morning estimate              "
		}
		fmt.Fprintf(w, "%s  all %5d h MAE %.2f | lead 1 %.2f  2–3 %.2f  4+ %.2f | windy %4d h MAE %.2f bias %+.2f\n",
			name, sc.nAll, div(sc.absAll, sc.nAll), div(sc.abs[0], sc.n[0]), div(sc.abs[1], sc.n[1]), div(sc.abs[2], sc.n[2]),
			sc.nStrong, div(sc.absStrong, sc.nStrong), div(sc.errStrong, sc.nStrong))
	}
	return nil
}
