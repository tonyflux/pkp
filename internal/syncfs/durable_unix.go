//go:build !windows

package syncfs

import (
	"os"
	"path/filepath"
)

// DurableReplace commits a previously fsynced private checkpoint file. Unlike
// RenameNoReplace this intentionally replaces existing application-owned state.
// Source and destination must share a directory; never use for user content.
func DurableReplace(from, to string) error {
	sourceDir, err := filepath.Abs(filepath.Dir(from))
	if err != nil {
		return pathError(from, err)
	}
	targetDir, err := filepath.Abs(filepath.Dir(to))
	if err != nil {
		return pathError(to, err)
	}
	if sourceDir != targetDir {
		return ErrUnsafePath
	}
	info, err := os.Lstat(from)
	if err != nil {
		return pathError(from, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafePath
	}
	dir, err := os.Open(targetDir)
	if err != nil {
		return pathError(targetDir, err)
	}
	defer dir.Close()
	if err = os.Rename(from, to); err != nil {
		return pathError(to, err)
	}
	if err = dir.Sync(); err != nil {
		return pathError(targetDir, err)
	}
	return nil
}
