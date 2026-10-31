//go:build !darwin

package services

import (
	"context"
	"errors"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/mpv"
)

// Without an embedded engine — none for the platform, or on Windows no
// libmpv-2.dll beside the binary, as beside this test's — the app must still
// start: a startup error ends app.Run, and a GUI build on Windows exits
// without a word. Playing keeps failing loudly.
func TestPlayerStartsWithoutEngine(t *testing.T) {
	svc := NewPlayerService(PlayerOptions{Logger: quiet()})
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	if _, err := svc.MpvVersion(); !errors.Is(err, mpv.ErrUnsupported) && !errors.Is(err, mpv.ErrNoLibrary) {
		t.Fatalf("MpvVersion error = %v, want mpv.ErrUnsupported or mpv.ErrNoLibrary", err)
	}
}
