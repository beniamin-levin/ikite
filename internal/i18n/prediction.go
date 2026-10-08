package i18n

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/prediction"
)

var (
	rePeak      = regexp.MustCompile(`(\d+)–(\d+) kt sustained, gusts (\d+)–(\d+) kt`)
	reCurrent   = regexp.MustCompile(`^([\d.]+) kt, gusts ([\d.]+) kt`)
	reHumidity  = regexp.MustCompile(`, ([\d.]+)% humidity`)
	reTemp      = regexp.MustCompile(`, ([\d.]+)°C`)
	rePressure  = regexp.MustCompile(`, ([\d.]+) hPa`)
	reDir       = regexp.MustCompile(`^(\d+)–(\d+)° \(([^)]+)\)`)
	reMatchPeak = regexp.MustCompile(`^(\d{2} \w{3}) ([\d.]+)% match peaked (\d{2}):(\d{2}) at ([\d.]+)/([\d.]+) kt`)
)

// FormatForecastDay returns a short day label for forecast tables.
func FormatForecastDay(p time.Time, lang string) string {
	if lang == LangHE {
		names := []string{"א'", "ב'", "ג'", "ד'", "ה'", "ו'", "ש'"}
		return names[int(p.Weekday())] + " " + fmt.Sprint(p.Day())
	}
	return p.Format("Mon 2")
}

// IntervalLabel returns a localized interval label for settings.
func IntervalLabel(minutes int, lang string) string {
	tr := &Translator{Lang: lang}
	if minutes == 60 {
		return tr.T("settings.hour")
	}
	return tr.T("settings.min", minutes)
}

// LocalizePrediction copies and translates user-visible prediction API fields.
func LocalizePrediction(res *prediction.Result, lang string) *prediction.Result {
	if res == nil || lang != LangHE {
		return res
	}
	tr := &Translator{Lang: lang}
	out := *res
	out.ExpectedPeak = localizePeak(res.ExpectedPeak, tr)
	out.Direction = localizeDirection(res.Direction, tr)
	out.Conditions = localizeConditions(res.Conditions, tr)
	out.Current = localizeCurrent(res.Current, tr)
	if res.History != nil {
		h := *res.History
		h.Summary = localizeHistorySummary(res.History.Summary, tr)
		out.History = &h
	}
	if res.OpenWRF != nil {
		w := *res.OpenWRF
		w.ExpectedPeak = localizePeak(res.OpenWRF.ExpectedPeak, tr)
		w.Direction = localizeDirection(res.OpenWRF.Direction, tr)
		out.OpenWRF = &w
	}
	return &out
}

func localizePeak(s string, tr *Translator) string {
	m := rePeak.FindStringSubmatch(s)
	if m == nil {
		return strings.ReplaceAll(strings.ReplaceAll(s, "kt sustained", "קשר רוח"), "gusts", "משבים")
	}
	return tr.T("pred.peak_fmt", m[1], m[2], m[3], m[4])
}

func localizeDirection(s string, tr *Translator) string {
	m := reDir.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	return tr.T("pred.dir_fmt", m[1], m[2], localizeCompass(m[3], tr))
}

func localizeCompass(s string, tr *Translator) string {
	key := "compass." + strings.ToLower(strings.ReplaceAll(s, "-", "_"))
	if t := tr.T(key); t != key {
		return t
	}
	return s
}

func localizeCurrent(s string, tr *Translator) string {
	if s == "" {
		return s
	}
	m := reCurrent.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	out := tr.T("pred.current_fmt", m[1], m[2])
	if m2 := reTemp.FindStringSubmatch(s); len(m2) > 1 {
		out += tr.T("pred.temp_fmt", m2[1])
	}
	if m2 := reHumidity.FindStringSubmatch(s); len(m2) > 1 {
		out += tr.T("pred.humidity_fmt", m2[1])
	}
	if m2 := rePressure.FindStringSubmatch(s); len(m2) > 1 {
		out += tr.T("pred.pressure_fmt", m2[1])
	}
	return out
}

func localizeConditions(s string, tr *Translator) string {
	if s == "" {
		return s
	}
	if s == "no KY readings for today yet; using Jul–Aug climatology baseline" {
		return tr.T("pred.no_readings")
	}
	if strings.HasPrefix(s, "no close Jul–Aug matches yet") {
		parts := strings.Split(s, "(comparing hours ")
		if len(parts) == 2 {
			rest := strings.TrimSuffix(parts[1], "); using summer baseline")
			hrs := strings.Split(rest, "–")
			if len(hrs) == 2 {
				return tr.T("pred.no_matches", strings.TrimSpace(hrs[0]), strings.TrimSpace(hrs[1]))
			}
		}
	}
	if strings.Contains(s, "similar good-wind summer days") {
		return localizeSimilarDays(s, tr)
	}
	return s
}

func localizeSimilarDays(s string, tr *Translator) string {
	parts := strings.Split(s, "; ")
	if len(parts) == 0 {
		return s
	}
	head := parts[0]
	if i := strings.Index(head, " similar good-wind summer days"); i > 0 {
		n := head[:i]
		hrs := ""
		if j := strings.Index(head, "(hours "); j >= 0 {
			hrs = strings.TrimSuffix(head[j+8:], ")")
		}
		out := tr.T("pred.similar_days", n, hrs)
		for _, p := range parts[1:] {
			out += "; " + localizeMatchPeak(p, tr)
		}
		return out
	}
	return s
}

func localizeMatchPeak(s string, tr *Translator) string {
	m := reMatchPeak.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	return tr.T("pred.match_peak", m[1], m[2], m[3], m[4], m[5], m[6])
}

func localizeHistorySummary(s string, tr *Translator) string {
	re := regexp.MustCompile(`^(\d+) past predictions: peak wind off by ([\d.]+) kt, peak hour off by ([\d.]+) h, ([\d.]+)% peaks in window, ([\d.]+)% good-window overlap$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	return tr.T("pred.history_summary", m[1], m[2], m[3], m[4], m[5])
}

func initPredictionMessages() {
	heMessages["pred.peak_fmt"] = "%s–%s קשר רוח, משבים %s–%s קשר"
	heMessages["pred.dir_fmt"] = "%s–%s° (%s)"
	heMessages["pred.current_fmt"] = "%s קשר, משבים %s קשר"
	heMessages["pred.temp_fmt"] = ", %s°C"
	heMessages["pred.humidity_fmt"] = ", %s%% לחות"
	heMessages["pred.pressure_fmt"] = ", %s hPa"
	heMessages["pred.no_readings"] = "אין עדיין מדידות KY להיום; משתמש בבסיס אקלים יולי–אוג'"
	heMessages["pred.no_matches"] = "אין עדיין התאמות קרובות ליולי–אוג' (השוואת שעות %s–%s); משתמש בבסיס קיץ"
	heMessages["pred.similar_days"] = "%s ימי קיץ דומים עם רוח טובה (שעות %s)"
	heMessages["pred.match_peak"] = "%s התאמה %s%% בשיא %s:%s ב-%s/%s קשר"
	heMessages["pred.history_summary"] = "%s תחזיות בעבר: סטיית שיא רוח %s קשר, סטיית שעת שיא %s ש', %s%% שיאים בחלון, %s%% חפיפת חלון טוב"

	enMessages["pred.peak_fmt"] = "%s–%s kt sustained, gusts %s–%s kt"
	enMessages["pred.dir_fmt"] = "%s–%s° (%s)"
	enMessages["pred.current_fmt"] = "%s kt, gusts %s kt"
	enMessages["pred.temp_fmt"] = ", %s°C"
	enMessages["pred.humidity_fmt"] = ", %s%% humidity"
	enMessages["pred.pressure_fmt"] = ", %s hPa"
	enMessages["pred.no_readings"] = "no KY readings for today yet; using Jul–Aug climatology baseline"
	enMessages["pred.no_matches"] = "no close Jul–Aug matches yet (comparing hours %s–%s); using summer baseline"
	enMessages["pred.similar_days"] = "%s similar good-wind summer days (hours %s)"
	enMessages["pred.match_peak"] = "%s %s%% match peaked %s:%s at %s/%s kt"
	enMessages["pred.history_summary"] = "%s past predictions: peak wind off by %s kt, peak hour off by %s h, %s%% peaks in window, %s%% good-window overlap"

	for k, v := range map[string]string{
		"compass.wsw":  "מערב-דרום מערב",
		"compass.sw":   "דרום מערב",
		"compass.w":    "מערב",
		"compass.se":   "דרום מזרח",
		"compass.w_sw": "מערב-דרום מערב",
	} {
		heMessages[k] = v
	}
}
