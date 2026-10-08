package collector

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/sources/ims"
	"github.com/ben/ikite-go/internal/store"
)

const (
	imsIncrementalWindow = 3 * time.Hour
	imsBackfillWindow    = 36 * time.Hour
	imsNearestMaxKm      = 15.0
)

// IMSService pulls IMS Envista observations on a short rolling window (~every 10 min).
// The Envista API (api.ims.gov.il/v1/envista) exposes station observations only; IMS public
// forecasts are served from separate non-Envista endpoints and are intentionally not collected here.
// Daily 36h backfill remains in ExtForecastService.runIMS.
type IMSService struct {
	Cfg   *config.Config
	Store *store.Store
	IMS   *ims.Client
	Log   *slog.Logger
}

type IMSRunOptions struct {
	// Backfill pulls a longer window (36h) — used after timezone/mapping fixes.
	Backfill bool
}

func (s *IMSService) Run(now time.Time) error {
	return s.RunOpts(now, IMSRunOptions{})
}

func (s *IMSService) RunOpts(now time.Time, opts IMSRunOptions) error {
	if s.IMS == nil || s.Cfg.IMSAPIToken == "" {
		s.Log.Info("ims skipped", "reason", "no API token")
		return nil
	}

	now = now.In(s.Cfg.Timezone)
	spots, err := s.Store.ListSpots()
	if err != nil {
		return err
	}

	stationIDs, err := s.resolveStationIDs(spots)
	if err != nil {
		return err
	}
	if len(stationIDs) == 0 {
		s.Log.Info("ims skipped", "reason", "no IMS stations configured or matched")
		return nil
	}

	window := imsIncrementalWindow
	if opts.Backfill {
		window = imsBackfillWindow
	}
	from := now.Add(-window)
	to := now.Add(time.Hour)
	var saved, failed int
	for _, stationID := range stationIDs {
		rows, err := s.IMS.FetchStationRange(stationID, from, to)
		if err != nil {
			if isIMSNoDataErr(err) {
				s.Log.Info("ims no recent rows", "station", stationID)
				continue
			}
			s.Log.Error("ims fetch", "station", stationID, "err", err)
			failed++
			continue
		}
		// Merge /data/latest so the tip of the series is never missed when the
		// day-range endpoint lags a few minutes behind.
		if latest, err := s.IMS.FetchStationLatest(stationID); err == nil {
			rows = mergeIMSReadings(rows, latest)
		} else if !isIMSNoDataErr(err) {
			s.Log.Warn("ims latest", "station", stationID, "err", err)
		}
		rows = ims.FilterReadingsSince(rows, from)
		if len(rows) == 0 {
			s.Log.Info("ims no rows in window", "station", stationID)
			continue
		}

		inserted := 0
		for _, rd := range rows {
			rd.Period = rd.Period.In(s.Cfg.Timezone)
			if err := s.Store.InsertWind(rd); err != nil {
				s.Log.Error("ims save", "station", stationID, "err", err)
				failed++
				inserted = 0
				break
			}
			if rd.Raw != "" {
				if err := s.Store.InsertWindLog(rd.Period, rd.Location, rd.Raw); err != nil {
					s.Log.Warn("ims raw log", "station", stationID, "err", err)
				}
			}
			inserted++
		}
		if inserted == 0 {
			continue
		}
		s.Log.Info("ims saved", "station", stationID, "rows", inserted, "location", ims.LocationKey(stationID))
		saved++
	}

	if failed > 0 && saved == 0 {
		return fmt.Errorf("ims: all fetches failed")
	}
	s.Log.Info("ims done", "stations", len(stationIDs), "saved", saved, "failed", failed)
	return nil
}

func (s *IMSService) resolveStationIDs(spots []models.Spot) ([]int, error) {
	seen := map[int]bool{}
	var needGPS []models.Spot

	for _, sp := range spots {
		if sp.IMSStationID != nil {
			seen[*sp.IMSStationID] = true
			continue
		}
		if sp.Collect && sp.HasCoords() {
			needGPS = append(needGPS, sp)
		}
	}

	if len(needGPS) > 0 {
		stations, err := s.IMS.FetchStations()
		if err != nil {
			return nil, fmt.Errorf("ims stations list: %w", err)
		}
		for _, sp := range needGPS {
			st, dist := ims.NearestStation(stations, *sp.Lat, *sp.Lon, imsNearestMaxKm)
			if st == nil {
				s.Log.Info("ims no nearby station", "spot", sp.ID, "lat", *sp.Lat, "lon", *sp.Lon, "max_km", imsNearestMaxKm)
				continue
			}
			if seen[st.ID] {
				continue
			}
			seen[st.ID] = true
			s.Log.Info("ims matched station by GPS",
				"spot", sp.ID,
				"spot_name", sp.Name,
				"station", st.ID,
				"station_name", st.Name,
				"distance_km", fmt.Sprintf("%.1f", dist),
			)
		}
	}

	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out, nil
}

func isIMSNoDataErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no wind rows")
}

func mergeIMSReadings(base, extra []models.WindReading) []models.WindReading {
	if len(extra) == 0 {
		return base
	}
	byTS := make(map[int64]models.WindReading, len(base)+len(extra))
	for _, rd := range base {
		byTS[rd.Period.Unix()] = rd
	}
	for _, rd := range extra {
		byTS[rd.Period.Unix()] = rd
	}
	out := make([]models.WindReading, 0, len(byTS))
	for _, rd := range byTS {
		out = append(out, rd)
	}
	return out
}
