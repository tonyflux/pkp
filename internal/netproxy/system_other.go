//go:build !darwin

package netproxy

// readSystemProxy has nothing to read outside macOS: the environment is all
// a desktop process is told, and System falls back to it.
func readSystemProxy() (SystemInfo, bool) { return SystemInfo{}, false }
