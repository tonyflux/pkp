//go:build !darwin && !windows

package videowin

import "unsafe"

// Fit does nothing where there is no native window code yet.
func Fit(unsafe.Pointer, int, int) {}

// ShowButtons does nothing where there is no native window code yet.
func ShowButtons(unsafe.Pointer, bool) {}

func FreeAspect(unsafe.Pointer) {}
