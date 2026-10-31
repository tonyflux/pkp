//go:build linux

package syncfs

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io/fs"
)

// RenameNoReplace never downgrades to a check-then-rename sequence.
func RenameNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: %s", errors.Join(ErrCollision, fs.ErrExist), to)
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return ErrUnsupported
	}
	return pathError(to, err)
}
