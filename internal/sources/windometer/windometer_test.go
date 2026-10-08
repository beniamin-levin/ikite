package windometer

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/begetproxy"
	"github.com/ben/ikite-go/internal/models"
)

func TestFetch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("slugs") != khSlug {
			t.Fatalf("slugs: %s", r.URL.Query().Get("slugs"))
		}
		_, _ = w.Write([]byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"recorded_at":1784190067,"stale":false}}}`))
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"recorded_at":1784190067,"stale":false}}}`))
	}))
	defer proxy.Close()

	client := New(begetproxy.New(proxy.URL, "test"), upstream.URL+"?slugs="+khSlug)
	now := time.Unix(1784190067, 0).In(time.UTC)
	reading, _, err := client.Fetch(now)
	if err != nil {
		t.Fatal(err)
	}
	if reading.Location != "kh" || reading.Wind != 11 || reading.Gust != 12 || reading.WindDir != 250 {
		t.Fatalf("reading: %+v", reading)
	}
	if !reading.Period.Equal(time.Unix(1784190067, 0).UTC()) {
		t.Fatalf("period: %v", reading.Period)
	}
}

func TestParseLive(t *testing.T) {
	body := []byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"recorded_at":1784190067,"stale":false}}}`)
	now := time.Unix(1784190067, 0).UTC()
	reading, err := ParseLive(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if reading.Location != "kh" || reading.Wind != 11 || reading.Gust != 12 {
		t.Fatalf("reading: %+v", reading)
	}
}

func TestMarshalLive(t *testing.T) {
	now := time.Unix(1784190067, 0).UTC()
	body, err := MarshalLive(models.WindReading{
		Period:  now,
		Wind:    11,
		Gust:    12,
		WindDir: 250,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseLive(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Wind != 11 || parsed.Gust != 12 || parsed.WindDir != 250 {
		t.Fatalf("parsed: %+v", parsed)
	}
}

func TestFetchStale(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":90,"Speed":8,"Gust":10,"stale":true}}}`))
	}))
	defer proxy.Close()

	client := New(begetproxy.New(proxy.URL, "test"), "http://example/live?slugs="+khSlug)
	reading, _, err := client.Fetch(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if reading.Wind != 0 || reading.Gust != 0 || reading.WindDir != 0 {
		t.Fatalf("stale should zero reading: %+v", reading)
	}
}

func TestParseLiveSourceTime(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 10, 0, 0, time.UTC)
	fresh := []byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"recorded_at":1790943000,"stale":false}}}`)
	r, err := ParseLive(fresh, now)
	if err != nil {
		t.Fatal(err)
	}
	if !r.SourceAt.Equal(time.Unix(1790943000, 0)) {
		t.Fatalf("SourceAt = %v, want recorded_at", r.SourceAt)
	}

	// The source itself says the reading is stale: no usable reading time, so
	// the collector leaves the slot empty instead of storing 0 kt.
	stale := []byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"recorded_at":1790943000,"stale":true}}}`)
	r, err = ParseLive(stale, now)
	if err != nil {
		t.Fatal(err)
	}
	if !r.SourceAt.IsZero() {
		t.Fatalf("stale reading has SourceAt %v, want zero", r.SourceAt)
	}

	// No recorded_at at all: Period falls back to now for display, but SourceAt
	// must stay zero — "now" is not when the meter measured anything.
	untimed := []byte(`{"ok":true,"results":{"pick-up-surf":{"Angle":250,"Speed":11,"Gust":12,"stale":false}}}`)
	if r, err = ParseLive(untimed, now); err != nil || !r.SourceAt.IsZero() {
		t.Fatalf("untimed: SourceAt = %v err=%v, want zero", r.SourceAt, err)
	}
}
