package collector

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/notify/telegram"
	"github.com/ben/ikite-go/internal/sources/windguru"
	"github.com/ben/ikite-go/internal/store"
)

const (
	forecastGustThreshold = 25

	// Send after Windguru (07:00) and openWRF (08:00) have been collected.
	forecastGustSendHour = 8
	forecastGustSendMin  = 10

	forecastGustAlertDayKey         = "forecast_gust_alert_day"
	forecastGustAlertFingerprintKey = "forecast_gust_alert_fingerprint"
)

// internationalSpotIDs are non-Israel spots excluded from forecast gust alerts.
var internationalSpotIDs = map[string]bool{
	"1091":  true, // Paros
	"5500":  true, // Mykonos
	"14905": true, // Squamish
	"2667":  true, // Tarifa
	"3708":  true, // Los Alcazares
	"4257":  true, // Zandvoort
}

type ForecastGustNotifyService struct {
	Cfg      *config.Config
	Store    *store.Store
	Telegram *telegram.Client
	Log      *slog.Logger
}

type ForecastGustNotifyOptions struct {
	Force  bool // bypass 08:10 and once-per-day gates
	DryRun bool // build/log message but do not send or persist
}

func (s *ForecastGustNotifyService) Run(now time.Time) error {
	return s.RunOpts(now, ForecastGustNotifyOptions{})
}

func (s *ForecastGustNotifyService) RunOpts(now time.Time, opts ForecastGustNotifyOptions) error {
	if s.Telegram == nil || !s.Telegram.Enabled() {
		if !opts.DryRun {
			return nil
		}
	}

	now = now.In(s.Cfg.Timezone)
	if !opts.Force && !forecastGustSendWindowOpen(now) {
		s.Log.Info("forecast gust alert skipped", "reason", "before 08:10", "now", now.Format("15:04"))
		return nil
	}

	dayKey := now.Format("2006-01-02")
	if !opts.Force {
		prevDay, err := s.Store.GetSetting(forecastGustAlertDayKey)
		if err != nil {
			s.Log.Warn("forecast gust alert day", "err", err)
		} else if prevDay == dayKey {
			s.Log.Info("forecast gust alert skipped", "reason", "already sent today", "day", dayKey)
			return nil
		}
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Cfg.Timezone)
	tomorrow := today.AddDate(0, 0, 1)

	spots, err := s.Store.ListSpots()
	if err != nil {
		return err
	}
	locations := israelSpotIDs(spots)
	if len(locations) == 0 {
		return nil
	}

	rows, err := s.Store.ListHighGustForecasts(locations, today, tomorrow, forecastGustThreshold)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		s.Log.Info("forecast gust alert skipped", "reason", "no rows")
		return nil
	}

	spotNames := map[string]string{}
	for _, sp := range spots {
		spotNames[sp.ID] = sp.Name
	}

	fingerprint := forecastGustFingerprint(rows)
	prev, err := s.Store.GetSetting(forecastGustAlertFingerprintKey)
	if err != nil {
		s.Log.Warn("forecast gust fingerprint", "err", err)
	}
	if !opts.Force && prev == fingerprint {
		s.Log.Info("forecast gust alert skipped", "reason", "unchanged")
		// Still mark the day so collectors later today do not retry.
		if err := s.Store.SetSetting(forecastGustAlertDayKey, dayKey); err != nil {
			s.Log.Warn("persist forecast gust alert day", "err", err)
		}
		return nil
	}

	msg := formatForecastGustAlert(rows, spotNames, s.Cfg.Timezone)
	if opts.DryRun {
		s.Log.Info("forecast gust alert dry-run", "rows", len(rows), "msg", msg)
		return nil
	}
	if s.Telegram == nil || !s.Telegram.Enabled() {
		return nil
	}
	if err := s.Telegram.Send(msg); err != nil {
		return fmt.Errorf("telegram forecast gust: %w", err)
	}
	if err := s.Store.SetSetting(forecastGustAlertFingerprintKey, fingerprint); err != nil {
		s.Log.Warn("persist forecast gust fingerprint", "err", err)
	}
	if err := s.Store.SetSetting(forecastGustAlertDayKey, dayKey); err != nil {
		s.Log.Warn("persist forecast gust alert day", "err", err)
	}
	s.Log.Info("forecast gust alert sent", "rows", len(rows))
	return nil
}

// forecastGustSendWindowOpen reports whether local time is at/after 08:10.
func forecastGustSendWindowOpen(now time.Time) bool {
	minutes := now.Hour()*60 + now.Minute()
	return minutes >= forecastGustSendHour*60+forecastGustSendMin
}

func israelSpotIDs(spots []models.Spot) []string {
	var out []string
	for _, sp := range spots {
		if internationalSpotIDs[sp.ID] {
			continue
		}
		out = append(out, sp.ID)
	}
	return out
}

func forecastGustFingerprint(rows []models.WindForecastRow) string {
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		gust := 0.0
		if r.Gust != nil {
			gust = *r.Gust
		}
		keys = append(keys, fmt.Sprintf("%s|%s|%s|%s|%.0f",
			r.Location,
			r.ForecastDate.Format("2006-01-02"),
			r.Period.Format("15:04"),
			windguru.DisplayModelName(r.IDModel, r.Model),
			gust,
		))
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(sum[:])
}

func formatForecastGustAlert(rows []models.WindForecastRow, spotNames map[string]string, loc *time.Location) string {
	type key struct {
		spot, date string
	}
	bySpotDay := map[key][]models.WindForecastRow{}
	for _, r := range rows {
		k := key{spot: r.Location, date: r.ForecastDate.Format("2006-01-02")}
		bySpotDay[k] = append(bySpotDay[k], r)
	}

	var keys []key
	for k := range bySpotDay {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].date != keys[j].date {
			return keys[i].date < keys[j].date
		}
		return keys[i].spot < keys[j].spot
	})

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Forecast gust %d+ kt\n", int(forecastGustThreshold)))
	for _, k := range keys {
		name := spotNames[k.spot]
		if name == "" {
			name = k.spot
		}
		b.WriteString(fmt.Sprintf("\n%s · %s\n", name, k.date))
		dayRows := bySpotDay[k]
		sort.Slice(dayRows, func(i, j int) bool {
			if !dayRows[i].Period.Equal(dayRows[j].Period) {
				return dayRows[i].Period.Before(dayRows[j].Period)
			}
			return windguru.DisplayModelName(dayRows[i].IDModel, dayRows[i].Model) <
				windguru.DisplayModelName(dayRows[j].IDModel, dayRows[j].Model)
		})
		for _, r := range dayRows {
			wind, gust := 0.0, 0.0
			if r.Wind != nil {
				wind = *r.Wind
			}
			if r.Gust != nil {
				gust = *r.Gust
			}
			model := windguru.DisplayModelName(r.IDModel, r.Model)
			b.WriteString(fmt.Sprintf("  %s %s %.0f-%.0f\n",
				r.Period.In(loc).Format("15:04"),
				model,
				wind,
				gust,
			))
		}
	}
	return strings.TrimSpace(b.String())
}
