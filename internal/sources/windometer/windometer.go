package windometer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/begetproxy"
	"github.com/ben/ikite-go/internal/models"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

const khSlug = "pick-up-surf"

// KHSlug is the Windometer spot slug for Kiryat Haim.
const KHSlug = khSlug

type Client struct {
	Proxy       *begetproxy.Client
	UpstreamURL string
}

func New(proxy *begetproxy.Client, upstreamURL string) *Client {
	return &Client{
		Proxy:       proxy,
		UpstreamURL: upstreamURL,
	}
}

type liveResp struct {
	OK      bool `json:"ok"`
	Results map[string]struct {
		Angle      float64 `json:"Angle"`
		Speed      float64 `json:"Speed"`
		Gust       float64 `json:"Gust"`
		RecordedAt int64   `json:"recorded_at"`
		Stale      bool    `json:"stale"`
	} `json:"results"`
}

// ParseLive decodes a Windometer live API body into a KH wind reading.
// Period uses second precision so frequent polls keep distinct samples.
// Stale readings are returned with zero wind/gust/dir (caller may skip insert).
func ParseLive(body []byte, now time.Time) (*models.WindReading, error) {
	if len(body) > 0 && body[0] == '<' {
		return nil, fmt.Errorf("windometer: blocked (HTML response)")
	}
	var parsed liveResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode windometer: %w", err)
	}
	if !parsed.OK {
		return nil, fmt.Errorf("windometer: ok=false")
	}
	spot, ok := parsed.Results[khSlug]
	if !ok {
		return nil, fmt.Errorf("windometer: missing slug %q", khSlug)
	}

	wind, gust, dir := spot.Speed, spot.Gust, spot.Angle
	if spot.Stale {
		wind, gust, dir = 0, 0, 0
	}

	period := now.Truncate(time.Second)
	var sourceAt time.Time
	if spot.RecordedAt > 0 {
		period = time.Unix(spot.RecordedAt, 0).In(now.Location()).Truncate(time.Second)
		sourceAt = period
	}
	// A reading the source itself calls stale is not a reading: leave SourceAt
	// zero so the collector keeps it out of the table instead of storing 0 kt.
	if spot.Stale {
		sourceAt = time.Time{}
	}

	return &models.WindReading{
		Period:   period,
		Location: "kh",
		Wind:     wind,
		Gust:     gust,
		WindDir:  dir,
		SourceAt: sourceAt,
	}, nil
}

// MarshalLive encodes a KH wind reading as a Windometer live API JSON body.
func MarshalLive(r models.WindReading) ([]byte, error) {
	stale := r.Wind == 0 && r.Gust == 0
	payload := liveResp{
		OK: true,
		Results: map[string]struct {
			Angle      float64 `json:"Angle"`
			Speed      float64 `json:"Speed"`
			Gust       float64 `json:"Gust"`
			RecordedAt int64   `json:"recorded_at"`
			Stale      bool    `json:"stale"`
		}{
			khSlug: {
				Angle:      r.WindDir,
				Speed:      r.Wind,
				Gust:       r.Gust,
				RecordedAt: r.Period.Unix(),
				Stale:      stale,
			},
		},
	}
	return json.Marshal(payload)
}

// Fetch loads the current KH reading from windometer live API (via Beget proxy).
func (c *Client) Fetch(now time.Time) (*models.WindReading, string, error) {
	fetchURL := c.UpstreamURL
	sep := "?"
	if strings.Contains(fetchURL, "?") {
		sep = "&"
	}
	fetchURL += sep + "v=" + strconv.FormatInt(now.UnixMilli(), 10)

	body, err := c.Proxy.Get(fetchURL, map[string]string{
		"user-agent": userAgent,
		"accept":     "application/json, */*",
	})
	if err != nil {
		return nil, string(body), err
	}
	reading, err := ParseLive(body, now)
	if err != nil {
		return nil, string(body), err
	}
	return reading, string(body), nil
}
