//go:build darwin && !cgo

package syncfs

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io/fs"
)

func RenameNoReplace(from, to string) error {
	err := unix.RenamexNp(from, to, unix.RENAME_EXCL)
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: %s", errors.Join(ErrCollision, fs.ErrExist), to)
	}
	return pathError(to, err)
}
