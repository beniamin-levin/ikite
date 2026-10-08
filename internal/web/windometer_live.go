package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/sources/windometer"
	"github.com/ben/ikite-go/internal/store"
)

const (
	windometerDefaultLiveURL = "https://windometer.info/api/spots/live.php?slugs=pick-up-surf"
	windometerLiveFreshness  = 10 * time.Second
	windometerLiveKH         = "kh"
)

func windometerLiveIsFresh(sampleAt, now time.Time) bool {
	if sampleAt.IsZero() {
		return false
	}
	return !now.Before(sampleAt) && now.Sub(sampleAt) <= windometerLiveFreshness
}

func (s *Server) windometerLiveFromMemory(now time.Time) ([]byte, bool) {
	s.windometerLiveMu.RLock()
	defer s.windometerLiveMu.RUnlock()
	if len(s.windometerLiveBody) == 0 || !windometerLiveIsFresh(s.windometerLiveAt, now) {
		return nil, false
	}
	body := make([]byte, len(s.windometerLiveBody))
	copy(body, s.windometerLiveBody)
	return body, true
}

func (s *Server) windometerLiveRemember(body []byte, fetchedAt time.Time) {
	s.windometerLiveMu.Lock()
	defer s.windometerLiveMu.Unlock()
	s.windometerLiveBody = append([]byte(nil), body...)
	s.windometerLiveAt = fetchedAt
}

func (s *Server) windometerLiveFromDB(now time.Time) ([]byte, bool) {
	if s.Store == nil {
		return nil, false
	}
	reading, err := s.Store.LatestWindReading(windometerLiveKH)
	if err != nil || reading == nil || !windometerLiveIsFresh(reading.Period, now) {
		return nil, false
	}
	body, err := windometer.MarshalLive(*reading)
	if err != nil {
		return nil, false
	}
	return body, true
}

// persistWindometerLive writes the KH live sample for the camera page.
// Runs off the request path so the proxy response is not blocked on MySQL.
func persistWindometerLive(st *store.Store, log *slog.Logger, body []byte, now time.Time) {
	if st == nil {
		return
	}
	reading, err := windometer.ParseLive(body, now)
	if err != nil {
		log.Warn("windometer live parse", "err", err)
		return
	}
	if reading == nil || (reading.Wind == 0 && reading.Gust == 0) {
		return
	}
	go func() {
		if err := st.InsertWind(*reading); err != nil {
			log.Error("windometer live insert", "err", err)
		}
	}()
}

func (s *Server) windometerLiveUpstreamURL(now time.Time) string {
	base := strings.TrimSpace(s.Cfg.WindometerLiveURL)
	if base == "" {
		base = windometerDefaultLiveURL
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "v=" + strconv.FormatInt(now.UnixMilli(), 10)
}

func (s *Server) fetchWindometerLiveUpstream(ctx context.Context, now time.Time) ([]byte, error) {
	if s.windometerLiveFetch != nil {
		return s.windometerLiveFetch(ctx, now)
	}
	fetchURL := s.windometerLiveUpstreamURL(now)

	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, */*")
	req.Header.Set("User-Agent", "ikite-go/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &windometerUpstreamError{status: resp.StatusCode}
	}
	return body, nil
}

type windometerUpstreamError struct {
	status int
}

func (e *windometerUpstreamError) Error() string {
	return "windometer upstream error"
}

func (s *Server) resolveWindometerLive(ctx context.Context, now time.Time) ([]byte, error) {
	if body, ok := s.windometerLiveFromMemory(now); ok {
		return body, nil
	}
	if body, ok := s.windometerLiveFromDB(now); ok {
		return body, nil
	}

	val, err, _ := s.windometerLiveSF.Do("live", func() (any, error) {
		now := time.Now().In(s.Cfg.Timezone)
		if body, ok := s.windometerLiveFromMemory(now); ok {
			return body, nil
		}
		if body, ok := s.windometerLiveFromDB(now); ok {
			return body, nil
		}

		body, err := s.fetchWindometerLiveUpstream(ctx, now)
		if err != nil {
			return nil, err
		}

		fetchedAt := time.Now().In(s.Cfg.Timezone)
		// Camera KH live feed persists samples; insert is async so the page stays snappy.
		persistWindometerLive(s.Store, s.Log, body, fetchedAt)
		s.windometerLiveRemember(body, fetchedAt)
		return body, nil
	})
	if err != nil {
		return nil, err
	}
	body, ok := val.([]byte)
	if !ok {
		return nil, nil
	}
	return body, nil
}

func writeWindometerLive(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// handleWindometerLiveAPI proxies the Windometer live API (browser CORS blocks direct calls).
// Used by /camera?cam=kh; persists fresh KH samples to wind_data (async).
func (s *Server) handleWindometerLiveAPI(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(s.Cfg.Timezone)
	body, err := s.resolveWindometerLive(r.Context(), now)
	if err != nil {
		var upstreamErr *windometerUpstreamError
		if errors.As(err, &upstreamErr) {
			s.Log.Error("windometer live proxy status", "status", upstreamErr.status)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		s.Log.Error("windometer live proxy", "err", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	writeWindometerLive(w, body)
}
