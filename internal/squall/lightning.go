package squall

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"time"
)

// Lightning comes from EUMETSAT's MTG Lightning Imager ("LI Accumulated Flash
// Area", 5-minute frames, published ~10 min after the scan), free with
// attribution. It is the earliest sign of a squall found so far: on 2026-10-06
// flashes were 25 km west of Kiryat Haim at 08:55 and 8 km away at 09:40 — the
// gust front hit at 10:07, while the nearest wind meter upwind gave 12 minutes.

const (
	lightningWMS = "https://view.eumetsat.int/geoserver/wms?service=WMS&version=1.3.0&request=GetMap" +
		"&layers=mtg_fd:li_afa&styles=&crs=EPSG:4326&format=image/png&transparent=true"
	// The window fetched: 31.5–34.5°N, 33.0–36.5°E at 1/120° a pixel (~0.8 km).
	lightningS, lightningN = 31.5, 34.5
	lightningW, lightningE = 33.0, 36.5
	lightningPxW           = 420
	lightningPxH           = 360
	lightningStep          = 5 * time.Minute
	// Flashes this close count from any direction; further out only on the sea
	// side (south through west to north), where Kiryat Haim's storms come from —
	// lightning over the Galilee drifting inland is not a threat.
	lightningAnyDirKm = 12
)

// LightningScan is what one lightning frame shows around the target.
type LightningScan struct {
	Px         int     // pixels with flashes in the whole window
	NearestKm  float64 // nearest flash, any direction; +Inf if none
	NearestBrg float64
	ThreatKm   float64 // nearest flash on the sea side, or within lightningAnyDirKm; +Inf if none
	ThreatBrg  float64
	Within40Px int
}

func lightningURL(at time.Time) string {
	return fmt.Sprintf("%s&bbox=%.1f,%.1f,%.1f,%.1f&width=%d&height=%d&time=%s", lightningWMS,
		lightningS, lightningW, lightningN, lightningE, lightningPxW, lightningPxH, at.UTC().Format("2006-01-02T15:04:05Z"))
}

// errNotPublished: EUMETSAT answers 502 for a frame it has not published yet.
var errNotPublished = fmt.Errorf("lightning frame not published yet")

// LightningFrame downloads the frame for a 5-minute slot.
func (c *Client) LightningFrame(at time.Time) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, lightningURL(at), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ikite.fyi squall alert")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusNotFound {
		return nil, errNotPublished
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lightning %s: status %d", at.UTC().Format("15:04Z"), resp.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	if ct := resp.Header.Get("Content-Type"); len(ct) < 9 || ct[:9] != "image/png" {
		return nil, fmt.Errorf("lightning %s: got %s, not a PNG", at.UTC().Format("15:04Z"), ct)
	}
	return buf.Bytes(), nil
}

// AnalyseLightning measures flashes around the target.
func AnalyseLightning(pngBytes []byte, t Target) (LightningScan, error) {
	sc := LightningScan{NearestKm: math.Inf(1), ThreatKm: math.Inf(1)}
	im, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return sc, fmt.Errorf("decode lightning png: %w", err)
	}
	b := im.Bounds()
	degX := (lightningE - lightningW) / float64(b.Dx())
	degY := (lightningN - lightningS) / float64(b.Dy())
	kmX := degX * 111.32 * math.Cos(t.Lat*math.Pi/180)
	kmY := degY * 110.57
	tx := (t.Lon - lightningW) / degX
	ty := (lightningN - t.Lat) / degY
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := color.NRGBAModel.Convert(im.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			if c.A < 128 {
				continue // anti-aliased fringe, not a flash
			}
			sc.Px++
			e, n := (float64(x)+0.5-tx)*kmX, (ty-float64(y)-0.5)*kmY
			d := math.Hypot(e, n)
			brg := math.Mod(math.Atan2(e, n)*180/math.Pi+360, 360)
			if d <= 40 {
				sc.Within40Px++
			}
			if d < sc.NearestKm {
				sc.NearestKm, sc.NearestBrg = d, brg
			}
			if (seaSide(brg) || d <= lightningAnyDirKm) && d < sc.ThreatKm {
				sc.ThreatKm, sc.ThreatBrg = d, brg
			}
		}
	}
	return sc, nil
}

// seaSide: bearings from the spot over the bay and the sea, S through W to N.
func seaSide(brg float64) bool { return brg >= 170 || brg <= 20 }

// lightningOn reports whether the lightning rule is enabled, and its range.
func (p Params) lightningOn() (float64, bool) {
	if p.LightningKm < 0 {
		return 0, false
	}
	return p.LightningKm, true
}

// LightningSample is a stored lightning frame for replays.
type LightningSample struct {
	FrameAt, AvailAt time.Time
	ThreatKm         float64 // +Inf when no flash on the sea side
	ThreatBrg        float64
	// Backfilled: fetched afterwards for a replay, not live — no alert could
	// have gone out from it.
	Backfilled bool
}

// SimulateLightning replays the lightning alert rule.
func SimulateLightning(samples []LightningSample, p Params) []SimAlert {
	km, on := p.Sane().lightningOn()
	if !on {
		return nil
	}
	var out []SimAlert
	for _, s := range samples {
		if n := len(out); n > 0 && s.AvailAt.Sub(out[n-1].At) < cooldown {
			continue
		}
		if s.ThreatKm <= km && s.AvailAt.Sub(s.FrameAt) <= 30*time.Minute {
			out = append(out, SimAlert{At: s.AvailAt, FrameAt: s.FrameAt, ETAMin: -1})
		}
	}
	return out
}
