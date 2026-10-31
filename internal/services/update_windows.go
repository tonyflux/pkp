package services

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func updatePlatform() (string, string) {
	v := windows.RtlGetVersion()
	return "windows-x64-arm64", fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

// ShellExecute's runas verb asks UAC before the caller quits. The installer must
// wait for /WAITPID rather than trying to replace a running executable or DLL.
func openUpdatePackage(path string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	args, _ := windows.UTF16PtrFromString(fmt.Sprintf("/WAITPID=%d", os.Getpid()))
	return windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL)
}
