//go:build darwin && cgo

package services

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tonyflux/pkp/internal/pcm"
	"github.com/tonyflux/pkp/internal/subsrelay"
)

// relayEvent is one thing the stand-in subsrelay received.
type relayEvent struct {
	kind    string // "start", "frame" or a control's type
	start   map[string]any
	pos     int64   // a frame's position_ms
	playPos float64 // where the player was when it arrived
	hz      float64 // zero-crossing estimate of a frame's PCM frequency
}

// fakeRelay is a stand-in subsrelay deployment that records what a session
// sends, as the real server would parse it.
type fakeRelay struct {
	srv      *httptest.Server
	player   *PlayerService
	node     func(w http.ResponseWriter) bool
	stopGate <-chan struct{}

	mu      sync.Mutex
	events  []relayEvent
	auth    string
	lookups int
	conn    *websocket.Conn
	open    int
	maxOpen int
}

func newFakeRelay(t *testing.T) *fakeRelay {
	f := &fakeRelay{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subsrelay/v1/node":
			f.mu.Lock()
			f.auth = r.Header.Get("Authorization")
			f.lookups++
			f.mu.Unlock()
			if f.node != nil && f.node(w) {
				return
			}
			fmt.Fprintf(w, `{"url":%q,"supported_languages":[{"code":"zh","name":"Chinese"}],"quota":{"limit":14400,"usage":0}}`,
				"ws"+strings.TrimPrefix(f.srv.URL, "http")+"/subsrelay/v1/stream")
		case "/subsrelay/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			f.mu.Lock()
			f.open++
			f.maxOpen = max(f.maxOpen, f.open)
			f.conn = conn
			f.mu.Unlock()
			defer func() {
				f.mu.Lock()
				conn.CloseNow()
				f.open--
				f.mu.Unlock()
			}()
			f.serve(conn)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelay) serve(conn *websocket.Conn) {
	ctx := context.Background()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		ev := relayEvent{playPos: f.player.State().Position}
		if typ == websocket.MessageBinary {
			n := int(binary.BigEndian.Uint16(data))
			var h struct {
				PositionMS int64 `json:"position_ms"`
			}
			json.Unmarshal(data[2:2+n], &h)
			ev.kind, ev.pos = "frame", h.PositionMS
			samples := data[2+n:]
			crossings := 0
			for i := 2; i+1 < len(samples); i += 2 {
				prev := int16(binary.LittleEndian.Uint16(samples[i-2:]))
				curr := int16(binary.LittleEndian.Uint16(samples[i:]))
				if (prev < 0) != (curr < 0) {
					crossings++
				}
			}
			if len(samples) > 0 {
				ev.hz = float64(crossings*pcm.SampleRate) / float64(len(samples))
			}
		} else {
			json.Unmarshal(data, &ev.start)
			ev.kind, _ = ev.start["type"].(string)
		}
		f.mu.Lock()
		f.events = append(f.events, ev)
		f.mu.Unlock()
		if ev.kind == "stop" {
			if f.stopGate != nil {
				<-f.stopGate
			}
			conn.Write(ctx, websocket.MessageText, []byte(`{"type":"stop"}`))
			return
		}
	}
}

func (f *fakeRelay) snapshot() []relayEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]relayEvent(nil), f.events...)
}

// find is the index of the first event of kind at or after from, or -1.
func (f *fakeRelay) find(from int, kind string) int {
	for i, ev := range f.snapshot()[from:] {
		if ev.kind == kind {
			return from + i
		}
	}
	return -1
}

func (f *fakeRelay) send(t *testing.T, msg string) {
	t.Helper()
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("relay send: %v", err)
	}
}

// captions records what the engine draws over the video.
func captions(t *testing.T) func() []string {
	var mu sync.Mutex
	var drawn []string
	old := drawCaption
	drawCaption = func(_ *PlayerService, text string, _ captionLook) {
		mu.Lock()
		drawn = append(drawn, text)
		mu.Unlock()
	}
	t.Cleanup(func() { drawCaption = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), drawn...)
	}
}

// aiStopped waits until no session is left running, so a test's last
// cleanup cannot race one.
func aiStopped(t *testing.T, d *DriveService) {
	t.Helper()
	waitFor(t, 10*time.Second, "the AI subtitle session to close", func() bool {
		d.ai.mu.Lock()
		last, looping := d.ai.last, d.ai.looping
		d.ai.mu.Unlock()
		if looping {
			return false
		}
		if last == nil {
			return true
		}
		select {
		case <-last:
			return true
		default:
			return false
		}
	})
}

const testGCID = "0123456789ABCDEF0123456789ABCDEF01234567"

// speechClip renders a 30 s clip with sound.
func speechClip(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), "speech.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// speechFile describes f1 as a video with a GCID, its original at base's
// /origin and its one transcode at /t480.
func speechFile(base string) func() string {
	return func() string {
		return fmt.Sprintf(`{"kind":"drive#file","id":"f1","name":"speech.mp4","phase":"PHASE_TYPE_COMPLETE","hash":%q,
			"medias":[
			 {"media_id":"m0","media_name":"Original","is_origin":true,"is_visible":true,
			  "video":{"width":1920,"height":1080,"bit_rate":5000000},"link":{"url":%q}},
			 {"media_id":"m1","media_name":"480P","is_default":true,"is_visible":true,
			  "video":{"width":852,"height":480,"bit_rate":500000},"link":{"url":%q}}]}`,
			testGCID, base+"/origin", base+"/t480")
	}
}

// aiVideo serves a speech clip as f1's original and its one transcode.
func aiVideo(t *testing.T) (video func() string, hits func(string) int) {
	t.Helper()
	media, hits := serveClip(t, speechClip(t))
	return speechFile(media.URL), hits
}

// With AI subtitles on, the video's sound goes up from the lightest
// rendition, ahead of the picture but never more than the lead; captions are
// drawn while their time is on screen; seek, pause and play are told to the
// service; turning them off stops the session and clears the picture.
func TestAISubtitlesFollowPlayback(t *testing.T) {
	video, hits := aiVideo(t)
	relay := newFakeRelay(t)
	drawn := captions(t)
	d, p := playingDrive(t, video, func(o *DriveOptions) { o.SubsrelayURL = relay.srv.URL })
	relay.player = p

	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 20*time.Second, "the video to load", func() bool { return p.State().Duration > 20 })
	if err := d.SetAISubtitles(true, "zh"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "two seconds of audio", func() bool {
		n := 0
		for _, ev := range relay.snapshot() {
			if ev.kind == "frame" {
				n++
			}
		}
		return n >= 20
	})
	if s := p.State().AISubtitles; !s.On || s.Status != "live" || s.Language != "zh" {
		t.Errorf("state = %+v, want on and live in zh", s)
	}
	if s := p.State().AISubtitles; !s.RequiresMembership || !s.Quota.Known || s.Quota.Account == "" || s.Quota.Limit != 14400 || s.Quota.Usage != 0 {
		t.Fatalf("node quota missing from player state: %+v", s)
	}
	relay.send(t, `{"type":"quota","quota":{"limit":14400,"usage":90,"next_reset_time":"2026-11-09T00:00:00Z"}}`)
	waitFor(t, time.Second, "the server quota snapshot", func() bool {
		return p.State().AISubtitles.Quota.Usage == 90
	})
	serverQuota := p.State().AISubtitles.Quota

	events := relay.snapshot()
	start := events[0]
	if start.kind != "start" || start.start["gcid"] != testGCID || start.start["media_id"] != "m1" ||
		start.start["file_id"] != "f1" || start.start["target_language"] != "zh" {
		t.Fatalf("first message = %+v, want start for f1's 480P transcode in zh", start)
	}
	relay.mu.Lock()
	auth := relay.auth
	relay.mu.Unlock()
	if token, err := d.accessToken(context.Background(), ""); err != nil || auth != "Bearer "+token {
		t.Errorf("Authorization = %q, want the account's token (%v)", auth, err)
	}
	if hits("/t480") == 0 {
		t.Error("the transcode was never read: the audio did not come from the lightest rendition")
	}
	first := events[1]
	if first.kind != "frame" || absDiff(first.pos, int64(first.playPos*1000)) > 1500 {
		t.Errorf("first audio at %d ms with the picture at %.1f s, want it to start at the picture", first.pos, first.playPos)
	}

	// Ahead of the picture, by up to the lead and no more.
	waitFor(t, 10*time.Second, "audio five seconds ahead", func() bool {
		for _, ev := range relay.snapshot() {
			if ev.kind == "frame" && ev.pos > int64(ev.playPos*1000)+5000 {
				return true
			}
		}
		return false
	})
	for _, ev := range relay.snapshot() {
		if ev.kind == "frame" && ev.pos > int64(ev.playPos*1000)+aiLeadMS+300 {
			t.Fatalf("audio at %d ms went up with the picture at %.2f s: beyond the %d ms lead", ev.pos, ev.playPos, aiLeadMS)
		}
	}
	// Built up, never sent in one go: the lead can only have grown with the
	// time played since the audio began.
	for _, ev := range relay.snapshot() {
		if ev.kind == "frame" && ev.pos-int64(ev.playPos*1000) > int64((ev.playPos-first.playPos)*1000)+1500 {
			t.Fatalf("audio at %d ms went up %.2f s into playback: a burst ahead of the picture", ev.pos, ev.playPos-first.playPos)
		}
	}

	now := int64(p.State().Position * 1000)
	relay.send(t, fmt.Sprintf(`{"type":"tokens","tokens":[{"text":"第一句","start_ms":%d,"end_ms":%d,"is_final":true}]}`, now+300, now+4000))
	waitFor(t, 5*time.Second, "the caption on screen", func() bool { return strings.Contains(strings.Join(drawn(), "|"), "第一句") })

	mark := len(relay.snapshot())
	if err := p.SeekTo(20); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "audio from the new position", func() bool {
		i := relay.find(mark, "seek")
		return i >= 0 && relay.find(i, "frame") >= 0
	})
	seek := relay.find(mark, "seek")
	if f := relay.snapshot()[relay.find(seek, "frame")]; f.pos < 19500 || f.pos > 21500 {
		t.Errorf("first audio after the seek at %d ms, want 20000", f.pos)
	}
	// The lead builds up again from the new position.
	time.Sleep(time.Second)
	for _, ev := range relay.snapshot()[seek:] {
		if ev.kind == "frame" && ev.pos-int64(ev.playPos*1000) > int64((ev.playPos-20)*1000)+1500 {
			t.Fatalf("audio at %d ms went up %.2f s after the seek: a burst ahead of the picture", ev.pos, ev.playPos-20)
		}
	}

	mark = len(relay.snapshot())
	if err := p.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "pause to be sent", func() bool { return relay.find(mark, "pause") >= 0 })
	mark = len(relay.snapshot())
	if err := p.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "resume to be sent", func() bool { return relay.find(mark, "resume") >= 0 })

	// A pause long enough for the service to act on drops the audio sent
	// ahead; it goes up again, from where the picture stopped.
	mark = len(relay.snapshot())
	if err := p.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	stopped := p.State().Position
	time.Sleep(aiPauseDrop + 500*time.Millisecond)
	if err := p.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "audio again from the paused picture", func() bool {
		i := relay.find(mark, "seek")
		return i >= 0 && relay.find(i, "frame") >= 0
	})
	if f := relay.snapshot()[relay.find(relay.find(mark, "seek"), "frame")]; absDiff(f.pos, int64(stopped*1000)) > 1500 {
		t.Errorf("audio after a long pause starts at %d ms, want the paused picture at %.1f s", f.pos, stopped)
	}

	if err := d.SetAISubtitles(false, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "stop to be sent", func() bool { return relay.find(mark, "stop") >= 0 })
	aiStopped(t, d)
	if all := drawn(); all[len(all)-1] != "" {
		t.Errorf("last drawn caption = %q, want the picture cleared", all[len(all)-1])
	}
	if s := p.State().AISubtitles; s.On || s.Status != "" {
		t.Errorf("state after off = %+v", s)
	}
	if got := p.State().AISubtitles.Quota; got != serverQuota {
		t.Errorf("turning off lost or locally decremented server quota: got %+v want %+v", got, serverQuota)
	}
}

// A refusal turns AI subtitles off and says why, in words the user can act
// on; nothing keeps retrying.
func TestAISubtitlesRefusalTurnsThemOff(t *testing.T) {
	video, _ := aiVideo(t)
	relay := newFakeRelay(t)
	relay.node = func(w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"type":"error","error":"vip_required","error_description":"VIP membership is required to use this feature"}`))
		return true
	}
	captions(t)
	d, p := playingDrive(t, video, func(o *DriveOptions) { o.SubsrelayURL = relay.srv.URL })
	relay.player = p

	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 20*time.Second, "the video to load", func() bool { return p.State().Duration > 20 })
	subtitle := filepath.Join(t.TempDir(), "regular.srt")
	if err := os.WriteFile(subtitle, []byte("1\n00:00:00,000 --> 00:00:29,000\nRegular subtitles\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.withContext(func(ctx context.Context) error {
		return p.mpv.Command(ctx, "sub-add", subtitle, "select")
	}); err != nil {
		t.Fatal(err)
	}
	var selected int
	if err := p.withContext(func(ctx context.Context) error {
		return p.mpv.GetProperty(ctx, "sid", &selected)
	}); err != nil || selected < 1 {
		t.Fatalf("regular subtitle not selected: %d (%v)", selected, err)
	}
	if err := d.SetAISubtitles(true, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "AI subtitles to turn off", func() bool { return !p.State().AISubtitles.On })
	aiStopped(t, d)
	if s := p.State().AISubtitles; s.On || s.Status != "" {
		t.Errorf("state = %+v, want subtitles off after the refusal", s)
	}
	relay.mu.Lock()
	lookups := relay.lookups
	relay.mu.Unlock()
	if lookups != 1 || relay.find(0, "frame") >= 0 {
		t.Errorf("refused service: %d lookups, events %+v; want one request and no audio", lookups, relay.snapshot())
	}
	var after int
	if err := p.withContext(func(ctx context.Context) error {
		return p.mpv.GetProperty(ctx, "sid", &after)
	}); err != nil || after != selected {
		t.Errorf("failed startup hid regular subtitle: got %d (%v), want %d", after, err, selected)
	}
}

// Shutdown returns only after the server has released the session and the
// overlay has been cleared, before the player can destroy its native core.
func TestAISubtitlesShutdownStopsSession(t *testing.T) {
	video, _ := aiVideo(t)
	relay := newFakeRelay(t)
	drawn := captions(t)
	d, p := playingDrive(t, video, func(o *DriveOptions) { o.SubsrelayURL = relay.srv.URL })
	relay.player = p
	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "the video to load", func() bool { return p.State().Duration > 20 })
	if err := d.SetAISubtitles(true, "zh"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "audio to reach the relay", func() bool { return relay.find(0, "frame") >= 0 })
	if err := d.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if relay.find(0, "stop") < 0 {
		t.Fatal("shutdown returned before the relay received stop")
	}
	if all := drawn(); len(all) == 0 || all[len(all)-1] != "" {
		t.Errorf("caption left after shutdown: %v", all)
	}
	if s := p.State().AISubtitles; s.On || s.Status != "" {
		t.Errorf("state after shutdown = %+v", s)
	}
	// Shutdown is idempotent and must not allow a new native session.
	d.ai.close()
	if err := d.SetAISubtitles(true, "zh"); err == nil || p.State().AISubtitles.On {
		t.Errorf("subtitle start after shutdown: %v, %+v", err, p.State().AISubtitles)
	}
}

// A video slow to open still gets its sound sent. Through a CDN a decoder
// takes seconds to get going; started at the picture, it was behind it the
// moment it opened and was started over, again and again, with nothing ever
// going up.
func TestAISubtitlesSlowSource(t *testing.T) {
	clip := speechClip(t)
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/t480" {
			// More than the slack before every answer.
			select {
			case <-time.After(aiSlackMS*time.Millisecond + 500*time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "speech.mp4", time.Unix(0, 0), bytes.NewReader(clip))
	}))
	t.Cleanup(media.Close)
	relay := newFakeRelay(t)
	captions(t)
	d, p := playingDrive(t, speechFile(media.URL), func(o *DriveOptions) { o.SubsrelayURL = relay.srv.URL })
	relay.player = p

	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 20*time.Second, "the video to load", func() bool { return p.State().Duration > 20 })
	if err := d.SetAISubtitles(true, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "two seconds of audio", func() bool {
		n := 0
		for _, ev := range relay.snapshot() {
			if ev.kind == "frame" {
				n++
			}
		}
		return n >= 20
	})
	if err := d.SetAISubtitles(false, ""); err != nil {
		t.Fatal(err)
	}
	aiStopped(t, d)
}

// Cancelling an intermediate language must not release an older session's
// slot, nor leave source work queued after the user turns captions off.
func TestAISubtitlesRapidChangesKeepReleaseChain(t *testing.T) {
	video, _ := aiVideo(t)
	for _, finish := range []string{"resume", "off", "shutdown"} {
		t.Run(finish, func(t *testing.T) {
			relay := newFakeRelay(t)
			gate := make(chan struct{})
			relay.stopGate = gate
			var release sync.Once
			unblock := func() { release.Do(func() { close(gate) }) }
			var metadata atomic.Int64
			d, p := playingDrive(t, func() string {
				metadata.Add(1)
				return video()
			}, func(o *DriveOptions) { o.SubsrelayURL = relay.srv.URL })
			relay.player = p
			t.Cleanup(func() {
				unblock()
				d.ai.close()
			})
			if _, err := d.PlayFile("", "f1"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, 20*time.Second, "the video to load", func() bool { return p.State().Duration > 20 })
			if err := d.SetAISubtitles(true, "zh"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, 10*time.Second, "the first session's audio", func() bool { return relay.find(0, "frame") >= 0 })
			last := func() <-chan struct{} {
				d.ai.mu.Lock()
				defer d.ai.mu.Unlock()
				return d.ai.last
			}
			first, before := last(), metadata.Load()
			if err := d.SetAISubtitles(true, "en"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, 5*time.Second, "the second generation to wait for the first", func() bool {
				return last() != first && relay.find(0, "stop") >= 0
			})
			second := last()
			if err := d.SetAISubtitles(true, "ja"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, 5*time.Second, "the third generation", func() bool { return last() != second })
			select {
			case <-second:
				t.Error("the cancelled middle generation released the still-open first session")
			case <-time.After(200 * time.Millisecond):
			}
			if got := metadata.Load(); got != before {
				t.Errorf("%d metadata requests while the old session is closing, want %d", got, before)
			}
			if finish == "resume" {
				mark := len(relay.snapshot())
				unblock()
				waitFor(t, 10*time.Second, "only the newest language to start", func() bool {
					return relay.find(mark, "start") >= 0 && relay.find(mark, "frame") >= 0
				})
				start := relay.snapshot()[relay.find(mark, "start")]
				if start.start["target_language"] != "ja" || metadata.Load() != before+1 {
					t.Errorf("resumed start=%v metadata=%d, want ja and one source resolution", start.start, metadata.Load()-before)
				}
				if err := d.SetAISubtitles(false, "ja"); err != nil {
					t.Fatal(err)
				}
			} else {
				stopped := make(chan error, 1)
				go func() {
					if finish == "shutdown" {
						stopped <- d.ServiceShutdown()
					} else {
						stopped <- d.SetAISubtitles(false, "ja")
					}
				}()
				waitFor(t, 5*time.Second, "the pending run to be cancelled", func() bool {
					d.ai.mu.Lock()
					defer d.ai.mu.Unlock()
					return !d.ai.on
				})
				select {
				case err := <-stopped:
					t.Errorf("%s returned before the first session closed: %v", finish, err)
				case <-time.After(200 * time.Millisecond):
				}
				unblock()
				select {
				case err := <-stopped:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("stopping did not finish after the old connection closed")
				}
				if got := metadata.Load(); got != before {
					t.Errorf("cancelled pending generations resolved %d extra sources", got-before)
				}
			}
			relay.mu.Lock()
			open, peak, lookups := relay.open, relay.maxOpen, relay.lookups
			relay.mu.Unlock()
			wantLookups := 1
			if finish == "resume" {
				wantLookups = 2
			}
			if open != 0 || peak != 1 || lookups != wantLookups {
				t.Errorf("connections open=%d peak=%d node lookups=%d, want 0/1/%d", open, peak, lookups, wantLookups)
			}
		})
	}
}

// Membership, source discovery and token refresh must use the run's cancellation,
// not merely stop waiting while the actual HTTP request keeps running.
func TestAISubtitlesCancelStartupRequests(t *testing.T) {
	video, _ := aiVideo(t)
	for _, phase := range []string{"membership", "metadata", "token"} {
		for _, shutdown := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shutdown=%t", phase, shutdown), func(t *testing.T) {
				entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				subsDone := make(chan struct{})
				var enteredOnce, cancelledOnce, releaseOnce, subsOnce sync.Once
				var stall atomic.Bool
				var session *accountSession
				block := func(r *http.Request) {
					// HTTP/1 detects a disconnected POST only after its
					// body reaches EOF and the server starts its background read.
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						t.Errorf("read stalled request body: %v", err)
						return
					}
					enteredOnce.Do(func() { close(entered) })
					select {
					case <-r.Context().Done():
						cancelledOnce.Do(func() { close(cancelled) })
					case <-release:
					}
				}
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/vip/v1/vip/info":
						if stall.Load() && phase == "membership" {
							block(r)
							return
						}
						fmt.Fprint(w, `{"data":{"status":"ok"}}`)
						return
					case "/drive/v1/files/f1":
						if stall.Load() {
							if phase == "metadata" {
								block(r)
								return
							}
							session.mu.RLock()
							tokens := session.tokens
							session.mu.RUnlock()
							if err := tokens.Invalidate(r.Context(), "a"); err != nil {
								t.Errorf("invalidate: %v", err)
							}
						}
						fmt.Fprint(w, video())
						return
					case "/v1/auth/token":
						if stall.Load() && phase == "token" {
							block(r)
							return
						}
					case "/oracle/subtitle":
						fmt.Fprint(w, `{"code":0,"data":[]}`)
						subsOnce.Do(func() { close(subsDone) })
						return
					}
					if !authAndTokenRoutes(w, r) {
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(api.Close)
				relay := newFakeRelay(t)
				d, p := playingDrive(t, video, func(o *DriveOptions) {
					o.APIBaseURL, o.AuthBaseURL, o.SubsrelayURL = api.URL, api.URL, relay.srv.URL
				})
				relay.player = p
				t.Cleanup(func() {
					releaseOnce.Do(func() { close(release) })
					d.ai.close()
				})
				if _, err := d.PlayFile("", "f1"); err != nil {
					t.Fatal(err)
				}
				waitFor(t, 20*time.Second, "the video to load", func() bool { return p.State().Duration > 20 })
				select {
				case <-subsDone:
				case <-time.After(5 * time.Second):
					t.Fatal("the initial subtitle lookup did not finish")
				}
				var err error
				session, err = d.sessionFor("")
				if err != nil {
					t.Fatal(err)
				}
				stall.Store(true)
				if err := d.SetAISubtitles(true, "zh"); err != nil {
					t.Fatal(err)
				}
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatalf("AI startup never reached the stalled %s request", phase)
				}
				stopped := make(chan error, 1)
				go func() {
					if shutdown {
						stopped <- d.ServiceShutdown()
					} else {
						stopped <- d.SetAISubtitles(false, "zh")
					}
				}()
				select {
				case err := <-stopped:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("stopping AI subtitles waited for the stalled %s server", phase)
				}
				select {
				case <-cancelled:
				case <-time.After(2 * time.Second):
					t.Fatalf("the actual %s HTTP request was not cancelled", phase)
				}
				relay.mu.Lock()
				lookups := relay.lookups
				relay.mu.Unlock()
				if lookups != 0 || len(relay.snapshot()) != 0 {
					t.Errorf("cancelled startup reached the relay: lookups=%d events=%v", lookups, relay.snapshot())
				}
				if _, err := session.api(); err != nil {
					t.Errorf("cancelling startup expired a valid account: %v", err)
				}
				stall.Store(false)
				if token, err := d.accessToken(context.Background(), ""); err != nil || token != "a" {
					t.Errorf("account cannot obtain a usable token after cancellation: %q (%v)", token, err)
				}
			})
		}
	}
}

func TestAISubtitlesMembershipBeforeSource(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		active, failed, basic bool
	}{
		{name: "nonmember"},
		{name: "membership unavailable", failed: true},
		{name: "member", active: true},
		{name: "basic deployment", basic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var membershipCalls, sourceCalls atomic.Int32
			d := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				switch r.URL.Path {
				case "/vip/v1/vip/info":
					membershipCalls.Add(1)
					if got := r.Header.Get("Authorization"); got != "Bearer playing-token" {
						t.Errorf("membership checked browsing account: Authorization=%q", got)
					}
					if tc.failed {
						w.WriteHeader(http.StatusBadGateway)
						fmt.Fprint(w, `{"error":"membership_unavailable"}`)
					} else if tc.active {
						fmt.Fprint(w, `{"data":{"status":"ok"}}`)
					} else {
						fmt.Fprint(w, `{"data":{"status":"expired"}}`)
					}
				case "/drive/v1/files/f1":
					sourceCalls.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":"invalid_request"}`)
				default:
					http.NotFound(w, r)
				}
			})
			owner, err := d.sessionFor("")
			if err != nil {
				t.Fatal(err)
			}
			token, err := owner.tokens.Token(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			token.AccessToken = "playing-token"
			if err := owner.tokens.Seed(token); err != nil {
				t.Fatal(err)
			}
			seedSession(t, d, "browsing@example.com")
			relay := newFakeRelay(t)
			url := relay.srv.URL
			if tc.basic {
				url = strings.Replace(url, "http://", "http://dev:password@", 1)
			}
			client, err := subsrelay.New(url)
			if err != nil {
				t.Fatal(err)
			}
			// No native player: rejected startup must not even touch subtitle
			// selection or audio discovery, let alone decode/upload anything.
			p := &PlayerService{account: owner.key, fileID: "f1", state: PlayerState{Duration: 60}}
			d.player = p
			d.ai = newAISubs(d, p, client)
			t.Cleanup(d.ai.close)
			if err := d.SetAISubtitles(true, "zh"); err != nil {
				t.Fatal(err)
			}
			aiStopped(t, d)
			state := p.state.AISubtitles
			if state.On || state.RequiresMembership == tc.basic || state.Error == "" {
				t.Errorf("failed startup state = %+v", state)
			}
			if got := membershipCalls.Load(); tc.basic && got != 0 {
				t.Errorf("Basic-auth deployment checked PikPak membership %d times", got)
			}
			wantSource := int32(0)
			if tc.basic || tc.active {
				wantSource = 1
			}
			if got := sourceCalls.Load(); got != wantSource {
				t.Errorf("source requests = %d, want %d", got, wantSource)
			}
			memberMessage := aiMessage(&subsrelay.Error{Code: "vip_required"})
			if want := !tc.basic && !tc.active && !tc.failed; (state.Error == memberMessage) != want {
				t.Errorf("membership refusal=%t, want %t: %q", state.Error == memberMessage, want, state.Error)
			}
			if relay.lookups != 0 || len(relay.snapshot()) != 0 {
				t.Errorf("failed startup reached relay: lookups=%d events=%v", relay.lookups, relay.snapshot())
			}
		})
	}
}

func TestAISubtitlesQuotaCannotSupersedeCurrentRun(t *testing.T) {
	for _, change := range []string{"off", "language", "rendition", "account", "file", "switching", "restoring", "closed", "cancelled", "current"} {
		t.Run(change, func(t *testing.T) {
			old := AISubtitleQuota{Account: "a", Known: true, Limit: 100, Usage: 10}
			p := &PlayerService{
				account: "a", fileID: "f1", gen: 5,
				state: PlayerState{Duration: 60, AISubtitles: AISubtitleState{On: true, Quota: old}},
			}
			a := &aiSubs{p: p, on: true, gen: 9}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &aiRun{a: a, account: "a", fileID: "f1", gen: 9, target: 5, ctx: ctx}
			switch change {
			case "off":
				a.on = false
			case "language":
				a.gen++
			case "rendition":
				p.gen++
			case "account":
				p.account = "b"
			case "file":
				p.fileID = "f2"
			case "switching":
				p.state.Quality.Switching = "480"
			case "restoring":
				p.restoring = 1
			case "closed":
				a.closed = true
			case "cancelled":
				cancel()
			}
			if r.setQuota(nil) {
				t.Fatal("omitted quota accepted as a known zero quota")
			}
			if got, want := r.setQuota(&subsrelay.Quota{}), change == "current"; got != want {
				t.Errorf("quota accepted=%t, want %t", got, want)
			}
			want := old
			if change == "current" {
				want = AISubtitleQuota{Account: "a", Known: true}
			}
			if got := p.state.AISubtitles.Quota; got != want {
				t.Errorf("quota = %+v, want %+v", got, want)
			}
		})
	}
}

func TestAISubtitlesNodeQuotaSurvivesRefusalAndOff(t *testing.T) {
	relay := newFakeRelay(t)
	relay.node = func(w http.ResponseWriter) bool {
		fmt.Fprint(w, `{"quota":{"exceeded":true,"limit":14400,"usage":14400,"next_reset_time":"2026-11-09T00:00:00Z"}}`)
		return true
	}
	client, err := subsrelay.New(strings.Replace(relay.srv.URL, "http://", "http://dev:password@", 1))
	if err != nil {
		t.Fatal(err)
	}
	p := &PlayerService{account: "a", fileID: "f1", gen: 5, state: PlayerState{Duration: 60}}
	a := newAISubs(&DriveService{log: quiet()}, p, client)
	a.on, a.gen = true, 9
	p.state.AISubtitles.On = true
	r := &aiRun{a: a, account: "a", fileID: "f1", gen: 9, target: 5, ctx: context.Background()}
	_, r.err = r.connect(subsrelay.Start{GCID: testGCID})
	if _, ok := r.err.(*aiQuotaError); !ok {
		t.Fatalf("connect = %v, want quota refusal", r.err)
	}
	want := AISubtitleQuota{
		Account: "a", Known: true, Exceeded: true, Limit: 14400, Usage: 14400,
		NextResetTime: "2026-11-09T00:00:00Z",
	}
	if got := p.state.AISubtitles.Quota; got != want {
		t.Fatalf("node quota lost on refusal: got %+v want %+v", got, want)
	}
	a.fail(r)
	if err := a.set(false, ""); err != nil {
		t.Fatal(err)
	}
	if state := p.state.AISubtitles; state.On || state.Quota != want {
		t.Errorf("failure/off lost exhausted quota: %+v", state)
	}
}

func TestAISubtitlesRepeatedEnableOrdersFailurePublication(t *testing.T) {
	p := &PlayerService{
		account: "a", fileID: "f1", gen: 5,
		state: PlayerState{Duration: 60, AISubtitles: AISubtitleState{On: true, Language: "zh", Status: "live"}},
	}
	a := &aiSubs{p: p, on: true, lang: "zh", gen: 9, looping: true, wake: make(chan struct{}, 1), log: quiet()}
	r := &aiRun{a: a, account: "a", fileID: "f1", gen: 9, target: 5, ctx: context.Background(), err: fmt.Errorf("connection failed")}
	// Hold the preference lock to stop a repeated enable inside its
	// transaction. It must already own the mirror's lock at this point.
	a.mu.Lock()
	locked := true
	set, finished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		set <- a.set(true, "zh")
	}()
	defer func() {
		if locked {
			a.mu.Unlock()
		}
		<-finished
	}()
	waitFor(t, time.Second, "the repeated enable to lock its player mirror", func() bool {
		if p.mu.TryLock() {
			p.mu.Unlock()
			return false
		}
		return true
	})
	failed := make(chan struct{})
	go func() {
		a.fail(r)
		close(failed)
	}()
	a.mu.Unlock()
	locked = false
	select {
	case err := <-set:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("repeated enable did not finish")
	}
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("the session failure did not finish")
	}
	p.mu.Lock()
	a.mu.Lock()
	defer a.mu.Unlock()
	defer p.mu.Unlock()
	if a.on || p.state.AISubtitles.On || p.state.AISubtitles.Status != "" || p.state.AISubtitles.Error == "" {
		t.Errorf("failure and repeated enable disagree: preference=%t player=%+v", a.on, p.state.AISubtitles)
	}
}

func TestAISubtitlesRunStatusCannotSupersedePreferences(t *testing.T) {
	for _, status := range []string{"connecting", "live"} {
		for _, change := range []string{"off", "language", "rendition", "restoring", "closed", "cancelled", "current"} {
			t.Run(status+"/"+change, func(t *testing.T) {
				p := &PlayerService{
					account: "a", fileID: "f1", gen: 5,
					state: PlayerState{Duration: 60, AISubtitles: AISubtitleState{On: true, Language: "zh"}},
				}
				a := &aiSubs{p: p, on: true, gen: 9}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				r := &aiRun{a: a, account: "a", fileID: "f1", gen: 9, target: 5, ctx: ctx}
				switch change {
				case "off":
					a.on = false
					p.state.AISubtitles.On = false
				case "language":
					a.gen++
					p.state.AISubtitles.Language = "en"
				case "rendition":
					p.gen++
				case "restoring":
					p.restoring = 1
				case "closed":
					a.closed = true
				case "cancelled":
					cancel()
				}
				before := p.state.AISubtitles
				if got, want := r.setStatus(status), change == "current"; got != want {
					t.Errorf("status update accepted=%t, want %t", got, want)
				}
				want := before
				if change == "current" {
					want.Status = status
				}
				if got := p.state.AISubtitles; got != want {
					t.Errorf("status update overwrote a newer choice: got %+v want %+v", got, want)
				}
			})
		}
	}
}

func TestAISubtitlesIgnoreStaleFailures(t *testing.T) {
	for _, change := range []string{"language", "rendition", "switching", "restoring", "current"} {
		t.Run(change, func(t *testing.T) {
			p := &PlayerService{
				account: "a", fileID: "f1", gen: 5,
				state: PlayerState{Duration: 60, AISubtitles: AISubtitleState{On: true, Language: "en"}},
			}
			a := &aiSubs{p: p, on: true, gen: 9, log: quiet()}
			r := &aiRun{a: a, account: "a", fileID: "f1", gen: 9, target: 5, ctx: context.Background(), err: fmt.Errorf("old connection failed")}
			switch change {
			case "language":
				a.gen++
			case "rendition":
				p.gen++
			case "switching":
				p.state.Quality.Switching = "480"
			case "restoring":
				p.restoring = 1
			}
			a.fail(r)
			if want := change != "current"; a.on != want || p.state.AISubtitles.On != want {
				t.Errorf("after %s, AI enabled=%t/%t, want %t", change, a.on, p.state.AISubtitles.On, want)
			}
			if change != "current" && p.state.AISubtitles.Error != "" {
				t.Errorf("stale failure reached the player: %+v", p.state.AISubtitles)
			}
		})
	}
}

func TestAISubtitlesRepeatedFailuresHaveDistinctRevisions(t *testing.T) {
	p := &PlayerService{
		account: "a", fileID: "f1", gen: 5,
		state: PlayerState{Duration: 60, AISubtitles: AISubtitleState{On: true, Language: "zh"}},
	}
	a := &aiSubs{
		p: p, on: true, lang: "zh", gen: 9, looping: true,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), log: quiet(),
	}
	message := ""
	for revision := uint64(1); revision <= 2; revision++ {
		r := &aiRun{
			a: a, account: "a", fileID: "f1", gen: a.gen, target: p.gen,
			ctx: context.Background(), err: &subsrelay.Error{Code: "vip_required"},
		}
		a.fail(r)
		state := p.state.AISubtitles
		if state.On || state.Status != "" || state.Error == "" || state.ErrorRevision != revision {
			t.Fatalf("accepted failure %d = %+v", revision, state)
		}
		if revision > 1 && state.Error != message {
			t.Fatalf("repeated refusal changed its text: %q / %q", message, state.Error)
		}
		message = state.Error
		if err := a.set(true, "zh"); err != nil {
			t.Fatal(err)
		}
		if state := p.state.AISubtitles; !state.On || state.Error != "" || state.ErrorRevision != revision {
			t.Fatalf("clearing the error changed its identity: %+v", state)
		}
		a.fail(r) // The old generation cannot publish another failure.
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		a.fail(&aiRun{
			a: a, account: "a", fileID: "f1", gen: a.gen, target: p.gen,
			ctx: cancelled, err: &subsrelay.Error{Code: "vip_required"},
		})
		if state := p.state.AISubtitles; !state.On || state.Error != "" || state.ErrorRevision != revision {
			t.Fatalf("stale or cancelled failure published a new error: %+v", state)
		}
		if err := a.set(false, "zh"); err != nil {
			t.Fatal(err)
		}
		if got := p.state.AISubtitles.ErrorRevision; got != revision {
			t.Fatalf("turning off changed error revision: %d, want %d", got, revision)
		}
		if err := a.set(true, "zh"); err != nil {
			t.Fatal(err)
		}
	}
	a.close()
	if got := p.state.AISubtitles.ErrorRevision; got != 2 {
		t.Errorf("shutdown changed error revision: %d, want 2", got)
	}
}

// A two-language original and English-only transcode make a stale AI source
// audible in the PCM itself, not just in a track id or source URL.
func TestAISubtitlesFollowQualityAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	original, transcode := filepath.Join(dir, "original.mkv"), filepath.Join(dir, "english.mp4")
	commands := [][]string{
		{"-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=30",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=30",
			"-f", "lavfi", "-i", "sine=frequency=1000:duration=30",
			"-map", "0:v", "-map", "1:a", "-map", "2:a", "-c:v", "libx264", "-c:a", "aac",
			"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=jpn",
			"-disposition:a:0", "default", "-disposition:a:1", "0", original},
		{"-i", original, "-map", "0:v", "-map", "0:a:0", "-c", "copy", "-movflags", "+faststart", transcode},
	}
	for _, args := range commands {
		if msg, err := exec.Command("ffmpeg", append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, msg)
		}
	}
	originBytes, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	transcodeBytes, err := os.ReadFile(transcode)
	if err != nil {
		t.Fatal(err)
	}
	gate, requested := make(chan struct{}), make(chan struct{})
	var release, request sync.Once
	unblock := func() { release.Do(func() { close(gate) }) }
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := originBytes
		if r.URL.Path == "/t480" {
			request.Do(func() { close(requested) })
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
			data = transcodeBytes
		}
		http.ServeContent(w, r, "speech", time.Unix(0, 0), bytes.NewReader(data))
	}))
	t.Cleanup(media.Close)
	relay := newFakeRelay(t)
	d, p := playingDrive(t, speechFile(media.URL), func(o *DriveOptions) { o.SubsrelayURL = relay.srv.URL })
	relay.player = p
	t.Cleanup(func() {
		unblock()
		d.ai.close()
	})
	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "the original to load", func() bool { return p.State().Duration > 20 })
	tracks, err := p.Tracks()
	if err != nil {
		t.Fatal(err)
	}
	japanese := matchTrack(tracks, "audio", trackRef{known: true, lang: "jpn"})
	if japanese == 0 {
		t.Fatal("the original has no Japanese track")
	}
	if err := p.SelectTrack("audio", japanese); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAISubtitles(true, "zh"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "Japanese PCM at the relay", func() bool {
		for _, ev := range relay.snapshot() {
			if ev.kind == "frame" && math.Abs(ev.hz-1000) < 40 {
				return true
			}
		}
		return false
	})
	mark := len(relay.snapshot())
	if err := d.SetPlaybackQuality("480"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("the player did not request the transcode")
	}
	// Hold the existing load lock so file-loaded can settle, but its
	// asynchronous track restoration cannot finish yet.
	p.loadMu.Lock()
	locked := true
	defer func() {
		if locked {
			p.loadMu.Unlock()
		}
	}()
	unblock()
	waitFor(t, 10*time.Second, "track restoration to be pending", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.state.Quality.Current == "480" && p.restoring != 0
	})
	waitFor(t, 5*time.Second, "the old AI session to stop", func() bool { return relay.find(mark, "stop") >= 0 })
	if i := relay.find(mark, "start"); i >= 0 {
		t.Errorf("AI restarted before the rendition's tracks settled: %v", relay.snapshot()[i])
	}
	p.loadMu.Unlock()
	locked = false
	waitFor(t, 10*time.Second, "English PCM from the new AI session", func() bool {
		i := relay.find(mark, "start")
		if i < 0 {
			return false
		}
		for _, ev := range relay.snapshot()[i:] {
			if ev.kind == "frame" && math.Abs(ev.hz-440) < 40 {
				return true
			}
		}
		return false
	})
	start := relay.snapshot()[relay.find(mark, "start")]
	if start.start["gcid"] != testGCID || start.start["media_id"] != "m1" {
		t.Errorf("transcode session identity = %v, want first-track GCID and m1", start.start)
	}
	tracks, err = p.Tracks()
	if err != nil || selectedTrack(tracks, "audio").lang != "eng" {
		t.Errorf("the player did not settle on English: %v (%v)", tracks, err)
	}
	if err := d.SetAISubtitles(false, "zh"); err != nil {
		t.Fatal(err)
	}
}

// One caption shows at a time: a newer one cuts an older one short, repeats
// sent after a seek are dropped, and a seek forgets what the service will
// send again.
func TestAICues(t *testing.T) {
	var c aiCues
	c.add(subsrelay.Cue{StartMS: 1000, EndMS: 3000, Text: "a"})
	c.add(subsrelay.Cue{StartMS: 2000, EndMS: 2600, Text: "b"})
	c.add(subsrelay.Cue{StartMS: 1000, EndMS: 3000, Text: "a"})
	for _, tc := range []struct {
		pos  int64
		want string
	}{{999, ""}, {1000, "a"}, {1999, "a"}, {2000, "b"}, {2599, "b"}, {2700, ""}} {
		if got := c.at(tc.pos); got != tc.want {
			t.Errorf("at(%d) = %q, want %q", tc.pos, got, tc.want)
		}
	}
	c.cut(2400)
	if got := c.at(1500); got != "a" {
		t.Errorf("after a seek to 2.4 s, at(1500) = %q, want the earlier caption kept", got)
	}
	if got := c.at(2100); got != "" {
		t.Errorf("after a seek to 2.4 s, at(2100) = %q, want it dropped for the service to resend", got)
	}
}

// Text from the service is shown as written: ASS markup in it stays text.
func TestASSCaptionEscapes(t *testing.T) {
	got := assCaption(` {\i1}x\Ny`+"\nz", captionLook{style: defaultSubtitleStyle})
	want := `{\an2\fs38\bord2.5\shad0}\h\{\` + "\u2060" + `i1}x\` + "\u2060" + `Ny\Nz`
	if got != want {
		t.Errorf("assCaption = %q, want %q", got, want)
	}
}

// AI captions follow subtitle style: size from the scale, the colour in ASS's
// blue-green-red order, and a raised caption placed by hand at the overlay's
// centre, between the bottom line and one line below the top.
func TestASSCaptionFollowsStyle(t *testing.T) {
	style := SubtitleStyle{Scale: 1.5, Raise: 50, Color: "#FFD60A"}
	got := assCaption("x", captionLook{style: style, resX: 1280})
	// fs 57; y = 698 − 0.5 × (698 − 57 × 1.25) = 384.6.
	want := `{\an2\pos(640,385)\fs57\bord2.5\shad0\1c&H0AD6FF&}x`
	if got != want {
		t.Errorf("raised caption = %q, want %q", got, want)
	}
	style.Raise = 100
	if got, want := assCaption("x", captionLook{style: style, resX: 1280}), `{\an2\pos(640,71)\fs57\bord2.5\shad0\1c&H0AD6FF&}x`; got != want {
		t.Errorf("top caption = %q, want %q", got, want)
	}
	// Without the overlay's width it stays where mpv puts it.
	if got := assCaption("x", captionLook{style: style}); strings.Contains(got, `\pos`) {
		t.Errorf("caption without a width = %q, want no \\pos", got)
	}
}
