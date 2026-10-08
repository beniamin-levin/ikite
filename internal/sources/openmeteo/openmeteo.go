package openmeteo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ben/ikite-go/internal/models"
	"github.com/ben/ikite-go/internal/sources/openwrf"
)

const (
	BaseURL = "https://api.open-meteo.com/v1/forecast"

	ModelAIFS   = "aifs"
	ModelUKMO   = "ukmo"
	ModelICONEU = "icon_eu"

	IDAIFS   = 1_000_003
	IDUKMO   = 1_000_004
	IDICONEU = 1_000_005

	APIModelAIFS = "ecmwf_aifs025_single"
	APIModelUKMO = "ukmo_global_deterministic_10km"
	// DWD ICON-EU, ~7km. The only sub-global model Open-Meteo carries that still
	// covers Israel: icon_d2 answers "no data for this location" at 32N 35E, and
	// Harmonie-AROME / AROME-France / UKV all return a nan latitude there. Worth
	// having as the finest grid available for the coast now that the 1km openWRF
	// feed is gone.
	APIModelICONEU = "icon_eu"
)

type Client struct {
	HTTP    *http.Client
	BaseURL string
}

func New() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		BaseURL: BaseURL,
	}
}

type ModelSpec struct {
	ID      int
	Name    string
	APIName string
}

func Models() []ModelSpec {
	return []ModelSpec{
		{ID: IDAIFS, Name: ModelAIFS, APIName: APIModelAIFS},
		{ID: IDUKMO, Name: ModelUKMO, APIName: APIModelUKMO},
		{ID: IDICONEU, Name: ModelICONEU, APIName: APIModelICONEU},
	}
}

type apiResp struct {
	Hourly struct {
		Time          []string   `json:"time"`
		WindSpeed10m  []*float64 `json:"wind_speed_10m"`
		WindGusts10m  []*float64 `json:"wind_gusts_10m"`
		WindDirection []*float64 `json:"wind_direction_10m"`
	} `json:"hourly"`
}

// FetchSpot returns forecast rows for one Open-Meteo model at a spot.
func (c *Client) FetchSpot(sp models.Spot, model ModelSpec, forecastDays int, loc *time.Location) ([]models.WindForecastRow, error) {
	if !sp.HasCoords() {
		return nil, fmt.Errorf("spot %s: missing lat/lon", sp.ID)
	}
	if forecastDays <= 0 {
		forecastDays = 7
	}
	if loc == nil {
		loc = time.UTC
	}
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%f", *sp.Lat))
	q.Set("longitude", fmt.Sprintf("%f", *sp.Lon))
	q.Set("hourly", "wind_speed_10m,wind_gusts_10m,wind_direction_10m")
	q.Set("wind_speed_unit", "kn")
	q.Set("models", model.APIName)
	q.Set("forecast_days", fmt.Sprintf("%d", forecastDays))
	q.Set("timezone", loc.String())

	endpoint := c.BaseURL + "?" + q.Encode()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ikite-go/1.0")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open-meteo HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed apiResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode open-meteo: %w", err)
	}
	n := len(parsed.Hourly.Time)
	if n == 0 {
		return nil, fmt.Errorf("open-meteo: empty hourly series")
	}

	key := openwrf.ForecastKey(sp)
	out := make([]models.WindForecastRow, 0, n)
	for i := 0; i < n; i++ {
		if parsed.Hourly.WindSpeed10m == nil || i >= len(parsed.Hourly.WindSpeed10m) || parsed.Hourly.WindSpeed10m[i] == nil {
			continue
		}
		wind := *parsed.Hourly.WindSpeed10m[i]
		gust := wind
		if parsed.Hourly.WindGusts10m != nil && i < len(parsed.Hourly.WindGusts10m) && parsed.Hourly.WindGusts10m[i] != nil {
			gust = *parsed.Hourly.WindGusts10m[i]
		}
		var dir *float64
		if parsed.Hourly.WindDirection != nil && i < len(parsed.Hourly.WindDirection) && parsed.Hourly.WindDirection[i] != nil {
			d := *parsed.Hourly.WindDirection[i]
			dir = &d
		}
		period, err := time.ParseInLocation("2006-01-02T15:04", parsed.Hourly.Time[i], loc)
		if err != nil {
			period, err = time.ParseInLocation("2006-01-02T15:04:05", parsed.Hourly.Time[i], loc)
			if err != nil {
				continue
			}
		}
		day := time.Date(period.Year(), period.Month(), period.Day(), 0, 0, 0, 0, loc)
		w, g := wind, gust
		out = append(out, models.WindForecastRow{
			ForecastDate: day,
			Location:     sp.ID,
			WindguruID:   key,
			IDModel:      model.ID,
			Model:        model.Name,
			Period:       period,
			Wind:         &w,
			Gust:         &g,
			WindDir:      dir,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("open-meteo %s: no usable rows", model.Name)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
