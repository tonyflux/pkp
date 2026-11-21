package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func runUpdateWatcher() bool {
	if len(os.Args) != 5 || os.Args[1] != "--pkp-update-watcher" {
		return false
	}
	exe, version, cache := os.Args[2], os.Args[3], os.Args[4]
	var parentValue, packageValue, installerValue uint64
	var directoryCount int
	input := bufio.NewReader(os.Stdin)
	if _, err := fmt.Fscan(input, &parentValue, &packageValue, &directoryCount); err != nil || parentValue == 0 || packageValue == 0 || directoryCount < 1 || directoryCount > 1024 {
		return true
	}
	parent, locked := windows.Handle(parentValue), windows.Handle(packageValue)
	defer windows.CloseHandle(parent)
	defer func() {
		if locked != 0 {
			windows.CloseHandle(locked)
		}
	}()
	directories := make([]windows.Handle, 0, directoryCount)
	defer func() {
		for _, handle := range directories {
			windows.CloseHandle(handle)
		}
	}()
	for range directoryCount {
		var value uint64
		if _, err := fmt.Fscan(input, &value); err != nil || value == 0 {
			return true
		}
		directories = append(directories, windows.Handle(value))
	}
	var automatic int
	if _, err := fmt.Fscan(input, &installerValue, &automatic); err != nil || installerValue == 0 {
		return true
	}
	installer := windows.Handle(installerValue)
	defer windows.CloseHandle(installer)
	if result, err := windows.WaitForSingleObject(installer, windows.INFINITE); err != nil || result != windows.WAIT_OBJECT_0 {
		return true
	}
	if automatic == 0 {
		return true
	}
	windows.CloseHandle(locked)
	locked = 0
	for _, handle := range directories {
		windows.CloseHandle(handle)
	}
	directories = nil
	var exit uint32
	if err := windows.GetExitCodeProcess(installer, &exit); err != nil {
		return true
	}
	if result, err := windows.WaitForSingleObject(parent, windows.INFINITE); err != nil || result != windows.WAIT_OBJECT_0 {
		return true
	}
	code, restart := windowsUpdateOutcome(exit)
	receipt, _ := json.Marshal(struct {
		Version string `json:"version"`
		Error   string `json:"error"`
	}{version, code})
	_ = os.WriteFile(filepath.Join(cache, "install-result.json"), receipt, 0600)
	if !restart {
		return true
	}
	// Another installer can keep the process-wide guard alive after this one exits.
	deadline := time.Now().Add(5 * time.Minute)
	name, _ := windows.UTF16PtrFromString(`Global\PKP.InstallerRunning`)
	for {
		mutex, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
		if err == windows.ERROR_FILE_NOT_FOUND {
			break
		}
		if err != nil && err != windows.ERROR_ACCESS_DENIED {
			showUpdateRestartError()
			return true
		}
		if err == nil {
			windows.CloseHandle(mutex)
		}
		if time.Now().After(deadline) {
			showUpdateRestartError()
			return true
		}
		time.Sleep(time.Second)
	}
	cmd := exec.Command(exe)
	if err := cmd.Start(); err != nil {
		showUpdateRestartError()
		return true
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			showUpdateRestartError()
		}
	case <-time.After(3 * time.Second):
	}
	return true
}

func showUpdateRestartError() {
	message, _ := windows.UTF16PtrFromString("PKP could not reopen after the update. Please start PKP manually.")
	title, _ := windows.UTF16PtrFromString("PKP")
	_, _ = windows.MessageBox(0, message, title, 0x10)
}
