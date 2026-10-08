package windguru

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestParseForecastSpotGFS(t *testing.T) {
	spotBody, err := os.ReadFile("testdata/forecast_spot_373090.json")
	if err != nil {
		t.Fatalf("read spot fixture: %v", err)
	}
	modelBody, err := os.ReadFile("testdata/forecast_gfs_373090.json")
	if err != nil {
		t.Fatalf("read model fixture: %v", err)
	}

	var spot forecastSpotResp
	if err := json.Unmarshal(spotBody, &spot); err != nil {
		t.Fatalf("spot: %v", err)
	}
	if len(spot.Tabs[0].IDModelArr) < 4 {
		t.Fatalf("expected 4 models, got %d", len(spot.Tabs[0].IDModelArr))
	}

	var parsed forecastModelResp
	if err := json.Unmarshal(modelBody, &parsed); err != nil {
		t.Fatalf("model: %v", err)
	}
	if parsed.Model != "gfs" {
		t.Fatalf("model: %s", parsed.Model)
	}

	loc := time.FixedZone("IST", 3*3600)
	mr := spot.Tabs[0].IDModelArr[0]

	rows, err := parseModelForecast(modelBody, 373090, mr, loc)
	if err != nil {
		t.Fatalf("fetchModel: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	if rows[0].IDModel != 3 {
		t.Fatalf("id_model: %d", rows[0].IDModel)
	}
	if rows[0].WindguruID != 373090 {
		t.Fatalf("windguru_id: %d", rows[0].WindguruID)
	}
	for _, r := range rows {
		if !sameCalendarDay(r.Period, r.ForecastDate) {
			t.Fatalf("period/forecast_date mismatch: %s / %s", r.Period, r.ForecastDate)
		}
	}
}
