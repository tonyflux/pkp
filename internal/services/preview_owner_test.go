package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type previewTestWindow struct {
	application.Window
	id    uint
	hooks []func(*application.WindowEvent)
}

func (w *previewTestWindow) ID() uint { return w.id }
func (w *previewTestWindow) RegisterHook(_ events.WindowEventType, hook func(*application.WindowEvent)) func() {
	w.hooks = append(w.hooks, hook)
	return func() {}
}

type previewTestBrowser struct {
	previewTestWindow
	clientID string
}

func (w *previewTestBrowser) ClientID() string { return w.clientID }

func previewTestContext(window application.Window) context.Context {
	return context.WithValue(context.Background(), application.WindowKey, window)
}

func previewTestService(t *testing.T) (*DriveService, string) {
	t.Helper()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "preview")
	}))
	t.Cleanup(origin.Close)
	player := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := player.stream.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = player.stream.Close() })
	return &DriveService{player: player}, origin.URL
}

func previewTestStatus(t *testing.T, url string) int {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func TestPreviewBrowserReconnectOwnership(t *testing.T) {
	for _, initialID := range []uint{0, 1} {
		t.Run(fmt.Sprintf("initial_id_%d", initialID), func(t *testing.T) {
			svc, origin := previewTestService(t)
			first := &previewTestBrowser{previewTestWindow: previewTestWindow{id: initialID}, clientID: "clientA"}
			url, err := svc.player.openPreview(svc.previewOwner(previewTestContext(first)), origin, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if status := previewTestStatus(t, url); status != http.StatusOK {
				t.Fatalf("published preview status = %d", status)
			}

			other := &previewTestBrowser{previewTestWindow: previewTestWindow{id: 2}, clientID: "clientB"}
			svc.ClosePreview(previewTestContext(other), url)
			if status := previewTestStatus(t, url); status != http.StatusOK {
				t.Fatalf("another browser client revoked preview: status = %d", status)
			}
			if len(svc.previewOwners) != 0 || len(first.hooks) != 0 || len(other.hooks) != 0 {
				t.Fatal("browser preview relied on native closing hooks")
			}

			native := &previewTestWindow{id: initialID}
			svc.ClosePreview(previewTestContext(native), url)
			if status := previewTestStatus(t, url); status != http.StatusOK {
				t.Fatalf("native window revoked browser preview: status = %d", status)
			}

			reconnected := &previewTestBrowser{previewTestWindow: previewTestWindow{id: 2}, clientID: "clientA"}
			svc.ClosePreview(previewTestContext(reconnected), url)
			if status := previewTestStatus(t, url); status != http.StatusNotFound {
				t.Fatalf("reconnected browser left old URL live: status = %d", status)
			}
			if len(reconnected.hooks) != 0 {
				t.Fatal("reconnected browser registered a native closing hook")
			}
		})
	}
}

func TestPreviewNativeOwnerCloseGuard(t *testing.T) {
	svc, origin := previewTestService(t)
	first := &previewTestWindow{id: 1}
	ctx := previewTestContext(first)
	owner := svc.previewOwner(ctx)
	if repeated := svc.previewOwner(ctx); repeated != owner || len(first.hooks) != 1 {
		t.Fatal("native owner changed or registered duplicate closing hooks")
	}
	url, err := svc.player.openPreview(owner, origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	other := &previewTestWindow{id: 2}
	svc.ClosePreview(previewTestContext(other), url)
	if status := previewTestStatus(t, url); status != http.StatusOK {
		t.Fatalf("another native window revoked preview: status = %d", status)
	}
	first.hooks[0](nil)
	if status := previewTestStatus(t, url); status != http.StatusNotFound {
		t.Fatalf("native window closing left preview live: status = %d", status)
	}
	if _, err := svc.player.openPreview(svc.previewOwner(ctx), origin, nil, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed native window published an in-flight preview: %v", err)
	}
}

func TestPreviewServerRejectsRegistrationGaps(t *testing.T) {
	if !application.System.IsServer() {
		t.Skip("server-mode browser registration boundary")
	}
	svc, origin := previewTestService(t)
	fallback := &previewTestWindow{id: 1}
	for _, ctx := range []context.Context{context.Background(), previewTestContext(fallback)} {
		url, err := svc.player.openPreview(svc.previewOwner(ctx), origin, nil, "")
		if url != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("unregistered browser obtained a preview capability: %q %v", url, err)
		}
	}
	registered := &previewTestBrowser{previewTestWindow: previewTestWindow{id: 2}, clientID: "clientA"}
	url, err := svc.player.openPreview(svc.previewOwner(previewTestContext(registered)), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if status := previewTestStatus(t, url); status != http.StatusOK {
		t.Fatalf("registered browser preview status = %d", status)
	}
	svc.ClosePreview(previewTestContext(registered), url)
	if status := previewTestStatus(t, url); status != http.StatusNotFound {
		t.Fatalf("registered browser could not revoke preview: %d", status)
	}
}
