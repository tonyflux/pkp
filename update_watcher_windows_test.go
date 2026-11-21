package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsWatcherHandoff(t *testing.T) {
	if mode := os.Getenv("PKP_TEST_WATCHER"); mode != "" {
		switch mode {
		case "watch":
			args := os.Args
			if err := os.Setenv("PKP_TEST_WATCHER", "restart"); err != nil {
				t.Fatal(err)
			}
			os.Args = []string{args[0], "--pkp-update-watcher", os.Getenv("PKP_TEST_APP"), "1.0.2", os.Getenv("PKP_TEST_CACHE")}
			runUpdateWatcher()
			os.Args = args
			return
		case "restart":
			if err := os.WriteFile(os.Getenv("PKP_TEST_MARKER"), []byte("restarted"), 0600); err != nil {
				t.Fatal(err)
			}
			os.Exit(0)
		default:
			_, _ = io.Copy(io.Discard, os.Stdin)
			code, err := strconv.Atoi(mode)
			if err != nil {
				t.Fatal(err)
			}
			os.Exit(code)
		}
	}
	for _, tc := range []struct {
		name      string
		exit      int
		automatic int
		receipt   string
		restart   bool
	}{
		{"success", 0, 1, "", true},
		{"busy", 71, 1, "installBusy", true},
		{"failed", 2, 1, "install", true},
		{"rollback", 72, 1, "installRollback", false},
		{"legacy", 0, 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := t.TempDir()
			marker := filepath.Join(cache, "restarted")
			watch := exec.Command(os.Args[0], "-test.run=^TestWindowsWatcherHandoff$")
			watch.Env = append(os.Environ(), "PKP_TEST_WATCHER=watch", "PKP_TEST_APP="+os.Args[0], "PKP_TEST_CACHE="+cache, "PKP_TEST_MARKER="+marker)
			input, err := watch.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = watch.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = watch.Process.Kill() })
			target, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(watch.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(target)
			current, _ := windows.GetCurrentProcess()
			parent, err := windows.CreateEvent(nil, 1, 1, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(parent)
			locked, err := os.Open(os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			defer locked.Close()
			installer := exec.Command(os.Args[0], "-test.run=^TestWindowsWatcherHandoff$")
			installerInput, err := installer.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			installer.Env = append(os.Environ(), "PKP_TEST_WATCHER="+strconv.Itoa(tc.exit))
			if err = installer.Start(); err != nil {
				t.Fatal(err)
			}
			defer installer.Wait()
			process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(installer.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(process)
			duplicate := func(handle windows.Handle) uint64 {
				t.Helper()
				var out windows.Handle
				if err := windows.DuplicateHandle(current, handle, target, &out, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
					t.Fatal(err)
				}
				return uint64(out)
			}
			p, f, d, i := duplicate(parent), duplicate(windows.Handle(locked.Fd())), duplicate(windows.Handle(locked.Fd())), duplicate(process)
			installerInput.Close()
			if _, err = fmt.Fprintf(input, "%d %d 1 %d\n%d %d\n", p, f, d, i, tc.automatic); err != nil {
				t.Fatal(err)
			}
			input.Close()
			if err := watch.Wait(); err != nil {
				t.Fatal(err)
			}
			_, restartErr := os.Stat(marker)
			if (restartErr == nil) != tc.restart {
				t.Fatalf("restart=%v, want %v", restartErr == nil, tc.restart)
			}
			data, err := os.ReadFile(filepath.Join(cache, "install-result.json"))
			if tc.automatic == 0 {
				if !os.IsNotExist(err) {
					t.Fatalf("legacy installer produced receipt: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				Version string `json:"version"`
				Error   string `json:"error"`
			}
			if err = json.Unmarshal(data, &receipt); err != nil || receipt.Version != "1.0.2" || receipt.Error != tc.receipt {
				t.Fatalf("receipt=%s, err=%v", data, err)
			}
		})
	}
}
