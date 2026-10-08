package collector

import (
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
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

func TestKYStorePeriod(t *testing.T) {
	now := time.Date(2026, 8, 12, 11, 35, 0, 0, time.FixedZone("IST", 3*3600))
	slot := time.Date(2026, 8, 12, 11, 35, 0, 0, now.Location())

	freshSrc := now.Add(-2 * time.Minute)
	got, pinned := kyStorePeriod(freshSrc, slot, now)
	if !pinned || !got.Equal(slot) {
		t.Fatalf("fresh: got %v pinned=%v", got, pinned)
	}

	// 2026-10-02: surfo froze at 14:30. Collected at 14:35:05 the tip is one slot
	// old — it was already shown at 14:30 and must not be copied into 14:35.
	// The old 45-minute rule did exactly that, eight times over.
	stuck := now.Add(-5*time.Minute - 5*time.Second)
	got, pinned = kyStorePeriod(stuck, slot, now)
	if pinned || !got.Equal(stuck) {
		t.Fatalf("stuck one slot: got %v pinned=%v, want source time kept", got, pinned)
	}
	got, pinned = kyStorePeriod(now.Add(-40*time.Minute), slot, now)
	if pinned {
		t.Fatalf("40 minutes stuck must not be pinned as live, got %v", got)
	}

	staleSrc := time.Date(2026, 7, 27, 11, 58, 0, 0, now.Location())
	got, pinned = kyStorePeriod(staleSrc, slot, now)
	if pinned || !got.Equal(staleSrc) {
		t.Fatalf("stale: got %v pinned=%v want source kept", got, pinned)
	}
}

func TestLiveReadingFresh(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 10, 4, 0, time.UTC)
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"no timestamp", time.Time{}, false},
		{"just taken", now.Add(-30 * time.Second), true},
		{"within the slot", now.Add(-4*time.Minute - 59*time.Second), true},
		{"exactly one slot old", now.Add(-5 * time.Minute), false},
		{"stuck 40 min", now.Add(-40 * time.Minute), false},
		{"slight clock skew ahead", now.Add(time.Minute), true},
		{"far in the future", now.Add(10 * time.Minute), false},
	}
	for _, c := range cases {
		if got := liveReadingFresh(now, c.at); got != c.want {
			t.Errorf("%s: liveReadingFresh = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMergeMissingModelsBorrowsOnlyWithheldModels(t *testing.T) {
	w := func(v float64) *float64 { return &v }
	own := []models.WindForecastRow{{IDModel: 3, Model: "gfs", Wind: w(10)}, {IDModel: 45, Model: "icon13", Wind: w(11)}}
	hires := []models.WindForecastRow{
		{IDModel: 3, Model: "gfs", Wind: w(99)}, // the spot's own GFS wins
		{IDModel: 923, Model: "wrf1", Wind: w(15)},
		{IDModel: 923, Model: "wrf1", Wind: w(16)},
		{IDModel: 119, Model: "zephr", Wind: w(14)},
	}
	rows, added := mergeMissingModels(own, hires)
	if added != 2 || len(rows) != 5 {
		t.Fatalf("added %d models, %d rows; want 2 models, 5 rows", added, len(rows))
	}
	for _, r := range rows {
		if r.IDModel == 3 && *r.Wind != 10 {
			t.Fatal("hires GFS replaced the spot's own GFS")
		}
	}
}
