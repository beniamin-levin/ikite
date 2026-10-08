package web

import (
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func TestActualSlotBounds(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	periods := []time.Time{
		day.Add(8 * time.Hour),
		day.Add(8*time.Hour + 30*time.Minute),
		day.Add(9 * time.Hour),
	}
	start, end := actualSlotBounds(periods, 0)
	if !start.Equal(periods[0]) || !end.Equal(periods[1]) {
		t.Fatalf("slot0: got %s–%s", start, end)
	}
	start, end = actualSlotBounds(periods, 2)
	if !start.Equal(periods[2]) || !end.Equal(periods[2].Add(30*time.Minute)) {
		t.Fatalf("last slot: got %s–%s want end %s", start, end, periods[2].Add(30*time.Minute))
	}
}

func TestAggregateActualCellMinWindMaxGust(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	readings := []models.WindReading{
		{Period: start.Add(-time.Minute), Wind: 20, Gust: 25, WindDir: 90}, // before
		{Period: start, Wind: 12, Gust: 14, WindDir: 180},
		{Period: start.Add(5 * time.Minute), Wind: 8, Gust: 18, WindDir: 200},
		{Period: start.Add(10 * time.Minute), Wind: 10, Gust: 16, WindDir: 190},
		{Period: end, Wind: 3, Gust: 30, WindDir: 10}, // exclusive end
	}
	cell := aggregateActualCell(readings, start, end, time.UTC)
	if cell.Empty {
		t.Fatal("expected non-empty cell")
	}
	if cell.Wind != 8 {
		t.Fatalf("min wind: got %.0f want 8", cell.Wind)
	}
	if cell.Gust != 18 {
		t.Fatalf("max gust: got %.0f want 18", cell.Gust)
	}
	// dir from max-gust reading (200) + 180 for arrow
	if cell.WindDir != 380 {
		t.Fatalf("wind dir: got %.0f want 380", cell.WindDir)
	}
	if len(cell.Samples) != 3 {
		t.Fatalf("samples: got %d want 3", len(cell.Samples))
	}
	if cell.Samples[0].TimeLabel != "10:00" || cell.Samples[1].TimeLabel != "10:05" || cell.Samples[2].TimeLabel != "10:10" {
		t.Fatalf("sample times: %+v", cell.Samples)
	}
	if cell.Samples[1].Wind != 8 || cell.Samples[1].Gust != 18 {
		t.Fatalf("sample[1]: %+v", cell.Samples[1])
	}
}

func TestAggregateActualCellEmpty(t *testing.T) {
	start := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	cell := aggregateActualCell(nil, start, start.Add(30*time.Minute), time.UTC)
	if !cell.Empty {
		t.Fatal("expected empty")
	}
}
