package squall

import (
	"sort"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

const (
	// A squall at the spot: the wind reaches eventMinKt…
	eventMinKt = 16.0
	// …at least eventJumpKt above what it was blowing 10–30 minutes before.
	// A sea breeze builds over an hour; a gust front does this in minutes.
	eventJumpKt = 10.0
	// It is over once the wind has stayed under eventEndKt for eventEndFor.
	eventEndKt  = 10.0
	eventEndFor = 10 * time.Minute
	// Events closer together than this are one storm.
	eventGap = 45 * time.Minute
)

// Event is a squall measured at the spot.
type Event struct {
	Start    time.Time
	End      time.Time // zero while it is still blowing
	PeakWind float64
	PeakGust float64
	PeakAt   time.Time
	DirDeg   float64 // at the peak
	Baseline float64 // wind before it hit
}

// FindEvents scans readings (any order) for sudden strong-wind onsets.
func FindEvents(rows []models.WindReading) []Event {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Period.Before(rows[j].Period) })
	var out []Event
	var cur *Event
	var calmSince time.Time
	for i, r := range rows {
		if cur != nil {
			if r.Wind > cur.PeakWind {
				cur.PeakWind, cur.PeakAt, cur.DirDeg = r.Wind, r.Period, r.WindDir
			}
			if r.Gust > cur.PeakGust {
				cur.PeakGust = r.Gust
			}
			if r.Wind < eventEndKt {
				if calmSince.IsZero() {
					calmSince = r.Period
				}
				if r.Period.Sub(calmSince) >= eventEndFor {
					cur.End = calmSince
					out = append(out, *cur)
					cur = nil
				}
			} else {
				calmSince = time.Time{}
			}
			continue
		}
		if r.Wind < eventMinKt {
			continue
		}
		if n := len(out); n > 0 && r.Period.Sub(out[n-1].End) < eventGap {
			continue
		}
		base, ok := baseline(rows[:i], r.Period)
		if !ok || r.Wind-base < eventJumpKt {
			continue
		}
		cur = &Event{Start: r.Period, PeakWind: r.Wind, PeakGust: r.Gust, PeakAt: r.Period, DirDeg: r.WindDir, Baseline: base}
		calmSince = time.Time{}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// baseline is the mean wind 10–30 minutes before t.
func baseline(before []models.WindReading, t time.Time) (float64, bool) {
	var sum float64
	var n int
	for i := len(before) - 1; i >= 0; i-- {
		age := t.Sub(before[i].Period)
		if age > 30*time.Minute {
			break
		}
		if age >= 10*time.Minute {
			sum += before[i].Wind
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}
