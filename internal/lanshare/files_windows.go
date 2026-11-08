//go:build windows

package lanshare

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/tonyflux/pkp/internal/winpath"
	"golang.org/x/sys/windows"
)

func ntOpen(parent windows.Handle, name string, directory bool) (*os.File, error) {
	nt, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: nt, Attributes: windows.OBJ_DONT_REPARSE | windows.OBJ_CASE_INSENSITIVE}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	flags := uint32(windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_FOR_BACKUP_INTENT | windows.FILE_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_DIRECTORY_FILE
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.GENERIC_READ|windows.SYNCHRONIZE, &attrs, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, flags, 0, 0)
	if status, ok := err.(windows.NTStatus); ok {
		err = status.Errno()
	}
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(handle)
		return nil, errForbidden
	}
	return os.NewFile(uintptr(handle), name), nil
}

func openAbsolute(path string, directory bool) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, `\\`) {
		return nil, errForbidden
	}
	// Resolve the DOS drive alias without allowing filesystem reparses.
	native, err := winpath.Native(path)
	if err != nil {
		return nil, err
	}
	return ntOpen(0, native, directory)
}

func openRelative(root *os.File, rel string, directory bool) (*os.File, error) {
	if rel == "" {
		var handle windows.Handle
		p := windows.CurrentProcess()
		if err := windows.DuplicateHandle(p, windows.Handle(root.Fd()), p, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(handle), root.Name()), nil
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
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\:\x00") {
			return nil, errForbidden
		}
		next, err := ntOpen(windows.Handle(parent.Fd()), part, i < len(parts)-1 || directory)
		if err != nil {
			return nil, err
		}
		if owned != nil {
			owned.Close()
		}
		owned, parent = next, next
	}
	result := owned
	owned = nil
	return result, nil
}

func handlePath(f *os.File) (string, error) {
	var path [32768]uint16
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &path[0], uint32(len(path)), 0)
	if err != nil {
		return "", err
	}
	if n >= uint32(len(path)) {
		return "", errUnavailable
	}
	return strings.TrimPrefix(windows.UTF16ToString(path[:n]), `\\?\`), nil
}

func handleIdentity(f *os.File) (key, version, volume string, err error) {
	h := windows.Handle(f.Fd())
	var st windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &st); err != nil {
		return
	}
	if st.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		err = errForbidden
		return
	}
	var basic struct {
		Creation, Access, Write, Change int64
		Attributes                      uint32
		Padding                         uint32
	}
	if err = windows.GetFileInformationByHandleEx(h, 0, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return
	}
	volume = fmt.Sprintf("%08x", st.VolumeSerialNumber)
	key = fmt.Sprintf("%s:%08x%08x:%d", volume, st.FileIndexHigh, st.FileIndexLow, basic.Creation)
	version = fmt.Sprintf("%s:%d:%d:%d:%d", key, uint64(st.FileSizeHigh)<<32|uint64(st.FileSizeLow), basic.Creation, basic.Change, basic.Write)
	return
}

func freeBytes(path string) (int64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err = windows.GetDiskFreeSpaceEx(name, &free, nil, nil); err != nil {
		return 0, err
	}
	return int64(free), nil
}

func isDiskFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}
