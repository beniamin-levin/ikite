package windguru

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ben/ikite-go/internal/begetproxy"
	"github.com/ben/ikite-go/internal/models"
)

type ForecastClient struct {
	Proxy *begetproxy.Client
}

func NewForecast(proxy *begetproxy.Client) *ForecastClient {
	return &ForecastClient{Proxy: proxy}
}

type forecastSpotResp struct {
	Tabs []struct {
		IDSpot     string     `json:"id_spot"`
		Lat        float64    `json:"lat"`
		Lon        float64    `json:"lon"`
		IDModelArr []modelRun `json:"id_model_arr"`
	} `json:"tabs"`
}

type modelRun struct {
	IDModel  int    `json:"id_model"`
	Rundef   string `json:"rundef"`
	Cachefix string `json:"cachefix"`
}

type forecastModelResp struct {
	IDSpot  int    `json:"id_spot"`
	IDModel int    `json:"id_model"`
	Model   string `json:"model"`
	WGModel struct {
		ModelName string `json:"model_name"`
		Initstamp int64  `json:"initstamp"`
	} `json:"wgmodel"`
	Fcst struct {
		Initstamp int64     `json:"initstamp"`
		Hours     []int     `json:"hours"`
		WINDSPD   []float64 `json:"WINDSPD"`
		GUST      []float64 `json:"GUST"`
		WINDDIR   []float64 `json:"WINDDIR"`
		TMP       []float64 `json:"TMP"`
	} `json:"fcst"`
}

// FetchSpotForecasts loads all available model forecasts for a Windguru spot.
// Prefers micro.windguru.cz (all free models with clear names); falls back to JSON iapi.
func (c *ForecastClient) FetchSpotForecasts(spotID int, forecastDate time.Time, loc *time.Location) ([]models.WindForecastRow, error) {
	rows, err := c.FetchSpotForecastsMicro(spotID, forecastDate, loc)
	if err == nil && len(rows) > 0 {
		return rows, nil
	}
	microErr := err

	rows, err = c.fetchSpotForecastsJSON(spotID, forecastDate, loc)
	if err != nil {
		if microErr != nil {
			return nil, fmt.Errorf("micro: %v; json: %w", microErr, err)
		}
		return nil, err
	}
	return rows, nil
}

func (c *ForecastClient) fetchSpotForecastsJSON(spotID int, forecastDate time.Time, loc *time.Location) ([]models.WindForecastRow, error) {
	spotBody, err := c.Proxy.Get(forecastSpotURL(spotID), wgHeaders(false))
	if err != nil {
		return nil, fmt.Errorf("forecast_spot %d: %w", spotID, err)
	}
	if len(spotBody) > 0 && spotBody[0] == '<' {
		return nil, fmt.Errorf("forecast_spot %d: blocked (HTML response)", spotID)
	}

	var spot forecastSpotResp
	if err := json.Unmarshal(spotBody, &spot); err != nil {
		return nil, fmt.Errorf("decode forecast_spot %d: %w", spotID, err)
	}
	if len(spot.Tabs) == 0 || len(spot.Tabs[0].IDModelArr) == 0 {
		return nil, fmt.Errorf("forecast_spot %d: no models", spotID)
	}

	// Collect unique model runs across all tabs.
	seen := map[int]bool{}
	var runs []modelRun
	for _, tab := range spot.Tabs {
		for _, mr := range tab.IDModelArr {
			if seen[mr.IDModel] {
				continue
			}
			seen[mr.IDModel] = true
			runs = append(runs, mr)
		}
	}

	var out []models.WindForecastRow
	for _, mr := range runs {
		rows, err := c.fetchModel(spotID, mr, loc)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			rows[i].Model = DisplayModelName(rows[i].IDModel, rows[i].Model)
		}
		out = append(out, rows...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("forecast_spot %d: no rows", spotID)
	}
	return out, nil
}

func (c *ForecastClient) fetchModel(spotID int, mr modelRun, loc *time.Location) ([]models.WindForecastRow, error) {
	body, err := c.Proxy.Get(forecastModelURL(spotID, mr), wgHeaders(true))
	if err != nil {
		return nil, fmt.Errorf("forecast model %d spot %d: %w", mr.IDModel, spotID, err)
	}
	if len(body) > 0 && body[0] == '<' {
		return nil, fmt.Errorf("forecast model %d: blocked (HTML response)", mr.IDModel)
	}
	return parseModelForecast(body, spotID, mr, loc)
}

func parseModelForecast(body []byte, spotID int, mr modelRun, loc *time.Location) ([]models.WindForecastRow, error) {
	var parsed forecastModelResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode forecast model %d: %w", mr.IDModel, err)
	}

	init := parsed.Fcst.Initstamp
	if init == 0 {
		init = parsed.WGModel.Initstamp
	}
	modelName := parsed.Model
	if modelName == "" {
		modelName = fmt.Sprintf("model%d", mr.IDModel)
	}

	rows := make([]models.WindForecastRow, 0, len(parsed.Fcst.Hours))
	for i, h := range parsed.Fcst.Hours {
		period := time.Unix(init+int64(h)*3600, 0).UTC()
		local := period.In(loc)
		row := models.WindForecastRow{
			ForecastDate: dateOnly(local),
			WindguruID:   spotID,
			IDModel:      mr.IDModel,
			Model:        modelName,
			Period:       local,
		}
		if v, ok := at(parsed.Fcst.WINDSPD, i); ok {
			row.Wind = &v
		}
		if v, ok := at(parsed.Fcst.GUST, i); ok {
			row.Gust = &v
		}
		if v, ok := at(parsed.Fcst.WINDDIR, i); ok {
			row.WindDir = &v
		}
		if v, ok := at(parsed.Fcst.TMP, i); ok {
			row.Temp = &v
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func sameCalendarDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func at(vals []float64, i int) (float64, bool) {
	if i < 0 || i >= len(vals) {
		return 0, false
	}
	return vals[i], true
}
