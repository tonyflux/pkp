package winframe

import (
	"log/slog"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	// saveDelay coalesces a drag or a live resize into one write.
	saveDelay = 500 * time.Millisecond
	// readyWait bounds how long placement waits for Wails to know the
	// screens and to have created the native window. Both happen on their
	// own goroutines around ApplicationStarted; past this the window is
	// shown wherever it is rather than not at all.
	readyWait = 3 * time.Second
)

// Keeper places a window created Hidden once the app has started, shows it,
// and from then on remembers its last normal frame at path.
type Keeper struct {
	window *application.WebviewWindow
	// path is "" when there is no config directory: the window is still
	// placed, nothing is remembered.
	path string
	log  *slog.Logger

	mu sync.Mutex
	// placed is set once the window has its frame. Moves before that are
	// the hidden default frame and Keeper's own placement.
	placed bool
	last   Rect
	dirty  bool
	timer  *time.Timer
}

// Attach wires a Keeper to window, which must have been created Hidden.
// Call it before app.Run.
func Attach(app *application.App, window *application.WebviewWindow, path string, log *slog.Logger) *Keeper {
	k := &Keeper{window: window, path: path, log: log}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		k.place(app)
	})
	window.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) { k.changed() })
	window.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) { k.changed() })
	// A hook runs before the window goes; it only writes what changed()
	// already read, so it never touches a closing window.
	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { k.flush() })
	app.OnShutdown(k.flush)
	return k
}

// place gives the window its frame — the saved one fitted to the screens
// attached now, or the first-launch default — and shows it.
func (k *Keeper) place(app *application.App) {
	var saved *Rect
	if k.path != "" {
		var err error
		if saved, err = Load(k.path); err != nil {
			k.log.Warn("window: saved frame unreadable, using the default", "path", k.path, "error", err)
			saved = nil
		}
	}
	screens := k.awaitScreens(app)
	if frame, floor, ok := Place(saved, screens); ok {
		k.window.SetMinSize(floor.Width, floor.Height)
		// Size before position: Cocoa keeps a window's bottom edge when its
		// height changes, so the top-left is set against the final size.
		k.window.SetSize(frame.Width, frame.Height)
		k.window.SetPosition(frame.X, frame.Y)
	} else {
		k.log.Warn("window: no screens known, showing the window unplaced")
	}
	k.mu.Lock()
	k.placed = true
	k.mu.Unlock()
	k.window.Show()
}

// awaitScreens waits until the screens are cached and the native window
// exists (its bounds are zero before), up to readyWait.
func (k *Keeper) awaitScreens(app *application.App) []Screen {
	deadline := time.Now().Add(readyWait)
	for {
		all := app.Screen.GetAll()
		if (len(all) > 0 && k.window.Bounds().Width > 0) || time.Now().After(deadline) {
			screens := make([]Screen, 0, len(all))
			for _, s := range all {
				wa := s.WorkArea
				screens = append(screens, Screen{
					WorkArea: Rect{X: wa.X, Y: wa.Y, Width: wa.Width, Height: wa.Height},
					Primary:  s.IsPrimary,
				})
			}
			return screens
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// changed records the window's frame after a move or resize, unless it is
// minimised, zoomed or full screen — those are not frames to come back to —
// and schedules a write.
func (k *Keeper) changed() {
	k.mu.Lock()
	placed := k.placed
	k.mu.Unlock()
	if !placed || k.window.IsMinimised() || k.window.IsMaximised() || k.window.IsFullscreen() {
		return
	}
	b := k.window.Bounds()
	if b.Width <= 0 || b.Height <= 0 {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.last = Rect{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}
	k.dirty = true
	if k.timer == nil {
		k.timer = time.AfterFunc(saveDelay, k.flush)
	} else {
		k.timer.Reset(saveDelay)
	}
}

// flush writes the last recorded frame if it has not been written yet.
func (k *Keeper) flush() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.dirty || k.path == "" {
		return
	}
	k.dirty = false
	if err := Save(k.path, k.last); err != nil {
		k.log.Warn("window: could not save the frame", "path", k.path, "error", err)
	}
}
