//go:build linux && pkp_surface_test

package mpv

import (
	"context"
	"image"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// Run explicitly under either Xvfb (GDK_BACKEND=x11) or a headless Weston
// (GDK_BACKEND=wayland). The normal headless command/property suite needs no display.
func TestLinuxSurface(t *testing.T) {
	if os.Getenv("PKP_TEST_VIDEO_SURFACE") != "1" {
		t.Skip("set PKP_TEST_VIDEO_SURFACE=1 inside an X11 or Wayland session")
	}
	clip := filepath.Join(t.TempDir(), "red-blue.mkv")
	command := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i",
		"color=c=red:s=320x180:r=10,drawbox=x=0:y=90:w=320:h=90:color=blue:t=fill",
		"-t", "1", "-c:v", "ffv1", "-y", clip)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate deterministic video (ffmpeg required): %v: %s", err, output)
	}
	windows, stop, ok := surfaceTestHost()
	if !ok {
		t.Fatal("GTK4 could not connect to the requested display")
	}
	defer func() {
		done := make(chan struct{})
		go func() { stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("GTK host did not stop")
		}
	}()
	testLinuxSurfaceCloseWithQueuedCall(t, windows)
	if t.Failed() {
		return
	}
	c := New(Options{Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), Extra: map[string]string{
		"ao": "null", "hwdec": "no", "loop-file": "inf", "pause": "yes",
	}})
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var version string
	if err := c.GetProperty(ctx, "mpv-version", &version); err != nil || !strings.HasPrefix(version, "mpv") {
		t.Fatalf("libmpv version = %q, %v", version, err)
	}
	if err := c.AttachSurface(windows[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitSurfaceReady(); err != nil {
		t.Fatal(err)
	}
	rect := Rect{Width: 320, Height: 240, Visible: true}
	if err := c.Command(ctx, "loadfile", clip); err != nil {
		t.Fatal(err)
	}
	assertColors := func(top, bottom [4]uint8) bool {
		return top[0] > 180 && top[2] < 70 && bottom[2] > 180 && bottom[0] < 70
	}
	waitFrame := func(previous uint64) SurfaceStats {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			stats := c.SurfaceStats()
			if stats.Attached && stats.Frames > previous && assertColors(stats.TopPixel, stats.BottomPixel) {
				return stats
			}
			time.Sleep(20 * time.Millisecond)
		}
		for _, name := range []string{"path", "pause", "video-codec", "current-vo", "current-hwdec", "vo-configured", "video-params", "video-out-params", "time-pos", "frame-drop-count", "decoder-frame-drop-count", "idle-active", "eof-reached"} {
			var value any
			err := c.GetProperty(ctx, name, &value)
			t.Logf("%s = %#v (%v)", name, value, err)
		}
		t.Fatalf("real upright red/blue frame never arrived: %+v", c.SurfaceStats())
		return SurfaceStats{}
	}
	checkCapture := func() {
		t.Helper()
		captureCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		img, err := c.Capture(captureCtx)
		if err != nil {
			t.Fatal(err)
		}
		// The 320x180 picture is letterboxed into 320x240. Capture must remove
		// those bars and retain the backing scale, not invert or squash the image.
		w, h := img.Bounds().Dx(), img.Bounds().Dy()
		if w < 320 || h < 180 || w*180 != h*320 {
			t.Fatalf("cropped capture = %dx%d, want 320:180 at backing resolution", w, h)
		}
		pixel := func(point image.Point) [4]uint8 {
			p := img.RGBAAt(point.X, point.Y)
			return [4]uint8{p.R, p.G, p.B, p.A}
		}
		if !assertColors(pixel(image.Pt(w/2, h/3)), pixel(image.Pt(w/2, h*2/3))) {
			t.Fatal("capture is inverted or did not contain the rendered red/blue picture")
		}
	}
	c.SetSurfaceRect(rect)
	stats := waitFrame(0)
	checkCapture()
	// A paused frame must redraw after each GL context recreation, both ways.
	for _, window := range []int{1, 0} {
		if err := c.MoveSurface(windows[window]); err != nil {
			t.Fatal(err)
		}
		c.SetSurfaceRect(rect)
		stats = waitFrame(stats.Frames)
		checkCapture()
	}
	rect.Visible = false
	c.SetSurfaceRect(rect)
	captureCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := c.Capture(captureCtx); err == nil {
		t.Fatal("hidden surface capture unexpectedly succeeded")
	}
	if time.Since(started) > time.Second {
		t.Fatal("hidden surface capture did not honor its bounded wait")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.SurfaceStats().Attached {
		t.Fatal("surface remained attached after teardown")
	}
}

// Hold GTK in its close callback until the worker has actually queued its
// native call. This reproduces the UI-thread writer versus queued reader.
func testLinuxSurfaceCloseWithQueuedCall(t *testing.T, windows [2]unsafe.Pointer) {
	for _, operation := range []string{"attach", "move", "rect"} {
		ok := t.Run(operation, func(t *testing.T) {
			c := New(Options{Logger: quiet(), Extra: map[string]string{"ao": "null"}})
			if err := c.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if operation != "attach" {
				if err := c.AttachSurface(windows[0]); err != nil {
					t.Fatal(err)
				}
			}
			entered, closed := surfaceCloseOnMain(c)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("GTK close callback did not start")
			}
			finished := make(chan error, 1)
			go func() {
				switch operation {
				case "attach":
					finished <- c.AttachSurface(windows[0])
				case "move":
					finished <- c.MoveSurface(windows[1])
				default:
					c.SetSurfaceRect(Rect{Width: 100, Height: 100, Visible: true})
					finished <- nil
				}
			}()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("close deadlocked with a queued surface call")
			}
			if err := c.WaitSurfaceReady(); err != ErrNotRunning {
				t.Fatalf("surface readiness after close: %v", err)
			}
			select {
			case err := <-finished:
				if operation != "rect" && err != ErrNotRunning {
					t.Fatalf("queued operation after close: %v, want ErrNotRunning", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("queued surface call did not return after close")
			}
			if c.SurfaceStats().Attached {
				t.Fatal("surface attached after close")
			}
		})
		if !ok {
			return
		}
	}
}
