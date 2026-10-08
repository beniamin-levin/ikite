package windguru

import (
	"os"
	"testing"
	"time"
)

func TestParseMicroForecast(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	text := `Windguru forecast

GFS 13 km (init: 2026-08-02 00 UTC)

        Date    WSPD    GUST   WDIRN    WDEG     TMP
     (UTC+3)   knots   knots    dir.    deg.       C

  Sun 2. 10h      10      12      NW     320      29
  Sun 2. 11h      11      13      NW     318      30

ICON 7 km (init: 2026-08-02 03 UTC)
<b>ICON 7 km (Europe)</b> forecasts for custom spots only available to Windguru PRO subscribers...

ICON 13 km (init: 2026-08-02 00 UTC)

        Date    WSPD    GUST   WDIRN    WDEG     TMP
     (UTC+3)   knots   knots    dir.    deg.       C

  Sun 2. 10h       9      15     NNW     330      28
  Mon 3. 08h      12      18      NW     310      27
`
	day := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	rows, err := ParseMicroForecast(text, 373090, day, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4 (PRO icon7 skipped)", len(rows))
	}
	dates := map[string]int{}
	for _, r := range rows {
		dates[r.ForecastDate.Format("2006-01-02")]++
	}
	if dates["2026-08-02"] != 3 || dates["2026-08-03"] != 1 {
		t.Fatalf("unexpected dates: %v", dates)
	}
	if rows[0].Model != "gfs" || rows[0].IDModel != 3 {
		t.Fatalf("row0 model = %s/%d", rows[0].Model, rows[0].IDModel)
	}
	if *rows[0].WindDir != 320 {
		t.Fatalf("dir = %v", *rows[0].WindDir)
	}
	if rows[2].Model != "icon13" || rows[2].IDModel != 45 {
		t.Fatalf("row2 model = %s/%d", rows[2].Model, rows[2].IDModel)
	}
	if rows[3].Model != "icon13" {
		t.Fatalf("row3 model = %s/%d", rows[3].Model, rows[3].IDModel)
	}
}

func TestDisplayModelName(t *testing.T) {
	if got := DisplayModelName(45, "icon"); got != "icon13" {
		t.Fatalf("got %q", got)
	}
	if got := DisplayModelName(43, ""); got != "icon7" {
		t.Fatalf("got %q", got)
	}
}

func TestFetchMicroLive(t *testing.T) {
	if os.Getenv("WG_MICRO_LIVE_TEST") == "" {
		t.Skip("set WG_MICRO_LIVE_TEST=1 to fetch micro.windguru.cz")
	}
	loc, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	c := &ForecastClient{}
	rows, err := c.FetchSpotForecastsMicro(373090, time.Now().In(loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 20 {
		t.Fatalf("got %d rows", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Model] = true
	}
	for _, want := range []string{"gfs", "ifs", "icon13", "gdps"} {
		if !seen[want] {
			t.Fatalf("missing model %s in %v", want, seen)
		}
	}
}

// Every model section must be attributed to its own heading. WRF* 1 km and
// Zephr-HD are parsed under Windguru's own ids; a heading we do not know (here
// "Foo* 2 km") must start a skipped section, never leak its rows into the model
// above it — that once stored two series under one id_model at identical
// timestamps on Tarifa (spot 976270) and broke the wind_forecast primary key.
func TestParseMicroForecastAttributesEachSection(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		t.Fatal(err)
	}
	text := `Windguru forecast

WRF 3 km (init: 2026-09-17 18 UTC)

        Date    WSPD    GUST   WDIRN    WDEG     TMP
     (UTC+3)   knots   knots    dir.    deg.       C

  Thu 18. 00h      14      19       W     270      24
  Thu 18. 03h      12      17       W     272      23

WRF* 1 km (init: 2026-09-17 18 UTC)

        Date    WSPD    GUST   WDIRN    WDEG     TMP
     (UTC+3)   knots   knots    dir.    deg.       C

  Thu 18. 00h      21      27       W     268      24
  Thu 18. 03h      19      25       W     269      23

Foo* 2 km (init: 2026-09-17 18 UTC)

        Date    WSPD    GUST   WDIRN    WDEG     TMP
     (UTC+3)   knots   knots    dir.    deg.       C

  Thu 18. 00h      44      50       W     260      24

Zephr-HD 2.6 km (init: 2026-09-17 18 UTC)

        Date    WSPD    GUST   WDIRN    WDEG     TMP
     (UTC+3)   knots   knots    dir.    deg.       C

  Thu 18. 00h      30      40       W     260      24

WRF 9 km (init: 2026-09-17 18 UTC)
<b>WRF 9 km (Egypt)</b> forecasts for custom spots only available to Windguru PRO subscribers...
`
	day := time.Date(2026, 9, 18, 0, 0, 0, 0, loc)
	rows, err := ParseMicroForecast(text, 976270, day, loc)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		id int
		at string
	}
	want := map[string]struct {
		id   int
		n    int
		wind float64 // first row
	}{"wrf3": {90, 2, 14}, "wrf1": {923, 2, 21}, "zephr": {119, 1, 30}}
	got := map[string]int{}
	seen := map[key]bool{}
	for _, r := range rows {
		w, ok := want[r.Model]
		if !ok {
			t.Fatalf("unexpected model %q (id %d) — the unknown or PRO-only section leaked", r.Model, r.IDModel)
		}
		if r.IDModel != w.id {
			t.Fatalf("%s stored as id %d, want %d", r.Model, r.IDModel, w.id)
		}
		if got[r.Model] == 0 && (r.Wind == nil || *r.Wind != w.wind) {
			t.Fatalf("%s first row wind %v, want %v", r.Model, r.Wind, w.wind)
		}
		got[r.Model]++
		k := key{r.IDModel, r.Period.String()}
		if seen[k] {
			t.Fatalf("duplicate row for id_model %d at %s — would violate the PK", k.id, k.at)
		}
		seen[k] = true
	}
	for m, w := range want {
		if got[m] != w.n {
			t.Fatalf("%s: %d rows, want %d", m, got[m], w.n)
		}
	}
}
