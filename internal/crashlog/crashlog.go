// Package crashlog saves fatal diagnostics locally without uploading them.
package crashlog

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"
)

const monitorArg = "--pkp-crash-monitor"
const monitorEnv = "PKP_INTERNAL_CRASH_MONITOR"

// Recorder forwards fatal errors to a separate process, which survives a crash.
type Recorder struct {
	pipe *os.File
	done chan error
}

// RunMonitor handles the private helper invocation before the app is started.
func RunMonitor() bool {
	if len(os.Args) != 2 || os.Args[1] != monitorArg || os.Getenv(monitorEnv) != "1" {
		return false
	}
	monitor(os.Stdin, os.Getenv("PKP_INTERNAL_CRASH_VERSION"), os.Getenv("PKP_INTERNAL_CRASH_PID"))
	return true
}

// Start registers Go's fatal output and starts the helper. Normal exits create no report.
func Start(version string) (*Recorder, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, err = monitorExecutable(exe)
	if err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, monitorArg)
	for _, key := range []string{"HOME", "LOCALAPPDATA", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TMPDIR", "TMP", "TEMP", "XDG_CACHE_HOME"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, monitorEnv+"=1", "PKP_INTERNAL_CRASH_VERSION="+version, "PKP_INTERNAL_CRASH_PID="+strconv.Itoa(os.Getpid()))
	cmd.Stdin = read
	if runtime.GOOS != "windows" {
		cmd.Stderr = os.Stderr
	}
	if err = cmd.Start(); err != nil {
		read.Close()
		write.Close()
		return nil, err
	}
	read.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err = debug.SetCrashOutput(write, debug.CrashOptions{}); err != nil {
		write.Close()
		<-done
		return nil, err
	}
	return &Recorder{pipe: write, done: done}, nil
}

// Record adds an error that is about to terminate the process without panicking.
func (r *Recorder) Record(stage string, err error) {
	if r != nil {
		_, _ = fmt.Fprintf(r.pipe, "%s: %v\n", stage, err)
	}
}

// Close detaches the runtime output and waits for the helper to finish.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	_ = r.pipe.Close()
	<-r.done
}

func monitor(input io.Reader, version, pid string) {
	buf := make([]byte, 32*1024)
	var n int
	var err error
	for n == 0 && err == nil {
		n, err = input.Read(buf)
	}
	if n == 0 {
		return
	}

	file, err := createReport(time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "crash report: %v\n", err)
		_, _ = io.Copy(io.Discard, input)
		return
	}
	defer file.Close()
	if version == "" {
		version = "unknown"
	}
	_, err = fmt.Fprintf(file, "PKP crash report\nTime: %s\nVersion: %s\nOS/arch: %s/%s\nGo: %s\nProcess: %s\n\n", time.Now().Format(time.RFC3339), version, runtime.GOOS, runtime.GOARCH, runtime.Version(), pid)
	if err == nil {
		_, err = io.Copy(file, io.MultiReader(bytes.NewReader(buf[:n]), input))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "crash report: %v\n", err)
		_, _ = io.Copy(io.Discard, input)
	}
}

func createReport(now time.Time) (*os.File, error) {
	name := "PKP-Crash-" + now.Format("20060102-150405") + "-*.log"
	desktop, desktopErr := desktopDir()
	if desktopErr == nil && desktop != "" {
		if file, err := os.CreateTemp(desktop, name); err == nil {
			return file, nil
		} else {
			desktopErr = err
		}
	}
	cache, err := os.UserCacheDir()
	if err == nil && cache != "" {
		dir := filepath.Join(cache, "pkp", "crash-reports")
		if err = os.MkdirAll(dir, 0700); err == nil {
			return os.CreateTemp(dir, name)
		}
	}
	return nil, fmt.Errorf("desktop: %v; cache: %v", desktopErr, err)
}
