//go:build windows

package mpv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// backendDefaults: none. The bundled libmpv is built without Lua, as on
// macOS, so there are no scripts to switch off (and mpv would refuse their
// options).
//
// No video output options: mpv draws into a window of its own inside the
// surface (--wid, set when the surface is attached) with its default
// output, which on Windows tries Direct3D 11 first: a flip-model swapchain,
// which DWM composites under the WebView2's transparent pixels. Should that
// fail, mpv's own fallbacks are still a better chance than none.
var backendDefaults map[string]string

// Client owns one libmpv core. It is safe for concurrent use.
type Client struct {
	opts Options
	log  *slog.Logger
	logs *mpvLog

	mu     sync.RWMutex
	handle uintptr
	wake   *wakeup
	rect   Rect

	loopDone chan struct{}

	handlerMu sync.RWMutex
	handler   func(Event)

	observeID uint64

	surface surface
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
	return c.handle != 0
}

// Start creates and initialises the core. libmpv is loaded first if it has
// not been; without it Start fails with ErrNoLibrary.
func (c *Client) Start(context.Context) error {
	l, err := lib()
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.handle != 0 {
		c.mu.Unlock()
		return fmt.Errorf("mpv: already running")
	}

	handle, _, _ := l.create.Call()
	if handle == 0 {
		c.mu.Unlock()
		return fmt.Errorf("mpv: could not create a core")
	}
	// Before anything can queue an event, so the loop hears of all of them.
	w := newWakeup()
	l.setWakeupCallback.Call(handle, l.wakeupCallback, w.id)
	fail := func(err error) error {
		l.terminateDestroy.Call(handle)
		w.release()
		c.mu.Unlock()
		return err
	}

	for name, value := range startOptions(c.opts, backendDefaults) {
		if err := l.setOption(handle, name, value); err != nil {
			return fail(err)
		}
	}

	r, _, _ := l.initialize.Call(handle)
	if status := int32(r); status < 0 {
		return fail(fmt.Errorf("mpv: initialise: %s", l.errorText(status)))
	}

	level, _ := cString("warn")
	l.requestLogMessages.Call(handle, uintptr(unsafe.Pointer(level)))

	c.handle = handle
	c.wake = w
	c.loopDone = make(chan struct{})
	done := c.loopDone
	c.mu.Unlock()

	go c.eventLoop(l, handle, w, done)
	return nil
}

// Close destroys the core. It is idempotent. The surface stays where it is,
// for the next core to draw into: mpv's window inside it goes with the core.
func (c *Client) Close() error {
	c.mu.Lock()
	handle, w, done := c.handle, c.wake, c.loopDone
	c.handle, c.wake, c.loopDone = 0, nil, nil
	c.mu.Unlock()

	if handle == 0 {
		return nil
	}
	l, _ := lib() // loaded: there was a core

	// The loop never waits inside mpv (see wakeup), so waking it is enough
	// for it to see the handle gone and return before the core goes.
	w.wake()
	if done != nil {
		<-done
	}
	l.terminateDestroy.Call(handle)
	w.release()
	return nil
}

// AttachSurface builds the video surface inside the given window (an HWND)
// and hands it to mpv, which draws into it from the next video on. Calling
// it twice is a no-op; a core started later is given the same surface.
func (c *Client) AttachSurface(window unsafe.Pointer) error {
	if c.opts.Headless {
		return nil
	}
	c.mu.RLock()
	handle := c.handle
	c.mu.RUnlock()
	if handle == 0 {
		return ErrNotRunning
	}
	if window == nil {
		return fmt.Errorf("mpv: no native window")
	}

	hwnd, err := c.surface.attach(uintptr(window))
	if err != nil {
		return fmt.Errorf("mpv: attach surface: %w", err)
	}
	l, _ := lib()
	return l.setProperty(handle, "wid", strconv.FormatUint(uint64(hwnd), 10))
}

// MoveSurface re-parents the attached surface into another window (an HWND),
// hidden until the next SetSurfaceRect. mpv's window inside goes with it, so
// the playing file never notices.
func (c *Client) MoveSurface(window unsafe.Pointer) error {
	if c.opts.Headless {
		return nil
	}
	if window == nil {
		return fmt.Errorf("mpv: no native window")
	}
	if err := c.surface.move(uintptr(window)); err != nil {
		return fmt.Errorf("mpv: move surface: %w", err)
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
	c.surface.setRect(rect)
}

// SurfaceStats reports the surface's state. mpv draws into its own window,
// out of reach from here, so Frames and the sampled pixels stay zero.
func (c *Client) SurfaceStats() SurfaceStats {
	c.mu.RLock()
	rect, handle := c.rect, c.handle
	c.mu.RUnlock()
	return SurfaceStats{
		Attached:   handle != 0 && c.surface.hwnd.Load() != 0,
		Rect:       rect,
		Diagnostic: c.surface.describe(),
	}
}

// Capture returns the picture as drawn on the surface, cropped to the video
// (letterbox bars off), at the surface's size in pixels. mpv draws the
// window itself, so it is asked: screenshot-raw's window mode renders what
// the window shows, subtitles included, on the GPU, where hardware-decoded
// frames already are.
func (c *Client) Capture(ctx context.Context) (*image.RGBA, error) {
	if c.opts.Headless {
		return nil, errors.New("mpv: headless client has no surface")
	}
	c.mu.RLock()
	handle := c.handle
	c.mu.RUnlock()
	if handle == 0 {
		return nil, ErrNotRunning
	}
	l, _ := lib()

	var result node
	if err := l.commandNode(handle, &result, "screenshot-raw", "window"); err != nil {
		return nil, err
	}
	defer l.freeNode(&result)
	shot, err := screenshotOf(&result)
	if err != nil {
		return nil, err
	}

	crop := c.videoRect(ctx, shot.w, shot.h)
	img := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	for y := range crop.Dy() {
		src := unsafe.Slice((*byte)(unsafe.Add(shot.data, (crop.Min.Y+y)*shot.stride+crop.Min.X*4)), crop.Dx()*4)
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < len(src); x += 4 {
			dst[x], dst[x+1], dst[x+2], dst[x+3] = src[x+2], src[x+1], src[x], 0xff
		}
	}
	return img, nil
}

// screenshot is screenshot-raw's image, still in libmpv's memory: bgr0 rows
// stride bytes apart.
type screenshot struct {
	w, h, stride int
	data         unsafe.Pointer
}

func screenshotOf(n *node) (screenshot, error) {
	var shot screenshot
	if n.format != formatNodeMap || n.pointer() == nil {
		return shot, fmt.Errorf("mpv: screenshot-raw returned node format %d", n.format)
	}
	list := (*nodeList)(n.pointer())
	var format string
	var size int
	values := unsafe.Slice(list.values, list.num)
	for i, key := range unsafe.Slice(list.keys, list.num) {
		v := &values[i]
		switch name := windows.BytePtrToString(key); {
		case v.format == formatInt64 && name == "w":
			shot.w = int(int64(v.u))
		case v.format == formatInt64 && name == "h":
			shot.h = int(int64(v.u))
		case v.format == formatInt64 && name == "stride":
			shot.stride = int(int64(v.u))
		case v.format == formatString && name == "format":
			format = windows.BytePtrToString((*byte)(v.pointer()))
		case v.format == formatByteArray && name == "data":
			array := (*byteArray)(v.pointer())
			shot.data, size = array.data, int(array.size)
		}
	}
	if format != "bgr0" {
		return shot, fmt.Errorf("mpv: screenshot-raw returned %q pixels", format)
	}
	// A negative stride (rows bottom-up) is allowed by mpv and never seen
	// from a window screenshot; refused rather than read wrong.
	if shot.data == nil || shot.w <= 0 || shot.h <= 0 || shot.stride < shot.w*4 || (shot.h-1)*shot.stride+shot.w*4 > size {
		return shot, fmt.Errorf("mpv: screenshot-raw returned a %dx%d image, stride %d, in %d bytes", shot.w, shot.h, shot.stride, size)
	}
	return shot, nil
}

// Command runs one mpv command. Arguments are converted to the string form
// mpv's command parser accepts, which is how input.conf expresses them.
func (c *Client) Command(_ context.Context, args ...any) error {
	if len(args) == 0 {
		return fmt.Errorf("mpv: empty command")
	}
	c.mu.RLock()
	handle := c.handle
	c.mu.RUnlock()
	if handle == 0 {
		return ErrNotRunning
	}
	l, _ := lib()

	words := make([]string, len(args))
	for i, arg := range args {
		words[i] = scalarString(arg)
	}
	argv, err := cArgv(words)
	if err != nil {
		return fmt.Errorf("mpv: %v: %w", args[0], err)
	}
	r, _, _ := l.command.Call(handle, uintptr(unsafe.Pointer(&argv[0])))
	if status := int32(r); status < 0 {
		return fmt.Errorf("mpv: %v: %s", args[0], l.errorText(status))
	}
	return nil
}

// GetProperty reads a property into out, which may be nil to discard it.
func (c *Client) GetProperty(_ context.Context, name string, out any) error {
	c.mu.RLock()
	handle := c.handle
	c.mu.RUnlock()
	if handle == 0 {
		return ErrNotRunning
	}
	l, _ := lib()

	cName, err := cString(name)
	if err != nil {
		return fmt.Errorf("mpv: get %s: %w", name, err)
	}
	var n node
	r, _, _ := l.getProperty.Call(handle, uintptr(unsafe.Pointer(cName)), formatNode, uintptr(unsafe.Pointer(&n)))
	if status := int32(r); status < 0 {
		return fmt.Errorf("mpv: get %s: %s", name, l.errorText(status))
	}
	defer l.freeNode(&n)

	if out == nil {
		return nil
	}
	raw, err := nodeJSON(&n)
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
	c.mu.RLock()
	handle := c.handle
	c.mu.RUnlock()
	if handle == 0 {
		return ErrNotRunning
	}
	l, _ := lib()
	return l.setProperty(handle, name, scalarString(value))
}

// ObserveProperty subscribes to change notifications for name. They arrive as
// events named EventPropertyChange.
func (c *Client) ObserveProperty(_ context.Context, name string) error {
	c.mu.Lock()
	handle := c.handle
	c.observeID++
	id := c.observeID
	c.mu.Unlock()
	if handle == 0 {
		return ErrNotRunning
	}
	l, _ := lib()

	cName, err := cString(name)
	if err != nil {
		return fmt.Errorf("mpv: observe %s: %w", name, err)
	}
	r, _, _ := l.observeProperty.Call(handle, uintptr(id), uintptr(unsafe.Pointer(cName)), formatNode)
	if status := int32(r); status < 0 {
		return fmt.Errorf("mpv: observe %s: %s", name, l.errorText(status))
	}
	return nil
}

// owns reports whether handle is still the client's core. Close clears it
// before waking the event loop.
func (c *Client) owns(handle uintptr) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.handle == handle
}

// eventLoop drains the queue each time mpv says there is something in it.
// The handle is checked before every event: Close only clears it and wakes
// the loop, and a restarted core is a different handle, so this loop still
// stops for its own.
func (c *Client) eventLoop(l *library, handle uintptr, w *wakeup, done chan struct{}) {
	defer close(done)
	for range w.ch {
		for {
			if !c.owns(handle) {
				return
			}
			ev := l.nextEvent(handle, w)
			if ev == nil {
				return
			}
			if ev.id == eventNone {
				break
			}
			if !c.dispatch(ev) {
				return
			}
		}
	}
}

// dispatch hands one event on; false once the core has shut down.
func (c *Client) dispatch(ev *event) bool {
	switch ev.id {
	case eventShutdown:
		c.emit(Event{Name: EventShutdown})
		return false

	case eventLogMessage:
		message := (*logMessage)(ev.data)
		c.logs.message(windows.BytePtrToString(message.prefix), trimNewline(windows.BytePtrToString(message.text)))

	case eventFileLoaded:
		c.emit(Event{Name: EventFileLoaded})

	case eventPlaybackRestart:
		c.emit(Event{Name: EventPlaybackRestart})

	case eventEndFile:
		end := (*endFile)(ev.data)
		c.emit(Event{Name: EventEndFile, Reason: endReason(end.reason)})

	case eventPropertyChange:
		property := (*propertyChange)(ev.data)
		out := Event{Name: EventPropertyChange, Property: windows.BytePtrToString(property.name)}
		if property.format == formatNode && property.data != nil {
			if raw, err := nodeJSON((*node)(property.data)); err == nil {
				out.Data = raw
			}
		} else {
			out.Data = json.RawMessage("null")
		}
		c.emit(out)
	}
	return true
}

func (c *Client) emit(ev Event) {
	c.handlerMu.RLock()
	fn := c.handler
	c.handlerMu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}
