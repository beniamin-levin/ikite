package ims

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/models"
)

const BaseURL = "https://api.ims.gov.il/v1/envista"

// LocationKey returns the wind_data.location used for an IMS station.
func LocationKey(stationID int) string {
	return fmt.Sprintf("ims%d", stationID)
}

// Station is IMS Envista station metadata (observations API).
type Station struct {
	ID        int
	Name      string
	ShortName string
	Lat       float64
	Lon       float64
}

func (st Station) HasCoords() bool {
	return st.Lat != 0 || st.Lon != 0
}

type Client struct {
	HTTP    *http.Client
	BaseURL string
	Token   string
}

func New(token string) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		BaseURL: BaseURL,
		Token:   token,
	}
}

type stationDataResp struct {
	StationID int `json:"stationId"`
	Data      []struct {
		Datetime string `json:"datetime"`
		Channels []struct {
			Name  string   `json:"name"`
			Value *float64 `json:"value"`
			Valid *bool    `json:"valid"`
		} `json:"channels"`
	} `json:"data"`
}

// FetchStationRange pulls IMS observations between from and to (inclusive day-ish).
// Wind speeds from IMS are m/s; converted to knots for storage.
// FetchStations returns metadata for all Envista stations.
func (c *Client) FetchStations() ([]Station, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("IMS API token not configured")
	}
	path := c.BaseURL + "/stations"
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "ApiToken "+c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IMS HTTP %d: %s", resp.StatusCode, truncate(string(body), 180))
	}
	body = trimIMSBody(body)
	if len(body) > 0 && body[0] == '<' {
		return nil, fmt.Errorf("IMS returned HTML: %s", truncate(string(body), 180))
	}
	return parseStationsJSON(body)
}

func (c *Client) FetchStationRange(stationID int, from, to time.Time) ([]models.WindReading, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("IMS API token not configured")
	}
	if !to.After(from) {
		return nil, fmt.Errorf("invalid IMS range")
	}
	// Query form is reliable; the /data/YYYY/MM/DD/HH/mm/... path often returns HTML errors with HTTP 200.
	// IMS treats `to` as exclusive calendar day, so bump to the next day.
	ist := time.FixedZone("IST", 3*3600)
	fromDay := from.In(ist)
	toDay := to.In(ist).Add(24 * time.Hour)
	path := fmt.Sprintf("%s/stations/%d/data/?from=%s&to=%s",
		c.BaseURL, stationID,
		fromDay.Format("2006/01/02"),
		toDay.Format("2006/01/02"),
	)
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "ApiToken "+c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IMS HTTP %d: %s", resp.StatusCode, truncate(string(body), 180))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("IMS HTTP %d: empty body", resp.StatusCode)
	}
	return parseStationDataBody(stationID, body)
}

// FetchStationLatest returns the newest observation row for a station.
// Prefer this (or merge with a range pull) so we do not miss the tip of the series.
func (c *Client) FetchStationLatest(stationID int) ([]models.WindReading, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("IMS API token not configured")
	}
	path := fmt.Sprintf("%s/stations/%d/data/latest", c.BaseURL, stationID)
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "ApiToken "+c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IMS HTTP %d: %s", resp.StatusCode, truncate(string(body), 180))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("IMS HTTP %d: empty body", resp.StatusCode)
	}
	return parseStationDataBody(stationID, body)
}

func parseStationDataBody(stationID int, body []byte) ([]models.WindReading, error) {
	body = trimIMSBody(body)
	if len(body) > 0 && body[0] == '<' {
		return nil, fmt.Errorf("IMS returned HTML: %s", truncate(string(body), 180))
	}

	var parsed stationDataResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Some responses are a bare array
		var alt []struct {
			Datetime string `json:"datetime"`
			Channels []struct {
				Name  string   `json:"name"`
				Value *float64 `json:"value"`
				Valid *bool    `json:"valid"`
			} `json:"channels"`
		}
		if err2 := json.Unmarshal(body, &alt); err2 != nil {
			return nil, fmt.Errorf("decode IMS: %w", err)
		}
		parsed.Data = alt
	}

	locKey := LocationKey(stationID)
	out := make([]models.WindReading, 0, len(parsed.Data))
	for _, row := range parsed.Data {
		period, err := parseIMSTime(row.Datetime)
		if err != nil {
			continue
		}
		var windMS, gustMS, dirMean, dirMax, tempC, humidity, pressure *float64
		for _, ch := range row.Channels {
			if ch.Valid != nil && !*ch.Valid {
				continue
			}
			if ch.Value == nil || !channelUsable(ch.Value, ch.Name) {
				continue
			}
			v := *ch.Value
			switch classifyChannel(ch.Name) {
			case "wind":
				windMS = &v
			case "gust":
				gustMS = &v
			case "dir":
				if strings.Contains(strings.ToLower(ch.Name), "max") {
					dirMax = &v
				} else {
					dirMean = &v
				}
			case "temp":
				tempC = &v
			case "humidity":
				humidity = &v
			case "pressure":
				pressure = &v
			}
		}
		if windMS == nil {
			continue
		}
		windKt := *windMS * 1.943844
		gustKt := windKt
		if gustMS != nil {
			gustKt = *gustMS * 1.943844
		}
		rd := models.WindReading{
			Period:   period,
			Location: locKey,
			Wind:     windKt,
			Gust:     gustKt,
		}
		if dir := pickIMSDirection(dirMean, dirMax); dir != nil {
			rd.WindDir = *dir
		}
		if tempC != nil {
			t := *tempC
			rd.Temp = &t
		}
		if humidity != nil {
			h := *humidity
			rd.Humidity = &h
		}
		if pressure != nil {
			p := *pressure
			rd.Pressure = &p
		}
		// Keep the full channel payload so unused IMS fields (rain, radiation, …)
		// remain available later without another historical pull.
		if raw, err := json.Marshal(row); err == nil {
			rd.Raw = string(raw)
		}
		out = append(out, rd)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("IMS station %d: no wind rows", stationID)
	}
	return out, nil
}

// imsClockZone is the clock IMS observations are really stamped in: Israel
// Standard Time (UTC+2) all year round. In summer the API still appends the
// current local offset, +03:00, to those standard-time digits, so taking the
// offset at face value put every summer reading an hour early. Measured against
// the Windguru/Holfuy meters at the same sites (Shavei Tzion, Kiryat Haim), IMS
// lined up best shifted +50–60 min, every week from Aug to Oct 2026; on
// 2026-10-06 IMS showed a squall at Shavei Tzion 50 min before the meter beside
// it did. In winter the offset is +02:00 anyway, so reading the digits as UTC+2
// is right in both seasons.
var imsClockZone = time.FixedZone("IST", 2*3600)

func parseIMSTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("bad IMS time %q", s)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, imsClockZone), nil
	}
	wall := s
	if i := strings.IndexByte(wall, 'Z'); i > 0 {
		wall = wall[:i]
	} else if i := strings.LastIndexAny(wall, "+-"); i > 10 {
		wall = wall[:i]
	}
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"02/01/2006 15:04",
		"2006/01/02 15:04",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, wall, imsClockZone); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad IMS time %q", s)
}

func classifyChannel(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, " ", "")
	switch {
	case n == "wsmax", n == "ws60mmax", n == "gust", n == "gusts", n == "wx", n == "ffgust",
		strings.HasSuffix(n, "smax") && strings.Contains(n, "w"):
		return "gust"
	case n == "wd", n == "wd1", n == "dd", n == "wd60m", n == "winddirection",
		n == "wdmax", n == "wd60mmax":
		return "dir"
	case n == "ws", n == "ws1", n == "ff", n == "ws60m", n == "windspeed":
		// WS1mm / Ws10mm are short-window maxima, not mean wind — ignore here.
		return "wind"
	case n == "td", n == "temp", n == "temperature", n == "tdry":
		return "temp"
	case n == "rh", n == "humidity", n == "relhum", n == "relativehumidity":
		return "humidity"
	case n == "bp", n == "pressure", n == "press", n == "slp", n == "qfe", n == "qff":
		return "pressure"
	default:
		return ""
	}
}

// pickIMSDirection prefers mean WD, then max-gust WD. Zero is treated as missing
// (AFEQ and some stations report 0 when the direction sensor is unavailable).
func pickIMSDirection(mean, max *float64) *float64 {
	if mean != nil && *mean != 0 {
		return mean
	}
	if max != nil && *max != 0 {
		return max
	}
	return nil
}

func channelUsable(v *float64, name string) bool {
	if v == nil {
		return false
	}
	// IMS sentinel for missing sensors.
	if *v <= -999 {
		return false
	}
	_ = name
	return true
}

type stationMeta struct {
	StationID int    `json:"stationId"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Location  *struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
		Lat       *float64 `json:"lat"`
		Lon       *float64 `json:"lon"`
	} `json:"location"`
}

func parseStationsJSON(body []byte) ([]Station, error) {
	var raw []stationMeta
	if err := json.Unmarshal(body, &raw); err == nil {
		return stationsFromMeta(raw), nil
	}
	var wrapped struct {
		Stations []stationMeta `json:"stations"`
		Data     []stationMeta `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		var one stationMeta
		if err2 := json.Unmarshal(body, &one); err2 != nil || one.StationID == 0 {
			return nil, fmt.Errorf("decode IMS stations: %w", err)
		}
		return stationsFromMeta([]stationMeta{one}), nil
	}
	if len(wrapped.Stations) > 0 {
		return stationsFromMeta(wrapped.Stations), nil
	}
	return stationsFromMeta(wrapped.Data), nil
}

func stationsFromMeta(raw []stationMeta) []Station {
	out := make([]Station, 0, len(raw))
	for _, row := range raw {
		if row.StationID == 0 {
			continue
		}
		st := Station{
			ID:        row.StationID,
			Name:      strings.TrimSpace(row.Name),
			ShortName: strings.TrimSpace(row.ShortName),
		}
		if row.Location != nil {
			switch {
			case row.Location.Latitude != nil && row.Location.Longitude != nil:
				st.Lat, st.Lon = *row.Location.Latitude, *row.Location.Longitude
			case row.Location.Lat != nil && row.Location.Lon != nil:
				st.Lat, st.Lon = *row.Location.Lat, *row.Location.Lon
			}
		}
		out = append(out, st)
	}
	return out
}

func trimIMSBody(body []byte) []byte {
	if len(body) >= 3 && body[0] == 0xEF && body[1] == 0xBB && body[2] == 0xBF {
		return body[3:]
	}
	return body
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
