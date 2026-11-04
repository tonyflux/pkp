package mpv

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClip is a 2 second, 9 KiB H.264/AAC file. Using a real container keeps
// the test on the same demuxer path as production playback.
const testClip = "testdata/clip.mp4"

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func startClient(t *testing.T) (*Client, <-chan Event) {
	t.Helper()

	events := make(chan Event, 512)
	c := New(Options{Headless: true, Logger: quiet()})
	c.SetEventHandler(func(ev Event) {
		select {
		case events <- ev:
		default:
		}
	})

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("start mpv: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, events
}

func waitEvent(t *testing.T, events <-chan Event, match func(Event) bool, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-events:
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for event", timeout)
			return Event{}
		}
	}
}

// The service layer reads playback state from observed properties, so a load
// that never produces a usable duration is indistinguishable from a hang.
func TestLoadFileReportsDuration(t *testing.T) {
	c, events := startClient(t)
	ctx := context.Background()

	var version string
	if err := c.GetProperty(ctx, "mpv-version", &version); err != nil {
		t.Fatalf("get mpv-version: %v", err)
	}
	if !strings.HasPrefix(version, "mpv") {
		t.Fatalf("mpv-version = %q, want an mpv prefix", version)
	}

	if err := c.ObserveProperty(ctx, "duration"); err != nil {
		t.Fatalf("observe duration: %v", err)
	}
	if err := c.Command(ctx, "loadfile", testClip, "replace"); err != nil {
		t.Fatalf("loadfile: %v", err)
	}

	waitEvent(t, events, func(ev Event) bool { return ev.Name == EventFileLoaded }, 20*time.Second)

	var duration float64
	if err := c.GetProperty(ctx, "duration", &duration); err != nil {
		t.Fatalf("get duration: %v", err)
	}
	if duration < 1.5 || duration > 2.5 {
		t.Errorf("duration = %v, want ~2", duration)
	}

	ev := waitEvent(t, events, func(ev Event) bool {
		return ev.Name == EventPropertyChange && ev.Property == "duration" && string(ev.Data) != "null"
	}, 10*time.Second)
	var observed float64
	if err := json.Unmarshal(ev.Data, &observed); err != nil {
		t.Fatalf("decode observed duration %q: %v", ev.Data, err)
	}
	if observed < 1.5 || observed > 2.5 {
		t.Errorf("observed duration = %v, want ~2", observed)
	}
}

// Properties come back as mpv nodes. Anything that only handled scalars would
// pass the duration test and still break the track list, which the subtitle
// and audio pickers are built from.
func TestTrackListDecodesAsStructuredNodes(t *testing.T) {
	c, events := startClient(t)
	ctx := context.Background()

	if err := c.Command(ctx, "loadfile", testClip, "replace"); err != nil {
		t.Fatalf("loadfile: %v", err)
	}
	waitEvent(t, events, func(ev Event) bool { return ev.Name == EventFileLoaded }, 20*time.Second)

	var tracks []struct {
		ID       int    `json:"id"`
		Type     string `json:"type"`
		Codec    string `json:"codec"`
		Selected bool   `json:"selected"`
	}
	if err := c.GetProperty(ctx, "track-list", &tracks); err != nil {
		t.Fatalf("get track-list: %v", err)
	}
	if len(tracks) < 2 {
		t.Fatalf("tracks = %d, want a video and an audio track", len(tracks))
	}

	kinds := map[string]string{}
	for _, track := range tracks {
		kinds[track.Type] = track.Codec
	}
	if kinds["video"] != "h264" || kinds["audio"] != "aac" {
		t.Errorf("tracks = %v, want h264 video and aac audio", kinds)
	}
}

func TestSetAndGetProperty(t *testing.T) {
	c, _ := startClient(t)
	ctx := context.Background()

	if err := c.SetProperty(ctx, "pause", true); err != nil {
		t.Fatalf("set pause: %v", err)
	}
	var paused bool
	if err := c.GetProperty(ctx, "pause", &paused); err != nil {
		t.Fatalf("get pause: %v", err)
	}
	if !paused {
		t.Error("pause = false after setting it to true")
	}

	// Seek targets are fractional; rendering them as integers would land on
	// the wrong frame.
	if err := c.SetProperty(ctx, "speed", 1.25); err != nil {
		t.Fatalf("set speed: %v", err)
	}
	var speed float64
	if err := c.GetProperty(ctx, "speed", &speed); err != nil {
		t.Fatalf("get speed: %v", err)
	}
	if speed != 1.25 {
		t.Errorf("speed = %v, want 1.25", speed)
	}
}

// The UI issues property reads from several components at once.
func TestConcurrentPropertyReads(t *testing.T) {
	c, _ := startClient(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 50)

	for range 25 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			var v string
			if err := c.GetProperty(ctx, "mpv-version", &v); err != nil {
				errs <- err
			} else if !strings.HasPrefix(v, "mpv") {
				errs <- errValue("mpv-version", v)
			}
		}()
		go func() {
			defer wg.Done()
			var v float64
			if err := c.GetProperty(ctx, "volume", &v); err != nil {
				errs <- err
			} else if v < 0 || v > 1000 {
				errs <- errValue("volume", v)
			}
		}()
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// Close must stop the event loop before the core is destroyed, or the loop
// sits inside mpv_wait_event on freed memory. A leaked goroutine here also
// means the app never exits.
func TestCloseStopsTheCore(t *testing.T) {
	c, _ := startClient(t)

	if !c.Running() {
		t.Fatal("Running() = false right after Start")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if c.Running() {
		t.Error("Running() = true after Close")
	}
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"command": func() error { return c.Command(ctx, "loadfile", testClip) },
		"get":     func() error { return c.GetProperty(ctx, "track-list", nil) },
		"set":     func() error { return c.SetProperty(ctx, "pause", true) },
		"observe": func() error { return c.ObserveProperty(ctx, "pause") },
	} {
		if err := call(); err != ErrNotRunning {
			t.Errorf("%s after close: err = %v, want ErrNotRunning", name, err)
		}
	}
	// Idempotent: ServiceShutdown may run after the core already quit.
	if err := c.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

// Argument conversion can overlap shutdown before the native call starts.
// Closing from a Stringer makes that old snapshot-to-call gap deterministic.
func TestCloseDuringValueConversion(t *testing.T) {
	for name, call := range map[string]func(*Client, any) error{
		"command": func(c *Client, value any) error {
			return c.Command(context.Background(), "set", "pause", value)
		},
		"set": func(c *Client, value any) error {
			return c.SetProperty(context.Background(), "pause", value)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := startClient(t)
			var closeErr error
			value := lifecycleValue(func() string {
				shutdown := make(chan error, 1)
				go func() { shutdown <- c.Close() }()
				closeErr = <-shutdown
				return "yes"
			})
			result := make(chan error, 1)
			go func() { result <- call(c, value) }()
			select {
			case err := <-result:
				if closeErr != nil {
					t.Fatalf("close during conversion: %v", closeErr)
				}
				if err != ErrNotRunning {
					t.Fatalf("call after shutdown during conversion: %v, want ErrNotRunning", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown deadlocked during value conversion")
			}
			if c.Running() {
				t.Fatal("Running() = true after shutdown during conversion")
			}
		})
	}
}

type lifecycleValue func() string

func (v lifecycleValue) String() string { return v() }

// The service's deferred track restoration may still be reading or writing
// properties when shutdown starts. Exercise real native calls on both sides
// of detach, rather than substituting a fake core.
func TestCloseWithConcurrentNativeCalls(t *testing.T) {
	c, events := startClient(t)
	ctx := context.Background()
	if err := c.Command(ctx, "loadfile", testClip, "replace"); err != nil {
		t.Fatalf("loadfile: %v", err)
	}
	waitEvent(t, events, func(ev Event) bool { return ev.Name == EventFileLoaded }, 20*time.Second)

	calls := []func() error{
		func() error {
			var tracks []struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			}
			return c.GetProperty(ctx, "track-list", &tracks)
		},
		func() error { return c.Command(ctx, "set", "pause", "yes") },
		func() error { return c.SetProperty(ctx, "volume", 50) },
		func() error { return c.ObserveProperty(ctx, "pause") },
	}
	const workersPerCall = 4
	ready := make(chan error, workersPerCall*len(calls))
	errs := make(chan error, workersPerCall*len(calls))
	closing := make(chan struct{})
	var wg sync.WaitGroup
	for range workersPerCall {
		for _, call := range calls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := call()
				ready <- err
				if err != nil {
					return
				}
				<-closing
				for {
					switch err := call(); err {
					case nil:
					case ErrNotRunning:
						return
					default:
						errs <- err
						return
					}
				}
			}()
		}
	}
	for range cap(ready) {
		if err := <-ready; err != nil {
			t.Errorf("native call before shutdown: %v", err)
		}
	}
	closed := make(chan error, 1)
	go func() {
		<-closing
		closed <- c.Close()
	}()
	close(closing)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("native callers did not stop after shutdown")
	}
	close(errs)
	for err := range errs {
		t.Errorf("native call during shutdown: %v", err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not drain native callers and the event loop")
	}
}

// While a file plays, mpv_wait_event always has a property change to return,
// and returning one drops Close's wakeup. The loop has to notice Close
// anyway, or Close — and quitting the app with it — waits forever.
func TestCloseWhilePlaying(t *testing.T) {
	c := New(Options{Headless: true, Logger: quiet()})
	playing := make(chan struct{})
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	defer release()
	var once sync.Once
	c.SetEventHandler(func(ev Event) {
		if ev.Name == EventPropertyChange && ev.Property == "time-pos" && string(ev.Data) != "null" {
			// Hold the loop outside mpv_wait_event, so Close's wakeup
			// arrives while changes pile up behind it.
			once.Do(func() {
				close(playing)
				<-gate
			})
		}
	})
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("start mpv: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	if err := c.SetProperty(ctx, "loop-file", "inf"); err != nil {
		t.Fatalf("set loop-file: %v", err)
	}
	if err := c.ObserveProperty(ctx, "time-pos"); err != nil {
		t.Fatalf("observe time-pos: %v", err)
	}
	if err := c.Command(ctx, "loadfile", testClip, "replace"); err != nil {
		t.Fatalf("loadfile: %v", err)
	}
	select {
	case <-playing:
	case <-time.After(20 * time.Second):
		t.Fatal("playback never reported a position")
	}

	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	detached := make(chan error, 1)
	go func() {
		for {
			if err := c.GetProperty(ctx, "mpv-version", nil); err != nil {
				detached <- err
				return
			}
		}
	}()
	select {
	case err := <-detached:
		if err != ErrNotRunning {
			t.Fatalf("read during close: %v, want ErrNotRunning", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not detach while the event handler was pending")
	}
	release()

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return while a file was playing")
	}
}

// Quitting from inside mpv (the user closing the window, a fatal error) has
// to reach the service, which uses it to reset its own state.
func TestShutdownEventIsReported(t *testing.T) {
	c, events := startClient(t)

	if err := c.Command(context.Background(), "quit"); err != nil {
		t.Fatalf("quit: %v", err)
	}
	waitEvent(t, events, func(ev Event) bool { return ev.Name == EventShutdown }, 10*time.Second)
}

// Headless clients have no surface; the stats must say so rather than
// reporting a plausible-looking zero state as "attached".
func TestHeadlessHasNoSurface(t *testing.T) {
	c, _ := startClient(t)
	stats := c.SurfaceStats()
	if stats.Attached {
		t.Error("Attached = true for a headless client")
	}
	if err := c.AttachSurface(nil); err != nil {
		t.Errorf("headless attach should be a no-op, got %v", err)
	}
}

type valueError struct {
	name  string
	value any
}

func (e valueError) Error() string {
	encoded, err := json.Marshal(e.value)
	if err != nil {
		return "unexpected value for " + e.name
	}
	return "unexpected value for " + e.name + ": " + string(encoded)
}

func errValue(name string, value any) error { return valueError{name: name, value: value} }
