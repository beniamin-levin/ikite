// Package squall warns of thunderstorm gust fronts heading for Kiryat Haim.
//
// A squall like the one on 2026-10-06 (5 → 27 kt in two minutes, over in 35)
// comes in off the sea, where there are no wind meters: Bat Galim, the nearest
// station upwind, saw it only 12 minutes before Kiryat Haim. The rain cell that
// drives it is visible on radar much further out, so the early warning comes
// from RainViewer's radar mosaic: find heavy-rain cells, measure how the rain
// field is moving between frames, and project when a cell reaches the spot.
// Station readings then confirm it is arriving, and every storm that does hit
// is replayed against the archived frames to tune the rules (see tune.go).
package squall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"time"
)

const (
	rainViewerMaps = "https://api.rainviewer.com/public/weather-maps.json"
	// Zoom 7 is the highest the free API serves; at 512 px a tile is ~0.51 km
	// a pixel here, which is plenty for cells several km across.
	tileZoom = 7
	tileSize = 512
	// The tile covering Haifa Bay: 33.75–36.56°E, ~31.9–34.3°N. Kiryat Haim
	// sits ~125 km from its west edge, so cells are seen far out to sea.
	tileX = 76
	tileY = 51
	// colourScheme 2 is Universal Blue, the only scheme the API still renders.
	colourScheme = 2
)

// Target is the point the alerts are for.
type Target struct {
	Name     string
	Lat, Lon float64
}

// KiryatHaim is the default target.
var KiryatHaim = Target{Name: "Kiryat Haim", Lat: 32.830, Lon: 35.070}

// Frame is one radar image as published by RainViewer.
type Frame struct {
	Time time.Time // when the radar scanned
	Path string
}

// Client talks to RainViewer.
type Client struct {
	HTTP *http.Client
}

func NewClient() *Client { return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}} }

type mapsResp struct {
	Host  string `json:"host"`
	Radar struct {
		Past []struct {
			Time int64  `json:"time"`
			Path string `json:"path"`
		} `json:"past"`
	} `json:"radar"`
}

// Frames lists the published radar frames, oldest first, and the tile host.
func (c *Client) Frames() (host string, frames []Frame, err error) {
	body, err := c.get(rainViewerMaps)
	if err != nil {
		return "", nil, err
	}
	var r mapsResp
	if err := json.Unmarshal(body, &r); err != nil {
		return "", nil, fmt.Errorf("decode radar frames: %w", err)
	}
	for _, f := range r.Radar.Past {
		frames = append(frames, Frame{Time: time.Unix(f.Time, 0), Path: f.Path})
	}
	return r.Host, frames, nil
}

// Tile downloads the Haifa Bay tile of a frame as PNG bytes.
func (c *Client) Tile(host string, f Frame) ([]byte, error) {
	// smooth=0 keeps exact palette colours so pixels decode to dBZ; snow=0.
	url := fmt.Sprintf("%s%s/%d/%d/%d/%d/%d/0_0.png", host, f.Path, tileSize, tileZoom, tileX, tileY, colourScheme)
	return c.get(url)
}

func (c *Client) get(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ikite.fyi squall alert")
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
		return nil, fmt.Errorf("radar %s: status %d", url, resp.StatusCode)
	}
	return body, nil
}

// Grid is a decoded tile: dBZ per pixel, 0 where there is no rain.
type Grid struct {
	W, H int
	DBZ  []int8
	rain []int32 // indices of pixels with any rain, built on first use
}

// rainIdx lists the pixels with rain. Most of the tile is dry most of the time,
// so scanning this instead of all 262k pixels keeps replays fast.
func (g *Grid) rainIdx() []int32 {
	if g.rain == nil {
		g.rain = make([]int32, 0, 64)
		for i, d := range g.DBZ {
			if d > 0 {
				g.rain = append(g.rain, int32(i))
			}
		}
	}
	return g.rain
}

func (g *Grid) At(x, y int) int {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return 0
	}
	return int(g.DBZ[y*g.W+x])
}

// Decode turns a Universal Blue PNG into dBZ.
func Decode(pngBytes []byte) (*Grid, error) {
	im, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("decode radar png: %w", err)
	}
	b := im.Bounds()
	g := &Grid{W: b.Dx(), H: b.Dy(), DBZ: make([]int8, b.Dx()*b.Dy())}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			c := color.NRGBAModel.Convert(im.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			if c.A != 255 {
				continue
			}
			if d, ok := universalBlue[uint32(c.R)<<16|uint32(c.G)<<8|uint32(c.B)]; ok {
				g.DBZ[y*g.W+x] = int8(d)
			}
		}
	}
	return g, nil
}

// Geometry converts between tile pixels and km from the target.
type Geometry struct {
	TX, TY  float64 // target position in tile pixels
	KmPerPx float64
}

// GeometryFor places a target on the Haifa Bay tile (Web Mercator).
func GeometryFor(t Target) Geometry {
	world := float64(tileSize) * math.Exp2(tileZoom)
	lat := t.Lat * math.Pi / 180
	gx := (t.Lon + 180) / 360 * world
	gy := (1 - math.Log(math.Tan(lat)+1/math.Cos(lat))/math.Pi) / 2 * world
	return Geometry{
		TX:      gx - float64(tileX*tileSize),
		TY:      gy - float64(tileY*tileSize),
		KmPerPx: 40075.016686 * math.Cos(lat) / world,
	}
}

// Km returns a pixel's offset from the target in km, x east and y north.
func (g Geometry) Km(px, py float64) (east, north float64) {
	return (px - g.TX) * g.KmPerPx, (g.TY - py) * g.KmPerPx
}
