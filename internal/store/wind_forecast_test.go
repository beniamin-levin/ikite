package store

import (
	"testing"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

func TestForecastRowDate(t *testing.T) {
	loc := time.FixedZone("IST", 3*3600)
	fromField := time.Date(2026, 8, 4, 0, 0, 0, 0, loc)
	fromPeriod := time.Date(2026, 8, 5, 14, 0, 0, 0, loc)

	if got := forecastRowDate(models.WindForecastRow{ForecastDate: fromField}); got != "2026-08-04" {
		t.Fatalf("from ForecastDate: got %s", got)
	}
	if got := forecastRowDate(models.WindForecastRow{Period: fromPeriod}); got != "2026-08-05" {
		t.Fatalf("from Period: got %s", got)
	}
}
