//go:build linux

package lanshare

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func statTimes(st *unix.Stat_t) string {
	return fmt.Sprintf("%d:%d:%d:%d", st.Mtim.Sec, st.Mtim.Nsec, st.Ctim.Sec, st.Ctim.Nsec)
}

// Linux stat lacks birth time; the authorized root uses syncfs statx identity, while file versions include ctime.
func statBirth(st *unix.Stat_t) string { return "" }

// Coarse Linux timestamps can repeat after a same-size write and restored mtime.
func statFingerprint(f *os.File, st *unix.Stat_t) (string, error) {
	if st.Mode & unix.S_IFMT != unix.S_IFREG {
		return "", nil
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, io.NewSectionReader(f, 0, st.Size)); err != nil {
		return "", err
	}
	return fmt.Sprintf(":%x", digest.Sum(nil)), nil
}

func handlePath(f *os.File) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
}
