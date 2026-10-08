package collector

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/sources/ims"
	"github.com/ben/ikite-go/internal/sources/imssea"
	"github.com/ben/ikite-go/internal/sources/openmeteo"
	"github.com/ben/ikite-go/internal/sources/openskiron"
	"github.com/ben/ikite-go/internal/sources/openwrf"
	"github.com/ben/ikite-go/internal/store"
)

// ExtForecastService pulls OpenSkiron, Open-Meteo, and IMS on a daily schedule.
type ExtForecastService struct {
	Cfg        *config.Config
	Store      *store.Store
	OpenMeteo  *openmeteo.Client
	OpenSkiron *openskiron.Client
	IMS        *ims.Client
	Log        *slog.Logger
	Notify     *ForecastGustNotifyService
}

type ExtForecastOptions struct {
	Force bool
}

func (s *ExtForecastService) Run(now time.Time, opts ExtForecastOptions) error {
	now = now.In(s.Cfg.Timezone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	// IMS updates its sea forecast in the afternoon (16:25 on 2026-10-06); the
	// 17:30 run (or a catch-up later that evening) saves that issue and nothing else.
	if !opts.Force && now.Hour() >= imsSeaEveningHour {
		if err := s.runIMSSea(today, now); err != nil {
			s.Log.Error("ims sea forecast", "err", err)
			return err
		}
		return nil
	}
	if !opts.Force && now.Hour() != 9 {
		s.Log.Info("ext forecast skipped", "reason", "not 9am", "hour", now.Hour())
		return nil
	}

	var errs []error
	ran := 0

	ran++
	if err := s.runOpenMeteo(today, now); err != nil {
		s.Log.Error("open-meteo", "err", err)
		errs = append(errs, err)
	}

	// openskiron.org has been offline since ~June 2026. While its host is simply
	// unreachable there is nothing to fix on our side, so log it once at WARN and
	// leave it out of the health check instead of failing every run with an ERROR.
	// The call is still made each run, so the feed resumes on its own if the site
	// comes back — it has returned from a month-long outage before.
	if err := s.runOpenSkiron(today, now); err != nil {
		if errors.Is(err, openskiron.ErrUnreachable) {
			s.Log.Warn("openskiron unavailable",
				"reason", "upstream host unreachable, skipping this run", "err", err)
		} else {
			ran++
			s.Log.Error("openskiron", "err", err)
			errs = append(errs, err)
		}
	} else {
		ran++
	}

	ran++
	if err := s.runIMSSea(today, now); err != nil {
		s.Log.Error("ims sea forecast", "err", err)
		errs = append(errs, err)
	}

	ran++
	if err := s.runIMS(now); err != nil {
		s.Log.Error("ims", "err", err)
		errs = append(errs, err)
	}

	if s.Notify != nil {
		if err := s.Notify.Run(now); err != nil {
			s.Log.Error("forecast gust notify", "err", err)
		}
	}

	if ran > 0 && len(errs) == ran {
		return fmt.Errorf("ext forecast: all %d sources failed", ran)
	}
	s.Log.Info("ext forecast done", "errors", len(errs), "sources_ran", ran)
	return nil
}

func (s *ExtForecastService) runOpenMeteo(today, now time.Time) error {
	if s.OpenMeteo == nil {
		return fmt.Errorf("open-meteo client not configured")
	}
	spots, err := s.Store.SpotsWithCoords()
	if err != nil {
		return err
	}
	if len(spots) == 0 {
		s.Log.Info("open-meteo skipped", "reason", "no spots with coords")
		return nil
	}
	var saved, failed int
	for _, sp := range spots {
		for _, model := range openmeteo.Models() {
			rows, err := s.OpenMeteo.FetchSpot(sp, model, 7, s.Cfg.Timezone)
			if err != nil {
				s.Log.Error("open-meteo fetch", "spot", sp.ID, "model", model.Name, "err", err)
				failed++
				continue
			}
			byDate := groupRowsByForecastDate(rows, sp.ID, openwrf.ForecastKey(sp))
			if err := s.Store.ReplaceWindForecastModelDays(openwrf.ForecastKey(sp), model.ID, today, now, byDate); err != nil {
				s.Log.Error("open-meteo save", "spot", sp.ID, "model", model.Name, "err", err)
				failed++
				continue
			}
			s.Log.Info("open-meteo saved", "spot", sp.ID, "model", model.Name, "rows", len(rows))
			saved++
		}
	}
	if failed > 0 && saved == 0 {
		return fmt.Errorf("open-meteo: all fetches failed")
	}
	return nil
}

// imsSeaEveningHour is the extra evening run that only fetches the IMS sea forecast.
const imsSeaEveningHour = 17

// runIMSSea saves the IMS official sea forecast in full and adds its wind to
// each spot on a covered coast as the ims_sea model. Each issue replaces only
// the hours it covers: the afternoon issue starts at 20:00, and must not wipe
// the morning issue's daytime forecast before it has been scored.
func (s *ExtForecastService) runIMSSea(today, now time.Time) error {
	f, _, err := imssea.Fetch(nil, s.Cfg.Timezone)
	if err != nil {
		return err
	}
	if err := s.Store.SaveIMSSeaForecast(f, now); err != nil {
		return fmt.Errorf("save ims sea forecast: %w", err)
	}
	regions := map[int]imssea.Region{}
	for _, r := range f.Regions {
		regions[r.ID] = r
	}
	spots, err := s.Store.SpotsWithCoords()
	if err != nil {
		return err
	}
	var saved int
	for _, sp := range spots {
		reg, ok := regions[imssea.RegionFor(*sp.Lat, *sp.Lon)]
		if !ok {
			continue
		}
		key := openwrf.ForecastKey(sp)
		rows := reg.Rows(sp.ID, key)
		if len(rows) == 0 {
			continue
		}
		if err := s.Store.ReplaceWindForecastModelFrom(key, imssea.ModelID, rows[0].Period, now, rows); err != nil {
			s.Log.Error("ims sea save", "spot", sp.ID, "err", err)
			continue
		}
		saved++
	}
	s.Log.Info("ims sea forecast saved", "issued", f.Issued.Format("2006-01-02 15:04"), "regions", len(f.Regions), "spots", saved)
	return nil
}

func (s *ExtForecastService) runOpenSkiron(today, now time.Time) error {
	if s.OpenSkiron == nil {
		return fmt.Errorf("openskiron client not configured")
	}
	spots, err := s.Store.SpotsWithCoords()
	if err != nil {
		return err
	}
	if len(spots) == 0 {
		s.Log.Info("openskiron skipped", "reason", "no spots with coords")
		return nil
	}
	raw, err := s.OpenSkiron.FetchGrib()
	if err != nil {
		return err
	}
	tomorrow := today.AddDate(0, 0, 1)
	var saved, failed int
	for _, sp := range spots {
		rows, err := openskiron.RowsForSpot(raw, sp, s.Cfg.Timezone)
		if err != nil {
			s.Log.Error("openskiron sample", "spot", sp.ID, "err", err)
			failed++
			continue
		}
		byDate := groupRowsByForecastDate(rows, sp.ID, openwrf.ForecastKey(sp))
		// Keep today + tomorrow like local openWRF (4km is ~2 days).
		toSave := filterOpenWRFDays(byDate, today, tomorrow)
		if len(toSave) == 0 {
			s.Log.Info("openskiron skipped", "spot", sp.ID, "reason", "no rows for today/tomorrow")
			continue
		}
		if err := s.Store.ReplaceWindForecastModelDays(openwrf.ForecastKey(sp), openskiron.ModelID, today, now, toSave); err != nil {
			s.Log.Error("openskiron save", "spot", sp.ID, "err", err)
			failed++
			continue
		}
		total := 0
		for _, dayRows := range toSave {
			total += len(dayRows)
		}
		s.Log.Info("openskiron saved", "spot", sp.ID, "rows", total)
		saved++
	}
	if failed > 0 && saved == 0 {
		return fmt.Errorf("openskiron: all spot samples failed")
	}
	return nil
}

func (s *ExtForecastService) runIMS(now time.Time) error {
	// 36h backfill at 09:00; incremental ~3h collection runs every 10 min via IMSService.
	if s.IMS == nil || s.Cfg.IMSAPIToken == "" {
		s.Log.Info("ims skipped", "reason", "no API token")
		return nil
	}
	spots, err := s.Store.SpotsWithIMS()
	if err != nil {
		return err
	}
	if len(spots) == 0 {
		s.Log.Info("ims skipped", "reason", "no spots with ims_station_id")
		return nil
	}
	from := now.Add(-36 * time.Hour)
	var saved, failed int
	seen := map[int]bool{}
	for _, sp := range spots {
		if sp.IMSStationID == nil || seen[*sp.IMSStationID] {
			continue
		}
		stationID := *sp.IMSStationID
		seen[stationID] = true
		rows, err := s.IMS.FetchStationRange(stationID, from, now.Add(time.Hour))
		if err != nil {
			s.Log.Error("ims fetch", "station", stationID, "err", err)
			failed++
			continue
		}
		for _, rd := range rows {
			rd.Period = rd.Period.In(s.Cfg.Timezone)
			if err := s.Store.InsertWind(rd); err != nil {
				s.Log.Error("ims save", "station", stationID, "err", err)
				failed++
				break
			}
		}
		s.Log.Info("ims saved", "station", stationID, "rows", len(rows), "location", ims.LocationKey(stationID))
		saved++
	}
	if failed > 0 && saved == 0 {
		return fmt.Errorf("ims: all fetches failed")
	}
	return nil
}
