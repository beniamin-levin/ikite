// Package bestweek picks, for each of the next days, where and when the kite
// wind is best, from the ikite estimate (wind_estimate). It feeds the morning
// Telegram summary and the /best page.
package bestweek

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/store"
)

const (
	// KiteWind is the estimate's mean wind from which an hour counts as
	// kiteable on a normal kite.
	KiteWind = 12.0
	// GustyGap: gust minus mean at or above this is called gusty.
	GustyGap = 8.0

	FromHour = 8
	ToHour   = 19
)

// Group is one spot on the summary. KY and KH are one spot for wind, so their
// estimates are averaged hour by hour.
type Group struct {
	Key  string
	Name string
	Locs []string
}

// Groups are the Israeli spots the summary ranks.
var Groups = []Group{
	{Key: "kykh", Name: "Kiryat Yam / Haim", Locs: []string{"ky", "kh"}},
	{Key: "bg", Name: "Bat Galim", Locs: []string{"bg"}},
	{Key: "st", Name: "Shavei Tzion", Locs: []string{"st"}},
	{Key: "15233", Name: "Betzet", Locs: []string{"15233"}},
	{Key: "hp", Name: "Hadera", Locs: []string{"hp"}},
}

// Hour is one estimated hour for a group.
type Hour struct {
	Hour      int
	Wind      float64
	Gust      float64
	Low, High float64
	Dir       float64 // -1 when unknown
}

// SpotDay is how one group looks on one day.
type SpotDay struct {
	Group Group
	Hours []Hour
	// The longest run of consecutive kiteable hours; Start..End inclusive.
	// RunHours is 0 when no hour reaches KiteWind.
	Start, End, RunHours int
	Mean                 float64 // mean wind over the run
	// Low and High are the likely range over the run (of the peak hour
	// without one); MaxGust the highest gust in it.
	Low, High, MaxGust float64
	Peak               Hour // the windiest hour of the day (of the run, if any)
}

// Gusty reports whether the peak hour's gusts run well above the mean.
func (d SpotDay) Gusty() bool { return d.Peak.Gust-d.Peak.Wind >= GustyGap }

// Window is "13–17" for a run, empty without one. End is the start of the
// last kiteable hour, so the window runs to End+1.
func (d SpotDay) Window() string {
	if d.RunHours == 0 {
		return ""
	}
	return fmt.Sprintf("%02d–%02d", d.Start, d.End+1)
}

// Day is one calendar day.
type Day struct {
	Date  time.Time
	Spots []SpotDay // best first
}

// Best is the top spot, or nil when no group has data for the day.
func (d Day) Best() *SpotDay {
	if len(d.Spots) == 0 {
		return nil
	}
	return &d.Spots[0]
}

// Kiteable is true when the best spot has at least two kiteable hours in a row.
func (d Day) Kiteable() bool {
	b := d.Best()
	return b != nil && b.RunHours >= 2
}

// KiteBand is 0..4 from light (big kite or foil) to strong (small kite).
func KiteBand(wind float64) int {
	switch {
	case wind < 12:
		return 0
	case wind < 15:
		return 1
	case wind < 18:
		return 2
	case wind < 22:
		return 3
	default:
		return 4
	}
}

var kiteSizes = [...]string{"12–14 m / foil", "10–12 m", "9–10 m", "7–8 m", "6 m or smaller"}

// KiteSize suggests a kite for a mean wind, for an average rider.
func KiteSize(wind float64) string { return kiteSizes[KiteBand(wind)] }

// Compass names a direction FROM, in degrees.
func Compass(dir float64) string {
	if dir < 0 {
		return ""
	}
	names := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	return names[int(math.Round(math.Mod(dir, 360)/45))%8]
}

// Load reads the estimate for every group from today for `days` days.
func Load(st *store.Store, today time.Time, days int) ([]Day, error) {
	to := today.AddDate(0, 0, days)
	rows := map[string][]models.WindEstimate{}
	for _, g := range Groups {
		for _, loc := range g.Locs {
			r, err := st.ListWindEstimate(loc, today, to)
			if err != nil {
				return nil, fmt.Errorf("estimate %s: %w", loc, err)
			}
			// The hourly nowcast, when there is one, is the better word on
			// the rest of today.
			nc, err := st.ListWindNowcast(loc, today, today.AddDate(0, 0, 1))
			if err != nil {
				return nil, fmt.Errorf("nowcast %s: %w", loc, err)
			}
			rows[loc] = withNowcast(r, nc)
		}
	}
	return Plan(rows, today, days), nil
}

func withNowcast(est, nc []models.WindEstimate) []models.WindEstimate {
	if len(nc) == 0 {
		return est
	}
	byHour := map[string]models.WindEstimate{}
	for _, r := range nc {
		byHour[r.Period.Format("2006-01-02 15")] = r
	}
	out := make([]models.WindEstimate, len(est))
	for i, r := range est {
		if n, ok := byHour[r.Period.Format("2006-01-02 15")]; ok {
			r = n
		}
		out[i] = r
	}
	return out
}

// Plan ranks the groups on each day from the estimate rows by location.
func Plan(rows map[string][]models.WindEstimate, today time.Time, days int) []Day {
	var out []Day
	for i := 0; i < days; i++ {
		date := today.AddDate(0, 0, i)
		key := date.Format("2006-01-02")
		day := Day{Date: date}
		for _, g := range Groups {
			hours := groupHours(g, rows, key)
			if len(hours) == 0 {
				continue
			}
			day.Spots = append(day.Spots, scoreDay(g, hours))
		}
		sort.SliceStable(day.Spots, func(a, b int) bool {
			x, y := day.Spots[a], day.Spots[b]
			if x.RunHours != y.RunHours {
				return x.RunHours > y.RunHours
			}
			if x.RunHours > 0 && x.Mean != y.Mean {
				return x.Mean > y.Mean
			}
			return x.Peak.Wind > y.Peak.Wind
		})
		out = append(out, day)
	}
	return out
}

// groupHours averages the group's locations hour by hour for one day.
func groupHours(g Group, rows map[string][]models.WindEstimate, day string) []Hour {
	type acc struct {
		h                     Hour
		n                     int
		x, y                  float64
		dirs                  int
		wind, gust, low, high float64
	}
	byHour := map[int]*acc{}
	for _, loc := range g.Locs {
		for _, r := range rows[loc] {
			if r.Period.Format("2006-01-02") != day {
				continue
			}
			h := r.Period.Hour()
			if h < FromHour || h > ToHour || r.Period.Minute() != 0 {
				continue
			}
			a := byHour[h]
			if a == nil {
				a = &acc{h: Hour{Hour: h}}
				byHour[h] = a
			}
			a.n++
			a.wind += r.Wind
			a.gust += r.Gust
			a.low += r.Low
			a.high += r.High
			if r.Dir >= 0 {
				rad := r.Dir * math.Pi / 180
				a.x += math.Cos(rad)
				a.y += math.Sin(rad)
				a.dirs++
			}
		}
	}
	var out []Hour
	for _, a := range byHour {
		n := float64(a.n)
		h := a.h
		h.Wind, h.Gust, h.Low, h.High = a.wind/n, a.gust/n, a.low/n, a.high/n
		h.Dir = -1
		if a.dirs > 0 {
			h.Dir = math.Mod(math.Atan2(a.y, a.x)*180/math.Pi+360, 360)
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hour < out[j].Hour })
	return out
}

func scoreDay(g Group, hours []Hour) SpotDay {
	d := SpotDay{Group: g, Hours: hours}
	runStart, runLen := -1, 0
	for i, h := range hours {
		contiguous := i > 0 && hours[i-1].Hour == h.Hour-1
		if h.Wind < KiteWind {
			runLen = 0
			continue
		}
		if runLen == 0 || !contiguous {
			runStart, runLen = i, 0
		}
		runLen++
		if runLen > d.RunHours || (runLen == d.RunHours && mean(hours[runStart:i+1]) > d.Mean) {
			d.RunHours = runLen
			d.Start, d.End = hours[runStart].Hour, h.Hour
			d.Mean = mean(hours[runStart : i+1])
		}
	}
	n := 0
	for _, h := range hours {
		if d.RunHours > 0 && (h.Hour < d.Start || h.Hour > d.End) {
			continue
		}
		if h.Wind > d.Peak.Wind {
			d.Peak = h
		}
		if h.Gust > d.MaxGust {
			d.MaxGust = h.Gust
		}
		if d.RunHours > 0 {
			d.Low += h.Low
			d.High += h.High
			n++
		}
	}
	if n > 0 {
		d.Low /= float64(n)
		d.High /= float64(n)
	} else {
		d.Low, d.High = d.Peak.Low, d.Peak.High
	}
	d.Low = math.Max(0, d.Low)
	return d
}

func mean(hs []Hour) float64 {
	s := 0.0
	for _, h := range hs {
		s += h.Wind
	}
	return s / float64(len(hs))
}

// Message is the Telegram summary. siteURL is linked at the bottom.
func Message(days []Day, siteURL string) string {
	var b strings.Builder
	b.WriteString("🪁 ikite — best kite days (ikite estimate)\n")
	any := false
	for _, d := range days {
		best := d.Best()
		if best == nil {
			continue
		}
		label := d.Date.Format("Mon 02/01")
		if !d.Kiteable() {
			fmt.Fprintf(&b, "\n%s: nothing kiteable (best %s, up to %.0f kt)", label, best.Group.Name, best.Peak.Wind)
			continue
		}
		any = true
		fmt.Fprintf(&b, "\n%s: %s %s, %.0f kt", label, best.Group.Name, best.Window(), best.Mean)
		fmt.Fprintf(&b, " (up to %.0f, gust %.0f)", best.Peak.Wind, best.Peak.Gust)
		if c := Compass(best.Peak.Dir); c != "" {
			b.WriteString(" " + c)
		}
		b.WriteString(" — " + KiteSize(best.Mean))
		if best.Gusty() {
			b.WriteString(", gusty")
		}
		var also []string
		for _, o := range d.Spots[1:] {
			if o.RunHours >= 2 {
				also = append(also, fmt.Sprintf("%s %s", o.Group.Name, o.Window()))
			}
		}
		if len(also) > 0 {
			b.WriteString("\n    also " + strings.Join(also, ", "))
		}
	}
	if !any {
		b.WriteString("\n\nNo day with two kiteable hours (12+ kt) in the forecast.")
	}
	if siteURL != "" {
		b.WriteString("\n\n" + siteURL)
	}
	return b.String()
}
