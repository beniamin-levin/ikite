package collector

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/sources/openwrf"
	"github.com/ben/ikite-go/internal/store"
)

type OpenWRFForecastService struct {
	Cfg     *config.Config
	Store   *store.Store
	OpenWRF *openwrf.Client
	Log     *slog.Logger
}

type OpenWRFForecastOptions struct {
	Force bool
}

func (s *OpenWRFForecastService) Run(now time.Time, opts OpenWRFForecastOptions) error {
	now = now.In(s.Cfg.Timezone)
	if !opts.Force && now.Hour() != 8 {
		s.Log.Info("openWRF forecast skipped", "reason", "not 8am", "hour", now.Hour())
		return nil
	}

	spots, err := s.Store.ListSpots()
	if err != nil {
		return err
	}
	files, err := s.OpenWRF.ListPDFs()
	if err != nil {
		return fmt.Errorf("list openWRF PDFs: %w", err)
	}
	mappings := openwrf.MatchSpots(spots, files)
	if len(mappings) == 0 {
		s.Log.Info("openWRF forecast skipped", "reason", "no matching spots")
		return nil
	}

	for _, m := range mappings {
		s.Log.Info("openWRF spot mapped",
			"spot", m.Spot.ID,
			"name", m.Spot.Name,
			"pdf", m.PDF.Name,
			"forecast_key", m.Forecast,
		)
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	var saved, skipped, failed int
	for _, m := range mappings {
		if !opts.Force {
			exists, err := s.Store.WindForecastModelAlreadyFetched(m.Forecast, today, openwrf.ModelID)
			if err != nil {
				return err
			}
			if exists {
				s.Log.Info("openWRF forecast skipped", "spot", m.Spot.ID, "reason", "already fetched today")
				skipped++
				continue
			}
		}

		rows, err := s.OpenWRF.FetchFile(m.PDF, now, s.Cfg.Timezone)
		if err != nil {
			s.Log.Error("openWRF fetch failed", "spot", m.Spot.ID, "pdf", m.PDF.Name, "err", err)
			failed++
			continue
		}
		byDate := groupRowsByForecastDate(rows, m.Spot.ID, m.Forecast)
		if err := s.Store.ReplaceWindForecastModelDays(m.Forecast, openwrf.ModelID, now, byDate); err != nil {
			s.Log.Error("openWRF save failed", "spot", m.Spot.ID, "err", err)
			failed++
			continue
		}
		total := 0
		for _, dayRows := range byDate {
			total += len(dayRows)
		}
		s.Log.Info("openWRF forecast saved",
			"spot", m.Spot.ID,
			"pdf", m.PDF.Name,
			"days", len(byDate),
			"rows", total,
			"model", openwrf.ModelName,
		)
		saved++
	}

	if failed > 0 && saved == 0 {
		return fmt.Errorf("openWRF: all %d spot fetches failed", failed)
	}
	s.Log.Info("openWRF forecast done", "saved", saved, "skipped", skipped, "failed", failed, "mapped", len(mappings))
	return nil
}

func groupRowsByForecastDate(rows []models.WindForecastRow, location string, windguruID int) map[string][]models.WindForecastRow {
	out := make(map[string][]models.WindForecastRow)
	for _, r := range rows {
		r.Location = location
		r.WindguruID = windguruID
		day := r.ForecastDate
		if day.IsZero() {
			day = time.Date(r.Period.Year(), r.Period.Month(), r.Period.Day(), 0, 0, 0, 0, r.Period.Location())
			r.ForecastDate = day
		}
		key := day.Format("2006-01-02")
		out[key] = append(out[key], r)
	}
	return out
}
