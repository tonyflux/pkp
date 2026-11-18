//go:build linux

package syncfs

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// RenameNoReplace never downgrades to a check-then-rename sequence.
func RenameNoReplace(from, to string) error {
	source, err := openNoFollow(filepath.Dir(from))
	if err != nil {
		return pathError(from, err)
	}
	defer source.Close()
	target, err := openNoFollow(filepath.Dir(to))
	if err != nil {
		return pathError(to, err)
	}
	defer target.Close()
	err = unix.Renameat2(int(source.Fd()), filepath.Base(from), int(target.Fd()), filepath.Base(to), unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: %s", errors.Join(ErrCollision, fs.ErrExist), to)
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return ErrUnsupported
	}
	return pathError(to, err)
}
