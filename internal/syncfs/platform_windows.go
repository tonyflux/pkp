//go:build windows

package syncfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tonyflux/pkp/internal/winpath"
	"golang.org/x/sys/windows"
)

func winOpen(path string, access uint32) (windows.Handle, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	ntpath, err := winpath.Native(strings.TrimPrefix(absolute, `\\?\`))
	if err != nil {
		return windows.InvalidHandle, err
	}
	name, err := windows.NewNTUnicodeString(ntpath)
	if err != nil {
		return windows.InvalidHandle, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{ObjectName: name, Attributes: windows.OBJ_DONT_REPARSE}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attrs, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if nt, ok := err.(windows.NTStatus); ok {
		err = nt.Errno()
	}
	return handle, err
}
func winInfo(path string) (windows.ByHandleFileInformation, windows.Handle, error) {
	var info windows.ByHandleFileInformation
	h, err := winOpen(path, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return info, h, pathError(path, err)
	}
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return info, windows.InvalidHandle, pathError(path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(h)
		return info, windows.InvalidHandle, ErrUnsafePath
	}
	return info, h, nil
}
func winVolume(h windows.Handle) (string, error) {
	var buf [1024]uint16
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 1) // VOLUME_NAME_GUID
	if err != nil {
		return "", pathError("", err)
	}
	if n >= uint32(len(buf)) {
		return "", ErrUnsupported
	}
	path := windows.UTF16ToString(buf[:n])
	end := strings.Index(path, "}\\")
	if !strings.HasPrefix(path, `\\?\Volume{`) || end < 0 {
		return "", ErrUnsupported
	}
	return strings.ToUpper(path[:end+1]), nil
}
func platformIdentity(path string) (Identity, error) {
	info, h, err := winInfo(path)
	if err != nil {
		return Identity{}, err
	}
	defer windows.CloseHandle(h)
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return Identity{}, ErrUnsafePath
	}
	volume, err := winVolume(h)
	if err != nil {
		return Identity{}, err
	}
	var name [64]uint16
	var flags, maxComponent uint32
	if err = windows.GetVolumeInformationByHandle(h, nil, 0, nil, &maxComponent, &flags, &name[0], uint32(len(name))); err != nil {
		return Identity{}, pathError(path, err)
	}
	filesystem := windows.UTF16ToString(name[:])
	if filesystem != "NTFS" && filesystem != "ReFS" {
		return Identity{}, ErrUnsupported
	}
	var caseFlags uint32
	if err = windows.GetFileInformationByHandleEx(h, 23, (*byte)(unsafe.Pointer(&caseFlags)), 4); err != nil {
		return Identity{}, ErrUnsupported
	}
	// Canonical DOS path resolves short-name aliases without following reparse points.
	var final [32768]uint16
	n, err := windows.GetFinalPathNameByHandle(h, &final[0], uint32(len(final)), 0)
	if err != nil {
		return Identity{}, pathError(path, err)
	}
	if n >= uint32(len(final)) {
		return Identity{}, ErrUnsupported
	}
	canonical := strings.TrimPrefix(windows.UTF16ToString(final[:n]), `\\?\`)
	if strings.HasPrefix(canonical, `UNC\`) {
		return Identity{}, ErrUnsupported
	}
	return Identity{Path: canonical, Volume: volume, File: fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), CaseSensitive: caseFlags&1 != 0, UnicodeSensitive: true}, nil
}
func platformFile(info fs.FileInfo, path string) (string, string, error) {
	st, h, err := winInfo(path)
	if err != nil {
		return "", "", err
	}
	defer windows.CloseHandle(h)
	volume, err := winVolume(h)
	if err != nil {
		return "", "", err
	}
	return volume, fmt.Sprintf("%08x:%08x%08x", st.VolumeSerialNumber, st.FileIndexHigh, st.FileIndexLow), nil
}
func platformVersion(info fs.FileInfo, path string) string {
	h, err := winOpen(path, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var basic struct {
		Creation, Access, Write, Change int64
		Attributes                      uint32
		Padding                         uint32
	}
	if windows.GetFileInformationByHandleEx(h, 0, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))) != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", basic.Creation, basic.Change)
}
func isReparse(info fs.FileInfo) bool {
	if st, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return st.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return info.Mode()&os.ModeSymlink != 0
}
func openNoFollow(path string) (*os.File, error) {
	if err := checkParentComponents(path); err != nil {
		return nil, err
	}
	info, h, err := winInfo(path)
	if err != nil {
		return nil, err
	}
	windows.CloseHandle(h)
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, ErrUnsafePath
	}
	handle, err := winOpen(path, windows.GENERIC_READ)
	if err != nil {
		return nil, err
	}
	var actual windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &actual); err != nil || actual.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(handle)
		return nil, ErrUnsafePath
	}
	return os.NewFile(uintptr(handle), path), nil
}
func checkParentComponents(path string) error { return checkComponents(filepath.Dir(path)) }

func RenameNoReplace(from, to string) error {
	if err := checkParentComponents(from); err != nil {
		return err
	}
	if err := checkParentComponents(to); err != nil {
		return err
	}
	info, err := os.Lstat(from)
	if err != nil {
		return pathError(from, err)
	}
	if isReparse(info) {
		return ErrUnsafePath
	}
	source, err := winOpen(from, windows.DELETE|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return pathError(from, err)
	}
	defer windows.CloseHandle(source)
	target, err := winOpen(filepath.Dir(to), windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return pathError(to, err)
	}
	defer windows.CloseHandle(target)
	name, err := windows.UTF16FromString(filepath.Base(to))
	if err != nil {
		return ErrUnsafePath
	}
	name = name[:len(name)-1]
	var header struct {
		Replace uint32
		Root    windows.Handle
		Length  uint32
		Name    [1]uint16
	}
	offset := unsafe.Offsetof(header.Name)
	data := make([]byte, int(offset)+2*len(name))
	payload := (*struct {
		Replace uint32
		Root    windows.Handle
		Length  uint32
	})(unsafe.Pointer(&data[0]))
	payload.Root = target
	payload.Length = uint32(2 * len(name))
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&data[offset])), len(name)), name)
	var status windows.IO_STATUS_BLOCK
	// Replace remains zero; target name is relative to an already-open directory,
	// so neither no-clobber nor no-reparse safety depends on a prior Lstat.
	err = windows.NtSetInformationFile(source, &status, &data[0], uint32(len(data)), windows.FileRenameInformation)
	if nt, ok := err.(windows.NTStatus); ok {
		err = nt.Errno()
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return fmt.Errorf("%w: %s", errors.Join(ErrCollision, fs.ErrExist), to)
	}
	return pathError(to, err)
}

func Watch(ctx context.Context, root Identity, notify func()) (func(), error) {
	if err := Validate(root); err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(root.Path)
	if err != nil {
		return nil, ErrUnsafePath
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, pathError(root.Path, err)
	}
	signal, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, pathError(root.Path, err)
	}
	events := make(chan struct{}, 1)
	closed := make(chan struct{})
	var once sync.Once
	var mutex sync.Mutex
	nativeDone := false
	closeNative := func() {
		once.Do(func() {
			mutex.Lock()
			defer mutex.Unlock()
			close(closed)
			if !nativeDone {
				windows.CancelIoEx(h, nil)
				windows.SetEvent(signal)
			}
		})
	}
	go func() {
		defer close(events)
		defer func() {
			mutex.Lock()
			defer mutex.Unlock()
			nativeDone = true
			windows.CloseHandle(h)
			windows.CloseHandle(signal)
		}()
		buffer := make([]byte, 64*1024)
		for {
			mutex.Lock()
			select {
			case <-closed:
				mutex.Unlock()
				return
			default:
			}
			windows.ResetEvent(signal)
			overlapped := windows.Overlapped{HEvent: signal}
			var count uint32
			err := windows.ReadDirectoryChanges(h, &buffer[0], uint32(len(buffer)), true, windows.FILE_NOTIFY_CHANGE_FILE_NAME|windows.FILE_NOTIFY_CHANGE_DIR_NAME|windows.FILE_NOTIFY_CHANGE_SIZE|windows.FILE_NOTIFY_CHANGE_LAST_WRITE|windows.FILE_NOTIFY_CHANGE_ATTRIBUTES, &count, &overlapped, 0)
			mutex.Unlock()
			if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
				select {
				case events <- struct{}{}:
				default:
				}
				return
			}
			windows.WaitForSingleObject(signal, windows.INFINITE)
			err = windows.GetOverlappedResult(h, &overlapped, &count, true)
			select {
			case <-closed:
				return
			default:
			}
			if err != nil && !errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR) {
				select {
				case events <- struct{}{}:
				default:
				}
				return
			}
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}()
	return debounce(ctx, events, notify, closeNative), nil
}

var ole32 = windows.NewLazySystemDLL("ole32.dll")
var shell32 = windows.NewLazySystemDLL("shell32.dll")
var coInitialize = ole32.NewProc("CoInitializeEx")
var coUninitialize = ole32.NewProc("CoUninitialize")
var coCreate = ole32.NewProc("CoCreateInstance")
var shellItem = shell32.NewProc("SHCreateItemFromParsingName")

type comObject struct{ vtable *[23]uintptr }

//go:uintptrescapes
func (c *comObject) call(slot int, args ...uintptr) int32 {
	all := append([]uintptr{uintptr(unsafe.Pointer(c))}, args...)
	result, _, _ := syscall.SyscallN(c.vtable[slot], all...)
	return int32(result)
}
func comError(hr int32) error { return fmt.Errorf("%w: HRESULT 0x%08x", ErrUnsupported, uint32(hr)) }

// The sink vetoes deletion when the shell cannot recycle. ALLOWUNDO alone may
// silently permanently delete on removable/network volumes and is not sufficient.
// See IFileOperationProgressSink::PreDeleteItem / TSF_DELETE_RECYCLE_IF_POSSIBLE.
type recycleSink struct {
	vtable *[19]uintptr
	result int32
	done   bool
}

var recycleVtable = func() *[19]uintptr {
	ok0 := syscall.NewCallback(func(this uintptr) uintptr { return 0 })
	ok1 := syscall.NewCallback(func(this, a uintptr) uintptr { return 0 })
	ok2 := syscall.NewCallback(func(this, a, b uintptr) uintptr { return 0 })
	ok3 := syscall.NewCallback(func(this, a, b, c uintptr) uintptr { return 0 })
	ok4 := syscall.NewCallback(func(this, a, b, c, d uintptr) uintptr { return 0 })
	ok5 := syscall.NewCallback(func(this, a, b, c, d, e uintptr) uintptr { return 0 })
	ok6 := syscall.NewCallback(func(this, a, b, c, d, e, f uintptr) uintptr { return 0 })
	v := &[19]uintptr{0, 0, 0, ok0, ok1, ok3, ok5, ok4, ok6, ok4, ok6, 0, 0, ok3, ok6, ok2, ok0, ok0, ok0}
	v[0] = syscall.NewCallback(func(this, iid, out uintptr) uintptr {
		guid := *(*windows.GUID)(unsafe.Pointer(iid))
		unknown := windows.GUID{Data1: 0, Data2: 0, Data3: 0, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
		sinkID := windows.GUID{Data1: 0x04b0f1a7, Data2: 0x9490, Data3: 0x44bc, Data4: [8]byte{0x96, 0xe1, 0x42, 0x96, 0xa3, 0x12, 0x52, 0xe2}}
		*(*uintptr)(unsafe.Pointer(out)) = 0
		if guid != unknown && guid != sinkID {
			return uintptr(uint32(0x80004002))
		}
		*(*uintptr)(unsafe.Pointer(out)) = this
		return 0
	})
	v[1] = syscall.NewCallback(func(this uintptr) uintptr { return 1 })
	v[2] = v[1]
	v[11] = syscall.NewCallback(func(this, flags, item uintptr) uintptr {
		if flags&0x80 == 0 {
			return uintptr(uint32(0x80004004))
		}
		return 0
	})
	v[12] = syscall.NewCallback(func(this, flags, item, hr, newItem uintptr) uintptr {
		sink := (*recycleSink)(unsafe.Pointer(this))
		sink.result = int32(hr)
		sink.done = true
		return 0
	})
	return v
}()

func Trash(path string) error {
	parent, err := Identify(filepath.Dir(path))
	if err != nil {
		return err
	}
	if _, err = ValidateRelative(parent, filepath.Base(path)); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitialize.Call(0, 2) // STA; only this worker thread, never an application window.
	if int32(hr) < 0 {
		return comError(int32(hr))
	}
	defer coUninitialize.Call()
	class := windows.GUID{Data1: 0x3ad05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xb8, 0x5b, 0xdb, 0x8e, 0x09}}
	iid := windows.GUID{Data1: 0x947aab5f, Data2: 0x0a5c, Data3: 0x4c13, Data4: [8]byte{0xb4, 0xd6, 0x4b, 0xf7, 0x83, 0x6f, 0xc9, 0xf8}}
	var operation *comObject
	hr, _, _ = coCreate.Call(uintptr(unsafe.Pointer(&class)), 0, 1, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&operation)))
	if int32(hr) < 0 {
		return comError(int32(hr))
	}
	defer operation.call(2)
	// SILENT | NOCONFIRMATION | NOERRORUI | NO_CONNECTED_ELEMENTS |
	// RECYCLEONDELETE | EARLYFAILURE | NOCOPYHOOKS. Never request elevation.
	if result := operation.call(5, 0x0004|0x0010|0x0400|0x2000|0x00080000|0x00100000|0x00800000); result < 0 {
		return comError(result)
	}
	itemID := windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ErrUnsafePath
	}
	var item *comObject
	hr, _, _ = shellItem.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&itemID)), uintptr(unsafe.Pointer(&item)))
	if int32(hr) < 0 {
		return comError(int32(hr))
	}
	defer item.call(2)
	sink := &recycleSink{vtable: recycleVtable}
	if result := operation.call(18, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(sink))); result < 0 {
		return comError(result)
	}
	if result := operation.call(21); result < 0 {
		return comError(result)
	}
	var aborted int32
	if result := operation.call(22, uintptr(unsafe.Pointer(&aborted))); result < 0 {
		return comError(result)
	}
	runtime.KeepAlive(sink)
	if aborted != 0 || !sink.done || sink.result < 0 {
		return ErrUnsupported
	}
	if _, err = os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return ErrUnstable
	}
	return nil
}
