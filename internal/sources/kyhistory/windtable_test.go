package kyhistory

import (
	"os"
	"testing"
	"time"
)

func TestWindtableURL(t *testing.T) {
	const base = "https://surfo.co.il/wp-content/themes/vibes-child/inc/weather/data/"
	cases := map[string]string{
		base + "api_wind.php":             base + "windtable.php",
		base + "api_wind.php?_t=12345":    base + "windtable.php",
		base + "something_else.php":       "",
		"https://example.com/api_wind.ph": "",
	}
	for in, want := range cases {
		if got := windtableURL(in); got != want {
			t.Errorf("windtableURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseWindtable(t *testing.T) {
	body, err := os.ReadFile("testdata/windtable_sample.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	loc := time.FixedZone("IST", 3*3600)
	now := time.Date(2026, 9, 10, 11, 20, 30, 0, loc)

	rows, err := parseWindtable(body, now)
	if err != nil {
		t.Fatalf("parseWindtable: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	// Oldest first, so the 11:19 row leads.
	if got := rows[0].Period.Format("2006-01-02 15:04"); got != "2026-09-10 11:19" {
		t.Errorf("first period = %s", got)
	}
	tip := rows[1]
	if tip.Wind != 10 || tip.Gust != 14 || tip.WindDir != 284 {
		t.Errorf("tip wind/gust/dir = %v/%v/%v, want 10/14/284", tip.Wind, tip.Gust, tip.WindDir)
	}
	if tip.Temp == nil || *tip.Temp != 31.9 {
		t.Errorf("tip temp = %v, want 31.9", tip.Temp)
	}
	if tip.Location != "ky" {
		t.Errorf("location = %q", tip.Location)
	}
}

// Rows carry a clock time only, so a table read just after midnight must date
// its trailing rows to the previous day rather than the coming one.
func TestParseWindtableAcrossMidnight(t *testing.T) {
	body, err := os.ReadFile("testdata/windtable_sample.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	loc := time.FixedZone("IST", 3*3600)
	now := time.Date(2026, 9, 11, 0, 3, 0, 0, loc)

	rows, err := parseWindtable(body, now)
	if err != nil {
		t.Fatalf("parseWindtable: %v", err)
	}
	if got := rows[len(rows)-1].Period.Format("2006-01-02 15:04"); got != "2026-09-10 11:20" {
		t.Errorf("tip period = %s, want 2026-09-10 11:20", got)
	}
}

// surfo renders its own banner when its feed has stopped; taking those rows
// would just re-import the staleness the fallback exists to work around.
func TestParseWindtableRejectsStaleBanner(t *testing.T) {
	body := []byte(`<div class="stale-alert">אין נתונים</div><table>` +
		`<tr><td>11:20</td><td>&deg;31.9</td><td></td><td>10<span>kt</span></td><td>14<span>kt</span></td></tr>` +
		`</table>`)
	if _, err := parseWindtable(body, time.Now()); err == nil {
		t.Fatal("expected an error for a stale-alert page")
	}
}
