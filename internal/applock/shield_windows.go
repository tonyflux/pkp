//go:build windows

package applock

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSetWindowDisplayAffinity = windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowDisplayAffinity")

const (
	wdaNone               = 0x0
	wdaExcludeFromCapture = 0x11 // Windows 10 2004 and later
	wdaMonitor            = 0x1  // older: the window captures as black
)

// ShieldWindow keeps window (an HWND) out of screenshots and screen
// recording while on, as a password field or the lock screen is up; off
// lets it back in.
func ShieldWindow(window unsafe.Pointer, on bool) {
	if window == nil {
		return
	}
	hwnd := uintptr(window)
	if !on {
		procSetWindowDisplayAffinity.Call(hwnd, wdaNone)
		return
	}
	if r, _, _ := procSetWindowDisplayAffinity.Call(hwnd, wdaExcludeFromCapture); r == 0 {
		procSetWindowDisplayAffinity.Call(hwnd, wdaMonitor)
	}
}
