package web

import (
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

// aggregateIndexCellAll aggregates with hover samples enabled.
func aggregateIndexCellAll(readings []models.WindReading, loc *time.Location) cellData {
	return aggregateIndexCell(readings, loc, maxIndexSamples)
}

func TestAggregateIndexCellMinMaxAndSamples(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 8, 6, 11, 20, 0, 0, loc)
	cell := aggregateIndexCellAll([]models.WindReading{
		{Period: base.Add(5 * time.Second), Wind: 12, Gust: 14, WindDir: 240},
		{Period: base.Add(15 * time.Second), Wind: 10, Gust: 16, WindDir: 250},
		{Period: base.Add(25 * time.Second), Wind: 11, Gust: 13, WindDir: 230},
	}, loc)
	if cell.Empty {
		t.Fatal("expected non-empty")
	}
	if cell.Wind != 10 || cell.Gust != 16 {
		t.Fatalf("min/max: got %.0f-%.0f want 10-16", cell.Wind, cell.Gust)
	}
	if cell.WindDir != 430 { // 250 + 180
		t.Fatalf("dir from max gust: got %.0f want 430", cell.WindDir)
	}
	if len(cell.Samples) != 3 {
		t.Fatalf("samples: got %d want 3", len(cell.Samples))
	}
	if cell.Samples[0].TimeLabel != "11:20:05" || cell.Samples[1].TimeLabel != "11:20:15" {
		t.Fatalf("sample labels: %+v", cell.Samples)
	}
}

func TestAggregateIndexCellSingleNoSamples(t *testing.T) {
	cell := aggregateIndexCellAll([]models.WindReading{
		{Period: time.Date(2026, 8, 6, 11, 20, 0, 0, time.UTC), Wind: 9, Gust: 11, WindDir: 200},
	}, time.UTC)
	if cell.Empty || cell.Wind != 9 || cell.Gust != 11 {
		t.Fatalf("cell: %+v", cell)
	}
	if len(cell.Samples) != 0 {
		t.Fatalf("expected no hover samples for single reading, got %d", len(cell.Samples))
	}
}

func TestIndexRowHasData(t *testing.T) {
	if indexRowHasData(rowData{Time: "16:02", Cells: []cellData{{Empty: true}, {Empty: true}}}) {
		t.Fatal("expected empty row to be hidden")
	}
	if !indexRowHasData(rowData{Time: "16:00", Cells: []cellData{{Empty: true}, {Empty: false, Wind: 8}}}) {
		t.Fatal("expected row with data to be kept")
	}
}

func TestIndexPeriodWindow8h(t *testing.T) {
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	spec := indexPeriodWindow("8h", now)
	if spec.Period != "8h" {
		t.Fatalf("period=%q", spec.Period)
	}
	if !spec.From.Equal(now.Add(-8 * time.Hour)) {
		t.Fatalf("from=%v", spec.From)
	}
	// The window is only actually listed if the row cap clears one row per bucket.
	if want := int(8 * time.Hour / spec.Bucket); spec.MaxRows < want {
		t.Fatalf("maxRows=%d cannot cover %d buckets of 8h", spec.MaxRows, want)
	}
}

func TestIndexPeriodWindowDayCoversWholeDay(t *testing.T) {
	now := time.Date(2026, 8, 12, 14, 37, 0, 0, time.UTC)
	spec := indexPeriodWindow("day", now)
	if spec.Period != "day" {
		t.Fatalf("period=%q", spec.Period)
	}
	midnight := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	if !spec.From.Equal(midnight) {
		t.Fatalf("from=%v, want midnight %v", spec.From, midnight)
	}
	// A full day of buckets must fit under the cap, or the table silently stops
	// short of the day it advertises.
	if want := int(24 * time.Hour / spec.Bucket); spec.MaxRows < want {
		t.Fatalf("maxRows=%d cannot cover %d buckets of a day", spec.MaxRows, want)
	}
}

func TestIndexPeriodWindowWeekCoversWholeWeek(t *testing.T) {
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	spec := indexPeriodWindow("week", now)
	if !spec.From.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("from=%v", spec.From)
	}
	if want := int(7 * 24 * time.Hour / spec.Bucket); spec.MaxRows < want {
		t.Fatalf("maxRows=%d cannot cover %d buckets of a week", spec.MaxRows, want)
	}
}

func TestIndexPeriodSampleBudgets(t *testing.T) {
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	base := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	readings := make([]models.WindReading, 0, 60)
	for i := 0; i < 60; i++ {
		readings = append(readings, models.WindReading{
			Period: base.Add(time.Duration(i) * time.Minute), Wind: 11, Gust: 15,
		})
	}
	for _, period := range []string{"", "8h", "day", "week"} {
		spec := indexPeriodWindow(period, now)
		cell := aggregateIndexCell(readings, time.UTC, spec.MaxSamples)
		if len(cell.Samples) > spec.MaxSamples {
			t.Fatalf("p=%q samples=%d, want <= %d", period, len(cell.Samples), spec.MaxSamples)
		}
		// The aggregate itself must never depend on how much hover detail ships.
		if cell.Empty || cell.Wind != 11 || cell.Gust != 15 {
			t.Fatalf("p=%q cell=%+v", period, cell)
		}
	}
	if spec := indexPeriodWindow("week", now); spec.MaxSamples != 0 {
		t.Fatalf("week should ship no hover samples, got %d", spec.MaxSamples)
	}
}

func TestIndexBucketKeyFoldsReadingsIntoSlots(t *testing.T) {
	spec := indexPeriodWindow("day", time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC))
	a := time.Date(2026, 8, 12, 14, 31, 0, 0, time.UTC)
	b := time.Date(2026, 8, 12, 14, 39, 59, 0, time.UTC)
	c := time.Date(2026, 8, 12, 14, 40, 0, 0, time.UTC)
	if ka, kb := indexBucketKey(a, time.UTC, spec), indexBucketKey(b, time.UTC, spec); ka != kb {
		t.Fatalf("same 10-minute slot got %q and %q", ka, kb)
	}
	if ka, kc := indexBucketKey(a, time.UTC, spec), indexBucketKey(c, time.UTC, spec); ka == kc {
		t.Fatalf("next slot shares key %q", kc)
	}
	if got := indexBucketKey(a, time.UTC, spec); got != "14:30" {
		t.Fatalf("key=%q, want 14:30", got)
	}
}

func TestAggregateIndexCellCapsSamples(t *testing.T) {
	base := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	readings := make([]models.WindReading, 0, 60)
	for i := 0; i < 60; i++ {
		readings = append(readings, models.WindReading{
			Period: base.Add(time.Duration(i) * time.Minute),
			Wind:   float64(10 + i%3),
			Gust:   float64(14 + i%5),
		})
	}
	cell := aggregateIndexCellAll(readings, time.UTC)
	if len(cell.Samples) > maxIndexSamples {
		t.Fatalf("samples=%d, want <= %d", len(cell.Samples), maxIndexSamples)
	}
	if len(cell.Samples) < 2 {
		t.Fatalf("expected a spread of samples, got %d", len(cell.Samples))
	}
	// Aggregation itself must still span every reading in the slot.
	if cell.Wind != 10 || cell.Gust != 18 {
		t.Fatalf("cell wind=%v gust=%v, want min 10 / max 18", cell.Wind, cell.Gust)
	}
}

func TestPruneEmptyDisplayedColumns(t *testing.T) {
	headers := []locHeader{
		{Key: "st", Name: "Shavei"},
		{Key: "ky-ims", Name: "IMS Afeq", IsIMS: true},
		{Key: "kh", Name: "KH"},
	}
	rows := []rowData{
		{Time: "14:00", Cells: []cellData{{Wind: 7, Gust: 9}, {Empty: true}, {Wind: 10, Gust: 12}}},
		{Time: "13:55", Cells: []cellData{{Wind: 6, Gust: 8}, {Empty: true}, {Empty: true}}},
	}
	headers, rows = pruneEmptyDisplayedColumns(headers, rows)
	if len(headers) != 2 || headers[0].Key != "st" || headers[1].Key != "kh" {
		t.Fatalf("headers=%+v", headers)
	}
	if len(rows[0].Cells) != 2 || rows[0].Cells[0].Wind != 7 || rows[0].Cells[1].Wind != 10 {
		t.Fatalf("row0 cells=%+v", rows[0].Cells)
	}
}

func TestIndexRowHasVisibleData(t *testing.T) {
	cols := []indexColSpec{
		{Key: "15233"},
		{Key: "hp"},
		{Key: "ky-ims", IsIMS: true, Parent: "ky"},
	}
	visible := map[string]bool{"15233": true, "ky": true}
	row := rowData{Cells: []cellData{{Empty: true}, {Empty: false, Wind: 10}, {Empty: true}}}
	if indexRowHasVisibleData(row, cols, visible) {
		t.Fatal("hp-only row should be dropped for first-page table")
	}
	row.Cells[0] = cellData{Empty: false, Wind: 8}
	if !indexRowHasVisibleData(row, cols, visible) {
		t.Fatal("visible Betzet cell should keep the row")
	}
	imsOnly := rowData{Cells: []cellData{{Empty: true}, {Empty: true}, {Empty: false, Wind: 7}}}
	if !indexRowHasVisibleData(imsOnly, cols, visible) {
		t.Fatal("IMS under visible parent should keep the row")
	}
}
