package squall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ben/ikite-go/internal/store"
)

// The IMS rain radar (https://ims.gov.il/en/RadarSatellite) publishes a frame
// every 5 minutes, about 5 minutes after the scan — twice as often as
// RainViewer. Its images are rain-only overlays (940×940) whose map extent IMS
// does not publish, so until that is fitted against RainViewer on a rainy day
// they are archived for analysis but do not drive alerts.

const (
	imsRadarIndex = "https://ims.gov.il/he/radar_satellite"
	imsRadarHost  = "https://ims.gov.il"
)

type imsRadarResp struct {
	Data struct {
		Types struct {
			IMSRadar []struct {
				ForecastTime string `json:"forecast_time"` // local wall clock
				FileName     string `json:"file_name"`
			} `json:"IMSRadar"`
		} `json:"types"`
		RadarColorbar json.RawMessage `json:"radar_colorbar"`
	} `json:"data"`
}

// archiveIMSRadar stores any IMS radar frames not yet archived. It returns how
// many were added.
func (s *Service) archiveIMSRadar(now time.Time) (int, error) {
	if s.ArchiveDir == "" {
		return 0, nil
	}
	last, err := s.Store.LatestIMSRadarFrame()
	if err != nil {
		return 0, err
	}
	// Frames come every 5 min; don't ask IMS more often than that.
	if last != nil && now.Sub(last.FetchedAt) < 4*time.Minute {
		return 0, nil
	}
	body, err := s.Radar.get(imsRadarIndex)
	if err != nil {
		return 0, err
	}
	var r imsRadarResp
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, fmt.Errorf("decode IMS radar index: %w", err)
	}
	dir := filepath.Join(s.ArchiveDir, "ims")
	if len(r.Data.RadarColorbar) > 0 {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "colorbar.json"), r.Data.RadarColorbar, 0o644)
	}
	added := 0
	for _, f := range r.Data.Types.IMSRadar {
		at, err := time.ParseInLocation("2006-01-02 15:04:05", f.ForecastTime, s.TZ)
		if err != nil || !strings.HasPrefix(f.FileName, "/") {
			continue
		}
		if last != nil && !at.After(last.FrameAt) {
			continue
		}
		if free, ok := freeBytes(s.ArchiveDir); ok && free < minFreeBytes {
			return added, nil
		}
		img, err := s.Radar.get(imsRadarHost + f.FileName)
		if err != nil {
			s.Log.Warn("ims radar frame", "frame", f.FileName, "err", err)
			continue
		}
		rel := filepath.Join("ims", at.Format("2006/01/02"), at.Format("1504")+".png")
		full := filepath.Join(s.ArchiveDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return added, err
		}
		if err := os.WriteFile(full, img, 0o644); err != nil {
			return added, err
		}
		if err := s.Store.InsertIMSRadarFrame(store.IMSRadarFrame{FrameAt: at, FetchedAt: now,
			Source: f.FileName, PNGFile: rel, Bytes: len(img)}); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}
