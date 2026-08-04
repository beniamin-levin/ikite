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
`
	day := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	rows, err := ParseMicroForecast(text, 373090, day, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (PRO icon7 skipped)", len(rows))
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
