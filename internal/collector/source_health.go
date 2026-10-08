package collector

import (
	"fmt"
	"time"
)

// A frozen upstream is silent. The collector keeps re-saving the same rows, the
// column on the site simply stops growing, and the only WARN goes to the
// journal where nobody is looking. surfo's Kiryat Yam feed froze on 2026-09-09
// at 06:59 and did not move again until 11:16 the next day; the outage was
// found by hand-comparing the site against surfo's own page. Say something.
const (
	// sourceStaleAfter is how far behind an upstream tip may fall before the
	// feed counts as broken rather than merely quiet. Comfortably above the
	// 5-minute collect slot and any single missed run.
	sourceStaleAfter = 30 * time.Minute

	// sourceStaleRepeat throttles the reminder while a feed stays broken, so a
	// multi-day outage costs a handful of messages rather than one per run.
	sourceStaleRepeat = 4 * time.Hour
)

func sourceStaleKey(source string) string {
	return "source_stale_notified_" + source
}

// reportSourceHealth notifies once when a feed freezes, repeats sparingly while
// it stays frozen, and notifies once when it recovers. A zero tip means the
// source gave us nothing at all, which is treated as stale.
func (s *Service) reportSourceHealth(source string, tip, now time.Time) {
	key := sourceStaleKey(source)
	raw, err := s.Store.GetSetting(key)
	if err != nil {
		s.Log.Warn("source health setting", "source", source, "err", err)
		return
	}
	var notifiedAt time.Time
	if raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			notifiedAt = t
		}
	}
	alarmed := raw != ""
	stale := tip.IsZero() || now.Sub(tip) > sourceStaleAfter

	switch {
	case stale && (!alarmed || now.Sub(notifiedAt) >= sourceStaleRepeat):
		s.Log.Warn("source stale", "source", source, "tip", tip,
			"age_min", int(now.Sub(tip).Minutes()), "repeat", alarmed)
		s.notifySource(sourceStaleMessage(source, tip, now))
		if err := s.Store.SetSetting(key, now.UTC().Format(time.RFC3339)); err != nil {
			s.Log.Warn("persist source stale state", "source", source, "err", err)
		}
	case !stale && alarmed:
		s.Log.Info("source recovered", "source", source, "tip", tip)
		s.notifySource(fmt.Sprintf("✅ ikite: %s feed is back — latest reading %s.",
			source, tip.Format("02/01 15:04")))
		if err := s.Store.SetSetting(key, ""); err != nil {
			s.Log.Warn("clear source stale state", "source", source, "err", err)
		}
	}
}

func sourceStaleMessage(source string, tip, now time.Time) string {
	if tip.IsZero() {
		return fmt.Sprintf("⚠️ ikite: %s feed returned nothing — no new readings are being stored.", source)
	}
	age := now.Sub(tip)
	return fmt.Sprintf("⚠️ ikite: %s feed is stuck at %s (%dh%02dm ago) — no new readings are being stored.",
		source, tip.Format("02/01 15:04"), int(age.Hours()), int(age.Minutes())%60)
}

func (s *Service) notifySource(msg string) {
	if s.Telegram == nil || !s.Telegram.Enabled() {
		return
	}
	if err := s.Telegram.Send(msg); err != nil {
		s.Log.Error("source health telegram failed", "err", err)
	}
}
