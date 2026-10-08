package openskiron

import (
	"bytes"
	"compress/bzip2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/sources/openwrf"
)

const (
	ModelID   = 1_000_002
	ModelName = "skiron"

	// Stable saildocs URL always points at the latest Israel 4km openWRF (wind-only minimal).
	DefaultMinimalURL = "https://www.openskiron.org/saildocs/Israel_4km_WRF_MINIMAL.grb.bz2"
	// Index page used to discover the full WAM GRIB (includes gusts) when available.
	DefaultIndexURL = "https://www.openskiron.org/en/openwrf"
)

// ErrUnreachable reports that the openskiron host could not be reached at all —
// DNS failure, refused connection, timeout or a 5xx. openskiron.org has been
// offline since around June 2026, so callers treat this as "source unavailable,
// try again next run" rather than as a collector failure. A 404 or a malformed
// payload is NOT this error: that would mean the site is up but the feed moved,
// which is worth surfacing loudly.
var ErrUnreachable = errors.New("openskiron unreachable")

type Client struct {
	HTTP       *http.Client
	MinimalURL string
	IndexURL   string
}

func New() *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 25 * time.Second},
		MinimalURL: DefaultMinimalURL,
		IndexURL:   DefaultIndexURL,
	}
}

var israelFullRE = regexp.MustCompile(`https?://[^"' ]*Israel_4km_WRF_WAM_[0-9]{6}-[0-9]{2}\.grb\.bz2|Israel_4km_WRF_WAM_[0-9]{6}-[0-9]{2}\.grb\.bz2`)

// FetchGrib downloads and decompresses the Israel OpenSkiron/openWRF GRIB once.
func (c *Client) FetchGrib() ([]byte, error) {
	return c.downloadGRIB()
}

// RowsForSpot samples a previously downloaded GRIB at the spot coordinates.
func RowsForSpot(raw []byte, sp models.Spot, loc *time.Location) ([]models.WindForecastRow, error) {
	if !sp.HasCoords() {
		return nil, fmt.Errorf("spot %s: missing lat/lon", sp.ID)
	}
	if loc == nil {
		loc = time.UTC
	}
	series, err := ExtractPoint(raw, *sp.Lat, *sp.Lon)
	if err != nil {
		return nil, err
	}
	key := openwrf.ForecastKey(sp)
	out := make([]models.WindForecastRow, 0, len(series))
	for _, s := range series {
		period := s.Time.In(loc)
		day := time.Date(period.Year(), period.Month(), period.Day(), 0, 0, 0, 0, loc)
		w := msToKnots(s.WindMS)
		g := msToKnots(s.GustMS)
		if g < w {
			g = w
		}
		dir := s.DirDeg
		out = append(out, models.WindForecastRow{
			ForecastDate: day,
			Location:     sp.ID,
			WindguruID:   key,
			IDModel:      ModelID,
			Model:        ModelName,
			Period:       period,
			Wind:         &w,
			Gust:         &g,
			WindDir:      &dir,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("openskiron: no samples at %.4f,%.4f", *sp.Lat, *sp.Lon)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Period.Before(out[j].Period) })
	return out, nil
}

// FetchSpot downloads the Israel GRIB and samples the spot.
func (c *Client) FetchSpot(sp models.Spot, loc *time.Location) ([]models.WindForecastRow, error) {
	raw, err := c.FetchGrib()
	if err != nil {
		return nil, err
	}
	return RowsForSpot(raw, sp, loc)
}

func (c *Client) downloadGRIB() ([]byte, error) {
	url := c.resolveURL()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ikite-go/1.0 (non-commercial)")
	req.Header.Set("Accept", "application/octet-stream,*/*")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnreachable, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: %s: HTTP %d", ErrUnreachable, url, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openskiron download: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 40<<20))
	if err != nil {
		return nil, err
	}
	if len(body) >= 2 && body[0] == 'B' && body[1] == 'Z' {
		decompressed, err := io.ReadAll(bzip2.NewReader(bytes.NewReader(body)))
		if err != nil {
			return nil, fmt.Errorf("openskiron bunzip: %w", err)
		}
		return decompressed, nil
	}
	if bytes.Contains(body[:min(16, len(body))], []byte("GRIB")) {
		return body, nil
	}
	return nil, fmt.Errorf("openskiron: unexpected payload (%d bytes)", len(body))
}

func (c *Client) resolveURL() string {
	// Prefer the stable saildocs URL (always latest). Optionally upgrade to full WAM
	// GRIB from the index when that host is reachable quickly.
	if c.IndexURL != "" {
		if u := c.latestFullURL(); u != "" {
			return u
		}
	}
	if c.MinimalURL != "" {
		return c.MinimalURL
	}
	return DefaultMinimalURL
}

func (c *Client) latestFullURL() string {
	req, err := http.NewRequest(http.MethodGet, c.IndexURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "ikite-go/1.0 (non-commercial)")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	matches := israelFullRE.FindAllString(string(body), -1)
	if len(matches) == 0 {
		return ""
	}
	best := matches[len(matches)-1]
	if len(best) < 4 || best[:4] != "http" {
		best = "https://www.openskiron.org/en/" + best
	}
	return best
}

func msToKnots(ms float64) float64 {
	return math.Round(ms*1.943844*10) / 10
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
