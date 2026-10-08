package ims

import (
	"testing"
	"time"
)

func TestParseIMSTimeReadsDigitsAsStandardTime(t *testing.T) {
	jerusalem, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	// Summer: IMS labels standard-time digits +03:00; the reading is really 15:10 IDT.
	got, err := parseIMSTime("2026-08-07T14:10:00+03:00")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 8, 7, 12, 10, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("summer: got %s want %s", got.UTC(), want)
	}
	if display := got.In(jerusalem).Format("15:04"); display != "15:10" {
		t.Fatalf("summer Israel display: got %s want 15:10", display)
	}

	// Winter: the offset is already +02:00, so nothing moves.
	got, err = parseIMSTime("2026-01-15T14:10:00+02:00")
	if err != nil {
		t.Fatal(err)
	}
	if display := got.In(jerusalem).Format("15:04"); display != "14:10" {
		t.Fatalf("winter Israel display: got %s want 14:10", display)
	}

	// No offset at all: same standard-time reading.
	got, err = parseIMSTime("2026-08-07 14:10")
	if err != nil {
		t.Fatal(err)
	}
	if display := got.In(jerusalem).Format("15:04"); display != "15:10" {
		t.Fatalf("no-offset display: got %s want 15:10", display)
	}
}

func TestPickIMSDirectionSkipsZero(t *testing.T) {
	zero := 0.0
	mean := 264.0
	if d := pickIMSDirection(&zero, &zero); d != nil {
		t.Fatal("expected nil for zero dirs")
	}
	if d := pickIMSDirection(&zero, &mean); d == nil || *d != 264 {
		t.Fatalf("want max fallback 264, got %v", d)
	}
	if d := pickIMSDirection(&mean, &zero); d == nil || *d != 264 {
		t.Fatalf("want mean 264, got %v", d)
	}
}
