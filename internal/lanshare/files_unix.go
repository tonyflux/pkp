//go:build darwin || linux

package lanshare

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func openAbsolute(path string, directory bool) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errForbidden
	}
	root, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return openRelative(root, strings.TrimPrefix(path, "/"), directory)
}

// Each component is opened relative to the previous pinned directory, never checked then reopened by path.
func openRelative(root *os.File, rel string, directory bool) (*os.File, error) {
	if rel == "" {
		fd, err := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), root.Name()), nil
	}
	parts := strings.Split(rel, "/")
	parent := root
	var owned *os.File
	defer func() {
		if owned != nil {
			owned.Close()
		}
	}()
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00") {
			return nil, errForbidden
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(int(parent.Fd()), part, flags, 0)
		if err != nil {
			return nil, err
		}
		next := os.NewFile(uintptr(fd), filepath.Join(parent.Name(), part))
		if owned != nil {
			owned.Close()
		}
		owned = next
		parent = next
	}
	result := owned
	owned = nil
	return result, nil
}

func handleIdentity(f *os.File) (key, version, volume string, err error) {
	var st unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &st); err != nil {
		return
	}
	key = fmt.Sprintf("%x:%x:%s", uint64(st.Dev), uint64(st.Ino), statBirth(&st))
	volume = fmt.Sprintf("%x", uint64(st.Dev))
	version = fmt.Sprintf("%s:%d:%s", key, st.Size, statTimes(&st))
	return
}

func freeBytes(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func isDiskFull(err error) bool { return errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EDQUOT) }
