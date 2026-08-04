package windguru

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// Canonical Windguru model metadata for micro.windguru.cz output.
type microModel struct {
	ID   int
	Name string
}

var microModelByPrefix = []struct {
	prefix string
	meta   microModel
}{
	{"GFS 13 km", microModel{ID: 3, Name: "gfs"}},
	{"IFS-HRES 9 km", microModel{ID: 117, Name: "ifs"}},
	{"ICON 7 km", microModel{ID: 43, Name: "icon7"}},
	{"ICON 13 km", microModel{ID: 45, Name: "icon13"}},
	{"GDPS 15 km", microModel{ID: 59, Name: "gdps"}},
	{"WRF 3 km", microModel{ID: 23, Name: "wrf3"}},
	{"WRF 9 km", microModel{ID: 42, Name: "wrf9"}},
	{"NAM 12 km", microModel{ID: 7, Name: "nam"}},
	{"HARMONIE 5 km", microModel{ID: 48, Name: "harmonie"}},
}

var (
	microModelHeader = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 /.+_-]+?) \(init:`)
	microRow         = regexp.MustCompile(`^\s+\w{3}\s+(\d+)\.\s+(\d{2})h\s+(\d+|-)\s+(\d+|-)\s+\S+\s+(\d+)(?:\s+(\d+|-))?`)
)

// FetchSpotForecastsMicro downloads all available models from micro.windguru.cz.
// PRO-only model sections without numeric rows are skipped.
func (c *ForecastClient) FetchSpotForecastsMicro(spotID int, forecastDate time.Time, loc *time.Location) ([]models.WindForecastRow, error) {
	url := fmt.Sprintf("https://micro.windguru.cz/?s=%d&m=all", spotID)
	body, err := c.fetchMicro(url)
	if err != nil {
		return nil, err
	}
	rows, err := ParseMicroForecast(string(body), spotID, forecastDate, loc)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("micro forecast spot %d: no rows for %s", spotID, forecastDate.Format("2006-01-02"))
	}
	return rows, nil
}

func (c *ForecastClient) fetchMicro(url string) ([]byte, error) {
	if c.Proxy != nil && c.Proxy.URL != "" {
		body, err := c.Proxy.Get(url, map[string]string{
			"Accept":     "text/html,text/plain,*/*",
			"User-Agent": "Mozilla/5.0 (compatible; ikite-go/1.0)",
		})
		if err == nil && len(body) > 0 {
			snippet := string(body)
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			if !strings.Contains(snippet, "Access Denied") {
				return body, nil
			}
		}
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("micro forecast: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("micro forecast: HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

// ParseMicroForecast parses micro.windguru.cz plain-text multi-model output.
func ParseMicroForecast(text string, spotID int, forecastDate time.Time, loc *time.Location) ([]models.WindForecastRow, error) {
	if loc == nil {
		return nil, fmt.Errorf("timezone is required")
	}
	day := dateOnly(forecastDate.In(loc))
	year := day.Year()
	month := day.Month()

	var out []models.WindForecastRow
	var cur *microModel
	blocked := false

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.Contains(line, "only available to Windguru PRO") {
			blocked = true
			continue
		}
		if m := microModelHeader.FindStringSubmatch(trim); m != nil {
			meta, ok := lookupMicroModel(m[1])
			if ok {
				cur = &meta
				blocked = false
			} else {
				cur = nil
				blocked = false
			}
			continue
		}
		if cur == nil || blocked {
			continue
		}
		rm := microRow.FindStringSubmatch(line)
		if rm == nil {
			continue
		}
		dom, _ := strconv.Atoi(rm[1])
		hour, _ := strconv.Atoi(rm[2])
		wind, okWind := parseMicroNum(rm[3])
		gust, okGust := parseMicroNum(rm[4])
		dir, _ := strconv.ParseFloat(rm[5], 64)
		if !okWind {
			continue
		}
		if !okGust {
			gust = wind
		}
		period := time.Date(year, month, dom, hour, 0, 0, 0, loc)
		// Handle month rollover near month boundaries.
		if period.Before(day.AddDate(0, 0, -1)) {
			period = time.Date(year, month+1, dom, hour, 0, 0, 0, loc)
		}
		if !sameCalendarDay(period, day) {
			continue
		}
		w, g, d := wind, gust, dir
		var temp *float64
		if len(rm) > 6 && rm[6] != "" {
			if t, ok := parseMicroNum(rm[6]); ok {
				temp = &t
			}
		}
		out = append(out, models.WindForecastRow{
			ForecastDate: day,
			WindguruID:   spotID,
			IDModel:      cur.ID,
			Model:        cur.Name,
			Period:       period,
			Wind:         &w,
			Gust:         &g,
			WindDir:      &d,
			Temp:         temp,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func lookupMicroModel(title string) (microModel, bool) {
	title = strings.TrimSpace(title)
	for _, item := range microModelByPrefix {
		if strings.HasPrefix(title, item.prefix) {
			return item.meta, true
		}
	}
	return microModel{}, false
}

func parseMicroNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// DisplayModelName normalizes stored model labels for the forecast UI.
func DisplayModelName(idModel int, model string) string {
	model = strings.TrimSpace(model)
	switch idModel {
	case 3:
		return "gfs"
	case 43:
		return "icon7"
	case 45:
		if model == "icon" || model == "" {
			return "icon13"
		}
	case 59:
		return "gdps"
	case 117:
		return "ifs"
	case 1000001:
		return "openWRF"
	}
	if model != "" {
		return model
	}
	return fmt.Sprintf("model%d", idModel)
}
