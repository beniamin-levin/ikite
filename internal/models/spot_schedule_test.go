package models

import (
	"testing"
	"time"
)

func TestSpotShouldCollectAt(t *testing.T) {
	sp := Spot{
		Collect:            true,
		CollectIntervalMin: 5,
		CollectStartHour:   8,
		CollectEndHour:     22,
	}
	loc := time.FixedZone("IST", 3*3600)
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, loc)
	last := now.Add(-6 * time.Minute)

	ok, _ := sp.ShouldCollectAt(now, last)
	if !ok {
		t.Fatal("expected collect after interval")
	}

	ok, reason := sp.ShouldCollectAt(now, now.Add(-2*time.Minute))
	if ok || reason != "interval not elapsed" {
		t.Fatalf("expected interval skip, got ok=%v reason=%q", ok, reason)
	}

	// 5‑minute interval with ~15s early fire (timer jitter / prior run delay) should still collect.
	ok, _ = sp.ShouldCollectAt(now, now.Add(-4*time.Minute-50*time.Second))
	if !ok {
		t.Fatal("expected collect within interval slack")
	}

	ok, reason = sp.ShouldCollectAt(time.Date(2026, 7, 15, 7, 0, 0, 0, loc), time.Time{})
	if ok || reason != "outside hours" {
		t.Fatalf("expected outside hours, got ok=%v reason=%q", ok, reason)
	}

	ok, reason = sp.ShouldCollectAt(time.Date(2026, 7, 15, 22, 30, 0, 0, loc), time.Time{})
	if ok || reason != "outside hours" {
		t.Fatalf("expected outside hours at stop hour, got ok=%v reason=%q", ok, reason)
	}

	ok, _ = sp.ShouldCollectAt(time.Date(2026, 7, 15, 21, 30, 0, 0, loc), time.Time{})
	if !ok {
		t.Fatal("expected collect during last allowed hour")
	}
}

func TestNormalizeCollectInterval(t *testing.T) {
	if got := NormalizeCollectInterval(15); got != 15 {
		t.Fatalf("got %d", got)
	}
	if got := NormalizeCollectInterval(99); got != 5 {
		t.Fatalf("got %d", got)
	}
}

func TestCollectMinuteSlotsAlignVisible(t *testing.T) {
	loc := time.FixedZone("IST", 3*3600)
	peers := []Spot{
		{ID: "15233", Visible: true, Collect: true, CollectIntervalMin: 5, CollectStartHour: 8, CollectEndHour: 22},
		{ID: "st", Visible: true, Collect: true, CollectIntervalMin: 5, CollectStartHour: 8, CollectEndHour: 22},
		{ID: "ky", Visible: true, Collect: true, CollectIntervalMin: 1, CollectStartHour: 8, CollectEndHour: 22},
		{ID: "hp", Visible: false, Collect: true, CollectIntervalMin: 5, CollectStartHour: 8, CollectEndHour: 22},
		{ID: "4257", Visible: false, Collect: true, CollectIntervalMin: 5, CollectStartHour: 8, CollectEndHour: 22},
	}

	at05 := time.Date(2026, 8, 11, 15, 5, 12, 0, loc)
	at06 := time.Date(2026, 8, 11, 15, 6, 12, 0, loc)
	at07 := time.Date(2026, 8, 11, 15, 7, 12, 0, loc)

	for _, id := range []string{"15233", "st", "ky"} {
		sp := spotByID(peers, id)
		if ok, reason := sp.InCollectMinuteSlot(at05, peers); !ok {
			t.Fatalf("%s should collect at :05, reason=%q", id, reason)
		}
		if ok, _ := sp.InCollectMinuteSlot(at06, peers); ok {
			t.Fatalf("%s must not collect at :06", id)
		}
		period := CollectSlotPeriod(at05, sp, peers)
		if period.Format("15:04:05") != "15:05:00" {
			t.Fatalf("%s period got %s", id, period.Format("15:04:05"))
		}
	}

	// Off-grid last reading must not block the next shared visible minute.
	st := spotByID(peers, "st")
	ok, reason := st.ShouldCollectSlot(at05, at05.Add(-2*time.Minute), peers)
	if !ok {
		t.Fatalf("visible spot should sync onto shared minute after off-grid read, reason=%q", reason)
	}
	ok, reason = st.ShouldCollectSlot(at05, at05.Truncate(time.Minute), peers)
	if ok || reason != "already collected this slot" {
		t.Fatalf("dedupe same slot: ok=%v reason=%q", ok, reason)
	}

	hp := spotByID(peers, "hp")
	other := spotByID(peers, "4257")
	if CollectMinuteOffset(hp, peers) == CollectMinuteOffset(other, peers) {
		t.Fatal("non-visible same-interval spots should get different offsets")
	}
	if ok, reason := hp.InCollectMinuteSlot(at05, peers); ok || reason != "reserved for visible spots" {
		t.Fatalf("hp at :05: ok=%v reason=%q", ok, reason)
	}

	// hp gets first non-visible offset for interval 5 → 1 → :06
	if off := CollectMinuteOffset(hp, peers); off != 1 {
		t.Fatalf("hp offset want 1 got %d", off)
	}
	if ok, _ := hp.InCollectMinuteSlot(at06, peers); !ok {
		t.Fatal("hp should collect at :06")
	}
	if ok, _ := other.InCollectMinuteSlot(at07, peers); !ok {
		t.Fatal("4257 should collect at :07")
	}
}

func spotByID(peers []Spot, id string) Spot {
	for _, p := range peers {
		if p.ID == id {
			return p
		}
	}
	panic(id)
}
