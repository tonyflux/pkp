//go:build linux

package lanshare

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func statTimes(st *unix.Stat_t) string {
	return fmt.Sprintf("%d:%d:%d:%d", st.Mtim.Sec, st.Mtim.Nsec, st.Ctim.Sec, st.Ctim.Nsec)
}

// Local root authorization is unsupported by syncfs on Linux; file versions still include ctime.
func statBirth(st *unix.Stat_t) string { return "" }

func handlePath(f *os.File) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
}
