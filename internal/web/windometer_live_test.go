package web

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/config"
	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/sources/windometer"
)

var errBodyMismatch = errors.New("body mismatch")

func TestWindometerLiveIsFresh(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 10, 0, time.UTC)
	if !windometerLiveIsFresh(now.Add(-5*time.Second), now) {
		t.Fatal("expected 5s old sample to be fresh")
	}
	if !windometerLiveIsFresh(now.Add(-9*time.Second), now) {
		t.Fatal("expected 9s old sample to be fresh")
	}
	if windometerLiveIsFresh(now.Add(-11*time.Second), now) {
		t.Fatal("expected 11s old sample to be stale")
	}
	if windometerLiveIsFresh(time.Time{}, now) {
		t.Fatal("expected zero time to be stale")
	}
}

func TestResolveWindometerLiveMemoryCache(t *testing.T) {
	s, err := New(&config.Config{Timezone: time.UTC}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"recorded_at":1784190067,"stale":false}}}`)
	now := time.Now().UTC()
	s.windometerLiveRemember(body, now)

	var fetches int32
	s.windometerLiveFetch = func(context.Context, time.Time) ([]byte, error) {
		atomic.AddInt32(&fetches, 1)
		return nil, nil
	}

	got, err := s.resolveWindometerLive(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("body: %s", got)
	}
	if fetches != 0 {
		t.Fatalf("upstream fetches: got %d want 0", fetches)
	}
}

func TestResolveWindometerLiveSingleflight(t *testing.T) {
	s, err := New(&config.Config{Timezone: time.UTC}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"recorded_at":1784190067,"stale":false}}}`)
	var fetches int32
	s.windometerLiveFetch = func(context.Context, time.Time) ([]byte, error) {
		atomic.AddInt32(&fetches, 1)
		time.Sleep(30 * time.Millisecond)
		return body, nil
	}

	const n = 12
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)
	for range n {
		go func() {
			defer wg.Done()
			got, err := s.resolveWindometerLive(context.Background(), time.Now().UTC())
			if err != nil {
				errs <- err
				return
			}
			if string(got) != string(body) {
				errs <- errBodyMismatch
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if fetches != 1 {
		t.Fatalf("upstream fetches: got %d want 1", fetches)
	}
}

func TestResolveWindometerLiveStaleMemoryRefetches(t *testing.T) {
	s, err := New(&config.Config{Timezone: time.UTC}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"recorded_at":1784190067,"stale":false}}}`)
	now := time.Now().UTC()
	s.windometerLiveRemember(body, now.Add(-windometerLiveFreshness-time.Second))

	var fetches int32
	s.windometerLiveFetch = func(context.Context, time.Time) ([]byte, error) {
		atomic.AddInt32(&fetches, 1)
		return body, nil
	}

	_, err = s.resolveWindometerLive(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if fetches != 1 {
		t.Fatalf("upstream fetches: got %d want 1", fetches)
	}
}

func TestMarshalLiveRoundTrip(t *testing.T) {
	now := time.Unix(1784190067, 0).UTC()
	body, err := windometer.MarshalLive(models.WindReading{
		Period:  now,
		Wind:    11,
		Gust:    12,
		WindDir: 250,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := windometer.ParseLive(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Wind != 11 || parsed.Gust != 12 || parsed.WindDir != 250 {
		t.Fatalf("parsed: %+v", parsed)
	}
}
