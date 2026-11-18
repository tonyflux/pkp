//go:build darwin && pkp_surface_test

package mpv

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("PKP_TEST_VIDEO_SURFACE") == "1" {
		runtime.LockOSThread()
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("PKP_TEST_VIDEO_SURFACE") != "1" {
		os.Exit(m.Run())
	}
	// init pins the Go startup goroutine to the process's AppKit thread.
	darwinSurfaceTestPrepare()
	result := make(chan int, 1)
	go func() {
		result <- m.Run()
		darwinSurfaceTestStop()
	}()
	darwinSurfaceTestRun()
	os.Exit(<-result)
}

func TestDarwinSurface(t *testing.T) {
	if os.Getenv("PKP_TEST_VIDEO_SURFACE") != "1" {
		t.Skip("set PKP_TEST_VIDEO_SURFACE=1 in a logged-in macOS desktop session")
	}
	c := New(Options{Logger: quiet(), Extra: map[string]string{
		"ao": "null", "hwdec": "no", "loop-file": "inf",
	}})
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		done := darwinSurfaceTestInvoke(func() { _ = c.Close() })
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("AppKit cleanup did not return")
		}
	})
	var attachErr error
	awaitDarwinSurfaceCall(t, darwinSurfaceTestInvoke(func() {
		attachErr = c.AttachSurface(darwinSurfaceTestWindow(0))
	}))
	if attachErr != nil {
		t.Fatal(attachErr)
	}
	if err := c.WaitSurfaceReady(); err != nil {
		t.Fatal(err)
	}
	if err := c.LoadFile(context.Background(), testClip, 0); err != nil {
		t.Fatal(err)
	}
	var rect = Rect{Width: 320, Height: 240, Visible: true}
	awaitDarwinSurfaceCall(t, darwinSurfaceTestInvoke(func() { c.SetSurfaceRect(rect) }))
	first := waitDarwinFrame(t, c, 0, darwinSurfaceTestWindowNumber(0))

	var moveErr error
	awaitDarwinSurfaceCall(t, darwinSurfaceTestInvoke(func() {
		moveErr = c.MoveSurface(darwinSurfaceTestWindow(1))
	}))
	if moveErr != nil {
		t.Fatal(moveErr)
	}
	awaitDarwinSurfaceCall(t, darwinSurfaceTestInvoke(func() { c.SetSurfaceRect(rect) }))
	waitDarwinFrame(t, c, first.Frames, darwinSurfaceTestWindowNumber(1))

	var closeErr error
	awaitDarwinSurfaceCall(t, darwinSurfaceTestInvoke(func() { closeErr = c.Close() }))
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if c.SurfaceStats().Attached {
		t.Fatal("surface remained attached after UI-thread close")
	}
	if err := c.WaitSurfaceReady(); err != ErrNotRunning {
		t.Fatalf("surface readiness after close: %v", err)
	}
}

func awaitDarwinSurfaceCall(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("AppKit surface operation did not return")
	}
}

func waitDarwinFrame(t *testing.T, c *Client, after uint64, window int64) SurfaceStats {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	windowText := fmt.Sprintf("window=%d ", window)
	for time.Now().Before(deadline) {
		stats := c.SurfaceStats()
		if stats.Attached && stats.Frames > after && strings.Contains(stats.Diagnostic, windowText) {
			return stats
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("surface did not draw in window %d: %+v", window, c.SurfaceStats())
	return SurfaceStats{}
}
