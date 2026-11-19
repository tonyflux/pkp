package services

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/mpv"
)

// One surface, two windows: only the window holding the video may place it.
// The page losing the video reports "hidden" on its way out, and that report
// can land after the other window took the video over — honouring it would
// blank the picture the user just moved.
func TestStageFollowsTheWindowHoldingTheVideo(t *testing.T) {
	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	main := application.NewWindow(application.WebviewWindowOptions{Name: "main"})
	popout := application.NewWindow(application.WebviewWindowOptions{Name: "player"})
	svc.AttachWindow(main)

	fromMain := context.WithValue(context.Background(), application.WindowKey, main)
	fromPopout := context.WithValue(context.Background(), application.WindowKey, popout)
	placed := func() mpv.Rect { return svc.SurfaceStats().Rect }

	dock := mpv.Rect{X: 900, Y: 120, Width: 360, Height: 203, Visible: true}
	if err := svc.SetStage(fromMain, dock); err != nil {
		t.Fatal(err)
	}
	if placed() != dock {
		t.Fatalf("dock stage = %+v, want %+v", placed(), dock)
	}

	if err := svc.HostVideo(context.Background()); err == nil {
		t.Error("HostVideo without a calling window must fail, not guess one")
	}

	// Popping out: the separate window takes the video, then the dock's
	// farewell arrives.
	svc.popout = popout
	if err := svc.HostVideo(fromPopout); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetStage(fromMain, mpv.Rect{X: 900, Y: 120, Width: 360, Height: 203}); err != nil {
		t.Fatal(err)
	}
	if placed() != dock {
		t.Errorf("the main window hid a picture it no longer holds: %+v", placed())
	}
	full := mpv.Rect{Width: 960, Height: 540, Visible: true}
	if err := svc.SetStage(fromPopout, full); err != nil {
		t.Fatal(err)
	}
	if placed() != full {
		t.Fatalf("separate window stage = %+v, want %+v", placed(), full)
	}

	// Back to the main window: the separate window's farewell is ignored in
	// turn, and the dock places the picture again.
	if err := svc.CloseWindow(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetStage(fromPopout, mpv.Rect{Width: 960, Height: 540}); err != nil {
		t.Fatal(err)
	}
	if placed() != full {
		t.Errorf("the closed window hid a picture it no longer holds: %+v", placed())
	}
	back := mpv.Rect{X: 900, Y: 120, Width: 360, Height: 203, Visible: true}
	if err := svc.SetStage(fromMain, back); err != nil {
		t.Fatal(err)
	}
	if placed() != back {
		t.Errorf("dock stage after return = %+v, want %+v", placed(), back)
	}
	if svc.CloseWindow() != nil || svc.popout != nil {
		t.Error("closing twice must be a no-op")
	}
}

// A queued stage must not prevent a closing hook from returning the surface,
// and must not place the old window's rectangle after that hook runs.
func TestClosingWhileStageIsQueued(t *testing.T) {
	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	main := application.NewWindow(application.WebviewWindowOptions{Name: "main"})
	popout := application.NewWindow(application.WebviewWindowOptions{Name: "player"})
	svc.AttachWindow(main)
	svc.popout = popout
	svc.host = popout.ID()
	queued := make(chan func(), 1)
	svc.surfaceDispatch = func(fn func()) {
		done := make(chan struct{})
		queued <- func() { defer close(done); fn() }
		<-done
	}
	ctx := context.WithValue(context.Background(), application.WindowKey, popout)
	done := make(chan error, 1)
	go func() { done <- svc.SetStage(ctx, mpv.Rect{Width: 800, Height: 600, Visible: true}) }()
	var stage func()
	select {
	case stage = <-queued:
	case <-time.After(time.Second):
		t.Fatal("stage was not queued")
	}
	closed := make(chan error, 1)
	go func() { closed <- svc.closePopout(popout) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing blocked behind the queued stage")
	}
	stage()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if svc.host != main.ID() || svc.SurfaceStats().Rect.Visible {
		t.Fatal("old stage changed the surface after closing")
	}
}

func TestOldClosingHookDoesNotCloseNewPopout(t *testing.T) {
	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	main := application.NewWindow(application.WebviewWindowOptions{Name: "main"})
	old := application.NewWindow(application.WebviewWindowOptions{Name: "old"})
	current := application.NewWindow(application.WebviewWindowOptions{Name: "current"})
	svc.AttachWindow(main)
	svc.popout = current
	svc.host = current.ID()
	if err := svc.closePopout(old); err != nil {
		t.Fatal(err)
	}
	if svc.popout != current || svc.host != current.ID() {
		t.Fatal("old closing hook affected the current window")
	}
	ctx := context.WithValue(context.Background(), application.WindowKey, old)
	if err := svc.HostVideo(ctx); err == nil {
		t.Fatal("closed window reclaimed the surface")
	}
}

func TestPlayerOpenWindowConcurrentClose(t *testing.T) {
	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	main := application.NewWindow(application.WebviewWindowOptions{Name: "main"})
	popout := application.NewWindow(application.WebviewWindowOptions{Name: "player"})
	svc.AttachWindow(main)
	svc.popout = popout
	svc.host = popout.ID()

	queued := make(chan func(), 1)
	svc.surfaceDispatch = func(fn func()) {
		done := make(chan struct{})
		queued <- func() { defer close(done); fn() }
		<-done
	}

	openErr := make(chan error, 1)
	go func() { openErr <- svc.OpenWindow(true) }()

	var openStep func()
	select {
	case openStep = <-queued:
	case <-time.After(time.Second):
		t.Fatal("OpenWindow did not dispatch onto surface thread")
	}

	closeErr := make(chan error, 1)
	go func() { closeErr <- svc.closePopout(popout) }()
	select {
	case err := <-closeErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("closePopout blocked behind OpenWindow")
	}

	openStep()
	select {
	case err := <-openErr:
		if err == nil {
			t.Fatal("OpenWindow succeeded after popout closed")
		}
	case <-time.After(time.Second):
		t.Fatal("OpenWindow did not finish")
	}

	if svc.popout != nil || svc.host != main.ID() || svc.popoutAudio {
		t.Fatalf("unexpected state after concurrent close: popout=%v host=%d audio=%v", svc.popout, svc.host, svc.popoutAudio)
	}
}

func TestPlayerSurfaceThreadConcurrentAccess(t *testing.T) {
	svc := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	main := application.NewWindow(application.WebviewWindowOptions{Name: "main"})
	popout := application.NewWindow(application.WebviewWindowOptions{Name: "player"})
	svc.AttachWindow(main)
	svc.popout = popout
	svc.host = popout.ID()

	fromPopout := context.WithValue(context.Background(), application.WindowKey, popout)
	fromMain := context.WithValue(context.Background(), application.WindowKey, main)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(4)
		go func(i int) {
			defer wg.Done()
			_ = svc.SetStage(fromPopout, mpv.Rect{Width: float64(400 + i), Height: 300, Visible: true})
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = svc.SetStage(fromMain, mpv.Rect{Width: float64(300 + i), Height: 200, Visible: true})
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = svc.ShowWindowButtons(fromPopout, i%2 == 0)
		}(i)
		go func(i int) {
			defer wg.Done()
			if i == 10 {
				_ = svc.CloseWindow()
			} else {
				_ = svc.OpenWindow(i%2 == 0)
			}
		}(i)
	}
	wg.Wait()

	if err := svc.CloseWindow(); err != nil {
		t.Fatal(err)
	}
	svc.windowClosing = true
	svc.popout = popout
	if err := svc.OpenWindow(false); err == nil {
		t.Fatal("OpenWindow succeeded while windowClosing was true")
	}
}
