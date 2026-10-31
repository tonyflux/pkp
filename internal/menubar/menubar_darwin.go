//go:build darwin

package menubar

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework CoreServices
#include <stdlib.h>
#include "menubar_darwin.h"
*/
import "C"

import (
	"sync/atomic"
	"unsafe"
)

// Activate brings PKP to the front, so a question it asks is not left
// behind another app's windows (a quit from the Dock's menu does not).
func Activate() { C.pkp_menubar_activate() }

// Badge shows or clears the attention dot on PKP's menu bar item and sets
// the name VoiceOver reads and the tooltip to label. It does nothing until
// the item exists; the next call catches up.
func Badge(on bool, label string) {
	text := C.CString(label)
	defer C.free(unsafe.Pointer(text))
	flag := C.int(0)
	if on {
		flag = 1
	}
	C.pkp_menubar_badge(flag, text)
}

// QuitBySystem reports whether the quit being decided comes from logging
// out, restarting or shutting down. Call it from the app's should-quit
// check, which AppKit runs on the main thread during the quit Apple event.
func QuitBySystem() bool { return C.pkp_menubar_quit_by_system() != 0 }

var clicked atomic.Pointer[func()]

// WatchClicks calls fn, on a goroutine of its own, for every mouse click in
// another app. Only the first call installs it.
func WatchClicks(fn func()) {
	clicked.Store(&fn)
	C.pkp_menubar_watch_clicks()
}

//export pkpMenubarClickedElsewhere
func pkpMenubarClickedElsewhere() {
	if fn := clicked.Load(); fn != nil {
		go (*fn)()
	}
}

// Fit gives nsWindow (an NSWindow) height points, keeping its top edge, and
// at most its screen's free height less margin above and below.
func Fit(nsWindow unsafe.Pointer, height, margin int) {
	if nsWindow == nil || height <= 0 {
		return
	}
	C.pkp_menubar_fit(nsWindow, C.int(height), C.int(margin))
}
