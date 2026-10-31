//go:build darwin && cgo

package syncfs

/*
#cgo CFLAGS: -mmacosx-version-min=12.0 -fblocks
#cgo LDFLAGS: -framework Foundation -framework CoreServices
#include <stdlib.h>
#include "platform_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func nativeVolume(path string) (string, bool, error) {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	var uuid [16]byte
	var sensitive C.int
	if code := C.pkp_sync_volume(p, (*C.uchar)(unsafe.Pointer(&uuid[0])), &sensitive); code != 0 {
		if code == C.ENOTSUP {
			return "", false, ErrUnsupported
		}
		return "", false, pathError(path, syscall.Errno(code))
	}
	return hex.EncodeToString(uuid[:]), sensitive != 0, nil
}
func platformIdentity(path string) (Identity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Identity{}, pathError(path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Identity{}, ErrUnsafePath
	}
	volume, sensitive, err := nativeVolume(path)
	if err != nil {
		return Identity{}, err
	}
	st := info.Sys().(*syscall.Stat_t)
	return Identity{Path: path, Volume: volume, File: fmt.Sprintf("%d:%d", st.Dev, st.Ino), CaseSensitive: sensitive, UnicodeSensitive: false}, nil
}
func platformFile(info fs.FileInfo, path string) (string, string, error) {
	volume, _, err := nativeVolume(path)
	if err != nil {
		return "", "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", ErrUnsupported
	}
	return volume, fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
func platformVersion(info fs.FileInfo, path string) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", st.Ctimespec.Sec, st.Ctimespec.Nsec)
}
func isReparse(info fs.FileInfo) bool { return false }

// Walk by directory descriptors: a concurrently replaced ancestor cannot turn
// the final open into a followed symlink outside the selected tree.
func openNoFollow(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Darwin exposes standard writable locations through these system aliases.
	// Resolve only the OS alias, never arbitrary user symlink ancestors.
	for _, alias := range []string{"/var", "/tmp", "/etc"} {
		if absolute == alias || strings.HasPrefix(absolute, alias+"/") {
			target, e := os.Readlink(alias)
			if e == nil && (target == "private"+alias || target == "/private"+alias) {
				absolute = "/private" + absolute
			}
			break
		}
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	if absolute == "/" {
		return os.NewFile(uintptr(fd), path), nil
	}
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
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

func RenameNoReplace(from, to string) error {
	source, err := openNoFollow(filepath.Dir(from))
	if err != nil {
		return pathError(from, err)
	}
	defer source.Close()
	target, err := openNoFollow(filepath.Dir(to))
	if err != nil {
		return pathError(to, err)
	}
	defer target.Close()
	a := C.CString(filepath.Base(from))
	defer C.free(unsafe.Pointer(a))
	b := C.CString(filepath.Base(to))
	defer C.free(unsafe.Pointer(b))
	if code := C.pkp_sync_rename(C.int(source.Fd()), a, C.int(target.Fd()), b); code != 0 {
		if code == C.EEXIST {
			return fmt.Errorf("%w: %s", errors.Join(ErrCollision, fs.ErrExist), to)
		}
		if code == C.ENOTSUP {
			return ErrUnsupported
		}
		return pathError(to, syscall.Errno(code))
	}
	return nil
}

// Trash uses Foundation directly, never Finder, AppleScript, or a dialog.
func Trash(path string) error {
	parent, err := Identify(filepath.Dir(path))
	if err != nil {
		return err
	}
	if _, err = ValidateRelative(parent, filepath.Base(path)); err != nil {
		return err
	}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	if code := C.pkp_sync_trash(p); code != 0 {
		if code == C.ENOTSUP {
			return ErrUnsupported
		}
		return pathError(path, syscall.Errno(code))
	}
	return nil
}

func Watch(ctx context.Context, root Identity, notify func()) (func(), error) {
	if err := Validate(root); err != nil {
		return nil, err
	}
	p := C.CString(root.Path)
	defer C.free(unsafe.Pointer(p))
	watcher := C.pkp_sync_watch(p)
	if watcher == nil {
		return nil, ErrUnsupported
	}
	events := make(chan struct{}, 1)
	var once sync.Once
	var mutex sync.Mutex
	freed := false
	closeNative := func() {
		once.Do(func() {
			mutex.Lock()
			defer mutex.Unlock()
			if !freed {
				C.pkp_sync_watch_stop(watcher)
			}
		})
	}
	go func() {
		defer func() {
			closeNative()
			mutex.Lock()
			defer mutex.Unlock()
			C.pkp_sync_watch_free(watcher)
			freed = true
		}()
		defer close(events)
		for {
			result := C.pkp_sync_watch_wait(watcher)
			if result < 0 {
				return
			}
			if result > 0 {
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()
	return debounce(ctx, events, notify, closeNative), nil
}
