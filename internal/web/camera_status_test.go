package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ben/ikite-go/internal/config"
)

func probeAll(st camState) func(context.Context, camFeed) camProbe {
	return func(context.Context, camFeed) camProbe { return camProbe{State: st} }
}

func TestParseIPCamState(t *testing.T) {
	cases := []struct {
		name, body string
		want       camState
		site       string
	}{
		{"live", `{"details":{"enabled":"1","streamavailable":"1","domainlockenabled":"0"},"streaminfo":{"connected":"1"}}`, camLive, ""},
		{"camera disconnected", `{"details":{"enabled":"1","streamavailable":"1"},"streaminfo":{"connected":"0","reconnect":"1"}}`, camOffline, ""},
		{"no stream", `{"details":{"enabled":"1","streamavailable":"0","streamid":null}}`, camOffline, ""},
		{"plan cannot embed", `{"details":{"enabled":"1","streamavailable":0,"cameracannotbeembedded":true}}`, camBlocked, ""},
		{"locked to the owner's sites", `{"details":{"enabled":"1","streamavailable":"1","domainlockenabled":"1","domainlockurl":"beachcam.co.il;mbc-dev-4c869.web.app;mybeachcam.app"},"streaminfo":{"connected":"1"}}`, camBlocked, "https://beachcam.co.il/"},
		{"locked, ikite allowed", `{"details":{"enabled":"1","streamavailable":"1","domainlockenabled":"1","domainlockurl":"example.com;ikite.fyi"},"streaminfo":{"connected":"1"}}`, camLive, ""},
		{"locked and cannot embed", `{"details":{"enabled":"1","streamavailable":0,"domainlockenabled":"1","domainlockurl":"beachcam.co.il;mbc-dev-4c869.web.app","cameracannotbeembedded":true}}`, camBlocked, "https://beachcam.co.il/"},
		{"garbage", `<html>`, camUnknown, ""},
	}
	for _, c := range cases {
		got := parseIPCamState([]byte(c.body))
		if got.State != c.want || got.Site != c.site {
			t.Errorf("%s: got %v %q, want %v %q", c.name, got.State, got.Site, c.want, c.site)
		}
	}
}

func TestParseYouTubeState(t *testing.T) {
	canon := `<link rel="canonical" href="https://www.youtube.com/watch?v=9lbGbvFREx0">`
	ended := canon + `"isLiveContent":true ... "isLiveNow":false,"startTimestamp":"2026-09-10T08:43:30+00:00","endTimestamp":"2026-09-11T02:08:15+00:00"`
	if got := parseYouTubeState([]byte(ended)); got.State != camOffline {
		t.Errorf("ended broadcast: got %v, want offline (it would replay old footage)", got.State)
	}
	live := canon + `"isLiveContent":true ... "isLiveNow":true,"startTimestamp":"2026-10-14T05:00:00+00:00"`
	if got := parseYouTubeState([]byte(live)); got.State != camLive || !strings.Contains(got.Embed, "/embed/9lbGbvFREx0") {
		t.Errorf("live broadcast: got %+v", got)
	}
	channel := `<link rel="canonical" href="https://www.youtube.com/channel/UCJB82Q8aRp7iXlVnV-0GhPw">`
	if got := parseYouTubeState([]byte(channel)); got.State != camOffline {
		t.Errorf("channel without a broadcast: got %v, want offline", got.State)
	}
	if got := parseYouTubeState([]byte(`<html>consent</html>`)); got.State != camUnknown {
		t.Errorf("unreadable page: got %v, want unknown", got.State)
	}
}

func TestChannelVerdict(t *testing.T) {
	own := youtubeEmbedURL("9lbGbvFREx0")
	other := camProbe{State: camLive, Embed: youtubeEmbedURL("NEWLIVEID01")}
	cases := []struct {
		name           string
		video, channel camProbe
		want           camState
		embed          string
	}{
		{"walled video, channel not broadcasting", camProbe{}, camProbe{State: camOffline}, camOffline, ""},
		{"walled video, channel live on it", camProbe{}, camProbe{State: camLive, Embed: own}, camLive, own},
		{"ended video, channel live elsewhere", camProbe{State: camOffline}, other, camLive, other.Embed},
		{"walled video, channel live elsewhere", camProbe{}, other, camUnknown, ""},
		{"both unreadable", camProbe{}, camProbe{}, camUnknown, ""},
	}
	for _, c := range cases {
		got := channelVerdict(c.video, c.channel, own)
		if got.State != c.want || got.Embed != c.embed {
			t.Errorf("%s: got %v %q, want %v %q", c.name, got.State, got.Embed, c.want, c.embed)
		}
	}
}

func cameraPage(t *testing.T, cam string, probe func(context.Context, camFeed) camProbe) string {
	t.Helper()
	s, err := New(&config.Config{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	s.camProbe = probe
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/camera?cam="+cam, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	return rr.Body.String()
}

// An ended YouTube broadcast still embeds and replays the recording: the page
// shows an offline box and a red dot instead.
func TestCameraOfflineShowsNoFootage(t *testing.T) {
	body := cameraPage(t, "ky", func(_ context.Context, f camFeed) camProbe {
		if f.YouTube != "" {
			return camProbe{State: camOffline}
		}
		return camProbe{State: camLive}
	})
	if strings.Contains(body, "youtube.com/embed") || strings.Contains(body, `id="featuredFrame"`) {
		t.Fatal("offline KY camera must not embed the old broadcast")
	}
	if !strings.Contains(body, `id="cameraOffline"`) || strings.Contains(body, "LIVE NOW") {
		t.Fatal("expected the offline box and no LIVE NOW badge")
	}
	if !strings.Contains(body, `<span class="dot offline"`) || !strings.Contains(body, `<span class="dot live"`) {
		t.Fatal("expected a red dot for KY and green dots for the live cameras")
	}
}

func TestCameraBlockedLinksToOwner(t *testing.T) {
	body := cameraPage(t, "hilton", func(context.Context, camFeed) camProbe {
		return camProbe{State: camBlocked, Site: "https://beachcam.co.il/"}
	})
	if strings.Contains(body, "ipcamlive.com/player") {
		t.Fatal("blocked camera must not be embedded")
	}
	if !strings.Contains(body, `href="https://beachcam.co.il/"`) || !strings.Contains(body, `<span class="dot blocked"`) {
		t.Fatal("expected a link to the owner's site and a red dot")
	}
}

// The KY channel restarted its stream under a new id: that broadcast is shown.
func TestCameraChannelFallback(t *testing.T) {
	body := cameraPage(t, "ky", func(_ context.Context, f camFeed) camProbe {
		if f.YouTube != "" {
			return camProbe{State: camLive, Embed: youtubeEmbedURL("NEWLIVEID01")}
		}
		return camProbe{State: camLive}
	})
	if strings.Count(body, "youtube.com/embed/NEWLIVEID01") != 1 || strings.Contains(body, `id="extraFrame"`) {
		t.Fatal("expected the channel's one current broadcast, once")
	}
	if !strings.Contains(body, "LIVE NOW") {
		t.Fatal("expected LIVE NOW")
	}
}

// When the check cannot be made the feed is shown, but not called live.
func TestCameraUnknownStillEmbeds(t *testing.T) {
	body := cameraPage(t, "bg", probeAll(camUnknown))
	if !strings.Contains(body, "alias=60997a5d22ca9") || strings.Contains(body, "LIVE NOW") {
		t.Fatal("expected the embed without the LIVE NOW badge")
	}
	if !strings.Contains(body, `<span class="dot unknown"`) {
		t.Fatal("expected a grey dot")
	}
}

func TestCameraSourceLink(t *testing.T) {
	cases := map[string]string{
		"kh": `href="https://ipcamlive.com/player/player.php?alias=6469b4554f45e"`,
		"ky": `href="https://www.youtube.com/channel/UCJB82Q8aRp7iXlVnV-0GhPw"`,
	}
	probe := func(_ context.Context, f camFeed) camProbe {
		if f.YouTube != "" {
			return camProbe{State: camOffline}
		}
		return camProbe{State: camLive}
	}
	for cam, want := range cases {
		if body := cameraPage(t, cam, probe); !strings.Contains(body, want) {
			t.Errorf("%s: expected source link %s", cam, want)
		}
	}
	body := cameraPage(t, "hilton", func(context.Context, camFeed) camProbe {
		return camProbe{State: camBlocked, Site: "https://beachcam.co.il/"}
	})
	if !strings.Contains(body, `class="camera-source">Source: <a href="https://beachcam.co.il/"`) {
		t.Error("hilton: expected the owner's site as the source")
	}
	body = cameraPage(t, "ky", probeAll(camLive))
	if !strings.Contains(body, `href="https://www.youtube.com/watch?v=9lbGbvFREx0"`) {
		t.Error("ky live: expected the broadcast as the source")
	}
}
