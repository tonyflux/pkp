package services

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Only the identity is used by viewerAccount; no native window is constructed.
type viewerCaller struct {
	application.Window
	id   uint
	name string
}

func (w viewerCaller) ID() uint     { return w.id }
func (w viewerCaller) Name() string { return w.name }

func TestViewerKeepsOpeningAccountAndNeverFallsBackAfterClose(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if !authAndTokenRoutes(w, r) {
			w.Write([]byte(`{}`))
		}
	})
	opening, err := svc.active()
	if err != nil {
		t.Fatal(err)
	}
	seedSession(t, svc, "another@example.com")
	caller := viewerCaller{id: 41, name: "viewer-test"}
	ctx := context.WithValue(context.Background(), application.WindowKey, application.Window(caller))
	svc.viewerWindows = map[uint]viewerWindow{41: {account: opening.key}}
	got, err := svc.viewerAccount(ctx)
	if err != nil || got != opening {
		t.Fatalf("viewer followed active account: got %v, error %v", got, err)
	}
	delete(svc.viewerWindows, 41)
	if _, err := svc.viewerAccount(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed viewer fell back to current account: %v", err)
	}
	svc.viewerWindows[41] = viewerWindow{account: "removed-account"}
	if _, err := svc.viewerAccount(ctx); !errors.Is(err, errNoSuchAccount) {
		t.Fatalf("removed account fell back to current account: %v", err)
	}
}

func TestViewerReturnCannotBeAcknowledgedByAnotherWindow(t *testing.T) {
	svc := &DriveService{
		player:        &PlayerService{window: &application.WebviewWindow{}},
		viewerWindows: map[uint]viewerWindow{41: {returning: true}},
	}
	caller := viewerCaller{id: 41, name: "viewer-test"}
	ctx := context.WithValue(context.Background(), application.WindowKey, application.Window(caller))
	if err := svc.FinishViewerReturn(ctx, 41, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("detached window acknowledged its own return: %v", err)
	}
	if !svc.viewerWindows[41].returning {
		t.Fatal("unauthorized acknowledgement changed the pending handoff")
	}
}
