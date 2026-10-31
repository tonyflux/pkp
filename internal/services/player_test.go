package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// fixture is shared with the mpv package: a 2 second H.264/AAC clip.
const fixture = "../mpv/testdata/clip.mp4"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// TestPlaybackThroughProxy exercises the full chain the desktop client relies
// on: publish an authenticated remote URL on the loopback proxy, hand the
// local URL to mpv, and drive it from the service API. The upstream only ever
// sees the injected credentials, and mpv only ever sees 127.0.0.1.
func TestPlaybackThroughProxy(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var mu sync.Mutex
	var auth string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		http.ServeContent(w, r, "clip.mp4", time.Unix(0, 0), bytes.NewReader(clip))
	}))
	defer origin.Close()

	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	localURL, err := svc.Open(origin.URL, map[string]string{"Authorization": "Bearer tok"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if base := svc.ProxyBaseURL(); base == "" || localURL[:len(base)] != base {
		t.Fatalf("local url %q is not served by the proxy at %q", localURL, base)
	}

	waitFor(t, 20*time.Second, "duration to be known", func() bool {
		return svc.State().Duration > 1.5
	})

	if state := svc.State(); state.Source != origin.URL {
		t.Errorf("Source = %q, want the upstream url %q", state.Source, origin.URL)
	}

	// Pause first so the 2 second clip cannot run to EOF while we seek.
	if err := svc.SetPaused(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := svc.SeekTo(1.0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	waitFor(t, 10*time.Second, "position to reach the seek target", func() bool {
		pos := svc.State().Position
		return pos >= 0.8 && pos <= 1.4
	})

	paused, err := svc.TogglePause()
	if err != nil {
		t.Fatalf("toggle pause: %v", err)
	}
	if paused {
		t.Error("TogglePause left playback paused, want resumed")
	}

	tracks, err := svc.Tracks()
	if err != nil {
		t.Fatalf("tracks: %v", err)
	}
	var kinds []string
	for _, tr := range tracks {
		kinds = append(kinds, tr.Type)
	}
	if len(tracks) < 2 {
		t.Errorf("tracks = %v, want at least a video and an audio track", kinds)
	}

	mu.Lock()
	gotAuth := auth
	mu.Unlock()
	if gotAuth != "Bearer tok" {
		t.Errorf("upstream Authorization = %q, want %q", gotAuth, "Bearer tok")
	}

	stats := svc.StreamStats()
	if stats.Requests == 0 {
		t.Error("proxy served no requests")
	}
	if stats.BytesServed == 0 {
		t.Error("proxy served no bytes")
	}
}

// The picture comes from the render surface, which a headless player has
// not got: nothing is written and the error says why. Naming is pure:
// "<stem> HH.MM.SS.png", " (n)" while taken, separators and extension
// stripped from the stem.
func TestScreenshotNeedsSurfaceAndNamesByPosition(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.mp4", time.Unix(0, 0), bytes.NewReader(clip))
	}))
	defer origin.Close()

	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	if _, err := svc.Screenshot("clip.mp4"); err == nil || !strings.Contains(err.Error(), "现在没有在播放") {
		t.Errorf("Screenshot with nothing playing: %v, want nothing is playing", err)
	}
	if _, err := svc.Open(origin.URL, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	waitFor(t, 20*time.Second, "duration to be known", func() bool { return svc.State().Duration > 1.5 })
	if _, err := svc.Screenshot("clip.mp4"); err == nil || !strings.Contains(err.Error(), "surface") {
		t.Errorf("headless Screenshot: %v, want a no-surface error", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "Pictures", "PKP")); len(entries) != 0 {
		t.Errorf("a failed screenshot left files behind: %v", entries)
	}

	dir := t.TempDir()
	want := filepath.Join(dir, "a_b 01.02.03.png")
	if got := screenshotPath(dir, "a/b.mp4", 3723.9); got != want {
		t.Errorf("screenshotPath = %q, want %q", got, want)
	}
	if err := os.WriteFile(want, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := screenshotPath(dir, "a/b.mp4", 3723.9), filepath.Join(dir, "a_b 01.02.03 (2).png"); got != want {
		t.Errorf("screenshotPath with the name taken = %q, want %q", got, want)
	}
	if got, want := screenshotPath(dir, ".mp4", 0), filepath.Join(dir, "截图 00.00.00.png"); got != want {
		t.Errorf("screenshotPath with an empty stem = %q, want %q", got, want)
	}
}

// Show in Finder after a screenshot: the file selected while it is there, the
// folder once it is gone, and never a path outside the screenshot folder —
// the page hands the path back.
func TestRevealScreenshotStaysInItsFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var revealed []string
	reveal := revealPath
	revealPath = func(path string, selectFile bool) error {
		revealed = append(revealed, fmt.Sprintf("%s|%v", path, selectFile))
		return nil
	}
	t.Cleanup(func() { revealPath = reveal })

	dir := filepath.Join(home, "Pictures", "PKP")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(dir, "clip 00.00.01.png")
	if err := os.WriteFile(shot, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	for _, path := range []string{shot, filepath.Join(dir, "gone.png")} {
		if err := svc.RevealScreenshot(path); err != nil {
			t.Fatalf("RevealScreenshot(%q): %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(home, "secret.txt"), filepath.Join(dir, "..", "secret.txt"), dir} {
		if err := svc.RevealScreenshot(path); err == nil {
			t.Errorf("RevealScreenshot(%q) went ahead, want refused", path)
		}
	}
	if want := []string{shot + "|true", dir + "|false"}; !slices.Equal(revealed, want) {
		t.Errorf("revealed %v, want %v", revealed, want)
	}
}

// An MP4 names its tracks where mpv does not look: the udta name, or a
// handler name such as Mandarin — but not the muxer's own SoundHandler.
func TestTrackTitleFromMP4Metadata(t *testing.T) {
	for _, c := range []struct {
		metadata map[string]string
		want     string
	}{
		{map[string]string{"handler_name": "国语", "language": "chi"}, "国语"},
		{map[string]string{"handler_name": "SoundHandler", "name": "导演评论"}, "导演评论"},
		{map[string]string{"handler_name": "SoundHandler"}, ""},
		{map[string]string{"handler_name": "Core Media Audio"}, ""},
		{map[string]string{"handler_name": "ISO Media file produced by Google Inc."}, ""},
		{map[string]string{"handler_name": "GPAC ISO Audio Handler"}, ""},
		{nil, ""},
	} {
		if got := trackTitle(c.metadata); got != c.want {
			t.Errorf("trackTitle(%v) = %q, want %q", c.metadata, got, c.want)
		}
	}
}

// A click gets its feedback from Loading: raised before the link lookup,
// held while the previous file is still what the state describes, lowered
// when mpv has the new file open — or when the new file fails, so the
// indicator never sticks over an error.
func TestLoadingSpansRequestUntilFileLoaded(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.ServeContent(w, r, "clip.mp4", time.Unix(0, 0), bytes.NewReader(clip))
	}))
	defer origin.Close()

	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	svc.setLoading(true)
	if !svc.State().Loading {
		t.Fatal("Loading should be raised before the link lookup")
	}
	if _, err := svc.Open(origin.URL, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	waitFor(t, 20*time.Second, "mpv to report the file loaded", func() bool {
		return !svc.State().Loading
	})
	if s := svc.State(); s.Duration <= 1.5 || s.Error != "" {
		t.Errorf("state after load = %+v, want the clip open without error", s)
	}

	// Switching to a file that cannot play: Loading rises on the request and
	// falls with the error, while the first clip's duration is still in the
	// state — which is exactly why Loading exists.
	if _, err := svc.Open(origin.URL+"/broken", nil); err != nil {
		t.Fatalf("open broken: %v", err)
	}
	waitFor(t, 20*time.Second, "the failed load to finish", func() bool {
		s := svc.State()
		return !s.Loading && s.Error != ""
	})

	if err := svc.StopPlayback(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if svc.State().Loading {
		t.Error("stop must lower Loading")
	}
}

// Stopping must revoke the proxy handle, otherwise a direct link keeps working
// after the user closed the file.
func TestStopRevokesProxyHandle(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.mp4", time.Unix(0, 0), bytes.NewReader(clip))
	}))
	defer origin.Close()

	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	localURL, err := svc.Open(origin.URL, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := svc.StopPlayback(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	resp, err := http.Get(localURL)
	if err != nil {
		t.Fatalf("get revoked url: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("revoked handle status = %d, want 404", resp.StatusCode)
	}
}

// Between the seek command and mpv restarting playback, time-pos still
// describes the old place. Reporting that for even one tick made the progress
// bar jump back and then forward; the target has to stand in until mpv lands.
func TestSeekReportsTargetUntilPlaybackRestarts(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.mp4", time.Unix(0, 0), bytes.NewReader(clip))
	}))
	defer origin.Close()

	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	if _, err := svc.Open(origin.URL, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	waitFor(t, 20*time.Second, "duration to be known", func() bool {
		return svc.State().Duration > 1.5
	})
	if err := svc.SetPaused(true); err != nil {
		t.Fatalf("pause: %v", err)
	}

	if err := svc.SeekTo(1.5); err != nil {
		t.Fatalf("seek: %v", err)
	}
	// Immediately after the command mpv has not moved yet; the reported
	// position must already be the target, never the stale one.
	if pos := svc.State().Position; pos != 1.5 {
		t.Fatalf("position right after SeekTo = %v, want the target 1.5", pos)
	}

	waitFor(t, 10*time.Second, "mpv to report landing", func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return !svc.seek.pending
	})
	// The hold was released by playback-restart, not by the time limit.
	svc.mu.Lock()
	held := time.Since(svc.seek.since)
	svc.mu.Unlock()
	if held >= seekHoldLimit {
		t.Errorf("seek hold lasted %v, want release on playback-restart well under %v", held, seekHoldLimit)
	}
	if pos := svc.State().Position; pos < 0.8 || pos > 1.6 {
		t.Errorf("position after landing = %v, want near the 1.5 target", pos)
	}

	// A relative step while paused composes on the landed position.
	if err := svc.Seek(-1); err != nil {
		t.Fatalf("relative seek: %v", err)
	}
	if pos := svc.State().Position; pos < 0 || pos > 0.7 {
		t.Errorf("position right after Seek(-1) = %v, want about 0.5", pos)
	}
}

// keep-open parks mpv paused on the last frame, and pause is a global
// property: the next file of a playlist used to load and sit there until the
// user pressed play.
func TestNextFileAfterEOFPlaysUnpaused(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.mp4", time.Unix(0, 0), bytes.NewReader(clip))
	}))
	defer origin.Close()

	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	if _, err := svc.Open(origin.URL, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	waitFor(t, 20*time.Second, "the first file to reach eof", func() bool {
		return svc.State().EOF
	})
	waitFor(t, 5*time.Second, "keep-open to pause on the last frame", func() bool {
		return svc.State().Paused
	})

	if _, err := svc.Open(origin.URL+"/next", nil); err != nil {
		t.Fatalf("open next: %v", err)
	}
	waitFor(t, 20*time.Second, "the next file to be playing", func() bool {
		state := svc.State()
		return !state.EOF && !state.Paused && state.Position > 0.2
	})
}

// Windows and in-flight requests own independent URLs; closing one must not
// break another picture or PDF.
func TestPreviewWindowIsolation(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "png")
	}))
	defer origin.Close()

	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	status := func(url string) int {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	first, err := svc.openPreview(1, origin.URL+"/a.png", nil, "")
	if err != nil {
		t.Fatalf("open first: %v", err)
	}
	if status(first) != http.StatusOK {
		t.Fatal("first preview not served")
	}
	second, err := svc.openPreview(2, origin.URL+"/b.png", nil, "")
	if err != nil {
		t.Fatalf("open second: %v", err)
	}
	third, err := svc.openPreview(2, origin.URL+"/c.pdf", nil, "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	svc.closePreview(2, second)
	if status(second) != http.StatusNotFound || status(third) != http.StatusOK {
		t.Fatal("revoking an old request changed the newer preview")
	}
	svc.closePreview(2, first)
	if status(first) != http.StatusOK {
		t.Error("another window revoked the first preview")
	}
	if status(third) != http.StatusOK {
		t.Error("third preview not served")
	}
	svc.closePreviewWindow(1)
	if status(first) != http.StatusNotFound {
		t.Error("closed window still serves its preview")
	}
	if status(third) != http.StatusOK {
		t.Fatal("closing first window revoked the third")
	}
	if _, err := svc.openPreview(1, origin.URL+"/late.png", nil, ""); err == nil {
		t.Error("a closed window published an in-flight preview")
	}
	svc.closePreview(2, "")
	if status(third) != http.StatusNotFound {
		t.Error("preview still served after ClosePreview")
	}
}
