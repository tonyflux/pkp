package services

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	EventViewerReturn          = "viewer:return"
	EventViewerReturnCancelled = "viewer:return-cancelled"
)

// ViewerWindowState is an independent snapshot, never a mirror of selection.
type ViewerWindowState struct {
	WindowID uint    `json:"windowId"`
	Entry    Entry   `json:"entry"`
	Siblings []Entry `json:"siblings"`
	Source   string  `json:"source"`
	Sort     string  `json:"sort"`
	Asc      bool    `json:"asc"`
	Comic    bool    `json:"comic"`
}

type ViewerReturnRequest struct {
	Account string            `json:"account"`
	State   ViewerWindowState `json:"state"`
}

type viewerWindow struct {
	window    *application.WebviewWindow
	account   string
	state     ViewerWindowState
	returning bool
}

func (d *DriveService) viewerAccount(ctx context.Context) (*accountSession, error) {
	if caller := callerWindow(ctx); caller != nil {
		d.viewerMu.Lock()
		view, ok := d.viewerWindows[caller.ID()]
		d.viewerMu.Unlock()
		if ok {
			s := d.byKey(view.account)
			if s == nil {
				return nil, errNoSuchAccount
			}
			return s, nil
		}
		if strings.HasPrefix(caller.Name(), "viewer-") {
			return nil, context.Canceled
		}
	}
	return d.active()
}

// ViewerIdentity scopes local reading positions to this window's account.
func (d *DriveService) ViewerIdentity(ctx context.Context) (string, error) {
	s, err := d.viewerAccount(ctx)
	if err != nil {
		return "", err
	}
	return s.key, nil
}

// ViewerList reads the opening account even while the main window browses another.
func (d *DriveService) ViewerList(ctx context.Context, req ListRequest) (ListPage, error) {
	s, err := d.viewerAccount(ctx)
	if err != nil {
		return ListPage{}, err
	}
	req.View = "all"
	req.Query = ""
	req.Category = "all"
	if idx := s.index(); idx != nil && idx.syncer.Ready() {
		return listIndexed(idx.store, req)
	}
	return d.listLive(s, req)
}

type previewOwnerKey struct {
	windowID uint
	clientID string
}

func (d *DriveService) previewOwner(ctx context.Context) previewOwnerKey {
	caller := callerWindow(ctx)
	if caller == nil {
		return previewOwnerKey{}
	}
	// Browser window IDs change on reconnect; the client ID survives registration too.
	if browser, ok := caller.(interface{ ClientID() string }); ok {
		return previewOwnerKey{clientID: browser.ClientID()}
	}
	id := caller.ID()
	owner := previewOwnerKey{windowID: id}
	d.viewerMu.Lock()
	if d.previewOwners == nil {
		d.previewOwners = make(map[uint]bool)
	}
	first := !d.previewOwners[id]
	d.previewOwners[id] = true
	d.viewerMu.Unlock()
	if first {
		caller.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
			if d.player != nil {
				d.player.closePreviewWindow(owner)
			}
		})
	}
	return owner
}

// OpenViewerWindow creates a detached viewer from the current file snapshot.
func (d *DriveService) OpenViewerWindow(ctx context.Context, state ViewerWindowState) error {
	s, err := d.viewerAccount(ctx)
	if err != nil {
		return err
	}
	// The page already has the entry; PreviewFile/ReadText checks it against
	// this account when loading. Opening the window must not wait for the API.
	if _, err := s.api(); err != nil {
		return err
	}
	app := application.Get()
	if app == nil {
		return i18n.New("无法打开查看窗口")
	}
	d.viewerMu.Lock()
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "viewer-" + rand.Text(),
		Title: state.Entry.Name,
		Width: 1000, Height: 760, MinWidth: 480, MinHeight: 360,
		URL: "/?window=viewer", Hidden: true,
		BackgroundColour: application.NewRGB(14, 16, 24),
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
	})
	if d.viewerWindows == nil {
		d.viewerWindows = make(map[uint]viewerWindow)
	}
	d.viewerWindows[window.ID()] = viewerWindow{window: window, account: s.key, state: state}
	d.viewerMu.Unlock()
	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		if d.player != nil {
			d.player.closePreviewWindow(previewOwnerKey{windowID: window.ID()})
		}
		d.viewerMu.Lock()
		delete(d.viewerWindows, window.ID())
		d.viewerMu.Unlock()
	})
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		d.settingsMu.Lock()
		settings := d.readSettings()
		d.settingsMu.Unlock()
		d.applyWebviewProxy(d.proxyConfig(settings))
	})
	window.Show()
	return nil
}

func (d *DriveService) ViewerWindow(ctx context.Context) (ViewerWindowState, error) {
	caller := callerWindow(ctx)
	if caller == nil {
		return ViewerWindowState{}, fmt.Errorf("viewer: no calling window")
	}
	d.viewerMu.Lock()
	view, ok := d.viewerWindows[caller.ID()]
	d.viewerMu.Unlock()
	if !ok {
		return ViewerWindowState{}, fmt.Errorf("viewer: window closed")
	}
	state := view.state
	state.WindowID = caller.ID()
	return state, nil
}

func (d *DriveService) viewerMainWindow() *application.WebviewWindow {
	if d.player == nil {
		return nil
	}
	d.player.mu.Lock()
	main := d.player.window
	d.player.mu.Unlock()
	return main
}

// ReturnViewerToMain requests a handoff; only the main window may acknowledge it.
func (d *DriveService) ReturnViewerToMain(ctx context.Context, state ViewerWindowState) error {
	caller := callerWindow(ctx)
	main := d.viewerMainWindow()
	if caller == nil || main == nil {
		return i18n.New("无法打开查看窗口")
	}
	s, err := d.viewerAccount(ctx)
	if err != nil {
		return err
	}
	if _, err := s.api(); err != nil {
		return err
	}
	d.viewerMu.Lock()
	view, ok := d.viewerWindows[caller.ID()]
	if !ok {
		d.viewerMu.Unlock()
		return context.Canceled
	}
	if view.returning {
		d.viewerMu.Unlock()
		return nil
	}
	view.returning = true
	d.viewerWindows[caller.ID()] = view
	d.viewerMu.Unlock()
	state.WindowID = caller.ID()
	main.Show()
	main.Focus()
	application.Get().Event.Emit(EventViewerReturn, ViewerReturnRequest{Account: view.account, State: state})
	return nil
}

// FinishViewerReturn closes the detached window only after the main page receives it.
func (d *DriveService) FinishViewerReturn(ctx context.Context, windowID uint, restored bool) error {
	caller := callerWindow(ctx)
	main := d.viewerMainWindow()
	if caller == nil || main == nil || caller.ID() != main.ID() {
		return context.Canceled
	}
	d.viewerMu.Lock()
	view, ok := d.viewerWindows[windowID]
	d.viewerMu.Unlock()
	if !ok || !view.returning {
		return nil
	}
	var err error
	if restored {
		var s *accountSession
		s, err = d.active()
		if err == nil {
			if s.key != view.account {
				err = context.Canceled
			} else {
				_, err = s.api()
			}
		}
		restored = err == nil
	}
	d.viewerMu.Lock()
	if current, exists := d.viewerWindows[windowID]; exists {
		current.returning = false
		d.viewerWindows[windowID] = current
	}
	d.viewerMu.Unlock()
	if restored {
		main.Show()
		main.Focus()
		view.window.Close()
	} else {
		application.Get().Event.Emit(EventViewerReturnCancelled, windowID)
	}
	return err
}

// Removed or expired accounts must not leave readable content in other windows.
func (d *DriveService) closeUnavailableViewers() {
	d.viewerMu.Lock()
	views := make([]viewerWindow, 0, len(d.viewerWindows))
	for _, view := range d.viewerWindows {
		views = append(views, view)
	}
	d.viewerMu.Unlock()
	for _, view := range views {
		s := d.byKey(view.account)
		if s == nil {
			view.window.Close()
			continue
		}
		if _, err := s.api(); err != nil {
			view.window.Close()
		}
	}
}
