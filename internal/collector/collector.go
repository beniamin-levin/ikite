package collector

import (
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/notify/telegram"
	"github.com/ben/ikite-go/internal/sources/kyhistory"
	"github.com/ben/ikite-go/internal/sources/windguru"
	"github.com/ben/ikite-go/internal/sources/windometer"
	"github.com/ben/ikite-go/internal/store"
)

type Service struct {
	Cfg      *config.Config
	Store    *store.Store
	WG       *windguru.Client
	KY       *kyhistory.Client
	KH       *windometer.Client
	Telegram *telegram.Client
	Log      *slog.Logger
}

// alertReadingMaxAge is how fresh the selected alert-spot reading must be to allow Telegram alerts.
const alertReadingMaxAge = 45 * time.Minute

type Result struct {
	WindKY     float64
	WindKH     float64
	MsgKY      string
	MsgNorth   string
	MsgBG      string
	AlertSent  bool
	SavedCount int
}

// alertReadingFresh reports whether the latest reading period is recent enough for alerts.
func alertReadingFresh(now, latest time.Time) bool {
	if latest.IsZero() {
		return false
	}
	age := now.Sub(latest)
	if age < 0 {
		age = -age
	}
	return age <= alertReadingMaxAge
}

func (s *Service) shouldCollectSpot(sp models.Spot, now time.Time) (bool, string) {
	if reason := sp.CollectSkipReason(now); reason != "" {
		return false, reason
	}
	last, err := s.Store.LatestWindPeriod(sp.ID)
	if err != nil {
		s.Log.Warn("latest wind period", "loc", sp.ID, "err", err)
	}
	return sp.ShouldCollectAt(now, last)
}

// RunWindguruStation fetches and saves one Windguru station (used by per-station timers).
// Windguru/Beget HTTP is only used when schedule checks pass.
func (s *Service) RunWindguruStation(now time.Time, stationID int) error {
	st, err := s.Store.SpotByWindguruID(stationID)
	if err != nil {
		return err
	}

	now = now.In(s.Cfg.Timezone)
	if ok, reason := s.shouldCollectSpot(*st, now); !ok {
		s.Log.Info("windguru skipped", "station", stationID, "loc", st.ID, "reason", reason)
		return nil
	}

	delay := 2 + rand.Intn(9) // 2–10 seconds
	s.Log.Info("windguru delay", "station", stationID, "seconds", delay)
	time.Sleep(time.Duration(delay) * time.Second)

	reading, _, err := s.WG.Fetch(stationID)
	if err != nil {
		return err
	}

	reading.Period = now
	reading.Location = st.ID
	if err := s.Store.InsertWind(*reading); err != nil {
		return fmt.Errorf("insert wg wind: %w", err)
	}

	s.Log.Info("saved windguru", "station", stationID, "loc", st.ID, "wind", reading.Wind, "gust", reading.Gust)
	return nil
}

func (s *Service) Run(now time.Time) (*Result, error) {
	now = now.In(s.Cfg.Timezone)
	hour := now.Hour()
	res := &Result{}

	threshold, err := s.Store.Threshold()
	if err != nil {
		return nil, fmt.Errorf("threshold: %w", err)
	}

	if ky, err := s.Store.SpotByID("ky"); err == nil {
		if ok, reason := s.shouldCollectSpot(*ky, now); ok {
			readings, stats, err := s.KY.Fetch(now)
			if err != nil {
				s.Log.Error("ky history fetch failed", "err", err)
			} else {
				for _, r := range readings {
					if err := s.Store.InsertWind(r); err != nil {
						s.Log.Error("insert ky wind", "err", err, "period", r.Period)
					} else {
						res.SavedCount++
					}
				}
				res.WindKY = stats.WindMax
				res.MsgKY = stats.Msg
				s.Log.Info("ky history saved", "rows", len(readings), "wind_ky", res.WindKY)
			}
		} else {
			s.Log.Info("ky skipped", "loc", ky.ID, "reason", reason)
		}
	}

	var kh *models.WindReading
	if spot, err := s.Store.SpotByID("kh"); err == nil {
		if ok, reason := s.shouldCollectSpot(*spot, now); ok {
			var err error
			kh, _, err = s.KH.Fetch(now)
			if err != nil {
				s.Log.Warn("windometer fetch failed", "err", err)
			} else if kh != nil {
				if err := s.Store.InsertWind(*kh); err != nil {
					s.Log.Error("insert kh wind", "err", err)
				} else {
					res.SavedCount++
					res.WindKH = kh.Wind
					s.Log.Info("saved kh", "wind", kh.Wind, "gust", kh.Gust)
				}
			}
		} else {
			s.Log.Info("kh skipped", "loc", spot.ID, "reason", reason)
			if w, err := s.Store.LatestWind("kh"); err == nil {
				res.WindKH = w
			}
		}
	}

	if res.WindKY == 0 {
		if w, err := s.Store.LatestWind("ky"); err == nil {
			res.WindKY = w
		}
	}

	gustST, _ := s.Store.LatestGust("st")
	gustBG, _ := s.Store.LatestGust("bg")
	gustBetzet, _ := s.Store.LatestGust("15233")

	res.MsgNorth = fmt.Sprintf("%d", int(gustST))
	if gustBetzet > 0 {
		res.MsgNorth = fmt.Sprintf("%d", int(gustBetzet))
	}
	res.MsgBG = fmt.Sprintf("%d", int(gustBG))

	if res.MsgKY == "" && res.WindKH > 0 {
		res.MsgKY = fmt.Sprintf("%.0f - %.0f", res.WindKH, res.WindKH)
	}

	alertLeft, err := s.Store.AlertTelegramSpotLeft()
	if err != nil {
		return nil, fmt.Errorf("alert left spot: %w", err)
	}
	if _, err := s.Store.SpotByID(alertLeft); err != nil {
		s.Log.Warn("alert left spot missing, falling back to 15233", "spot", alertLeft, "err", err)
		alertLeft = "15233"
	}
	alertSpot, err := s.Store.AlertTelegramSpot()
	if err != nil {
		return nil, fmt.Errorf("alert spot: %w", err)
	}
	if _, err := s.Store.SpotByID(alertSpot); err != nil {
		s.Log.Warn("alert spot missing, falling back to ky", "spot", alertSpot, "err", err)
		alertSpot = "ky"
	}
	alertRight, err := s.Store.AlertTelegramSpotRight()
	if err != nil {
		return nil, fmt.Errorf("alert right spot: %w", err)
	}
	if _, err := s.Store.SpotByID(alertRight); err != nil {
		s.Log.Warn("alert right spot missing, falling back to bg", "spot", alertRight, "err", err)
		alertRight = "bg"
	}
	alertIntervalMin, err := s.Store.AlertTelegramIntervalMin()
	if err != nil {
		return nil, fmt.Errorf("alert interval: %w", err)
	}
	alertInterval := time.Duration(alertIntervalMin) * time.Minute

	leftMsg := alertSideGust(s.Store, alertLeft, res.MsgNorth)
	rightMsg := alertSideGust(s.Store, alertRight, res.MsgBG)

	alertWind := res.WindKY
	alertMsg := res.MsgKY
	if alertSpot != "ky" {
		wind, gust, err := s.Store.LatestWindGust(alertSpot)
		if err != nil {
			s.Log.Warn("latest alert spot wind", "spot", alertSpot, "err", err)
		} else {
			alertWind = wind
			alertMsg = fmt.Sprintf("%.0f - %.0f", wind, gust)
		}
	} else if alertWind == 0 {
		if w, err := s.Store.LatestWind("ky"); err == nil {
			alertWind = w
			res.WindKY = w
		}
	}
	if alertMsg == "" {
		alertMsg = fmt.Sprintf("%.0f", alertWind)
	}

	latestAlertPeriod, err := s.Store.LatestWindPeriod(alertSpot)
	if err != nil {
		s.Log.Warn("latest alert spot period", "spot", alertSpot, "err", err)
	}
	spotFresh := alertReadingFresh(now, latestAlertPeriod)

	overThreshold := alertWind >= threshold
	inAlertHours := hour >= s.Cfg.AlertStartHour && hour <= s.Cfg.AlertEndHour

	lastAlert, err := s.Store.LastWindAlertAt()
	if err != nil {
		s.Log.Warn("last wind alert time", "err", err)
	}
	intervalOK := lastAlert.IsZero() || now.Sub(lastAlert) >= alertInterval

	shouldAlert := inAlertHours && threshold != 999 && overThreshold && spotFresh && intervalOK

	if inAlertHours && threshold != 999 && overThreshold && !spotFresh {
		s.Log.Info("telegram alert skipped",
			"reason", "alert spot data stuck or stale",
			"spot", alertSpot,
			"latest", latestAlertPeriod,
			"max_age_min", int(alertReadingMaxAge.Minutes()),
			"wind", alertWind,
			"threshold", threshold,
		)
	} else if inAlertHours && threshold != 999 && overThreshold && spotFresh && !intervalOK {
		s.Log.Info("telegram alert skipped",
			"reason", "interval not elapsed",
			"spot", alertSpot,
			"interval_min", alertIntervalMin,
			"last_alert", lastAlert,
		)
	}

	if shouldAlert && s.Telegram.Enabled() {
		msg := fmt.Sprintf("%s | %s | %s", leftMsg, alertMsg, rightMsg)
		if err := s.Telegram.Send(msg); err != nil {
			s.Log.Error("telegram alert failed", "err", err)
		} else {
			res.AlertSent = true
			if err := s.Store.SetLastWindAlertAt(now); err != nil {
				s.Log.Warn("persist last wind alert time", "err", err)
			}
			s.Log.Info("telegram alert sent",
				"msg", msg,
				"left", alertLeft,
				"spot", alertSpot,
				"right", alertRight,
				"interval_min", alertIntervalMin,
			)
		}
	}

	return res, nil
}

func alertSideGust(st *store.Store, spotID, fallback string) string {
	gust, err := st.LatestGust(spotID)
	if err != nil || gust <= 0 {
		if fallback != "" {
			return fallback
		}
		return "0"
	}
	return fmt.Sprintf("%d", int(gust))
}
