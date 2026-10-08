package collector

import (
	"fmt"
	"io"
	"math"
	"time"

	"github.com/ben/ikite-go/internal/estimate"
)

// backtestScore accumulates how one calibration method did over held-out hours.
type backtestScore struct {
	n, nStrong, inRange, ng int
	absErr, err, absStrong  float64
	strongErr, gustAbs      float64
}

func (b *backtestScore) add(p estimate.Point, obs, gustPeak float64) {
	e := p.Wind - obs
	b.n++
	b.absErr += math.Abs(e)
	b.err += e
	if obs >= p.Low && obs <= p.High {
		b.inRange++
	}
	if obs >= 15 {
		b.nStrong++
		b.absStrong += math.Abs(e)
		b.strongErr += e
	}
	if gustPeak > 0 && p.Gust > 0 {
		b.ng++
		b.gustAbs += math.Abs(p.Gust - gustPeak)
	}
}

func (b *backtestScore) merge(o backtestScore) {
	b.n += o.n
	b.nStrong += o.nStrong
	b.inRange += o.inRange
	b.ng += o.ng
	b.absErr += o.absErr
	b.err += o.err
	b.absStrong += o.absStrong
	b.strongErr += o.strongErr
	b.gustAbs += o.gustAbs
}

func (b backtestScore) String() string {
	div := func(x float64, n int) float64 {
		if n == 0 {
			return math.NaN()
		}
		return x / float64(n)
	}
	return fmt.Sprintf("hours %4d  MAE %.2f  bias %+.2f  in-range %3.0f%%  ≥15kt: %3d h MAE %.2f bias %+.2f  gust MAE %.2f",
		b.n, div(b.absErr, b.n), div(b.err, b.n), 100*div(float64(b.inRange), b.n),
		b.nStrong, div(b.absStrong, b.nStrong), div(b.strongErr, b.nStrong), div(b.gustAbs, b.ng))
}

// Backtest replays the last `days` days (today included) for every spot, each
// day calibrated only on the days before it, exactly as the daily run would
// have, and scores the time-of-day calibration against the time-of-day plus
// direction one. Nothing is written.
func (s *EstimateService) Backtest(w io.Writer, now time.Time, days int) error {
	now = now.In(s.Cfg.Timezone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	spots, err := s.Store.CollectSpots()
	if err != nil {
		return fmt.Errorf("collect spots: %w", err)
	}
	methods := []struct {
		name string
		opt  estimate.Options
	}{
		{"time of day      ", estimate.Options{ByDirection: false}},
		{"time + direction ", estimate.Options{ByDirection: true}},
	}
	totals := make([]backtestScore, len(methods))
	for _, sp := range spots {
		histFrom := today.AddDate(0, 0, -(estimateHistoryDays + days))
		end := today.AddDate(0, 0, 1)
		obs, err := s.Store.HourlyObserved(sp.ID, histFrom, end, estimateFromHour, estimateToHour)
		if err != nil {
			return fmt.Errorf("%s observed: %w", sp.ID, err)
		}
		past, err := s.Store.ListWindForecastByLocationRange(sp.ID, histFrom, today)
		if err != nil {
			return fmt.Errorf("%s forecasts: %w", sp.ID, err)
		}
		history := pairHistory(obs, groupForecasts(past))
		if len(history) == 0 {
			continue
		}
		scores := make([]backtestScore, len(methods))
		for d := days - 1; d >= 0; d-- {
			day := today.AddDate(0, 0, -d)
			dayS := day.Format("2006-01-02")
			for i, m := range methods {
				cal := fitBeforeWith(history, day, m.opt)
				if !cal.Usable() {
					continue
				}
				for _, h := range history {
					if h.day != dayS {
						continue
					}
					if p, ok := cal.Estimate(h.past.Hour, h.past.Forecasts); ok {
						scores[i].add(p, h.past.Wind, h.past.GustPeak)
					}
				}
			}
		}
		if scores[0].n == 0 {
			continue
		}
		fmt.Fprintf(w, "%s (%s)\n", sp.Name, sp.ID)
		for i, m := range methods {
			fmt.Fprintf(w, "  %s %s\n", m.name, scores[i])
			totals[i].merge(scores[i])
		}
	}
	fmt.Fprintf(w, "ALL SPOTS\n")
	for i, m := range methods {
		fmt.Fprintf(w, "  %s %s\n", m.name, totals[i])
	}
	return nil
}
