//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package drive

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive flock(2) on f without waiting. A flock
// belongs to the open file, so two opens conflict even inside one process.
func tryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
