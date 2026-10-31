package services

import (
	"context"
	"testing"

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
