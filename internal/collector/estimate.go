package collector

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/estimate"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/sources/windguru"
	"github.com/ben/ikite-go/internal/store"
)

const (
	// Calibrate on the last estimateHistoryDays of same-day forecasts vs the meter.
	estimateHistoryDays = 60
	// Kiting hours — the same window the forecast overview shows, and the only
	// hours the meters reliably cover.
	estimateFromHour = 6
	estimateToHour   = 21
)

// EstimateService writes ikite's blended best-estimate wind for every spot that
// has both a meter and enough model history to calibrate against. It runs after
// each forecast source lands (07:30 and 09:30), so the estimate always reflects
// the newest models.
type EstimateService struct {
	Cfg   *config.Config
	Store *store.Store
	Log   *slog.Logger
}

// Run estimates today-and-forward for each spot. With backfillDays > 0 it first
// replays the previous days one at a time, calibrating each only on days before
// it and estimating from the forecasts stored for it — exactly what the daily
// run would have produced then. Backfilled rows carry today's issued_at, so they
// are distinguishable from ones issued on the day.
func (s *EstimateService) Run(now time.Time, backfillDays int) error {
	now = now.In(s.Cfg.Timezone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)

	spots, err := s.Store.CollectSpots()
	if err != nil {
		return fmt.Errorf("collect spots: %w", err)
	}

	var done, skipped, failed int
	for _, sp := range spots {
		n, err := s.runSpot(sp.ID, today, now, backfillDays)
		switch {
		case err != nil:
			failed++
			s.Log.Error("estimate failed", "spot", sp.ID, "err", err)
		case n == 0:
			skipped++
		default:
			done++
		}
	}
	s.Log.Info("estimate done", "spots", done, "skipped", skipped, "failed", failed, "backfill_days", backfillDays)
	if failed > 0 && done == 0 {
		return fmt.Errorf("estimate: all %d attempted spots failed", failed)
	}
	return nil
}

// runSpot returns the number of hours estimated for today-and-forward.
func (s *EstimateService) runSpot(spot string, today, now time.Time, backfillDays int) (int, error) {
	histFrom := today.AddDate(0, 0, -(estimateHistoryDays + backfillDays))

	obs, err := s.Store.HourlyObserved(spot, histFrom, today, estimateFromHour, estimateToHour)
	if err != nil {
		return 0, fmt.Errorf("observed: %w", err)
	}
	if len(obs) == 0 {
		s.Log.Info("estimate skipped", "spot", spot, "reason", "no meter data")
		return 0, nil
	}
	past, err := s.Store.ListWindForecastByLocationRange(spot, histFrom, today.AddDate(0, 0, -1))
	if err != nil {
		return 0, fmt.Errorf("past forecasts: %w", err)
	}
	future, err := s.Store.ListWindForecastByLocationRange(spot, today, time.Time{})
	if err != nil {
		return 0, fmt.Errorf("forecasts: %w", err)
	}

	pastByHour := groupForecasts(past)
	history := pairHistory(obs, pastByHour)

	// Replay earlier days first, oldest to newest.
	for d := backfillDays; d >= 1; d-- {
		day := today.AddDate(0, 0, -d)
		pts := estimateHours(fitBefore(history, day), pastByHour, day, day.AddDate(0, 0, 1), now)
		if err := s.Store.ReplaceWindEstimate(spot, day, day.AddDate(0, 0, 1), pts); err != nil {
			return 0, fmt.Errorf("backfill %s: %w", day.Format("2006-01-02"), err)
		}
	}

	cal := fitBefore(history, today)
	if !cal.Usable() {
		s.Log.Info("estimate skipped", "spot", spot, "reason", "fewer than two models with enough history",
			"models", len(cal.Models))
		return 0, nil
	}
	pts := estimateHours(cal, groupForecasts(future), today, time.Time{}, now)
	if err := s.Store.ReplaceWindEstimate(spot, today, time.Time{}, pts); err != nil {
		return 0, fmt.Errorf("save: %w", err)
	}
	s.Log.Info("estimate saved", "spot", spot, "hours", len(pts), "models", cal.ModelNames(),
		"sigma_kt", fmt.Sprintf("%.2f", cal.Sigma))
	return len(pts), nil
}

// hourForecasts is every model's forecast for one wall-clock hour.
type hourForecasts struct {
	period    time.Time
	forecasts []estimate.Forecast
}

// groupForecasts keys on-the-hour forecasts within kiting hours by wall-clock
// hour. Half-hourly rows (openWRF) are dropped: the meter buckets are centred
// on the hour, so a :30 value would be scored against the wrong window.
func groupForecasts(rows []models.WindForecastRow) map[string]*hourForecasts {
	out := map[string]*hourForecasts{}
	for _, r := range rows {
		if r.Wind == nil || r.Period.Minute() != 0 || r.Period.Second() != 0 {
			continue
		}
		if h := r.Period.Hour(); h < estimateFromHour || h > estimateToHour {
			continue
		}
		f := estimate.Forecast{
			Model: windguru.DisplayModelName(r.IDModel, r.Model),
			Wind:  *r.Wind,
			Dir:   -1,
		}
		if r.Gust != nil {
			f.Gust = *r.Gust
		}
		if r.WindDir != nil {
			f.Dir = *r.WindDir
		}
		k := wallHour(r.Period)
		h := out[k]
		if h == nil {
			h = &hourForecasts{period: r.Period}
			out[k] = h
		}
		h.forecasts = append(h.forecasts, f)
	}
	return out
}

type datedPast struct {
	day  string
	past estimate.Past
}

func pairHistory(obs []models.ObservedHour, fc map[string]*hourForecasts) []datedPast {
	var out []datedPast
	for _, o := range obs {
		h, ok := fc[wallHour(o.Hour)]
		if !ok {
			continue
		}
		out = append(out, datedPast{
			day:  o.Hour.Format("2006-01-02"),
			past: estimate.Past{Hour: o.Hour.Hour(), Forecasts: h.forecasts, Wind: o.Wind, GustPeak: o.GustPeak},
		})
	}
	return out
}

// fitBefore calibrates on the estimateHistoryDays before day — never on day
// itself or later, so no estimate is fitted on what it is trying to predict.
func fitBefore(history []datedPast, day time.Time) estimate.Calibration {
	return fitBeforeWith(history, day, estimate.DefaultOptions)
}

func fitBeforeWith(history []datedPast, day time.Time, opt estimate.Options) estimate.Calibration {
	from := day.AddDate(0, 0, -estimateHistoryDays).Format("2006-01-02")
	to := day.Format("2006-01-02")
	var train []estimate.Past
	for _, h := range history {
		if h.day >= from && h.day < to {
			train = append(train, h.past)
		}
	}
	return estimate.FitWith(train, opt)
}

// estimateHours estimates every grouped hour whose day is in [from, to); a zero
// `to` is open-ended.
func estimateHours(cal estimate.Calibration, fc map[string]*hourForecasts, from, to, issued time.Time) []models.WindEstimate {
	if !cal.Usable() {
		return nil
	}
	fromS := from.Format("2006-01-02")
	toS := ""
	if !to.IsZero() {
		toS = to.Format("2006-01-02")
	}
	var out []models.WindEstimate
	for _, h := range fc {
		day := h.period.Format("2006-01-02")
		if day < fromS || (toS != "" && day >= toS) {
			continue
		}
		p, ok := cal.Estimate(h.period.Hour(), h.forecasts)
		if !ok {
			continue
		}
		out = append(out, models.WindEstimate{
			Period: h.period, Wind: p.Wind, Gust: p.Gust, Low: p.Low, High: p.High,
			Dir: p.Dir, Models: p.Models, IssuedAt: issued,
		})
	}
	return out
}

// wallHour compares hours by their stored wall-clock digits; see ObservedHour.
func wallHour(t time.Time) string { return t.Format("2006-01-02 15") }
