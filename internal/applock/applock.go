// Package applock asks the operating system whether the person at the
// computer is its owner — Touch ID or the login password on macOS, Windows
// Hello on Windows — and tells when the system locks the screen. PKP's App Lock
// is built on it: PKP never sees or collects a system
// password; the system's own dialog does the asking.
package applock

// Result is how an authentication ended.
type Result int

const (
	// Verified: the system confirmed the computer's owner.
	Verified Result = iota
	// Cancelled: the user, or the system, dismissed the dialog.
	Cancelled
	// Failed: the system refused the attempt (wrong password, too many tries).
	Failed
	// Unavailable: this computer cannot ask (no login password, no Windows
	// Hello, a platform without native code).
	Unavailable
)

// Outcome is an authentication's result and, unless Verified, the system's
// reason in words fit to show.
type Outcome struct {
	Result  Result
	Message string
}
