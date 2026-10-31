//go:build !darwin

package menubar

import "unsafe"

// Activate does nothing where there is no native code yet.
func Activate() {}

// Badge does nothing where there is no native code yet.
func Badge(bool, string) {}

// QuitBySystem is false where there is no native code yet.
func QuitBySystem() bool { return false }

// WatchClicks does nothing where there is no native code yet.
func WatchClicks(func()) {}

// Fit does nothing where there is no native code yet.
func Fit(unsafe.Pointer, int, int) {}
