package main

import (
	"errors"
	"golang.org/x/sys/windows"
)

// NSIS waits for every profile's process, not just the one requesting update.
func holdUpdateInstallLock() (func(), error) {
	name, _ := windows.UTF16PtrFromString(`Global\PKP.ApplicationRunning`)
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return nil, err
	}
	installerName, _ := windows.UTF16PtrFromString(`Global\PKP.InstallerRunning`)
	installer, installerErr := windows.OpenMutex(windows.SYNCHRONIZE, false, installerName)
	if installerErr == nil {
		windows.CloseHandle(installer)
		windows.CloseHandle(handle)
		return nil, errors.New("PKP installation is in progress; try again when it finishes")
	}
	if installerErr != windows.ERROR_FILE_NOT_FOUND {
		windows.CloseHandle(handle)
		return nil, installerErr
	}
	return func() { windows.CloseHandle(handle) }, nil
}
