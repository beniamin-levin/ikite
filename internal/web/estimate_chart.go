package web

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/ben/ikite-go/internal/i18n"
	"github.com/ben/ikite-go/internal/models"
)

const (
	// The chart covers kiting hours only, like the overview grid: a day is 06–21.
	chartFromHour = 6
	chartToHour   = 21
	// "Strongest expected" looks this far ahead. Further out, the models are too
	// unsure for a single headline hour to mean much.
	strongestLookahead = 7 * 24 * time.Hour
)

// chartPoint is one hour on the expected-wind chart. Nil fields are gaps.
type chartPoint struct {
	Label    string   `json:"label"`
	Wind     *float64 `json:"wind"`
	Gust     *float64 `json:"gust"`
	Low      *float64 `json:"low"`
	High     *float64 `json:"high"`
	Dir      string   `json:"dir"`
	Models   int      `json:"models"`
	Measured *float64 `json:"measured"`
}

type chartDay struct {
	Label string `json:"label"`
	First int    `json:"first"` // index of the day's first point
	Mid   int    `json:"mid"`   // index to hang the day's label on
}

type expectedChart struct {
	Points []chartPoint `json:"points"`
	Days   []chartDay   `json:"days"`
}

// strongestHour is the headline: the windiest estimated hour coming up.
type strongestHour struct {
	When      string
	Wind      string
	Low, High string
	Gust      string
	Dir       string
}

// buildExpectedChart lays out one point per kiting hour for each day in
// [from, to] that has either an estimate or a measurement. Keys are wall-clock
// hours, matching how both tables store them.
func buildExpectedChart(est []models.WindEstimate, obs []models.ObservedHour, from, to time.Time, lang string) *expectedChart {
	if len(est) == 0 {
		return nil
	}
	byHour := make(map[string]models.WindEstimate, len(est))
	for _, e := range est {
		byHour[hourKey(e.Period)] = e
	}
	measured := make(map[string]float64, len(obs))
	for _, o := range obs {
		measured[hourKey(o.Hour)] = o.Wind
	}

	c := &expectedChart{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		// Build the day, then keep it only if it carries anything.
		var day []chartPoint
		var any bool
		dayLabel := i18n.FormatForecastDay(d, lang)
		for h := chartFromHour; h <= chartToHour; h++ {
			k := fmt.Sprintf("%s %02d", d.Format("2006-01-02"), h)
			p := chartPoint{Label: fmt.Sprintf("%s · %02d:00", dayLabel, h)}
			if e, ok := byHour[k]; ok {
				p.Wind, p.Gust = round1(e.Wind), round1(e.Gust)
				p.Low, p.High = round1(e.Low), round1(e.High)
				p.Dir = compass(e.Dir)
				p.Models = e.Models
				any = true
			}
			if m, ok := measured[k]; ok {
				p.Measured = round1(m)
				any = true
			}
			day = append(day, p)
		}
		if !any {
			continue
		}
		first := len(c.Points)
		c.Days = append(c.Days, chartDay{Label: dayLabel, First: first, Mid: first + (chartToHour-chartFromHour)/2})
		c.Points = append(c.Points, day...)
	}
	if len(c.Days) == 0 {
		return nil
	}
	return c
}

// findStrongest picks the windiest estimated hour from now to now+lookahead.
// Past hours are not a forecast any more, so they never win.
func findStrongest(est []models.WindEstimate, now time.Time, lang string) *strongestHour {
	nowKey := now.Format("2006-01-02 15")
	endKey := now.Add(strongestLookahead).Format("2006-01-02 15")
	var best *models.WindEstimate
	for i := range est {
		k := hourKey(est[i].Period)
		if k < nowKey || k > endKey {
			continue
		}
		if best == nil || est[i].Wind > best.Wind {
			best = &est[i]
		}
	}
	if best == nil {
		return nil
	}
	day := time.Date(best.Period.Year(), best.Period.Month(), best.Period.Day(), 0, 0, 0, 0, time.UTC)
	return &strongestHour{
		When: fmt.Sprintf("%s %02d:00", i18n.FormatForecastDay(day, lang), best.Period.Hour()),
		Wind: fmt.Sprintf("%.0f", best.Wind),
		Low:  fmt.Sprintf("%.0f", best.Low),
		High: fmt.Sprintf("%.0f", best.High),
		Gust: fmt.Sprintf("%.0f", best.Gust),
		Dir:  compass(best.Dir),
	}
}

func (c *expectedChart) JSON() ([]byte, error) { return json.Marshal(c) }

func round1(v float64) *float64 {
	r := math.Round(v*10) / 10
	return &r
}

var compassPoints = [...]string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE",
	"S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}

// compass names the direction the wind blows FROM; "" when unknown.
func compass(deg float64) string {
	if deg < 0 || math.IsNaN(deg) {
		return ""
	}
	i := int(math.Mod(deg+11.25, 360) / 22.5)
	return compassPoints[i%16]
}
