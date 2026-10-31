//go:build windows

package videowin

import (
	"math"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// Win32 has no content aspect ratio. The window's procedure is wrapped
// instead, to keep the proportions while the user drags an edge or a corner
// (WM_SIZING), and Fit sets the frame itself. Both run on the thread that
// owns the window.

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procCallWindowProcW   = user32.NewProc("CallWindowProcW")
	procGetClientRect     = user32.NewProc("GetClientRect")
	procGetMonitorInfoW   = user32.NewProc("GetMonitorInfoW")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procIsIconic          = user32.NewProc("IsIconic")
	procIsWindow          = user32.NewProc("IsWindow")
	procIsZoomed          = user32.NewProc("IsZoomed")
	procMonitorFromWindow = user32.NewProc("MonitorFromWindow")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
)

const (
	wmNCDestroy = 0x0082
	wmSizing    = 0x0214

	// WM_SIZING's edges.
	wmszTop      = 3
	wmszTopLeft  = 4
	wmszTopRight = 5
	wmszBottom   = 6

	gwlpWndProc = ^uintptr(3) // GWLP_WNDPROC, -4

	swpNoZOrder      = 0x0004
	swpNoActivate    = 0x0010
	swpNoOwnerZOrder = 0x0200

	monitorDefaultToNearest = 2
)

type rect struct {
	left, top, right, bottom int32
}

func (r rect) width() int32  { return r.right - r.left }
func (r rect) height() int32 { return r.bottom - r.top }

type monitorInfo struct {
	size    uint32
	monitor rect
	work    rect
	flags   uint32
}

// shape is a window Fit has seen: the procedure it had before, and the
// content's width over its height to keep (0 for none yet).
type shape struct {
	previous uintptr
	ratio    float64
}

// shapes is only ever touched on the windows' own thread.
var shapes = map[uintptr]*shape{}

var wndProc = sync.OnceValue(func() uintptr { return windows.NewCallback(shapeWndProc) })

// Fit shapes hwnd (an HWND) for a w×h picture: its content takes the
// picture's proportions at about the area it had, centred where it was and
// inside its screen's work area, and keeps them through the user's resizes.
// Maximised, minimised or fullscreen, only the proportions are taken; the
// caller fits it again once it is back. It is posted to the window's thread,
// in order with what is asked of the window after it, such as Show.
func Fit(hwnd unsafe.Pointer, w, h int) {
	if hwnd == nil || w <= 0 || h <= 0 {
		return
	}
	application.InvokeAsync(func() { fit(uintptr(hwnd), float64(w)/float64(h)) })
}

// ShowButtons does nothing on Windows: the window has no frame there, and
// its page draws the caption buttons in its own title bar, which comes and
// goes with the controls.
func ShowButtons(unsafe.Pointer, bool) {}

func FreeAspect(hwnd unsafe.Pointer) {
	application.InvokeAsync(func() {
		if s := shapes[uintptr(hwnd)]; s != nil {
			s.ratio = 0
		}
	})
}

func fit(hwnd uintptr, ratio float64) {
	// Closed while this waited for its turn.
	if alive, _, _ := procIsWindow.Call(hwnd); alive == 0 {
		return
	}
	s := shapes[hwnd]
	if s == nil {
		s = &shape{}
		shapes[hwnd] = s
		s.previous, _, _ = procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, wndProc())
	}
	s.ratio = ratio

	zoomed, _, _ := procIsZoomed.Call(hwnd)
	iconic, _, _ := procIsIconic.Call(hwnd)
	if zoomed != 0 || iconic != 0 {
		return
	}
	var window, client rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&window)))
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	screen := monitorInfo{size: uint32(unsafe.Sizeof(monitorInfo{}))}
	monitor, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	if ok, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&screen))); ok == 0 {
		return
	}
	// Fullscreen is a window over the whole monitor.
	if window == screen.monitor {
		return
	}

	dw, dh := window.width()-client.width(), window.height()-client.height()
	width := math.Sqrt(float64(client.width()) * float64(client.height()) * ratio)
	height := width / ratio
	if limit := float64(screen.work.width() - dw); width > limit {
		width, height = limit, limit/ratio
	}
	if limit := float64(screen.work.height() - dh); height > limit {
		width, height = limit*ratio, limit
	}
	w := int32(math.Round(width)) + dw
	h := int32(math.Round(height)) + dh
	// Already this shape: refitting must not creep by rounding.
	if abs(w-window.width()) <= 1 && abs(h-window.height()) <= 1 {
		return
	}
	x := min(max((window.left+window.right)/2-w/2, screen.work.left), screen.work.right-w)
	y := min(max((window.top+window.bottom)/2-h/2, screen.work.top), screen.work.bottom-h)
	procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		swpNoZOrder|swpNoActivate|swpNoOwnerZOrder)
}

func shapeWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	s := shapes[hwnd]
	switch {
	case s == nil:
		// Only windows in shapes are given this procedure.
		return 0
	case msg == wmSizing && s.ratio > 0:
		keepRatio(hwnd, wParam, *(**rect)(unsafe.Pointer(&lParam)), s.ratio)
		return 1
	case msg == wmNCDestroy:
		delete(shapes, hwnd)
		procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, s.previous)
	}
	r, _, _ := procCallWindowProcW.Call(s.previous, hwnd, msg, wParam, lParam)
	return r
}

// keepRatio reshapes the frame being dragged to ratio by moving an edge the
// user is not holding: the bottom for a side, the right for the top or the
// bottom, and for a corner the top or bottom beside it.
func keepRatio(hwnd, edge uintptr, r *rect, ratio float64) {
	var window, client rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&window)))
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	dw, dh := window.width()-client.width(), window.height()-client.height()
	switch edge {
	case wmszTop, wmszBottom:
		r.right = r.left + int32(math.Round(float64(r.height()-dh)*ratio)) + dw
	case wmszTopLeft, wmszTopRight:
		r.top = r.bottom - int32(math.Round(float64(r.width()-dw)/ratio)) - dh
	default:
		r.bottom = r.top + int32(math.Round(float64(r.width()-dw)/ratio)) + dh
	}
}

func abs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
