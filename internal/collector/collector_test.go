package collector

import (
	"testing"
	"time"
)

func TestAlertReadingFresh(t *testing.T) {
	now := time.Date(2026, 7, 28, 16, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		latest time.Time
		want   bool
	}{
		{name: "zero", want: false},
		{name: "fresh", latest: now.Add(-10 * time.Minute), want: true},
		{name: "at max age", latest: now.Add(-alertReadingMaxAge), want: true},
		{name: "just stale", latest: now.Add(-alertReadingMaxAge - time.Minute), want: false},
		{name: "day old stuck", latest: now.Add(-28 * time.Hour), want: false},
		{name: "slight future skew", latest: now.Add(2 * time.Minute), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alertReadingFresh(now, tc.latest); got != tc.want {
				t.Fatalf("alertReadingFresh(%v) = %v, want %v", tc.latest, got, tc.want)
			}
		})
	}
}
