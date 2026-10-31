//go:build !darwin && !windows

package applock

import (
	"unsafe"

	"github.com/tonyflux/pkp/internal/i18n"
)

func unsupported() string { return i18n.T("这个系统上的 PKP 还不能使用系统身份验证") }

// Available is false: there is no native code for this system yet, and a lock
// that cannot be opened must not be switched on.
func Available() (bool, string) { return false, unsupported() }

// Authenticate cannot ask on this system.
func Authenticate(unsafe.Pointer, string) Outcome {
	return Outcome{Result: Unavailable, Message: unsupported()}
}

// WatchScreenLock does nothing where there is no native code yet.
func WatchScreenLock(func()) {}

// ShieldWindow does nothing where there is no native code yet.
func ShieldWindow(unsafe.Pointer, bool) {}
