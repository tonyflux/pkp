package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tonyflux/pkp/internal/update"
)

func updatePlatform() (string, string) {
	version, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return "", ""
	}
	return "darwin-universal", strings.TrimSpace(string(version))
}

// prepareUpdatePackage runs outside AppKit's quit hook; mounting, copying and
// authorization must finish while the old application is still running.
func prepareUpdatePackage(ctx context.Context, path string, p update.Package, version, cacheDir string) (_ *updateQuit, err error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	bundle, err := update.AppBundle(executable)
	if err != nil {
		return nil, err
	}
	lockPath, err := update.InstallLockPath(bundle)
	if err != nil {
		return nil, err
	}
	helperPath := filepath.Join(bundle, "Contents", "Helpers", "pkp-updater")
	helper, err := os.Open(helperPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", update.ErrInstallLocation, err)
	}
	defer helper.Close()
	info, err := helper.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, update.ErrInstallLocation
	}
	workspace, err := os.MkdirTemp(cacheDir, ".installer-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(workspace) }
	launched := false
	defer func() {
		if err != nil && !launched {
			cleanup()
		}
	}()
	copiedPath := filepath.Join(workspace, "pkp-updater")
	copied, err := os.OpenFile(copiedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return nil, err
	}
	_, copyErr := io.Copy(copied, helper)
	closeErr := copied.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(cacheDir, "install.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	cmd := exec.Command(copiedPath,
		"--package", path, "--target", bundle, "--version", version,
		"--sha256", p.SHA256, "--size", fmt.Sprint(p.Size),
		"--parent-pid", fmt.Sprint(os.Getpid()),
		"--receipt", filepath.Join(cacheDir, updateReceiptName), "--lock", lockPath)
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	launched = true
	type preparation struct {
		Ready bool   `json:"ready"`
		Error string `json:"error"`
	}
	result := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		var ready preparation
		err := json.NewDecoder(io.LimitReader(output, 4096)).Decode(&ready)
		output.Close()
		if err == nil && !ready.Ready {
			err = updateInstallError(ready.Error)
		}
		result <- err
		_ = cmd.Wait()
		close(exited)
	}()
	var mu sync.Mutex
	committed, cancelled := false, false
	abort := func() {
		mu.Lock()
		defer mu.Unlock()
		if committed || cancelled {
			return
		}
		cancelled = true
		_ = input.Close() // EOF before install aborts, including a crashed parent.
		go func() {
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-exited
			}
			cleanup()
		}()
	}
	select {
	case err = <-result:
		if err != nil {
			abort()
			return nil, err
		}
	case <-ctx.Done():
		abort()
		return nil, ctx.Err()
	}
	return &updateQuit{
		start: func() error {
			mu.Lock()
			defer mu.Unlock()
			if cancelled || committed {
				return errors.New("update: installer is no longer awaiting confirmation")
			}
			select {
			case <-exited:
				return errors.New("update: installer exited before confirmation")
			default:
			}
			if _, err := io.WriteString(input, "install\n"); err != nil {
				return err
			}
			committed = true
			_ = input.Close()
			return nil
		},
		cancel: abort,
	}, nil
}
