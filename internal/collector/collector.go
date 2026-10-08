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

// kyPinMaxAge is how fresh the Surfo KY history tip must be before we re-stamp
// it onto the shared visible collect slot. Older tips keep their source time
// so a stuck upstream feed is not shown as live "today".
// liveReadingMaxAge is how old a live reading may be and still fill the current
// table slot. Slots are 5 minutes apart and every live source reports at least
// that often (Windguru stations every 0.5–2.6 min, surfo every minute), so an
// older reading has either already been shown in an earlier slot or comes from a
// source that has stopped updating. surfo flags its own feed stale at the same
// 5 minutes. A stuck source must leave the slot empty, not be re-stamped as new.
//
// This was 45 minutes for KY, which copied a frozen surfo reading into up to 8
// fresh-looking slots (e.g. 2026-10-02: the 14:30 reading shown at 14:35–15:10).
const liveReadingMaxAge = 5 * time.Minute

// liveReadingFresh reports whether a reading taken at sourceAt may fill the slot
// being collected at now. Strictly under the max age, so a reading exactly one
// slot old is not shown twice. A zero time — no timestamp, or flagged stale by
// the source — is never fresh. A little clock skew into the future is tolerated.
func liveReadingFresh(now, sourceAt time.Time) bool {
	if sourceAt.IsZero() {
		return false
	}
	age := now.Sub(sourceAt)
	return age < liveReadingMaxAge && age > -liveReadingMaxAge
}

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
	return readingWithinAge(now, latest, alertReadingMaxAge)
}

func readingWithinAge(now, t time.Time, maxAge time.Duration) bool {
	if t.IsZero() {
		return false
	}
	age := now.Sub(t)
	if age < 0 {
		age = -age
	}
	return age <= maxAge
}

// kyStorePeriod returns the period to persist for the newest KY history tip.
// When the tip is fresh it is pinned onto slotPeriod so KY shares the table row;
// when Surfo is stuck on old data, the source timestamp is kept (not shown as live).
func kyStorePeriod(sourcePeriod, slotPeriod, now time.Time) (period time.Time, pinned bool) {
	if liveReadingFresh(now, sourcePeriod) {
		return slotPeriod, true
	}
	return sourcePeriod, false
}

// kySourceName labels the Kiryat Yam upstream in health notifications.
const kySourceName = "surfo KY"

// storedTip is the newest period already in the database for a location, used
// as the health signal when the upstream call itself fails.
func (s *Service) storedTip(location string) time.Time {
	t, err := s.Store.LatestWindPeriod(location)
	if err != nil {
		s.Log.Warn("latest wind period", "loc", location, "err", err)
		return time.Time{}
	}
	return t
}

func (s *Service) collectPeers() []models.Spot {
	peers, err := s.Store.CollectSpots()
	if err != nil {
		s.Log.Warn("collect spots for minute slots", "err", err)
		return nil
	}
	return peers
}

func (s *Service) shouldCollectSpot(sp models.Spot, now time.Time, peers []models.Spot) (bool, string) {
	last, err := s.Store.LatestWindPeriod(sp.ID)
	if err != nil {
		s.Log.Warn("latest wind period", "loc", sp.ID, "err", err)
	}
	// Uses spots.visible from the DB (admin/settings), never client layout prefs.
	return sp.ShouldCollectSlot(now, last, peers)
}

// RunWindguruStation fetches and saves one Windguru station (used by per-station timers).
// Windguru/Beget HTTP is only used when schedule checks pass.
func (s *Service) RunWindguruStation(now time.Time, stationID int) error {
	st, err := s.Store.SpotByWindguruID(stationID)
	if err != nil {
		return err
	}

	now = now.In(s.Cfg.Timezone)
	peers := s.collectPeers()
	if ok, reason := s.shouldCollectSpot(*st, now, peers); !ok {
		s.Log.Info("windguru skipped", "station", stationID, "loc", st.ID, "reason", reason)
		return nil
	}

	// Keep visible spots inside the shared minute; stagger the rest lightly.
	delay := rand.Intn(3) // 0–2s
	if !st.Visible {
		delay = 2 + rand.Intn(9) // 2–10s
	}
	s.Log.Info("windguru delay", "station", stationID, "seconds", delay, "visible", st.Visible)
	time.Sleep(time.Duration(delay) * time.Second)

	now = time.Now().In(s.Cfg.Timezone)
	if ok, reason := st.InCollectMinuteSlot(now, peers); !ok {
		s.Log.Info("windguru skipped after delay", "station", stationID, "loc", st.ID, "reason", reason)
		return nil
	}

	reading, _, err := s.WG.Fetch(stationID)
	if err != nil {
		return err
	}
	if !liveReadingFresh(now, reading.SourceAt) {
		s.Log.Warn("windguru station stale; slot left empty", "station", stationID, "loc", st.ID,
			"reading_at", reading.SourceAt, "age", now.Sub(reading.SourceAt).Round(time.Second))
		return nil
	}

	// Stamp onto the scheduled minute so visible spots share one table row key.
	reading.Period = models.CollectSlotPeriod(time.Now().In(s.Cfg.Timezone), *st, peers)
	reading.Location = st.ID
	if err := s.Store.InsertWind(*reading); err != nil {
		return fmt.Errorf("insert wg wind: %w", err)
	}

	s.Log.Info("saved windguru", "station", stationID, "loc", st.ID, "wind", reading.Wind, "gust", reading.Gust, "period", reading.Period)
	return nil
}

func (s *Service) Run(now time.Time) (*Result, error) {
	now = now.In(s.Cfg.Timezone)
	hour := now.Hour()
	res := &Result{}
	peers := s.collectPeers()

	threshold, err := s.Store.Threshold()
	if err != nil {
		return nil, fmt.Errorf("threshold: %w", err)
	}

	if ky, err := s.Store.SpotByID("ky"); err == nil {
		if ok, reason := s.shouldCollectSpot(*ky, now, peers); ok {
			readings, stats, err := s.KY.Fetch(now)
			if err != nil {
				s.Log.Error("ky history fetch failed", "err", err)
				s.reportSourceHealth(kySourceName, s.storedTip("ky"), now)
			} else {
				slotPeriod := models.CollectSlotPeriod(now, *ky, peers)
				var pinnedPeriod time.Time
				pinned := false
				for i, r := range readings {
					// Keep historical rows, but pin a *fresh* tip onto the shared
					// visible minute so KY shares the first-page table row.
					if i == len(readings)-1 {
						var ok bool
						r.Period, ok = kyStorePeriod(r.Period, slotPeriod, now)
						if ok {
							pinned = true
							pinnedPeriod = r.Period
						} else {
							s.Log.Warn("ky source stale; not pinning to current slot",
								"source_period", readings[i].Period, "slot", slotPeriod)
						}
					}
					if err := s.Store.InsertWind(r); err != nil {
						s.Log.Error("insert ky wind", "err", err, "period", r.Period)
					} else {
						res.SavedCount++
					}
				}
				if pinned {
					res.WindKY = stats.WindMax
					res.MsgKY = stats.Msg
					s.Log.Info("ky history saved", "rows", len(readings), "wind_ky", res.WindKY, "period", pinnedPeriod)
				} else {
					s.Log.Info("ky history saved without live pin", "rows", len(readings),
						"source_period", readings[len(readings)-1].Period)
				}
				s.reportSourceHealth(kySourceName, readings[len(readings)-1].Period, now)
			}
		} else {
			s.Log.Info("ky skipped", "loc", ky.ID, "reason", reason)
		}
	}

	var kh *models.WindReading
	if spot, err := s.Store.SpotByID("kh"); err == nil {
		if ok, reason := s.shouldCollectSpot(*spot, now, peers); ok {
			var err error
			kh, _, err = s.KH.Fetch(now)
			if err != nil {
				s.Log.Warn("windometer fetch failed", "err", err)
			} else if kh != nil && !liveReadingFresh(now, kh.SourceAt) {
				s.Log.Warn("kh source stale; slot left empty", "reading_at", kh.SourceAt)
				// Stale data must not drive alerts either.
				kh = nil
			} else if kh != nil {
				kh.Period = models.CollectSlotPeriod(now, *spot, peers)
				if err := s.Store.InsertWind(*kh); err != nil {
					s.Log.Error("insert kh wind", "err", err)
				} else {
					res.SavedCount++
					res.WindKH = kh.Wind
					s.Log.Info("saved kh", "wind", kh.Wind, "gust", kh.Gust, "period", kh.Period)
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
