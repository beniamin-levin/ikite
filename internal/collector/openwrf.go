package collector

import (
	"fmt"
	"log/slog"
	"sort"
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
	Notify  *ForecastGustNotifyService
}

type OpenWRFForecastOptions struct {
	Force bool
}

// The openWRF publisher uploads one run per day, but the upload time drifts and a
// run is sometimes skipped entirely. A single 08:00 attempt loses the whole day
// whenever the file lands late, so retry across the day and stop per spot as soon
// as today's rows are stored.
const (
	openWRFRetryFromHour = 5
	openWRFRetryToHour   = 21
)

func (s *OpenWRFForecastService) Run(now time.Time, opts OpenWRFForecastOptions) error {
	now = now.In(s.Cfg.Timezone)
	if !opts.Force && (now.Hour() < openWRFRetryFromHour || now.Hour() > openWRFRetryToHour) {
		s.Log.Info("openWRF forecast skipped", "reason", "outside retry window",
			"hour", now.Hour(), "from", openWRFRetryFromHour, "to", openWRFRetryToHour)
		return nil
	}

	spots, err := s.Store.ListSpots()
	if err != nil {
		return err
	}
	mappings, err := s.OpenWRF.ListSpotMappings(spots)
	if err != nil {
		return fmt.Errorf("list openWRF sources: %w", err)
	}
	if len(mappings) == 0 {
		s.Log.Info("openWRF forecast skipped", "reason", "no matching spots")
		return nil
	}

	for _, m := range mappings {
		s.Log.Info("openWRF spot mapped",
			"spot", m.Spot.ID,
			"name", m.Spot.Name,
			"source", m.Label,
			"url", m.URL,
			"forecast_key", m.Forecast,
		)
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	tomorrow := today.AddDate(0, 0, 1)

	var saved, failed, alreadyHave, stale int
	for _, m := range mappings {
		// Already have today's run for this spot - skip the download entirely so the
		// hourly retry costs nothing once the day has landed.
		if !opts.Force {
			done, err := s.Store.WindForecastModelAlreadyFetched(m.Forecast, today, openwrf.ModelID)
			if err != nil {
				s.Log.Warn("openWRF already-fetched check", "spot", m.Spot.ID, "err", err)
			} else if done {
				alreadyHave++
				continue
			}
		}

		rows, err := s.OpenWRF.FetchForMapping(m, now, s.Cfg.Timezone)
		if err != nil {
			s.Log.Error("openWRF fetch failed", "spot", m.Spot.ID, "source", m.Label, "err", err)
			failed++
			continue
		}
		byDate := groupRowsByForecastDate(rows, m.Spot.ID, m.Forecast)
		toSave := filterOpenWRFDays(byDate, today, tomorrow)

		if len(toSave) == 0 {
			from, to := forecastDateRange(byDate)
			stale++
			s.Log.Info("openWRF forecast skipped",
				"spot", m.Spot.ID,
				"reason", "upstream file has no rows for today or tomorrow",
				"source_from", from,
				"source_to", to,
				"source_age_days", sourceAgeDays(to, today),
				"today", today.Format("2006-01-02"))
			continue
		}

		if err := s.Store.ReplaceWindForecastModelDays(m.Forecast, openwrf.ModelID, today, now, toSave); err != nil {
			s.Log.Error("openWRF save failed", "spot", m.Spot.ID, "err", err)
			failed++
			continue
		}
		total := 0
		for _, dayRows := range toSave {
			total += len(dayRows)
		}
		s.Log.Info("openWRF forecast saved",
			"spot", m.Spot.ID,
			"source", m.Label,
			"days", len(toSave),
			"rows", total,
			"model", openwrf.ModelName,
		)
		saved++
	}

	if failed > 0 && saved == 0 {
		return fmt.Errorf("openWRF: all %d spot fetches failed", failed)
	}
	s.Log.Info("openWRF forecast done",
		"saved", saved, "failed", failed, "mapped", len(mappings),
		"already_had_today", alreadyHave, "upstream_stale", stale)

	if s.Notify != nil {
		if err := s.Notify.Run(now); err != nil {
			s.Log.Error("forecast gust notify", "err", err)
		}
	}
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

func filterOpenWRFDays(byDate map[string][]models.WindForecastRow, today, tomorrow time.Time) map[string][]models.WindForecastRow {
	out := make(map[string][]models.WindForecastRow)
	for _, key := range []string{today.Format("2006-01-02"), tomorrow.Format("2006-01-02")} {
		if rows, ok := byDate[key]; ok && len(rows) > 0 {
			out[key] = rows
		}
	}
	return out
}

// forecastDateRange returns the first and last forecast day present in the parsed
// upstream file, so a skip reports how stale that file is rather than just saying
// there were no usable rows.
func forecastDateRange(byDate map[string][]models.WindForecastRow) (string, string) {
	if len(byDate) == 0 {
		return "", ""
	}
	keys := make([]string, 0, len(byDate))
	for k := range byDate {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys[0], keys[len(keys)-1]
}

// sourceAgeDays reports how many days behind today the newest upstream day is.
func sourceAgeDays(newest string, today time.Time) int {
	if newest == "" {
		return -1
	}
	d, err := time.ParseInLocation("2006-01-02", newest, today.Location())
	if err != nil {
		return -1
	}
	return int(today.Sub(d).Hours() / 24)
}
