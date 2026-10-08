// Package imssea reads the IMS (Israel Meteorological Service) official sea
// forecast: per coastal region, 12-hour blocks of wind direction and speed
// ranges, wave height and sea temperature, written by IMS forecasters.
//
// https://ims.gov.il/sites/default/files/ims_data/xml_files/isr_sea.xml
package imssea

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ben/ikite-go/internal/models"
)

const (
	URL = "https://ims.gov.il/sites/default/files/ims_data/xml_files/isr_sea.xml"
	// ModelID is the wind_forecast id_model for the IMS sea forecast (external
	// models use ids from 1000000; 1000001–1000005 are taken).
	ModelID   = 1000006
	ModelName = "ims_sea"
	kmhToKt   = 1 / 1.852
)

// IMS sea-forecast regions.
const (
	RegionCentral  = 210
	RegionGalilee  = 211 // Sea of Galilee
	RegionNorthern = 212
	RegionSouthern = 213
	RegionEilat    = 214
)

// Block is one forecast period for one region.
type Block struct {
	From, To       time.Time
	DirFrom, DirTo float64 // the wind direction range, degrees FROM; -1 when missing
	WindMinKmh     float64
	WindMaxKmh     float64
	SeaState       int // IMS sea-state code (e.g. 30, 40); -1 when missing
	WaveMinCm      int
	WaveMaxCm      int
	SeaTempC       *float64
}

// Region is one coastal region's forecast.
type Region struct {
	ID     int
	Name   string
	Blocks []Block
}

// Forecast is one issue of the IMS sea forecast.
type Forecast struct {
	Issued  time.Time
	Regions []Region
}

type xmlDoc struct {
	Issued    string `xml:"Identification>IssueDateTime"`
	Locations []struct {
		ID     int    `xml:"LocationMetaData>LocationId"`
		Name   string `xml:"LocationMetaData>LocationNameEng"`
		Blocks []struct {
			From     string `xml:"DateTimeFrom"`
			To       string `xml:"DateTimeTo"`
			Elements []struct {
				Name  string `xml:"ElementName"`
				Value string `xml:"ElementValue"`
			} `xml:"Element"`
		} `xml:"LocationData>TimeUnitData"`
	} `xml:"Location"`
}

// Fetch downloads and parses the current sea forecast. It also returns the raw
// XML so it can be archived.
func Fetch(client *http.Client, tz *time.Location) (*Forecast, []byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, URL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ikite-go/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("ims sea forecast: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("ims sea forecast: status %d", resp.StatusCode)
	}
	f, err := Parse(body, tz)
	return f, body, err
}

// Parse reads the XML (ISO-8859-8; only its English and numeric fields are used).
func Parse(body []byte, tz *time.Location) (*Forecast, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		if !strings.EqualFold(label, "ISO-8859-8") {
			return nil, fmt.Errorf("unexpected charset %q", label)
		}
		return hebrewLatin(in)
	}
	var doc xmlDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode ims sea forecast: %w", err)
	}
	issued, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(doc.Issued), tz)
	if err != nil {
		return nil, fmt.Errorf("ims sea forecast issue time %q: %w", doc.Issued, err)
	}
	f := &Forecast{Issued: issued}
	for _, loc := range doc.Locations {
		reg := Region{ID: loc.ID, Name: strings.TrimSpace(loc.Name)}
		for _, tu := range loc.Blocks {
			from, err1 := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(tu.From), tz)
			to, err2 := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(tu.To), tz)
			if err1 != nil || err2 != nil || !to.After(from) {
				continue
			}
			b := Block{From: from, To: to, DirFrom: -1, DirTo: -1, SeaState: -1}
			for _, el := range tu.Elements {
				v := strings.TrimSpace(el.Value)
				switch strings.ToLower(strings.TrimSpace(el.Name)) {
				case "wind direction and speed":
					b.DirFrom, b.DirTo, b.WindMinKmh, b.WindMaxKmh = parseWind(v)
				case "sea status and waves height":
					b.SeaState, b.WaveMinCm, b.WaveMaxCm = parseSea(v)
				case "sea temperature":
					if t, err := strconv.ParseFloat(v, 64); err == nil {
						b.SeaTempC = &t
					}
				}
			}
			reg.Blocks = append(reg.Blocks, b)
		}
		f.Regions = append(f.Regions, reg)
	}
	return f, nil
}

// parseWind reads "270-360/15-25": direction range (degrees) / speed range (km/h).
func parseWind(v string) (dirFrom, dirTo, minKmh, maxKmh float64) {
	dirFrom, dirTo = -1, -1
	dirs, speeds, ok := strings.Cut(v, "/")
	if !ok {
		return
	}
	if a, b, ok := cutRange(dirs); ok {
		dirFrom, dirTo = a, b
	}
	if a, b, ok := cutRange(speeds); ok {
		minKmh, maxKmh = math.Min(a, b), math.Max(a, b)
	}
	return
}

// parseSea reads "40   / 30-60": sea-state code / wave height range (cm).
func parseSea(v string) (state, minCm, maxCm int) {
	state = -1
	code, waves, _ := strings.Cut(v, "/")
	if c, err := strconv.Atoi(strings.TrimSpace(code)); err == nil {
		state = c
	}
	if a, b, ok := cutRange(waves); ok {
		minCm, maxCm = int(math.Min(a, b)), int(math.Max(a, b))
	}
	return
}

func cutRange(s string) (float64, float64, bool) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		x, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return x, x, err == nil
	}
	x, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	y, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	return x, y, err1 == nil && err2 == nil
}

// MidDirection is the middle of the shorter arc between two directions, so
// "315-045" is north and "180-135" is 158°.
func MidDirection(a, b float64) float64 {
	if a < 0 || b < 0 {
		return -1
	}
	d := math.Mod(b-a+540, 360) - 180 // signed shortest difference, −180..180
	return math.Mod(a+d/2+360, 360)
}

// RegionFor picks the sea-forecast region for a spot, or 0 when none covers it
// (spots abroad, inland meters).
func RegionFor(lat, lon float64) int {
	switch {
	case lat >= 32.65 && lat <= 33.0 && lon >= 35.45 && lon <= 35.7:
		return RegionGalilee
	case lat >= 29.3 && lat <= 29.65 && lon >= 34.85 && lon <= 35.05:
		return RegionEilat
	case lon < 34.2 || lon > 35.2 || lat < 31.2 || lat > 33.15:
		return 0 // not on Israel's Mediterranean coast
	case lat >= 32.55:
		return RegionNorthern
	case lat >= 31.75:
		return RegionCentral
	}
	return RegionSouthern
}

// Rows expands a region's blocks into hourly forecast rows for one spot: the
// middle of IMS's speed range as the wind and its top as the gust, so the
// forecast table shows IMS's own range.
func (r Region) Rows(location string, windguruID int) []models.WindForecastRow {
	var out []models.WindForecastRow
	for _, b := range r.Blocks {
		if b.WindMaxKmh <= 0 {
			continue
		}
		wind := (b.WindMinKmh + b.WindMaxKmh) / 2 * kmhToKt
		gust := b.WindMaxKmh * kmhToKt
		dir := MidDirection(b.DirFrom, b.DirTo)
		for t := b.From.Truncate(time.Hour); t.Before(b.To); t = t.Add(time.Hour) {
			if t.Before(b.From) {
				continue
			}
			w, g := round1(wind), round1(gust)
			row := models.WindForecastRow{
				ForecastDate: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()),
				Location:     location,
				WindguruID:   windguruID,
				IDModel:      ModelID,
				Model:        ModelName,
				Period:       t,
				Wind:         &w,
				Gust:         &g,
			}
			if dir >= 0 {
				d := math.Round(dir)
				row.WindDir = &d
			}
			out = append(out, row)
		}
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// hebrewLatin converts ISO-8859-8 to UTF-8 (Hebrew letters 0xE0–0xFA map to
// U+05D0–U+05EA; anything else above ASCII becomes U+FFFD).
func hebrewLatin(in io.Reader) (io.Reader, error) {
	raw, err := io.ReadAll(in)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Grow(len(raw) * 2)
	for _, c := range raw {
		switch {
		case c < 0x80:
			buf.WriteByte(c)
		case c >= 0xE0 && c <= 0xFA:
			buf.WriteRune(rune(0x05D0 + int(c) - 0xE0))
		default:
			buf.WriteRune(utf8.RuneError)
		}
	}
	return &buf, nil
}
