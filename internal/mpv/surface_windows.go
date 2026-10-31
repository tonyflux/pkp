//go:build windows

package mpv

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// surface is the video surface on Windows: a child window of the application
// window, kept at the bottom of its siblings — below the WebView2
// controller's own child window, which covers the client area. mpv is given
// it as --wid, creates its own window inside when a video starts, and keeps
// that one the size of this one; this one goes where the page's stage is,
// as the macOS view does. DWM composites mpv's swapchain under the webview,
// which shows it wherever the page paints nothing, as long as the webview's
// default background is transparent — which it is only while a page shows
// the stage (services.PlayerService.followStage).
//
// It is disabled and never takes input: the webview above it gets every
// click, over the picture too, as the page's controls need.
//
// One surface for the whole application, in one window at a time, owned by
// the thread that runs the application's windows (Wails' main thread):
// creating and re-parenting it happen there. Placing and showing it are
// posted there (SWP_ASYNCWINDOWPOS), so a page reporting its stage never
// waits on the UI.
type surface struct {
	hwnd atomic.Uintptr // set on the main thread
}

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procFindWindowExW    = user32.NewProc("FindWindowExW")
	procGetDpiForWindow  = user32.NewProc("GetDpiForWindow")
	procGetParent        = user32.NewProc("GetParent")
	procGetWindow        = user32.NewProc("GetWindow")
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procMapWindowPoints  = user32.NewProc("MapWindowPoints")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procSetParent        = user32.NewProc("SetParent")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procShowWindow       = user32.NewProc("ShowWindow")

	procGetStockObject = windows.NewLazySystemDLL("gdi32.dll").NewProc("GetStockObject")
)

const (
	wsChild            = 0x40000000
	wsDisabled         = 0x08000000
	wsClipChildren     = 0x02000000
	wsExNoParentNotify = 0x00000004

	swpNoSize         = 0x0001
	swpNoMove         = 0x0002
	swpNoActivate     = 0x0010
	swpShowWindow     = 0x0040
	swpHideWindow     = 0x0080
	swpNoOwnerZOrder  = 0x0200
	swpAsyncWindowPos = 0x4000

	hwndBottom = 1
	swHide     = 0
	gwHwndNext = 2
	blackBrush = 4
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

type winRect struct {
	left, top, right, bottom int32
}

type windowClass struct {
	name     *uint16
	instance windows.Handle
}

// surfaceClass registers the surface's window class, once: no window
// procedure of its own, and black for the moment before mpv's window covers
// it.
var surfaceClass = sync.OnceValues(func() (windowClass, error) {
	var class windowClass
	class.name, _ = windows.UTF16PtrFromString("PKPVideoSurface")
	if err := windows.GetModuleHandleEx(0, nil, &class.instance); err != nil {
		return class, err
	}
	brush, _, _ := procGetStockObject.Call(blackBrush)
	info := wndClassEx{
		wndProc:    procDefWindowProcW.Addr(),
		instance:   class.instance,
		background: brush,
		className:  class.name,
	}
	info.size = uint32(unsafe.Sizeof(info))
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&info))); atom == 0 {
		return class, fmt.Errorf("register window class: %w", err)
	}
	return class, nil
})

// attach creates the surface in parent (an HWND), hidden, unless it exists
// already, and returns it.
func (s *surface) attach(parent uintptr) (hwnd uintptr, err error) {
	application.InvokeSync(func() {
		if hwnd = s.hwnd.Load(); hwnd != 0 {
			return
		}
		class, cerr := surfaceClass()
		if cerr != nil {
			err = cerr
			return
		}
		created, _, e := procCreateWindowExW.Call(
			wsExNoParentNotify, uintptr(unsafe.Pointer(class.name)), 0,
			wsChild|wsClipChildren|wsDisabled, 0, 0, 1, 1,
			parent, 0, uintptr(class.instance), 0)
		if created == 0 {
			err = fmt.Errorf("create window: %w", e)
			return
		}
		// A new child starts at the bottom already; the webview has to
		// stay above it whatever came before.
		procSetWindowPos.Call(created, hwndBottom, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		hwnd = created
		s.hwnd.Store(created)
	})
	return hwnd, err
}

// move re-parents the surface into another window, below its webview, and
// hides it until the next setRect: the old rectangle means nothing there.
// mpv's window inside goes with it, so the playing file never notices.
func (s *surface) move(parent uintptr) (err error) {
	application.InvokeSync(func() {
		hwnd := s.hwnd.Load()
		if hwnd == 0 {
			err = errors.New("no video surface to move")
			return
		}
		procShowWindow.Call(hwnd, swHide)
		if current, _, _ := procGetParent.Call(hwnd); current != parent {
			if previous, _, e := procSetParent.Call(hwnd, parent); previous == 0 {
				err = fmt.Errorf("set parent: %w", e)
				return
			}
		}
		procSetWindowPos.Call(hwnd, hwndBottom, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	})
	return err
}

// setRect places the surface in web coordinates: CSS pixels from the top-left
// of the client area, which WebView2 scales by the window's DPI. Each edge
// is rounded on its own, so the surface meets the page's hole exactly.
func (s *surface) setRect(rect Rect) {
	hwnd := s.hwnd.Load()
	if hwnd == 0 {
		return
	}
	parent, _, _ := procGetParent.Call(hwnd)
	scale := float64(dpiOf(parent)) / 96
	left, top := math.Round(rect.X*scale), math.Round(rect.Y*scale)
	width := max(math.Round((rect.X+rect.Width)*scale)-left, 1)
	height := max(math.Round((rect.Y+rect.Height)*scale)-top, 1)
	flags := uintptr(swpNoActivate | swpNoOwnerZOrder | swpAsyncWindowPos | swpHideWindow)
	if rect.Visible {
		flags = swpNoActivate | swpNoOwnerZOrder | swpAsyncWindowPos | swpShowWindow
	}
	// Signed, as SetWindowPos reads them: a stage may start left of or
	// above the client area.
	procSetWindowPos.Call(hwnd, hwndBottom,
		uintptr(int(left)), uintptr(int(top)), uintptr(int(width)), uintptr(int(height)), flags)
}

// describe is the live window state of the surface. "No picture" has several
// causes — hidden, misplaced, above the webview, mpv never made its window
// — and from outside the process they look the same.
func (s *surface) describe() string {
	hwnd := s.hwnd.Load()
	if hwnd == 0 {
		return "surface=none"
	}
	parent, _, _ := procGetParent.Call(hwnd)
	var frame winRect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&frame)))
	procMapWindowPoints.Call(0, parent, uintptr(unsafe.Pointer(&frame)), 2)
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	below, _, _ := procGetWindow.Call(hwnd, gwHwndNext)
	mpvClass, _ := windows.UTF16PtrFromString("mpv")
	player, _, _ := procFindWindowExW.Call(hwnd, 0, uintptr(unsafe.Pointer(mpvClass)), 0)
	return fmt.Sprintf("surface=live hwnd=%#x parent=%#x visible=%d frame=%d,%d %dx%d dpi=%d bottom=%t mpv=%#x",
		hwnd, parent, visible, frame.left, frame.top, frame.right-frame.left, frame.bottom-frame.top,
		dpiOf(parent), below == 0, player)
}

// dpiOf is hwnd's DPI, which WebView2 scales CSS pixels by; 96 on Windows
// releases before GetDpiForWindow (1607).
func dpiOf(hwnd uintptr) uintptr {
	if procGetDpiForWindow.Find() == nil {
		if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi != 0 {
			return dpi
		}
	}
	return 96
}
