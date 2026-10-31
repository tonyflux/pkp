//go:build !windows

package download

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// diskFree is the space an unprivileged user may still fill under dir.
func diskFree(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	// Bsize is uint32 on darwin and int64 on linux.
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// sparse is a no-op: APFS and ext4 leave a truncated file's gaps unwritten.
func sparse(*os.File) error { return nil }

func isNoSpace(err error) bool { return errors.Is(err, syscall.ENOSPC) }
