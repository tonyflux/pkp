package services

import (
	"context"
	"math"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/videowin"
)

// The separate player window. There is one video surface, a native view, and
// popping out moves that view into another window instead of starting a
// second player: the file, its position, the audio and the buffer carry on
// untouched. The playback session — playlist, cursor, play mode, what
// happens when a file ends — stays with the main window's page. The separate
// window's page mirrors it (EventPlayerSession) and sends what the user asks
// for back (EventPlayerCommand); mpv's own state reaches both pages anyway.

const (
	// EventPlayerWindowClosed tells every page the separate player window is
	// gone and the surface is back in the main window.
	EventPlayerWindowClosed = "player:window-closed"
	// EventPlayerSession and EventPlayerCommand go from one page to the
	// other; Go never emits them, it only registers their types.
	// EventPlayerSession carries the main window's session to the separate
	// window, which shows it but does not own it.
	EventPlayerSession = "player:session"
	// EventPlayerCommand carries a session change asked for in the separate
	// window to the main window, which carries it out.
	EventPlayerCommand = "player:command"
)

// PlayerSession is the playback session as the main window's page holds it.
type PlayerSession struct {
	Playlist []Entry `json:"playlist"`
	Index    int     `json:"index"`
	// Mode is the play mode: "sequential", "one", "shuffle" or "stop".
	Mode string `json:"mode"`
	// Owner is the account the videos belong to, which the separate window
	// names as the main window's player does.
	Owner  Account `json:"owner"`
	Source string  `json:"source"`
}

// PlayerCommand is a session change asked for in the separate window.
type PlayerCommand struct {
	// Name is "sync" (send the session again), "playAt", "stop",
	// "cancelNext" (skip the automatic advance once) or "mode".
	Name string `json:"name"`
	// Index is the playlist position for "playAt".
	Index int `json:"index"`
	// Mode is the play mode for "mode", as in PlayerSession.
	Mode string `json:"mode,omitempty"`
}

// playerWindowURL is the page the separate window loads; the frontend mounts
// the bare player for it instead of the library.
const playerWindowURL = "/?window=player"

// OpenWindow opens the separate player window, or brings it forward when it
// is already open. Only video claims the surface with HostVideo once it is up.
//
// Music resizes freely and keeps the standard window buttons visible.
// It is a player's window, shaped like the picture, which it stays through
// every resize, and moved by dragging the picture. While the video plays on
// untouched it has no frame at all — the corners and shadow stay — and its
// title bar, traffic lights included, comes over the picture with the
// controls (ShowWindowButtons). Windows has no inset title bar to keep the
// buttons in: the window has no frame there, its page draws the caption
// buttons in that title bar, and it is never maximised — its shape is the
// picture's.
func (p *PlayerService) OpenWindow(audio bool) error {
	p.openWindowMu.Lock()
	defer p.openWindowMu.Unlock()
	p.popoutMu.Lock()
	if p.popout != nil {
		popout := p.popout
		p.popoutMu.Unlock()
		live := false
		p.onSurfaceThread(func() {
			if p.windowClosing || !p.liveSurfaceWindow(popout) {
				return
			}
			live = true
			p.popoutMu.Lock()
			changed := p.popoutAudio != audio
			p.popoutAudio = audio
			p.popoutMu.Unlock()
			if application.Get() != nil {
				if changed {
					p.configurePopoutOnSurface(popout, audio)
				}
				popout.Show()
				popout.Focus()
			}
		})
		if !live {
			return i18n.New("无法打开播放窗口")
		}
		return nil
	}
	app := application.Get()
	if app == nil {
		p.popoutMu.Unlock()
		return i18n.New("无法打开播放窗口")
	}
	p.popoutMu.Unlock()

	onWindows := runtime.GOOS == "windows"
	width, height, minHeight := 960, 540, 270
	if audio {
		width, height, minHeight = 560, 700, 420
	}
	maximise := application.ButtonEnabled
	if onWindows && !audio {
		maximise = application.ButtonHidden
	}
	popout := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "player",
		Title:     i18n.T("PKP 播放器"),
		Width:     width,
		Height:    height,
		MinWidth:  480,
		MinHeight: minHeight,
		URL:       playerWindowURL,
		// Shown once shaped like the picture, so it never jumps.
		Hidden: true,
		// Letterboxing and the moment before the first frame are black, as
		// in any player; the page paints nothing over the picture. On
		// Windows the webview turns see-through with the stage (followStage).
		BackgroundColour:    application.NewRGB(0, 0, 0),
		Frameless:           onWindows && !audio,
		MaximiseButtonState: maximise,
		Mac: application.MacWindow{
			// The page draws the title bar; this one only holds the
			// traffic lights, inset to sit in it. The page's drag regions
			// move the window: nothing native takes its clicks.
			TitleBar: application.MacTitleBarHiddenInset,
		},
	})
	// Closing leaves the same session in the main window's playback bar.
	popout.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		var err error
		p.onSurfaceThread(func() { err = p.closePopout(popout) })
		if err != nil {
			p.log.Warn("player: video surface could not return to the main window", "error", err)
			e.Cancel()
		}
	})
	// Fullscreen ignores the proportions; coming out, the frame is the one
	// from before, which may be another video's shape. Fullscreen also kept
	// the traffic lights on.
	popout.OnWindowEvent(events.Mac.WindowDidExitFullScreen, func(*application.WindowEvent) {
		p.shapePopout(true)
		p.onSurfaceThread(func() {
			if !p.liveSurfaceWindow(popout) {
				return
			}
			p.popoutMu.Lock()
			show := p.buttons
			p.popoutMu.Unlock()
			videowin.ShowButtons(popout.NativeWindow(), show)
		})
	})
	// Windows: leaving fullscreen, or the taskbar, puts back a frame that
	// was not refitted meanwhile.
	for _, back := range []events.WindowEventType{events.Windows.WindowUnFullscreen, events.Windows.WindowUnMinimise} {
		popout.OnWindowEvent(back, func(*application.WindowEvent) { p.shapePopout(true) })
	}
	opened := false
	p.onSurfaceThread(func() {
		if p.windowClosing {
			return
		}
		p.popoutMu.Lock()
		p.popout = popout
		p.popoutAudio = audio
		p.shaped = 0
		p.buttons = true
		opened = true
		p.popoutMu.Unlock()
		popout.Show()
		p.shapePopout(true)
	})
	if !opened {
		popout.Close()
		return i18n.New("无法打开播放窗口")
	}

	return nil
}

func (p *PlayerService) configurePopoutOnSurface(window *application.WebviewWindow, audio bool) {
	if audio {
		if p.liveSurfaceWindow(window) {
			videowin.FreeAspect(window.NativeWindow())
			videowin.ShowButtons(window.NativeWindow(), true)
		}
		window.SetMinSize(480, 420)
		window.SetSize(560, 700)
	} else {
		window.SetMinSize(480, 270)
	}
	if runtime.GOOS == "windows" {
		window.SetFrameless(!audio)
		if audio {
			window.SetMaximiseButtonState(application.ButtonEnabled)
		} else {
			window.SetMaximiseButtonState(application.ButtonHidden)
		}
	}
	p.shapePopout(true)
}

// ShowWindowButtons shows or hides the separate window's traffic lights,
// which come and go with the title bar its page draws over the picture.
// Only that page calls it, for its own window.
func (p *PlayerService) ShowWindowButtons(ctx context.Context, show bool) error {
	caller := callerWindow(ctx)
	p.onSurfaceThread(func() {
		if p.windowClosing {
			return
		}
		p.popoutMu.Lock()
		popout := p.popout
		if popout == nil || caller == nil || caller.ID() != popout.ID() {
			p.popoutMu.Unlock()
			return
		}
		if p.popoutAudio {
			show = true
		}
		p.buttons = show
		p.popoutMu.Unlock()
		videowin.ShowButtons(popout.NativeWindow(), show)
	})
	return nil
}

// notePicture records the picture's display size and reshapes the separate
// window when its proportions changed: another video, not another rendition
// of the same one, and not the moment between two files when there is none.
func (p *PlayerService) notePicture(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	p.popoutMu.Lock()
	p.picture = [2]int{w, h}
	p.popoutMu.Unlock()
	p.shapePopout(false)
}

// shapePopout applies the latest picture dimensions on the UI thread, after
// checking that the window is still current. Shutdown must not wait for it.
func (p *PlayerService) shapePopout(force bool) {
	if application.Get() == nil {
		return
	}
	application.InvokeAsync(func() {
		p.popoutMu.Lock()
		popout, w, h := p.popout, p.picture[0], p.picture[1]
		if popout == nil || p.popoutAudio || w <= 0 || h <= 0 {
			p.popoutMu.Unlock()
			return
		}
		ratio := float64(w) / float64(h)
		if !force && math.Abs(ratio-p.shaped) < 0.01*ratio {
			p.popoutMu.Unlock()
			return
		}
		p.shaped = ratio
		p.popoutMu.Unlock()
		if !p.liveSurfaceWindow(popout) {
			return
		}
		videowin.Fit(popout.NativeWindow(), w, h)
	})
}

// HostVideo moves the video surface into the calling window. The separate
// window's page calls it once it is up — its webview, which the surface is
// slotted under, exists by then — and before it reports a stage.
func (p *PlayerService) HostVideo(ctx context.Context) error {
	caller := callerWindow(ctx)
	if caller == nil {
		return i18n.New("找不到要放入视频的窗口")
	}
	if err := p.ensureSurface(); err != nil {
		return err
	}
	var err error
	p.onSurfaceThread(func() {
		if !p.liveSurfaceWindow(caller) {
			err = i18n.New("找不到要放入视频的窗口")
			return
		}
		err = p.moveSurface(caller)
	})
	return err
}

// CloseWindow puts the video back in the main window and closes the separate
// one.
func (p *PlayerService) CloseWindow() error {
	p.openWindowMu.Lock()
	defer p.openWindowMu.Unlock()
	var popout *application.WebviewWindow
	var err error
	p.onSurfaceThread(func() { popout, err = p.returnHome(nil) })
	if err != nil {
		return err
	}
	if popout != nil {
		popout.Close()
	}
	return nil
}

// poppedOut is whether the separate player window has the video now.
func (p *PlayerService) poppedOut() bool {
	p.popoutMu.Lock()
	defer p.popoutMu.Unlock()
	return p.popout != nil
}

// returnHome moves the surface back into the main window, then announces it,
// so the main window's page can place the picture the moment it hears. It
// runs once per separate window: from the buttons before they close it, and
// from the window's closing hook when it is closed by hand. It hands back
// the window that is to close, nil when there was none.
func (p *PlayerService) returnHome(expected *application.WebviewWindow) (*application.WebviewWindow, error) {
	p.popoutMu.Lock()
	popout := p.popout
	p.popoutMu.Unlock()
	if popout == nil || (expected != nil && popout != expected) {
		return nil, nil
	}
	p.mu.Lock()
	main := p.window
	p.mu.Unlock()
	if main != nil {
		if err := p.moveSurface(main); err != nil {
			return nil, err
		}
	}
	p.popoutMu.Lock()
	p.popout = nil
	p.popoutMu.Unlock()
	delete(p.seeThrough, popout.ID())
	if app := application.Get(); app != nil {
		app.Event.Emit(EventPlayerWindowClosed)
	}
	return popout, nil
}

func (p *PlayerService) closePopout(popout *application.WebviewWindow) error {
	_, err := p.returnHome(popout)
	return err
}

// moveSurface runs on the UI thread, with the target still alive.
func (p *PlayerService) moveSurface(to application.Window) error {
	if p.host == to.ID() {
		return nil
	}
	p.mu.Lock()
	attached := p.attached
	p.mu.Unlock()
	if attached {
		if err := p.mpv.MoveSurface(to.NativeWindow()); err != nil {
			return err
		}
	}
	p.host = to.ID()
	return nil
}

// callerWindow is the window whose page made the bound call. Wails puts it
// in the call's context; a call from Go has none.
func callerWindow(ctx context.Context) application.Window {
	if ctx == nil {
		return nil
	}
	window, _ := ctx.Value(application.WindowKey).(application.Window)
	return window
}
