package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// playingDrive is a signed-in DriveService with a headless player behind it,
// against a stand-in API whose drive file f1 is whatever video() describes.
// tweak adjusts the service's options.
func playingDrive(t *testing.T, video func() string, tweak ...func(*DriveOptions)) (*DriveService, *PlayerService) {
	return playingDriveWith(t, nil, video, tweak...)
}

// playingDriveWith is playingDrive whose stand-in API also answers what
// routes handles (it reports whether it did).
func playingDriveWith(t *testing.T, routes func(http.ResponseWriter, *http.Request) bool, video func() string, tweak ...func(*DriveOptions)) (*DriveService, *PlayerService) {
	t.Helper()
	player := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := player.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("player startup: %v", err)
	}
	t.Cleanup(func() { _ = player.ServiceShutdown() })

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path == "/drive/v1/files/f1" {
			io.WriteString(w, video())
			return
		}
		if routes != nil && routes(w, r) {
			return
		}
		if r.URL.Path == "/vip/v1/vip/info" {
			io.WriteString(w, `{"data":{"status":"ok"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(api.Close)
	opts := DriveOptions{
		ConfigDir:   t.TempDir(),
		APIBaseURL:  api.URL,
		AuthBaseURL: api.URL,
		NoIndexSync: true,
		Logger:      quiet(),
	}
	for _, f := range tweak {
		f(&opts)
	}
	d := NewDriveService(opts, player)
	if err := d.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("drive startup: %v", err)
	}
	seedSession(t, d, "user@example.com")
	return d, player
}

// videoJSON answers a play-usage get the way production does for a
// transcoded video: the original and one HEVC transcode.
func videoJSON(origin, transcode string) string {
	return fmt.Sprintf(`{"kind":"drive#file","id":"f1","name":"clip.mp4","phase":"PHASE_TYPE_COMPLETE",
		"medias":[
		 {"media_id":"m0","media_name":"Original","is_origin":true,"is_visible":true,
		  "video":{"width":1920,"height":1080,"bit_rate":5000000},"link":{"url":%q}},
		 {"media_id":"m1","media_name":"480P","is_default":true,"is_visible":true,
		  "video":{"width":852,"height":480,"bit_rate":500000},"link":{"url":%q}}]}`, origin, transcode)
}

// serveClip serves data on every path but /broken, and counts the requests
// each path got.
func serveClip(t *testing.T, data []byte) (*httptest.Server, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/broken" {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "clip.mp4", time.Unix(0, 0), bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
}

// watchState samples what the UI would be shown until the returned stop is
// called, and reports every sample that check rejects.
func watchState(p *PlayerService, check func(PlayerState) string) (stop func() []string) {
	var mu sync.Mutex
	var bad []string
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
				if problem := check(p.State()); problem != "" {
					mu.Lock()
					bad = append(bad, problem)
					mu.Unlock()
				}
			}
		}
	}()
	return func() []string {
		close(done)
		<-finished
		mu.Lock()
		defer mu.Unlock()
		return bad
	}
}

func landedNear(t *testing.T, p *PlayerService, want float64) {
	t.Helper()
	waitFor(t, 10*time.Second, "playback to land", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return !p.seek.pending && p.swap == nil
	})
	if pos := p.State().Position; math.Abs(pos-want) > 0.4 {
		t.Errorf("position = %.2f, want about %.2f", pos, want)
	}
}

// Switching renditions is a reload, but nothing on screen may say so: the
// position and duration hold, a paused video stays paused where it was, and
// the choice outlives the video — the next one starts in it.
func TestQualitySwitchKeepsPlace(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	media, _ := serveClip(t, clip)
	d, p := playingDrive(t, func() string { return videoJSON(media.URL+"/origin", media.URL+"/t480") })

	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 20*time.Second, "duration to be known", func() bool { return p.State().Duration > 1.5 })
	q := p.State().Quality
	if len(q.Options) != 2 || q.Current != OriginKey || q.Choice != AutoQuality {
		t.Fatalf("quality after play = %+v, want auto on the original of two options", q)
	}
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := p.SeekTo(1.0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	landedNear(t, p, 1.0)

	stop := watchState(p, func(s PlayerState) string {
		if s.Duration <= 0 || math.Abs(s.Position-1.0) > 0.4 || s.Error != "" || s.Loading {
			return fmt.Sprintf("pos=%.2f dur=%.2f err=%q loading=%v", s.Position, s.Duration, s.Error, s.Loading)
		}
		return ""
	})
	if err := d.SetPlaybackQuality("480"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	waitFor(t, 20*time.Second, "the 480 rendition to take over", func() bool {
		q := p.State().Quality
		return q.Current == "480" && q.Switching == ""
	})
	landedNear(t, p, 1.0)
	if bad := stop(); len(bad) > 0 {
		t.Errorf("the switch showed %d jumps, first %s", len(bad), bad[0])
	}
	state := p.State()
	if !state.Paused {
		t.Error("the switch resumed a paused video")
	}
	if state.Source != media.URL+"/t480" || state.Quality.Choice != "480" {
		t.Errorf("source = %q, choice = %q", state.Source, state.Quality.Choice)
	}

	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play again: %v", err)
	}
	waitFor(t, 20*time.Second, "the next play to load", func() bool { return !p.State().Loading })
	if q := p.State().Quality; q.Current != "480" || q.Choice != "480" {
		t.Errorf("next play quality = %+v, want it to start in the saved 480", q)
	}
}

// A rendition that will not open leaves the one playing in charge: same
// place, same pause, no player error — only the switch says it failed.
func TestFailedQualitySwitchStaysOnCurrent(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	media, hits := serveClip(t, clip)
	d, p := playingDrive(t, func() string { return videoJSON(media.URL+"/origin", media.URL+"/broken") })

	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 20*time.Second, "duration to be known", func() bool { return p.State().Duration > 1.5 })
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := p.SeekTo(1.0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	landedNear(t, p, 1.0)

	stop := watchState(p, func(s PlayerState) string {
		if s.Error != "" || s.Duration <= 0 {
			return fmt.Sprintf("err=%q dur=%.2f", s.Error, s.Duration)
		}
		return ""
	})
	if err := d.SetPlaybackQuality("480"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	waitFor(t, 20*time.Second, "the switch to fail", func() bool {
		q := p.State().Quality
		return q.Failed == "480" && q.Switching == ""
	})
	landedNear(t, p, 1.0)
	if bad := stop(); len(bad) > 0 {
		t.Errorf("the failed switch surfaced as a player failure: %v", bad[0])
	}
	state := p.State()
	if state.Quality.Current != OriginKey || state.Source != media.URL+"/origin" {
		t.Errorf("after the failure current = %q, source = %q; want the original", state.Quality.Current, state.Source)
	}
	if !state.Paused {
		t.Error("the failed switch resumed a paused video")
	}
	if hits("/broken") == 0 {
		t.Error("mpv never asked for the new rendition: the failure path was not exercised")
	}
	var path string
	if err := p.mpv.GetProperty(context.Background(), "path", &path); err != nil || path != p.current.URL {
		t.Errorf("mpv plays %q (%v), want the original's handle back", path, err)
	}
}

// The audio track the user picked and subtitles they switched off survive
// the reload, although mpv picks its defaults afresh for every file.
func TestQualitySwitchKeepsTrackChoice(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "subs.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:02,000\nhello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "tracks.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", fixture, "-i", fixture, "-i", srt,
		"-map", "0:v", "-map", "0:a", "-map", "1:a", "-map", "2:s", "-c:v", "copy", "-c:a", "copy", "-c:s", "mov_text",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=jpn", "-metadata:s:s:0", "language=chi",
		"-disposition:s:0", "default", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	requested, release := make(chan struct{}), make(chan struct{})
	var requestOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/t480" {
			requestOnce.Do(func() { close(requested) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "tracks.mp4", time.Unix(0, 0), bytes.NewReader(data))
	}))
	t.Cleanup(media.Close)
	t.Cleanup(unblock)
	var metadataMu sync.Mutex
	metadata, failMetadata := 0, false
	d, p := playingDrive(t, func() string {
		metadataMu.Lock()
		defer metadataMu.Unlock()
		metadata++
		if failMetadata {
			return "{" // A second request must be rejected before fetching links.
		}
		return videoJSON(media.URL+"/origin", media.URL+"/t480")
	})

	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 20*time.Second, "duration to be known", func() bool { return p.State().Duration > 1.5 })
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	tracks, err := p.Tracks()
	if err != nil {
		t.Fatal(err)
	}
	if selectedID(tracks, "sub") == 0 {
		t.Fatal("the fixture's default subtitle was not picked, so the test proves nothing")
	}
	jpn := matchTrack(tracks, "audio", trackRef{known: true, lang: "jpn"})
	if err := p.SelectTrack("audio", jpn); err != nil {
		t.Fatal(err)
	}
	if err := p.SelectTrack("subtitle", 0); err != nil {
		t.Fatal(err)
	}

	if err := d.SetPlaybackQuality("480"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("the player did not request the transcode")
	}
	p.loadMu.Lock()
	locked := true
	defer func() {
		if locked {
			p.loadMu.Unlock()
		}
	}()
	unblock()
	waitFor(t, 20*time.Second, "the loaded rendition to wait for track restoration", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.state.Quality.Current == "480" && p.restoring != 0
	})
	p.mu.Lock()
	gen := p.gen
	p.mu.Unlock()
	metadataMu.Lock()
	before := metadata
	failMetadata = true
	metadataMu.Unlock()
	if err := d.SetPlaybackQuality(OriginKey); err != errSwitchBusy {
		t.Errorf("switch during pending restoration = %v, want busy", err)
	}
	metadataMu.Lock()
	after := metadata
	failMetadata = false
	metadataMu.Unlock()
	p.mu.Lock()
	afterGen, switching := p.gen, p.state.Quality.Switching
	p.mu.Unlock()
	if after != before || afterGen != gen || switching != "480" {
		t.Errorf("pending restoration was superseded: metadata=%d/%d generation=%d/%d switching=%q",
			before, after, gen, afterGen, switching)
	}
	p.loadMu.Unlock()
	locked = false
	waitFor(t, 10*time.Second, "the quality switch to finish restoring tracks", func() bool {
		return p.State().Quality.Switching == ""
	})
	waitFor(t, 10*time.Second, "the japanese track with subtitles off", func() bool {
		tracks, err := p.Tracks()
		if err != nil {
			return false
		}
		audio := selectedTrack(tracks, "audio")
		return audio.lang == "jpn" && selectedID(tracks, "sub") == 0
	})
}

func TestQualitySwitchRemainsBusyDuringRecovery(t *testing.T) {
	for _, phase := range []string{"reverting", "restoring"} {
		t.Run(phase, func(t *testing.T) {
			p := &PlayerService{
				fileID: "f1", gen: 7,
				state: PlayerState{Quality: QualityState{Current: OriginKey, Choice: OriginKey}},
			}
			if phase == "reverting" {
				p.swap = &swap{reverting: true}
			} else {
				p.restoring = 1
			}
			before := p.state.Quality
			if _, err := p.beginSwitch("f1", "480", "480", false); err != errSwitchBusy {
				t.Errorf("switch during %s = %v, want busy", phase, err)
			}
			if p.gen != 7 || p.state.Quality.Choice != before.Choice || p.state.Quality.Switching != before.Switching {
				t.Errorf("rejected switch mutated the pending recovery: gen=%d quality=%+v", p.gen, p.state.Quality)
			}
		})
	}
}

// slowReader serves at about rate bytes a second.
type slowReader struct {
	*bytes.Reader
	rate int
}

func (s slowReader) Read(b []byte) (int, error) {
	if len(b) > 2048 {
		b = b[:2048]
	}
	n, err := s.Reader.Read(b)
	time.Sleep(time.Duration(n) * time.Second / time.Duration(s.rate))
	return n, err
}

// Auto steps down when the network cannot carry the rendition; a fixed
// choice is left alone however much it stalls — the user asked for it.
func TestAutoStepsDownWhenPlaybackStalls(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), "long.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440", "-t", "20", "-c:v", "libx264", "-b:v", "600k",
		"-c:a", "aac", "-b:a", "64k", "-movflags", "+faststart", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	grace, window, limit := stallGrace, stallWindow, stallLimit
	stallGrace, stallWindow, stallLimit = 300*time.Millisecond, 10*time.Second, 1500*time.Millisecond
	t.Cleanup(func() { stallGrace, stallWindow, stallLimit = grace, window, limit })

	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.ReadSeeker = bytes.NewReader(data)
		if r.URL.Path == "/origin" {
			body = slowReader{bytes.NewReader(data), 24 << 10}
		}
		http.ServeContent(w, r, "long.mp4", time.Unix(0, 0), body)
	}))
	t.Cleanup(media.Close)
	d, p := playingDrive(t, func() string { return videoJSON(media.URL+"/origin", media.URL+"/t480") })

	if err := d.SetPlaybackQuality(OriginKey); err != nil {
		t.Fatalf("choose the original: %v", err)
	}
	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 30*time.Second, "the original to stall past the limit", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.stall.stalled(time.Now(), false) >= 2*stallLimit
	})
	time.Sleep(2 * stateInterval)
	if q := p.State().Quality; q.Current != OriginKey || q.Switching != "" {
		t.Fatalf("a fixed choice moved: %+v", q)
	}

	if err := d.SetPlaybackQuality(AutoQuality); err != nil {
		t.Fatalf("choose auto: %v", err)
	}
	waitFor(t, 30*time.Second, "auto to step down", func() bool {
		q := p.State().Quality
		return q.Current == "480" && q.Switching == ""
	})
	if q := p.State().Quality; !q.Lowered || q.Choice != AutoQuality {
		t.Errorf("after the step down quality = %+v, want auto, lowered", q)
	}
}
