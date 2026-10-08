package kyhistory

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/begetproxy"
	"github.com/ben/ikite-go/internal/models"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/50.0.2661.102 Safari/537.36"

type Client struct {
	Proxy       *begetproxy.Client
	UpstreamURL string
	// Log is optional; when set the client reports which surfo path served the
	// readings, which is the only way to tell a fallback apart from a healthy feed.
	Log *slog.Logger
}

func (c *Client) logf(msg string, args ...any) {
	if c.Log != nil {
		c.Log.Warn(msg, args...)
	}
}

func New(proxy *begetproxy.Client, upstreamURL string) *Client {
	return &Client{
		Proxy:       proxy,
		UpstreamURL: upstreamURL,
	}
}

// AlertStats is computed from the five most recent history rows (same as PHP).
type AlertStats struct {
	WindMax float64
	WindMin float64
	GustMax float64
	Temp    float64
	Msg     string
}

type apiWindResponse struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

func (c *Client) Fetch(now time.Time) ([]models.WindReading, AlertStats, error) {
	fetchURL := c.UpstreamURL
	sep := "?"
	if strings.Contains(fetchURL, "?") {
		sep = "&"
	}
	fetchURL += sep + "_t=" + strconv.FormatInt(now.UnixMilli(), 10)

	body, err := c.Proxy.Get(fetchURL, map[string]string{
		"user-agent": userAgent,
		"accept":     "*/*",
	})
	if err != nil {
		return nil, AlertStats{}, err
	}
	if len(body) > 0 && body[0] == '<' {
		return nil, AlertStats{}, fmt.Errorf("ky wind api: blocked (HTML response)")
	}

	rows, err := parseAPIWind(body, now.Location())
	if err != nil {
		return nil, AlertStats{}, err
	}
	rows = c.mergeWindtable(rows, now)

	stats := alertStatsFromRows(rows)
	return rows, stats, nil
}

func parseAPIWind(body []byte, loc *time.Location) ([]models.WindReading, error) {
	var parsed apiWindResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode ky wind api: %w", err)
	}
	if len(parsed.Rows) == 0 {
		return nil, fmt.Errorf("ky wind api: no rows")
	}

	out := make([]models.WindReading, 0, len(parsed.Rows))
	for _, row := range parsed.Rows {
		if len(row) < 8 {
			continue
		}
		period, err := parseAPIPeriod(row[0], row[1], loc)
		if err != nil {
			continue
		}
		dir, _ := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		wind, _ := strconv.ParseFloat(strings.TrimSpace(row[3]), 64)
		gust, _ := strconv.ParseFloat(strings.TrimSpace(row[4]), 64)
		temp, _ := strconv.ParseFloat(strings.TrimSpace(row[5]), 64)
		humidity, _ := strconv.ParseFloat(strings.TrimSpace(row[6]), 64)
		pressure, _ := strconv.ParseFloat(strings.TrimSpace(row[7]), 64)

		t := temp
		h := humidity
		p := pressure
		out = append(out, models.WindReading{
			Period:   period,
			Location: "ky",
			Wind:     wind,
			Gust:     gust,
			WindDir:  dir,
			Temp:     &t,
			Humidity: &h,
			Pressure: &p,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ky wind api: no valid rows")
	}
	return out, nil
}

func parseAPIPeriod(dateStr, timeStr string, loc *time.Location) (time.Time, error) {
	dateStr = strings.ReplaceAll(strings.TrimSpace(dateStr), `\`, "/")
	timeStr = strings.TrimSpace(timeStr)
	if len(timeStr) == 5 {
		timeStr += ":00"
	}
	return time.ParseInLocation("02/01/2006 15:04:05", dateStr+" "+timeStr, loc)
}

func alertStatsFromRows(rows []models.WindReading) AlertStats {
	stats := AlertStats{WindMin: 99}
	start := 0
	if len(rows) > 5 {
		start = len(rows) - 5
	}
	recent := rows[start:]
	if len(recent) == 0 {
		return stats
	}
	if recent[len(recent)-1].Temp != nil {
		stats.Temp = *recent[len(recent)-1].Temp
	}
	for _, r := range recent {
		if r.Wind > stats.WindMax {
			stats.WindMax = r.Wind
		}
		if r.Wind < stats.WindMin {
			stats.WindMin = r.Wind
		}
		if r.Gust > stats.GustMax {
			stats.GustMax = r.Gust
		}
	}
	stats.Msg = fmt.Sprintf("%.0f - %.0f, %.0fC", stats.WindMin, stats.GustMax, stats.Temp)
	return stats
}
