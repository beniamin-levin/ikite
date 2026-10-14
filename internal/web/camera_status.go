package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// camState is whether a camera feed shows a live picture right now.
type camState int

const (
	camUnknown camState = iota // the check failed: the feed is shown, but not called live
	camLive
	camOffline // the stream is down, or a YouTube embed would replay an ended broadcast
	camBlocked // the owner does not allow the feed on other sites
)

func (c camState) String() string {
	switch c {
	case camLive:
		return "live"
	case camOffline:
		return "offline"
	case camBlocked:
		return "blocked"
	}
	return "unknown"
}

// camFeed is one camera stream: an ipcamlive alias or a YouTube video.
// Channel, for a YouTube feed, is the channel whose current broadcast replaces
// the video once the video's broadcast has ended (the owner restarts the stream
// under a new id).
type camFeed struct {
	IPCam   string
	YouTube string
	Channel string
	Embed   string
	Label   string // i18n key, shown above the feed when a card carries two
}

// camProbe is the checked state of one feed. Embed replaces the feed's own
// embed URL when the check found the stream under another id. Site is the
// owner's page for a blocked feed.
type camProbe struct {
	State camState
	Embed string
	Site  string
}

const (
	camStatusTTL     = time.Minute
	camStatusTimeout = 6 * time.Second
	camColdWait      = 4 * time.Second
	camEmbedDomain   = "ikite.fyi"
)

type camStatusCache struct {
	mu      sync.Mutex
	probes  map[string]camProbe
	at      time.Time
	running chan struct{} // closed when the running refresh ends; nil when idle
}

func (f camFeed) key() string {
	if f.IPCam != "" {
		return "ipcam:" + f.IPCam
	}
	return "yt:" + f.YouTube
}

// youtubeEmbedURL autoplays muted: YouTube blocks an unmuted embed from
// starting on its own.
func youtubeEmbedURL(id string) string {
	return "https://www.youtube.com/embed/" + id + "?autoplay=1&mute=1"
}

// cameraStatus returns the state of every feed. A stale result is served while
// a refresh runs in the background; only the first call waits for the check.
func (s *Server) cameraStatus(feeds []camFeed) map[string]camProbe {
	c := &s.camStatus
	c.mu.Lock()
	probes, fresh := c.probes, time.Since(c.at) < camStatusTTL
	if !fresh && c.running == nil {
		done := make(chan struct{})
		c.running = done
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), camStatusTimeout)
			defer cancel()
			got := s.probeCameras(ctx, feeds)
			c.mu.Lock()
			c.probes, c.at, c.running = got, time.Now(), nil
			c.mu.Unlock()
			close(done)
		}()
	}
	running := c.running
	c.mu.Unlock()
	if probes != nil || running == nil {
		return probes
	}
	select {
	case <-running:
	case <-time.After(camColdWait):
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.probes
}

func (s *Server) probeCameras(ctx context.Context, feeds []camFeed) map[string]camProbe {
	probe := s.camProbe
	if probe == nil {
		probe = probeCamera
	}
	out := make(map[string]camProbe, len(feeds))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, f := range feeds {
		wg.Add(1)
		go func(f camFeed) {
			defer wg.Done()
			p := probe(ctx, f)
			mu.Lock()
			out[f.key()] = p
			mu.Unlock()
		}(f)
	}
	wg.Wait()
	return out
}

var camHTTP = &http.Client{Timeout: camStatusTimeout}

func probeCamera(ctx context.Context, f camFeed) camProbe {
	if f.IPCam != "" {
		return probeIPCam(ctx, f.IPCam)
	}
	p := probeYouTube(ctx, "https://www.youtube.com/watch?v="+url.QueryEscape(f.YouTube))
	if p.State == camLive || f.Channel == "" {
		return p
	}
	// YouTube walls the watch page off from the server ("confirm you're not a
	// bot"), but not the channel's /live page, which is the channel's current
	// broadcast. A channel without one cannot have this video live, and when
	// the video's broadcast has ended the channel may be live under a new id.
	ch := probeYouTube(ctx, "https://www.youtube.com/channel/"+url.PathEscape(f.Channel)+"/live")
	return channelVerdict(p, ch, youtubeEmbedURL(f.YouTube))
}

// channelVerdict combines a video's own check with its channel's /live page.
func channelVerdict(video, channel camProbe, embed string) camProbe {
	switch {
	case channel.State == camOffline:
		return camProbe{State: camOffline}
	case channel.State == camLive && channel.Embed == embed:
		return channel
	case channel.State == camLive && video.State == camOffline:
		return channel
	}
	return video
}

func camGet(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ikite.fyi camera check)")
	req.Header.Set("Accept-Language", "en")
	// Skips YouTube's cookie-consent interstitial, which has no player data.
	req.Header.Set("Cookie", "SOCS=CAI")
	resp, err := camHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func probeIPCam(ctx context.Context, alias string) camProbe {
	u := "https://ipcamlive.com/player/getcamerastreamstate.php?alias=" + url.QueryEscape(alias) +
		"&targetdomain=" + camEmbedDomain + "&getstreaminfo=1"
	body, err := camGet(ctx, u, 1<<16)
	if err != nil {
		return camProbe{}
	}
	return parseIPCamState(body)
}

// parseIPCamState reads ipcamlive's getcamerastreamstate answer. Its numbers
// come as strings or as numbers, depending on the camera.
func parseIPCamState(body []byte) camProbe {
	var r struct {
		Details    map[string]any `json:"details"`
		StreamInfo map[string]any `json:"streaminfo"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Details == nil {
		return camProbe{}
	}
	flag := func(m map[string]any, k string) bool {
		switch v := m[k].(type) {
		case bool:
			return v
		case string:
			return v == "1" || v == "true"
		case float64:
			return v == 1
		}
		return false
	}
	locked := flag(r.Details, "domainlockenabled")
	lock, _ := r.Details["domainlockurl"].(string)
	allowed, site := false, ""
	for _, d := range strings.Split(lock, ";") {
		d = strings.TrimSpace(strings.ToLower(d))
		if d == camEmbedDomain || strings.HasSuffix(d, "."+camEmbedDomain) {
			allowed = true
		}
		if site == "" && d != "" && !strings.Contains(d, "-dev") {
			site = "https://" + d + "/"
		}
	}
	if flag(r.Details, "cameracannotbeembedded") || (locked && !allowed) {
		if !locked {
			site = ""
		}
		return camProbe{State: camBlocked, Site: site}
	}
	if !flag(r.Details, "enabled") || !flag(r.Details, "streamavailable") ||
		r.StreamInfo == nil || !flag(r.StreamInfo, "connected") {
		return camProbe{State: camOffline}
	}
	return camProbe{State: camLive}
}

func probeYouTube(ctx context.Context, u string) camProbe {
	body, err := camGet(ctx, u, 4<<20)
	if err != nil {
		return camProbe{}
	}
	return parseYouTubeState(body)
}

var (
	ytCanonicalRe = regexp.MustCompile(`<link rel="canonical" href="https://www\.youtube\.com/watch\?v=([A-Za-z0-9_-]{11})"`)
	ytLiveNowRe   = regexp.MustCompile(`"isLiveNow":(true|false)`)
)

// parseYouTubeState reads a watch page (or a channel's /live page, which is the
// watch page of its current broadcast when there is one). An ended broadcast
// still embeds, but plays the old recording, so only isLiveNow counts.
func parseYouTubeState(page []byte) camProbe {
	m := ytCanonicalRe.FindSubmatch(page)
	if m == nil {
		// A channel /live page without a broadcast is the channel page.
		if strings.Contains(string(page), `<link rel="canonical" href="https://www.youtube.com/channel/`) {
			return camProbe{State: camOffline}
		}
		return camProbe{}
	}
	live := ytLiveNowRe.FindSubmatch(page)
	if live == nil {
		// Not a broadcast at all: a plain video is never a live picture.
		if strings.Contains(string(page), `"isLiveContent":false`) {
			return camProbe{State: camOffline}
		}
		return camProbe{}
	}
	if string(live[1]) != "true" {
		return camProbe{State: camOffline}
	}
	return camProbe{State: camLive, Embed: youtubeEmbedURL(string(m[1]))}
}
