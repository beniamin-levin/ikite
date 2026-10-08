package collector

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/sources/windguru"
	"github.com/ben/ikite-go/internal/store"
)

type WGForecastService struct {
	Cfg    *config.Config
	Store  *store.Store
	WG     *windguru.ForecastClient
	Log    *slog.Logger
	Notify *ForecastGustNotifyService
}

type WGForecastOptions struct {
	Force bool // skip 7am window
}

func (s *WGForecastService) Run(now time.Time, opts WGForecastOptions) error {
	if s.Cfg.BegetProxyURL == "" {
		return fmt.Errorf("BEGET_PROXY_URL not set")
	}

	now = now.In(s.Cfg.Timezone)
	if !opts.Force && now.Hour() != 7 {
		s.Log.Info("wg forecast skipped", "reason", "not 7am", "hour", now.Hour())
		return nil
	}

	spots, err := s.Store.SpotsWithWindguruForecast()
	if err != nil {
		return err
	}
	if len(spots) == 0 {
		s.Log.Info("wg forecast skipped", "reason", "no spots with windguru_id")
		return nil
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	fetchedAt := now

	for _, sp := range spots {
		if sp.WindguruID == nil {
			continue
		}

		rows, err := s.WG.FetchSpotForecasts(*sp.WindguruID, today, s.Cfg.Timezone)
		if err != nil {
			return fmt.Errorf("spot %s windguru %d: %w", sp.ID, *sp.WindguruID, err)
		}
		if h := sp.WindguruHiresID; h != nil && *h != *sp.WindguruID {
			hires, err := s.WG.FetchSpotForecastsMicro(*h, today, s.Cfg.Timezone)
			if err != nil {
				// The spot's own models are still worth saving.
				s.Log.Warn("wg hires forecast", "spot", sp.ID, "hires_id", *h, "err", err)
			} else {
				var added int
				rows, added = mergeMissingModels(rows, hires)
				s.Log.Info("wg hires models added", "spot", sp.ID, "hires_id", *h, "models", added)
			}
		}
		for i := range rows {
			rows[i].Location = sp.ID
			rows[i].WindguruID = *sp.WindguruID
		}
		if err := s.Store.ReplaceAllWindForecast(*sp.WindguruID, today, fetchedAt, rows); err != nil {
			return err
		}
		s.Log.Info("wg forecast saved",
			"spot", sp.ID,
			"windguru_id", *sp.WindguruID,
			"rows", len(rows),
			"models", countModels(rows),
			"days", countDays(rows),
		)
	}

	if s.Notify != nil {
		if err := s.Notify.Run(now); err != nil {
			s.Log.Error("forecast gust notify", "err", err)
		}
	}
	return nil
}

// mergeMissingModels appends the models in extra that rows does not already
// have, so a spot keeps its own series for every model it has and only borrows
// the ones its Windguru spot withholds.
func mergeMissingModels(rows, extra []models.WindForecastRow) ([]models.WindForecastRow, int) {
	have := map[int]bool{}
	for _, r := range rows {
		have[r.IDModel] = true
	}
	added := map[int]bool{}
	for _, r := range extra {
		if have[r.IDModel] {
			continue
		}
		added[r.IDModel] = true
		rows = append(rows, r)
	}
	return rows, len(added)
}

func countModels(rows []models.WindForecastRow) int {
	seen := map[int]bool{}
	for _, r := range rows {
		seen[r.IDModel] = true
	}
	return len(seen)
}

func countDays(rows []models.WindForecastRow) int {
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.ForecastDate.Format("2006-01-02")] = true
	}
	return len(seen)
}
