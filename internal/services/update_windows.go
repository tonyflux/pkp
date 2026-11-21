package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/tonyflux/pkp/internal/crashlog"
	"github.com/tonyflux/pkp/internal/update"
)

func updatePlatform() (string, string) {
	v := windows.RtlGetVersion()
	platform := "windows-x64-arm64"
	if exe, err := os.Executable(); err == nil && matchesWindowsInstall(registry.CURRENT_USER, exe) {
		platform = "windows-user-x64-arm64"
	}
	return platform, fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

func matchesWindowsInstall(root registry.Key, exe string) bool {
	key, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\Uninstall\PKPPKP`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return false
	}
	defer key.Close()
	dir, _, err := key.GetStringValue("InstallLocation")
	if err != nil || dir == "" {
		uninstall, _, readErr := key.GetStringValue("UninstallString")
		if readErr != nil || len(uninstall) < 2 || uninstall[0] != '"' || uninstall[len(uninstall)-1] != '"' {
			return false
		}
		dir = filepath.Dir(uninstall[1 : len(uninstall)-1])
	}
	return strings.EqualFold(filepath.Clean(filepath.Join(dir, "pkp.exe")), filepath.Clean(exe))
}

func prepareUpdatePackage(_ context.Context, path string, p update.Package, version, cacheDir string) (*updateQuit, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	user := matchesWindowsInstall(registry.CURRENT_USER, exe)
	if !user && !matchesWindowsInstall(registry.LOCAL_MACHINE, exe) {
		return nil, update.ErrInstallLocation
	}
	return &updateQuit{start: func() error {
		if user != matchesWindowsInstall(registry.CURRENT_USER, exe) || (!user && !matchesWindowsInstall(registry.LOCAL_MACHINE, exe)) {
			return update.ErrInstallLocation
		}
		return openUpdatePackage(path, exe, version, cacheDir, p, user)
	}, cancel: func() {}}, nil
}

// Keep the unelevated watcher alive across shutdown; it reopens the same profile.
func openUpdatePackage(path, exe, version, cacheDir string, p update.Package, user bool) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	watcher, err := crashlog.UpdateWatcherExecutable(exe)
	if err != nil {
		return err
	}
	ready, signal, err := os.Pipe()
	if err != nil {
		return err
	}
	defer ready.Close()
	defer signal.Close()
	cmd := exec.Command(watcher, "--pkp-update-watcher", exe, version, cacheDir)
	cmd.Stdin = ready
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	ready.Close()
	go cmd.Wait()

	target, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(target)
	var directories []windows.Handle
	defer func() {
		for _, handle := range directories {
			windows.CloseHandle(handle)
		}
	}()
	var paths []string
	for dir := filepath.Dir(path); ; {
		paths = append(paths, dir)
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			break
		}
		dir = parentDir
	}
	for i := len(paths) - 1; i >= 0; i-- {
		dirName, err := windows.UTF16PtrFromString(paths[i])
		if err != nil {
			return err
		}
		handle, err := windows.CreateFile(dirName, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return err
		}
		directories = append(directories, handle)
		var info windows.ByHandleFileInformation
		if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
			return err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("update: cache path contains a reparse point: %s", paths[i])
		}
	}
	// Deny writes and renames from the final checksum until the installer exits.
	packageHandle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	lockedFile := os.NewFile(uintptr(packageHandle), path)
	defer lockedFile.Close()
	var packageInfo windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(packageHandle, &packageInfo); err != nil {
		return err
	}
	if packageInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return update.ErrIntegrity
	}
	if err = update.CheckOpenFile(lockedFile, p); err != nil {
		return err
	}
	current, err := windows.GetCurrentProcess()
	if err != nil {
		return err
	}
	var parent, locked windows.Handle
	if err = windows.DuplicateHandle(current, current, target, &parent, windows.SYNCHRONIZE, false, 0); err != nil {
		return err
	}
	if err = windows.DuplicateHandle(current, packageHandle, target, &locked, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return err
	}
	directoryHandles := make([]uint64, 0, len(directories))
	for _, handle := range directories {
		var duplicate windows.Handle
		if err = windows.DuplicateHandle(current, handle, target, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return err
		}
		directoryHandles = append(directoryHandles, uint64(duplicate))
	}
	if _, err = fmt.Fprint(signal, uint64(parent), " ", uint64(locked), " ", len(directoryHandles)); err != nil {
		return err
	}
	for _, handle := range directoryHandles {
		if _, err = fmt.Fprint(signal, " ", handle); err != nil {
			return err
		}
	}
	if _, err = fmt.Fprintln(signal); err != nil {
		return err
	}
	verb := "runas"
	if user {
		verb = "open"
	}
	verbPtr, _ := windows.UTF16PtrFromString(verb)
	args := fmt.Sprintf("/WAITPID=%d", os.Getpid())
	if p.InstallProtocol == 1 {
		args = fmt.Sprintf(`/S /PKPUPDATE=1 /WAITPID=%d /EXPECTDIR="%s"`, os.Getpid(), filepath.Dir(exe))
	}
	argsPtr, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	info := struct {
		size       uint32
		mask       uint32
		hwnd       windows.Handle
		verb       *uint16
		file       *uint16
		parameters *uint16
		directory  *uint16
		show       int32
		instance   windows.Handle
		idList     uintptr
		class      *uint16
		classKey   windows.Handle
		hotkey     uint32
		icon       windows.Handle
		process    windows.Handle
	}{mask: 0x140, verb: verbPtr, file: name, parameters: argsPtr, show: windows.SW_SHOWNORMAL}
	info.size = uint32(unsafe.Sizeof(info))
	call := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")
	if ok, _, callErr := call.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		return callErr
	}
	if info.process == 0 {
		return fmt.Errorf("update: installer started without a process handle; finish or close it before retrying")
	}
	defer windows.CloseHandle(info.process)
	var installer windows.Handle
	if err = windows.DuplicateHandle(current, info.process, target, &installer, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return err
	}
	automatic := 0
	if p.InstallProtocol == 1 {
		automatic = 1
	}
	if _, err = fmt.Fprintln(signal, uint64(installer), automatic); err != nil {
		return err
	}
	return nil
}
