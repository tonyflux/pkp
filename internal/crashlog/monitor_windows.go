package crashlog

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Windows cannot replace a running executable, so the monitor runs a cached copy.
func monitorExecutable(exe string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "pkp", "crash-monitor")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	pathHash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(exe))))
	in, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return "", err
	}
	// shortcut: old helper copies remain cached, remove after app upgrades if cache growth matters.
	dest := filepath.Join(dir, fmt.Sprintf("monitor-%x-%d-%d.exe", pathHash[:8], info.Size(), info.ModTime().UnixNano()))
	if _, statErr := os.Stat(dest); statErr == nil {
		return dest, nil
	}
	tmp, err := os.CreateTemp(dir, "monitor-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err = io.Copy(tmp, in); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = os.Chtimes(tmp.Name(), info.ModTime(), info.ModTime()); err != nil {
		return "", err
	}
	if err = os.Rename(tmp.Name(), dest); err != nil {
		if existing, statErr := os.Stat(dest); statErr == nil && existing.Size() == info.Size() {
			return dest, nil
		}
		return "", fmt.Errorf("create crash monitor: %w", err)
	}
	return dest, nil
}
