//go:build darwin

package lanshare

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func statTimes(st *unix.Stat_t) string {
	return fmt.Sprintf("%d:%d:%d:%d", st.Mtim.Sec, st.Mtim.Nsec, st.Ctim.Sec, st.Ctim.Nsec)
}

func statBirth(st *unix.Stat_t) string {
	return fmt.Sprintf("%d:%d", st.Btim.Sec, st.Btim.Nsec)
}

func handlePath(f *os.File) (string, error) {
	var path [unix.PathMax]byte
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, f.Fd(), unix.F_GETPATH, uintptr(unsafe.Pointer(&path[0])))
	if errno != 0 {
		return "", errno
	}
	return unix.ByteSliceToString(path[:]), nil
}
