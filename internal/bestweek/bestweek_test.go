package bestweek

import (
	"strings"
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func est(loc string, day time.Time, winds map[int]float64, dir float64) []models.WindEstimate {
	var out []models.WindEstimate
	for h, w := range winds {
		out = append(out, models.WindEstimate{Location: loc, Period: day.Add(time.Duration(h) * time.Hour),
			Wind: w, Gust: w + 5, Low: w - 2, High: w + 2, Dir: dir})
	}
	return out
}

func TestPlanPicksLongestRunAndAveragesKYKH(t *testing.T) {
	day := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	rows := map[string][]models.WindEstimate{
		// KY and KH average to 13 kt from 13 to 16.
		"ky": est("ky", day, map[int]float64{10: 8, 12: 10, 13: 14, 14: 14, 15: 14, 16: 12, 17: 9}, 270),
		"kh": est("kh", day, map[int]float64{10: 8, 12: 10, 13: 12, 14: 12, 15: 12, 16: 14, 17: 9}, 290),
		// Betzet is stronger but only for one hour.
		"15233": est("15233", day, map[int]float64{14: 18, 15: 9}, 270),
	}
	days := Plan(rows, day, 2)
	if len(days) != 2 {
		t.Fatalf("got %d days", len(days))
	}
	b := days[0].Best()
	if b == nil || b.Group.Key != "kykh" {
		t.Fatalf("best = %+v, want kykh", b)
	}
	if b.RunHours != 4 || b.Window() != "13–17" {
		t.Fatalf("run %d window %q, want 4 h 13–17", b.RunHours, b.Window())
	}
	if b.Mean != 13 {
		t.Fatalf("mean %.2f, want 13", b.Mean)
	}
	if c := Compass(b.Peak.Dir); c != "W" {
		t.Fatalf("compass %q, want W", c)
	}
	if !days[0].Kiteable() || days[1].Best() != nil {
		t.Fatalf("kiteable/empty day wrong")
	}
	msg := Message(days, "https://ikite.fyi/best")
	if !strings.Contains(msg, "Kiryat Yam / Haim 13–17, 13 kt") || !strings.Contains(msg, "10–12 m") {
		t.Fatalf("message:\n%s", msg)
	}
}

func TestKiteSizeAndCompass(t *testing.T) {
	if KiteSize(10) != "12–14 m / foil" || KiteSize(16) != "9–10 m" || KiteSize(25) != "6 m or smaller" {
		t.Fatal("kite sizes")
	}
	if Compass(355) != "N" || Compass(-1) != "" || Compass(225) != "SW" {
		t.Fatal("compass")
	}
}
