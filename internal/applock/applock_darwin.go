//go:build darwin

package applock

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework LocalAuthentication
#include <stdlib.h>
#include "applock_darwin.h"
*/
import "C"

import (
	"sync/atomic"
	"unsafe"

	"github.com/tonyflux/pkp/internal/i18n"
)

// describe words why the system did not verify (applock_darwin.h): the
// system's own words where it has them, ours for the cases the lock screen
// and Settings explain. It frees message.
func describe(reason C.int, message *C.char) string {
	if message != nil {
		defer C.free(unsafe.Pointer(message))
	}
	switch reason {
	case C.PKP_APPLOCK_NO_PASSCODE:
		return i18n.T("这台 Mac 没有设置登录密码，无法使用系统身份验证")
	case C.PKP_APPLOCK_CANCELLED:
		return i18n.T("已取消验证")
	case C.PKP_APPLOCK_NOT_PASSED:
		return i18n.T("身份验证没有通过")
	case C.PKP_APPLOCK_SYSTEM:
		if text := C.GoString(message); message != nil && text != "" {
			return text
		}
	}
	return i18n.T("系统身份验证不可用")
}

// Available reports whether this Mac can authenticate its owner now — it
// can whenever the account has a login password — and if not, why.
func Available() (bool, string) {
	var message *C.char
	var reason C.int
	if C.pkp_applock_available(&message, &reason) != 0 {
		return true, ""
	}
	return false, describe(reason, message)
}

// Authenticate shows macOS's own dialog (Touch ID, an Apple Watch or the
// login password) and blocks until it ends. reason completes the system's
// sentence 「“PKP” is trying to…」. The dialog is the system's and needs no
// window; window is ignored here.
func Authenticate(_ unsafe.Pointer, reason string) Outcome {
	text := C.CString(reason)
	defer C.free(unsafe.Pointer(text))
	var message *C.char
	var why C.int
	result := Result(C.pkp_applock_authenticate(text, &message, &why))
	if result == Verified {
		return Outcome{Result: result}
	}
	return Outcome{Result: result, Message: describe(why, message)}
}

var locked atomic.Pointer[func()]

// WatchScreenLock calls fn, on a goroutine of its own, whenever the screen
// locks or the session is switched away. Only the first call installs the
// observers; later ones replace fn.
func WatchScreenLock(fn func()) {
	locked.Store(&fn)
	C.pkp_applock_watch_screen()
}

//export pkpApplockScreenLocked
func pkpApplockScreenLocked() {
	if fn := locked.Load(); fn != nil {
		go (*fn)()
	}
}

// ShieldWindow keeps window (an NSWindow) out of screenshots, screen
// recording and screen sharing while on, as a password field or the lock
// screen is up; off lets it back in.
func ShieldWindow(window unsafe.Pointer, on bool) {
	flag := C.int(0)
	if on {
		flag = 1
	}
	C.pkp_applock_shield(window, flag)
}
