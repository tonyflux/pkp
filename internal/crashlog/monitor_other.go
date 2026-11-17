//go:build !windows

package crashlog

func monitorExecutable(exe string) (string, error) { return exe, nil }
