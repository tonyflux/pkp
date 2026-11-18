//go:build linux

package syncfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

func Watch(ctx context.Context, root Identity, notify func()) (func(), error) {
	if err := Validate(root); err != nil {
		return nil, err
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC|unix.IN_NONBLOCK)
	if err != nil {
		return nil, pathError(root.Path, err)
	}
	wake, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		unix.Close(fd)
		return nil, pathError(root.Path, err)
	}
	watches := make(map[int]string)
	// Reconcile directory watches after moves and overflow, including entire
	// populated trees moved in. All additions use a pinned, nofollow descriptor.
	refresh := func() error {
		seen := make(map[int]bool)
		var walk func(string) error
		walk = func(path string) error {
			f, e := openNoFollow(path)
			if e != nil {
				return e
			}
			defer f.Close()
			volume, e := linuxVolume(f)
			if e != nil {
				return e
			}
			if volume != root.Volume {
				return ErrRootChanged
			}
			wd, e := unix.InotifyAddWatch(fd, "/proc/self/fd/"+strconv.FormatUint(uint64(f.Fd()), 10), unix.IN_ONLYDIR|unix.IN_CREATE|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_MODIFY|unix.IN_ATTRIB|unix.IN_CLOSE_WRITE|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_UNMOUNT)
			if e != nil {
				return e
			}
			watches[wd], seen[wd] = path, true
			children, e := f.ReadDir(-1)
			if e != nil {
				return e
			}
			for _, child := range children {
				if child.IsDir() && child.Type()&os.ModeSymlink == 0 {
					if e = walk(filepath.Join(path, child.Name())); e != nil {
						return e
					}
				}
			}
			return nil
		}
		if err := Validate(root); err != nil {
			return err
		}
		if err := walk(root.Path); err != nil {
			return err
		}
		for wd := range watches {
			if !seen[wd] {
				unix.InotifyRmWatch(fd, uint32(wd))
				delete(watches, wd)
			}
		}
		return Validate(root)
	}
	if err = refresh(); err != nil {
		unix.Close(wake)
		unix.Close(fd)
		return nil, pathError(root.Path, err)
	}
	events := make(chan struct{}, 1)
	var once sync.Once
	var mutex sync.Mutex
	closed := false
	closeNative := func() {
		once.Do(func() {
			mutex.Lock()
			defer mutex.Unlock()
			if !closed {
				unix.Write(wake, []byte{1, 0, 0, 0, 0, 0, 0, 0})
			}
		})
	}
	go func() {
		defer func() {
			mutex.Lock()
			defer mutex.Unlock()
			closed = true
			unix.Close(fd)
			unix.Close(wake)
		}()
		var buffer [64 * 1024]byte
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}, {Fd: int32(wake), Events: unix.POLLIN}}
		for {
			_, err := unix.Poll(poll, -1)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil || poll[1].Revents != 0 {
				return
			}
			n, err := unix.Read(fd, buffer[:])
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil {
				notify() // A failed event stream requires a full consumer rescan.
				return
			}
			rebuild := false
			for offset := 0; offset+unix.SizeofInotifyEvent <= n; {
				event := (*unix.InotifyEvent)(unsafe.Pointer(&buffer[offset]))
				if event.Mask & (unix.IN_Q_OVERFLOW|unix.IN_ISDIR|unix.IN_IGNORED|unix.IN_MOVE_SELF|unix.IN_DELETE_SELF|unix.IN_UNMOUNT) != 0 {
					rebuild = true
				}
				offset += unix.SizeofInotifyEvent + int(event.Len)
			}
			if rebuild {
				if err := refresh(); err != nil {
					notify()
					return
				}
			}
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}()
	return debounce(ctx, events, notify, closeNative), nil
}
