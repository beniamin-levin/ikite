package collector

import (
	"strings"
	"testing"
	"time"
)

func TestSourceStaleMessage(t *testing.T) {
	loc := time.FixedZone("IST", 3*3600)
	tip := time.Date(2026, 9, 9, 6, 59, 0, 0, loc)
	now := time.Date(2026, 9, 10, 11, 16, 0, 0, loc)

	msg := sourceStaleMessage("surfo KY", tip, now)
	for _, want := range []string{"surfo KY", "09/09 06:59", "28h17m"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}

	if got := sourceStaleMessage("surfo KY", time.Time{}, now); !strings.Contains(got, "returned nothing") {
		t.Errorf("zero tip message = %q", got)
	}
}

func TestSourceStaleKey(t *testing.T) {
	if got := sourceStaleKey("surfo KY"); got != "source_stale_notified_surfo KY" {
		t.Fatalf("key = %q", got)
	}
}
