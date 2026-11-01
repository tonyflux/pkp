package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// AppBundle resolves aliases before identifying the installed executable's bundle.
func AppBundle(executable string) (string, error) {
	path, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	macOS := filepath.Dir(path)
	contents := filepath.Dir(macOS)
	bundle := filepath.Dir(contents)
	if filepath.Base(macOS) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.EqualFold(filepath.Ext(bundle), ".app") {
		return "", ErrInstallLocation
	}
	return bundle, nil
}

// InstallLockPath is per installed bundle, not per account/profile.
func InstallLockPath(bundle string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "pkp", "install-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(bundle))
	return filepath.Join(dir, hex.EncodeToString(digest[:])+".lock"), nil
}

// HoldInstallLock prevents new profiles from opening a bundle while it is replaced.
func HoldInstallLock() (func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	bundle, err := AppBundle(executable)
	if errors.Is(err, ErrInstallLocation) {
		return func() {}, nil // The bare development binary is not replaceable.
	}
	if err != nil {
		return nil, err
	}
	path, err := InstallLockPath(bundle)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_SH|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("PKP installation is in progress; try again when it finishes: %w", err)
	}
	return func() { _ = unix.Close(fd) }, nil
}
