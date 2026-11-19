//go:build linux

package syncfs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux sync roots are local ext4, XFS, Btrfs or tmpfs. Network/FUSE/overlay
// filesystems and casefold/encrypted directories are refused: their identity or
// name equivalence cannot be represented honestly by Identity's two booleans.
func linuxVolume(f *os.File) (string, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		return "", pathError(f.Name(), err)
	}
	switch st.Type {
	case unix.EXT4_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.TMPFS_MAGIC:
	default:
		return "", ErrUnsupported
	}
	if st.Fsid.Val == [2]int32{} {
		return "", ErrUnsupported
	}
	info, err := f.Stat()
	if err != nil {
		return "", pathError(f.Name(), err)
	}
	if info.IsDir() && st.Type != unix.TMPFS_MAGIC {
		flags, err := unix.IoctlGetInt(int(f.Fd()), unix.FS_IOC_GETFLAGS)
		if err != nil {
			return "", pathError(f.Name(), err)
		}
		// FS_CASEFOLD_FL and FS_ENCRYPT_FL may change child name semantics.
		if flags & (0x40000000 | 0x00000800) != 0 {
			return "", ErrUnsupported
		}
		if st.Type == unix.XFS_SUPER_MAGIC {
			// XFS_IOC_FSGEOMETRY_V1: stable 112-byte ABI; flags at byte 92.
			var geometry [112]byte
			_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), 0x80705864, uintptr(unsafe.Pointer(&geometry[0])))
			if errno != 0 {
				return "", pathError(f.Name(), errno)
			}
			if binary.NativeEndian.Uint32(geometry[92:96]) & (1 << 12) != 0 {
				return "", ErrUnsupported // XFS ASCII-only case folding is not Unicode folding.
			}
		}
	}
	return fmt.Sprintf("linux:%x:%08x%08x", st.Type, uint32(st.Fsid.Val[0]), uint32(st.Fsid.Val[1])), nil
}

func linuxFile(f *os.File, info fs.FileInfo) (string, string, error) {
	volume, err := linuxVolume(f)
	if err != nil {
		return "", "", err
	}
	var st unix.Statx_t
	if err = unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_INO|unix.STATX_BTIME|unix.STATX_CTIME|unix.STATX_MTIME, &st); err != nil {
		return "", "", pathError(f.Name(), err)
	}
	original, ok := info.Sys().(*syscall.Stat_t)
	if !ok || original.Ino != st.Ino || original.Dev != unix.Mkdev(st.Dev_major, st.Dev_minor) {
		return "", "", ErrUnstable
	}
	if st.Mask & unix.STATX_INO == 0 {
		return "", "", ErrUnsupported
	}
	file := fmt.Sprintf("%d", st.Ino)
	if st.Mask & unix.STATX_BTIME != 0 {
		file += fmt.Sprintf(":%d:%d", st.Btime.Sec, st.Btime.Nsec)
	}
	return volume, file, nil
}

func platformIdentity(path string) (Identity, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return Identity{}, pathError(path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Identity{}, pathError(path, err)
	}
	if !info.IsDir() {
		return Identity{}, ErrUnsafePath
	}
	volume, file, err := linuxFile(f, info)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Path: path, Volume: volume, File: file, CaseSensitive: true, UnicodeSensitive: true}, nil
}

func platformFile(info fs.FileInfo, path string) (string, string, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return "", "", pathError(path, err)
	}
	defer f.Close()
	return linuxFile(f, info)
}

func platformVersion(ctx context.Context, info fs.FileInfo, path string) string {
	f, err := openNoFollow(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var st unix.Statx_t
	if err = unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_INO|unix.STATX_CTIME|unix.STATX_MTIME, &st); err != nil {
		return ""
	}
	original, ok := info.Sys().(*syscall.Stat_t)
	if !ok || original.Ino != st.Ino || original.Dev != unix.Mkdev(st.Dev_major, st.Dev_minor) || st.Mask & unix.STATX_CTIME == 0 {
		return ""
	}
	// Linux 6.8 can reuse both ctime and mtime within a scheduler tick. Without
	// a filesystem change counter, timestamps alone cannot authorize hash reuse.
	if info.IsDir() {
		names, err := f.Readdirnames(-1)
		if err != nil || ctx.Err() != nil {
			return ""
		}
		sort.Strings(names)
		digest := sha256.New()
		for _, name := range names {
			digest.Write([]byte(name))
			digest.Write([]byte{0})
		}
		return fmt.Sprintf("%d:%d:%s", st.Ctime.Sec, st.Ctime.Nsec, hex.EncodeToString(digest.Sum(nil)))
	}
	if info.Mode().IsRegular() {
		sum, err := hashFile(ctx, f)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("%d:%d:%s", st.Ctime.Sec, st.Ctime.Nsec, sum)
	}
	return ""
}

func isReparse(info fs.FileInfo) bool { return info.Mode() & os.ModeSymlink != 0 }

// Pin each ancestor before opening the next; no user or system symlink aliases.
func openNoFollow(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if absolute == "/" {
		return os.NewFile(uintptr(fd), path), nil
	}
	parts := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	for i, part := range parts {
		flags := unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}
