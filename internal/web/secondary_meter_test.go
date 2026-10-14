package web

import (
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func TestSecondaryMeterJoinsParentCell(t *testing.T) {
	at := time.Date(2026, 10, 14, 13, 5, 0, 0, time.UTC)
	readings := mergeSecondaryMeters([]models.WindReading{
		{Period: at, Location: "hp", Wind: 14, Gust: 17, WindDir: 340},
		{Period: at, Location: "2259", Wind: 9, Gust: 19, WindDir: 5},
		{Period: at, Location: "2259", Wind: 0, Gust: 22}, // no lull sent
	})
	if len(readings) != 2 {
		t.Fatalf("got %d readings, want the one without a lull dropped", len(readings))
	}
	for _, rd := range readings {
		if rd.Location != "hp" {
			t.Fatalf("reading filed under %q, want hp", rd.Location)
		}
	}
	cell := aggregateIndexCell(readings, time.UTC, 0)
	if cell.Wind != 9 || cell.Gust != 19 {
		t.Fatalf("cell = %v/%v, want the lower wind 9 and the higher gust 19", cell.Wind, cell.Gust)
	}

	spots := withoutSecondaryMeters([]models.Spot{{ID: "hp"}, {ID: "2259"}, {ID: "by"}})
	if len(spots) != 2 || spots[0].ID != "hp" || spots[1].ID != "by" {
		t.Fatalf("spots = %+v, want hp and by", spots)
	}
}
