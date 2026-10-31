//go:build darwin

package videowin

/*
#cgo LDFLAGS: -framework Cocoa
#include "videowin_darwin.h"
*/
import "C"

import "unsafe"

// Fit shapes nsWindow (an NSWindow) for a w×h picture: its content takes the
// picture's proportions at about the area it had, centred where it was and
// inside its screen, and keeps them through the user's resizes. In
// fullscreen only the proportions are set; the frame follows on the way out.
// It returns once AppKit has applied it.
func Fit(nsWindow unsafe.Pointer, w, h int) {
	if nsWindow == nil || w <= 0 || h <= 0 {
		return
	}
	C.pkp_videowin_fit(nsWindow, C.int(w), C.int(h))
}

// ShowButtons fades nsWindow's traffic lights in or out, for a window whose
// title bar comes and goes with the controls over the picture. Hidden, they
// take no clicks. In fullscreen they always show: they belong to the title
// bar the system reveals at the top of the screen.
func ShowButtons(nsWindow unsafe.Pointer, show bool) {
	if nsWindow == nil {
		return
	}
	on := C.int(0)
	if show {
		on = 1
	}
	C.pkp_videowin_show_buttons(nsWindow, on)
}

// FreeAspect lets a music window resize independently in both dimensions.
func FreeAspect(nsWindow unsafe.Pointer) {
	if nsWindow != nil {
		C.pkp_videowin_free_aspect(nsWindow)
	}
}
