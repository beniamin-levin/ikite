package kyhistory

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// surfo publishes the Kiryat Yam station through two paths: api_wind.php (JSON,
// the whole current day) and windtable.php (HTML, roughly the last 25 minutes).
// They are not always as healthy as each other — on 2026-09-09 the JSON tip
// froze at 06:59 and stayed there for 28 hours while the HTML table is what a
// human opens to check the wind. So when the JSON stops moving, ask the HTML
// what it can see.
const (
	apiWindFile   = "api_wind.php"
	windtableFile = "windtable.php"

	// historyStaleAfter is how far behind the JSON tip may fall before the
	// fallback is worth a second request.
	historyStaleAfter = 15 * time.Minute
)

// windtableURL derives the HTML table URL from the configured JSON one, or ""
// when the configured URL is not the endpoint this swap knows about.
func windtableURL(apiURL string) string {
	base, _, _ := strings.Cut(apiURL, "?")
	if !strings.HasSuffix(base, apiWindFile) {
		return ""
	}
	return strings.TrimSuffix(base, apiWindFile) + windtableFile
}

var (
	windtableRow    = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	windtableCell   = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)
	windtableRotate = regexp.MustCompile(`rotate\(\s*(-?[0-9.]+)deg\s*\)`)
	windtableTime   = regexp.MustCompile(`^([0-9]{1,2}):([0-9]{2})$`)
	windtableNumber = regexp.MustCompile(`-?[0-9]+(?:\.[0-9]+)?`)
	windtableTag    = regexp.MustCompile(`(?s)<[^>]*>`)
)

// FetchWindtable reads surfo's HTML table directly. Exported so the collector's
// -probe-ky flag can exercise the fallback while the JSON feed is healthy.
func (c *Client) FetchWindtable(now time.Time) ([]models.WindReading, error) {
	target := windtableURL(c.UpstreamURL)
	if target == "" {
		return nil, fmt.Errorf("ky windtable: cannot derive URL from %q", c.UpstreamURL)
	}
	body, err := c.Proxy.Get(target+"?_t="+strconv.FormatInt(now.UnixMilli(), 10), map[string]string{
		"user-agent": userAgent,
		"accept":     "text/html,*/*",
	})
	if err != nil {
		return nil, err
	}
	return parseWindtable(body, now)
}

// parseWindtable reads the HTML table into readings, newest last. Its rows carry
// only a clock time, so they are dated from now and stepped back a day when they
// would otherwise land in the future (the table spans midnight).
func parseWindtable(body []byte, now time.Time) ([]models.WindReading, error) {
	doc := string(body)
	if strings.Contains(doc, `class="stale-alert"`) {
		// surfo renders this banner itself when its own feed has stopped.
		return nil, fmt.Errorf("ky windtable: source reports stale data")
	}

	var out []models.WindReading
	for _, row := range windtableRow.FindAllStringSubmatch(doc, -1) {
		cells := windtableCell.FindAllStringSubmatch(row[1], -1)
		if len(cells) < 5 {
			continue
		}
		clock := windtableTime.FindStringSubmatch(strings.TrimSpace(cellText(cells[0][1])))
		if clock == nil {
			continue
		}
		hh, _ := strconv.Atoi(clock[1])
		mm, _ := strconv.Atoi(clock[2])
		period := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		if period.Sub(now) > 30*time.Minute {
			period = period.AddDate(0, 0, -1)
		}

		wind, ok := cellNumber(cells[3][1])
		if !ok {
			continue
		}
		gust, ok := cellNumber(cells[4][1])
		if !ok {
			continue
		}

		r := models.WindReading{
			Period:   period,
			Location: "ky",
			Wind:     wind,
			Gust:     gust,
		}
		if dir := windtableRotate.FindStringSubmatch(cells[2][1]); dir != nil {
			r.WindDir, _ = strconv.ParseFloat(dir[1], 64)
		}
		if temp, ok := cellNumber(cells[1][1]); ok {
			t := temp
			r.Temp = &t
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ky windtable: no rows")
	}

	// surfo lists newest first; the rest of the pipeline expects oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func cellText(inner string) string {
	return strings.TrimSpace(html.UnescapeString(windtableTag.ReplaceAllString(inner, "")))
}

// cellNumber pulls the first number out of a cell's text. Tags are stripped
// first so that the numbers inside style attributes stay out of it.
func cellNumber(inner string) (float64, bool) {
	m := windtableNumber.FindString(cellText(inner))
	if m == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// mergeWindtable tops up a stale JSON history with anything newer the HTML table
// has. A failure here is not an error: the JSON rows are still the best we have.
func (c *Client) mergeWindtable(rows []models.WindReading, now time.Time) []models.WindReading {
	if len(rows) == 0 {
		return rows
	}
	tip := rows[len(rows)-1].Period
	if now.Sub(tip) <= historyStaleAfter {
		return rows
	}

	extra, err := c.FetchWindtable(now)
	if err != nil {
		c.logf("ky windtable fallback failed", "err", err, "json_tip", tip)
		return rows
	}
	added := 0
	for _, r := range extra {
		if r.Period.After(tip) {
			rows = append(rows, r)
			added++
		}
	}
	c.logf("ky windtable fallback used", "json_tip", tip, "rows_added", added,
		"windtable_tip", extra[len(extra)-1].Period)
	return rows
}
