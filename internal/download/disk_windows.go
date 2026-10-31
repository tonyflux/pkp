//go:build windows

package download

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// diskFree is the space this user may still fill under dir (quotas count).
func diskFree(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}

// sparse marks f sparse. Without it NTFS zero-fills everything before a
// write far into the file, synchronously, before the write returns.
func sparse(f *os.File) error {
	var n uint32
	return windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &n, nil)
}

// isNoSpace is ERROR_DISK_FULL or ERROR_HANDLE_DISK_FULL.
func isNoSpace(err error) bool {
	return errors.Is(err, syscall.Errno(112)) || errors.Is(err, syscall.Errno(39))
}
