//go:build linux

package syncfs

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Trash implements the freedesktop trash specification with descriptor-relative
// renames. It never falls back to unlink or a cross-filesystem copy/delete.
func Trash(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := Identify(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	if _, err = ValidateRelative(parent, filepath.Base(absolute)); err != nil {
		return err
	}
	source, err := openNoFollow(absolute)
	if err != nil {
		return pathError(absolute, err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return pathError(absolute, err)
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return ErrUnsafePath
	}
	from, err := openNoFollow(parent.Path)
	if err != nil {
		return pathError(parent.Path, err)
	}
	defer from.Close()
	trash, original, err := linuxTrashDir(absolute, info.Sys().(*syscall.Stat_t).Dev)
	if err != nil {
		return err
	}
	files, err := privateTrashDir(filepath.Join(trash, "files"))
	if err != nil {
		return err
	}
	defer files.Close()
	metadata, err := privateTrashDir(filepath.Join(trash, "info"))
	if err != nil {
		return err
	}
	defer metadata.Close()
	// A random basename stays below NAME_MAX even for maximal source names.
	name := fmt.Sprintf("pkp-%x", randomTrashName())
	if name == "pkp-" {
		return ErrUnavailable
	}
	metaName := name + ".trashinfo"
	fd, err := unix.Openat(int(metadata.Fd()), metaName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return pathError(trash, err)
	}
	meta := os.NewFile(uintptr(fd), metaName)
	defer meta.Close()
	moved := false
	defer func() {
		if !moved {
			unix.Unlinkat(int(metadata.Fd()), metaName, 0)
		}
	}()
	encoded := (&url.URL{Path: original}).EscapedPath()
	if _, err = fmt.Fprintf(meta, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", encoded, time.Now().Format("2006-01-02T15:04:05")); err != nil {
		return pathError(trash, err)
	}
	if err = meta.Sync(); err != nil {
		return pathError(trash, err)
	}
	if err = Validate(parent); err != nil {
		return err
	}
	var current unix.Stat_t
	if err = unix.Fstatat(int(from.Fd()), filepath.Base(absolute), &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return pathError(absolute, err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if current.Ino != st.Ino || current.Dev != st.Dev {
		return ErrUnstable
	}
	if err = unix.Renameat2(int(from.Fd()), filepath.Base(absolute), int(files.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) {
			return ErrUnsupported
		}
		return pathError(absolute, err)
	}
	moved = true // From this point the metadata must remain, including on sync failure.
	if err = unix.Fstatat(int(files.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return pathError(trash, err)
	}
	if current.Ino != st.Ino || current.Dev != st.Dev {
		// A concurrent final-component substitution must not discard either file.
		if unix.Renameat2(int(files.Fd()), name, int(from.Fd()), filepath.Base(absolute), unix.RENAME_NOREPLACE) == nil {
			moved = false
		}
		return ErrUnstable
	}
	if err = files.Sync(); err != nil {
		return pathError(trash, err)
	}
	if err = metadata.Sync(); err != nil {
		return pathError(trash, err)
	}
	return nil
}

func randomTrashName() []byte {
	var name [16]byte
	if _, err := rand.Read(name[:]); err != nil {
		return nil
	}
	return name[:]
}

func privateTrashDir(path string) (*os.File, error) {
	parent, err := openNoFollow(filepath.Dir(path))
	if err != nil {
		return nil, pathError(path, err)
	}
	defer parent.Close()
	if err = unix.Mkdirat(int(parent.Fd()), filepath.Base(path), 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, pathError(path, err)
	}
	f, err := openNoFollow(path)
	if err != nil {
		return nil, pathError(path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, pathError(path, err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || st.Uid != uint32(os.Getuid()) || info.Mode().Perm() & 0077 != 0 {
		f.Close()
		return nil, ErrUnsafePath
	}
	return f, nil
}

func linuxTrashDir(path string, device uint64) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", pathError(path, err)
	}
	data := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		data = filepath.Join(home, ".local", "share")
	}
	// Home trash is permitted only on the source filesystem. XDG_DATA_HOME may
	// live on another disk, in which case the mount-local trash is required.
	ancestor := data
	for {
		info, e := os.Lstat(ancestor)
		if e == nil {
			if info.Mode() & os.ModeSymlink != 0 {
				return "", "", ErrUnsafePath
			}
			if info.Sys().(*syscall.Stat_t).Dev == device {
				if e = checkComponents(ancestor); e != nil {
					return "", "", e
				}
				if e = os.MkdirAll(data, 0700); e != nil {
					return "", "", pathError(data, e)
				}
				trash := filepath.Join(data, "Trash")
				f, e := privateTrashDir(trash)
				if e != nil {
					return "", "", e
				}
				f.Close()
				return trash, path, nil
			}
			break
		}
		if !errors.Is(e, os.ErrNotExist) || ancestor == "/" {
			return "", "", pathError(ancestor, e)
		}
		ancestor = filepath.Dir(ancestor)
	}
	// Find the source mount, not just its st_dev: bind mounts can share a device.
	top := filepath.Dir(path)
	var source unix.Statx_t
	if err = unix.Statx(unix.AT_FDCWD, top, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &source); err != nil || source.Mask & unix.STATX_MNT_ID == 0 {
		return "", "", ErrUnsupported
	}
	for top != "/" {
		next := filepath.Dir(top)
		var st unix.Statx_t
		if err = unix.Statx(unix.AT_FDCWD, next, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &st); err != nil {
			return "", "", pathError(next, err)
		}
		if st.Mnt_id != source.Mnt_id {
			break
		}
		top = next
	}
	// The per-user mount trash is the specification's safe alternative to the
	// optional shared sticky .Trash directory; never adopt another user's dir.
	trash := filepath.Join(top, ".Trash-"+strconv.Itoa(os.Getuid()))
	f, err := privateTrashDir(trash)
	if err != nil {
		return "", "", err
	}
	f.Close()
	relative, err := filepath.Rel(top, path)
	return trash, relative, err
}
