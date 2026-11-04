//go:build darwin

package mpv

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/darwin/libmpv/include -Wno-deprecated-declarations
#cgo LDFLAGS: -L${SRCDIR}/../../build/darwin/libmpv/lib -lmpv -framework Cocoa -framework QuartzCore -framework OpenGL
#include <stdlib.h>
#include <mpv/client.h>
#include "surface_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"sync"
	"time"
	"unsafe"
)

// backendDefaults are the render API's, over the shared defaults.
var backendDefaults = map[string]string{
	"vo": "libmpv", // required by the render API
	// Frames are drawn inside Core Animation's draw of the video layer, on
	// the main thread. By default mpv hands each frame over up to 50ms early
	// and mpv_render_context_render then sleeps until its display time —
	// on that thread, which also applies every WebKit layer commit. While a
	// video played, the page dropped from 72 to 25–40 fps, scrolling
	// included (measured 2026-10-15, 24 fps video: 24ms per draw). At 0 the
	// frame arrives when it is due and the draw returns in about 1ms; A/V
	// stayed within 5ms and no frame was dropped.
	"video-timing-offset": "0",
}

// Client owns one libmpv core. It is safe for concurrent use.
type Client struct {
	opts Options
	log  *slog.Logger
	logs *mpvLog

	// Native callers hold mu until their handle use ends; Close drains them
	// before detaching. The event loop is drained separately via loopDone.
	mu     sync.RWMutex
	handle *C.mpv_handle
	rect   Rect

	loopDone chan struct{}

	handlerMu sync.RWMutex
	handler   func(Event)

	observeID C.uint64_t
}

// New returns a client that has not started a core yet.
func New(opts Options) *Client {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Client{opts: opts, log: log, logs: &mpvLog{log: log}}
}

// SetEventHandler installs the callback for asynchronous events. It runs on
// the event goroutine, so it must not block or re-enter the client.
func (c *Client) SetEventHandler(fn func(Event)) {
	c.handlerMu.Lock()
	c.handler = fn
	c.handlerMu.Unlock()
}

// Running reports whether a core is attached and accepting commands.
func (c *Client) Running() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.handle != nil
}

// Start creates and initialises the core.
func (c *Client) Start(context.Context) error {
	c.mu.Lock()
	if c.handle != nil {
		c.mu.Unlock()
		return fmt.Errorf("mpv: already running")
	}

	handle := C.mpv_create()
	if handle == nil {
		c.mu.Unlock()
		return fmt.Errorf("mpv: could not create a core")
	}

	for name, value := range startOptions(c.opts, backendDefaults) {
		if err := setOptionString(handle, name, value); err != nil {
			C.mpv_terminate_destroy(handle)
			c.mu.Unlock()
			return err
		}
	}

	if status := C.mpv_initialize(handle); status < 0 {
		C.mpv_terminate_destroy(handle)
		c.mu.Unlock()
		return fmt.Errorf("mpv: initialise: %s", errorString(status))
	}

	level := C.CString("warn")
	C.mpv_request_log_messages(handle, level)
	C.free(unsafe.Pointer(level))

	c.handle = handle
	c.loopDone = make(chan struct{})
	done := c.loopDone
	c.mu.Unlock()

	go c.eventLoop(handle, done)
	return nil
}

// Close detaches the render surface and destroys the core. It is idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	handle := c.handle
	c.handle = nil
	done := c.loopDone
	c.loopDone = nil
	c.mu.Unlock()

	if handle == nil {
		return nil
	}

	// The render context holds a reference to the core, so it has to go
	// first; destroying the core underneath it is undefined. Headless cores
	// never had one.
	if !c.opts.Headless {
		C.pkp_surface_detach()
	}

	// Wake the event loop so it observes the detached handle and returns
	// before the core is torn down underneath it.
	C.mpv_wakeup(handle)
	if done != nil {
		<-done
	}
	C.mpv_terminate_destroy(handle)
	return nil
}

// AttachSurface builds the video surface inside the given NSWindow and binds
// a render context to it. Calling it twice is a no-op.
func (c *Client) AttachSurface(nsWindow unsafe.Pointer) error {
	if c.opts.Headless {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	handle := c.handle
	if handle == nil {
		return ErrNotRunning
	}
	if nsWindow == nil {
		return fmt.Errorf("mpv: no native window")
	}

	var cErr *C.char
	if status := C.pkp_surface_attach(nsWindow, handle, &cErr); status != 0 {
		message := "unknown error"
		if cErr != nil {
			message = C.GoString(cErr)
			C.free(unsafe.Pointer(cErr))
		}
		return fmt.Errorf("mpv: attach surface: %s", message)
	}
	return nil
}

// MoveSurface re-parents the attached surface into another NSWindow, hidden
// until the next SetSurfaceRect. The render context is untouched, so the
// playing file never notices.
func (c *Client) MoveSurface(nsWindow unsafe.Pointer) error {
	if c.opts.Headless {
		return nil
	}
	if nsWindow == nil {
		return fmt.Errorf("mpv: no native window")
	}
	var cErr *C.char
	if status := C.pkp_surface_move(nsWindow, &cErr); status != 0 {
		message := "unknown error"
		if cErr != nil {
			message = C.GoString(cErr)
			C.free(unsafe.Pointer(cErr))
		}
		return fmt.Errorf("mpv: move surface: %s", message)
	}
	return nil
}

// SetSurfaceRect moves the video surface. The rectangle is in web
// coordinates, which is what the page can measure. A headless client has no
// surface but still records it, so SurfaceStats reports what was asked for.
func (c *Client) SetSurfaceRect(rect Rect) {
	c.mu.Lock()
	c.rect = rect
	c.mu.Unlock()
	if c.opts.Headless {
		return
	}

	C.pkp_surface_set_rect(
		C.double(rect.X), C.double(rect.Y),
		C.double(math.Max(rect.Width, 1)), C.double(math.Max(rect.Height, 1)),
		boolToCInt(rect.Visible),
	)
}

// SurfaceStats reports what the surface has actually drawn.
func (c *Client) SurfaceStats() SurfaceStats {
	c.mu.RLock()
	rect := c.rect
	c.mu.RUnlock()

	var top, bottom [4]C.uchar
	C.pkp_surface_probe(&top[0], &bottom[0])

	description := C.pkp_surface_describe()
	defer C.free(unsafe.Pointer(description))

	stats := SurfaceStats{
		Attached:   C.pkp_surface_attached() == 1,
		Frames:     uint64(C.pkp_surface_frames()),
		Rect:       rect,
		Diagnostic: C.GoString(description),
	}
	for i := range 4 {
		stats.TopPixel[i] = uint8(top[i])
		stats.BottomPixel[i] = uint8(bottom[i])
	}
	return stats
}

// Capture returns the picture as drawn on the surface, cropped to the video
// (letterbox bars off), at the surface's backing resolution. It waits for
// the next draw, which a pending capture forces even while paused; a hidden
// surface never draws, so the wait is bounded by ctx.
func (c *Client) Capture(ctx context.Context) (*image.RGBA, error) {
	if c.opts.Headless {
		return nil, errors.New("mpv: headless client has no surface")
	}
	timeout := time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	var buf *C.uchar
	var width, height C.int
	switch C.pkp_surface_capture(C.int(timeout/time.Millisecond), &buf, &width, &height) {
	case -1:
		return nil, errors.New("mpv: no video surface")
	case -2:
		return nil, errors.New("mpv: the video surface is not drawing")
	}
	defer C.free(unsafe.Pointer(buf))
	w, h := int(width), int(height)
	pixels := unsafe.Slice((*byte)(unsafe.Pointer(buf)), w*h*4)

	crop := c.videoRect(ctx, w, h)

	// GL rows run bottom-up.
	img := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	for y := range crop.Dy() {
		src := ((h-1-(crop.Min.Y+y))*w + crop.Min.X) * 4
		copy(img.Pix[y*img.Stride:], pixels[src:src+crop.Dx()*4])
	}
	return img, nil
}

// Command runs one mpv command. Arguments are converted to the string form
// mpv's command parser accepts, which is how input.conf expresses them.
func (c *Client) Command(_ context.Context, args ...any) error {
	if len(args) == 0 {
		return fmt.Errorf("mpv: empty command")
	}
	// Convert values before borrowing the core: a Stringer may call back
	// into the client, including closing it.
	argv := make([]*C.char, len(args)+1)
	for i, arg := range args {
		argv[i] = C.CString(scalarString(arg))
	}
	defer func() {
		for _, arg := range argv {
			if arg != nil {
				C.free(unsafe.Pointer(arg))
			}
		}
	}()

	c.mu.RLock()
	handle := c.handle
	if handle == nil {
		c.mu.RUnlock()
		return ErrNotRunning
	}

	status := C.mpv_command(handle, &argv[0])
	c.mu.RUnlock()
	if status < 0 {
		return fmt.Errorf("mpv: %v: %s", args[0], errorString(status))
	}
	return nil
}

// GetProperty reads a property into out, which may be nil to discard it.
func (c *Client) GetProperty(_ context.Context, name string, out any) error {
	c.mu.RLock()
	handle := c.handle
	if handle == nil {
		c.mu.RUnlock()
		return ErrNotRunning
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var node C.mpv_node
	status := C.mpv_get_property(handle, cName, C.MPV_FORMAT_NODE, unsafe.Pointer(&node))
	c.mu.RUnlock()
	if status < 0 {
		return fmt.Errorf("mpv: get %s: %s", name, errorString(status))
	}
	defer C.mpv_free_node_contents(&node)

	if out == nil {
		return nil
	}
	raw, err := nodeJSON(&node)
	if err != nil {
		return fmt.Errorf("mpv: encode %s: %w", name, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("mpv: decode %s: %w", name, err)
	}
	return nil
}

// SetProperty writes a property. Values go over as strings, which mpv parses
// per the property's own type.
func (c *Client) SetProperty(_ context.Context, name string, value any) error {
	text := scalarString(value)
	c.mu.RLock()
	defer c.mu.RUnlock()
	handle := c.handle
	if handle == nil {
		return ErrNotRunning
	}
	if err := setPropertyString(handle, name, text); err != nil {
		return err
	}
	return nil
}

// ObserveProperty subscribes to change notifications for name. They arrive as
// events named EventPropertyChange.
func (c *Client) ObserveProperty(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	handle := c.handle
	c.observeID++
	id := c.observeID
	if handle == nil {
		return ErrNotRunning
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	if status := C.mpv_observe_property(handle, id, cName, C.MPV_FORMAT_NODE); status < 0 {
		return fmt.Errorf("mpv: observe %s: %s", name, errorString(status))
	}
	return nil
}

// owns reports whether handle is still the client's core. Close clears it
// before waking the event loop.
func (c *Client) owns(handle *C.mpv_handle) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.handle == handle
}

func (c *Client) eventLoop(handle *C.mpv_handle, done chan struct{}) {
	defer close(done)

	// Checked before every wait, not only on a timeout: while a file plays,
	// property changes arrive faster than the timeout, and mpv_wait_event
	// drops Close's wakeup when it returns one of them instead. The loop
	// must stop here, or Close waits for it forever. A restarted core is a
	// different handle, so this loop still stops for its own.
	for c.owns(handle) {
		event := C.mpv_wait_event(handle, C.double(1))
		if event == nil {
			return
		}

		switch event.event_id {
		case C.MPV_EVENT_SHUTDOWN:
			c.emit(Event{Name: EventShutdown})
			return

		case C.MPV_EVENT_LOG_MESSAGE:
			message := (*C.mpv_event_log_message)(event.data)
			c.logs.message(C.GoString(message.prefix), trimNewline(C.GoString(message.text)))

		case C.MPV_EVENT_FILE_LOADED:
			c.emit(Event{Name: EventFileLoaded})

		case C.MPV_EVENT_PLAYBACK_RESTART:
			c.emit(Event{Name: EventPlaybackRestart})

		case C.MPV_EVENT_END_FILE:
			end := (*C.mpv_event_end_file)(event.data)
			c.emit(Event{Name: EventEndFile, Reason: endReason(end.reason)})

		case C.MPV_EVENT_PROPERTY_CHANGE:
			property := (*C.mpv_event_property)(event.data)
			ev := Event{Name: EventPropertyChange, Property: C.GoString(property.name)}
			if property.format == C.MPV_FORMAT_NODE && property.data != nil {
				if raw, err := nodeJSON((*C.mpv_node)(property.data)); err == nil {
					ev.Data = raw
				}
			} else {
				ev.Data = json.RawMessage("null")
			}
			c.emit(ev)
		}
	}
}

func (c *Client) emit(ev Event) {
	c.handlerMu.RLock()
	fn := c.handler
	c.handlerMu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}
