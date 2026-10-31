package syncfs

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// DurableReplace replaces only application-owned private state, never user files.
func DurableReplace(from, to string) error {
	source, err := filepath.Abs(from)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(to)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Dir(source), filepath.Dir(target)) {
		return ErrUnsafePath
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || isReparse(info) {
		return ErrUnsafePath
	}
	old, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	next, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(old, next, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
