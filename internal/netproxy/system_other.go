//go:build !darwin && (!linux || !cgo)

package netproxy

// Builds without a native desktop-settings backend use the environment.
func readSystemProxy() (SystemInfo, bool) { return SystemInfo{}, false }
