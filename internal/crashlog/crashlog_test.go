package crashlog

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if RunMonitor() {
		return
	}
	if mode := os.Getenv("PKP_CRASH_TEST_MODE"); mode != "" {
		r, err := Start("1.2.3-test")
		if err != nil {
			panic(err)
		}
		switch mode {
		case "panic":
			go func() { panic("crashlog test panic") }()
			select {}
		case "fatal":
			r.Record("startup", errors.New("crashlog test fatal"))
			os.Exit(1)
		case "normal":
			r.Close()
		}
		return
	}
	os.Exit(m.Run())
}

func TestCrashReports(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the real Windows desktop is resolved by the OS, not HOME")
	}
	for _, tc := range []struct {
		name     string
		mode     string
		desktop  bool
		want     string
		wantFail bool
	}{
		{name: "goroutine panic", mode: "panic", desktop: true, want: "panic: crashlog test panic", wantFail: true},
		{name: "fatal exit", mode: "fatal", desktop: true, want: "startup: crashlog test fatal", wantFail: true},
		{name: "normal exit", mode: "normal", desktop: true},
		{name: "missing desktop", mode: "fatal", want: "startup: crashlog test fatal", wantFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			desktop := filepath.Join(home, "Desktop")
			if tc.desktop {
				if err := os.Mkdir(desktop, 0700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), "HOME="+home, "PKP_CRASH_TEST_MODE="+tc.mode)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantFail {
				t.Fatalf("child error = %v, output: %s", err, output)
			}
			dir := desktop
			if !tc.desktop {
				dir = filepath.Join(home, "Library", "Caches", "pkp", "crash-reports")
				if runtime.GOOS != "darwin" {
					dir = filepath.Join(home, ".cache", "pkp", "crash-reports")
				}
			}
			var reports []string
			deadline := time.Now().Add(5 * time.Second)
			for {
				reports, err = filepath.Glob(filepath.Join(dir, "PKP-Crash-*.log"))
				if err != nil {
					t.Fatal(err)
				}
				if len(reports) > 0 || tc.want == "" || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if tc.want == "" {
				if len(reports) != 0 {
					t.Fatalf("normal exit created a report: %v", reports)
				}
				return
			}
			if len(reports) != 1 {
				t.Fatalf("reports = %v, child output: %s", reports, output)
			}
			data, err := os.ReadFile(reports[0])
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, want := range []string{tc.want, "Version: 1.2.3-test", "OS/arch: " + runtime.GOOS + "/" + runtime.GOARCH} {
				if !strings.Contains(text, want) {
					t.Errorf("report missing %q: %s", want, text)
				}
			}
			if tc.mode == "panic" && !strings.Contains(text, "goroutine ") {
				t.Errorf("panic report missing stack trace: %s", text)
			}
			if info, err := os.Stat(reports[0]); err != nil || info.Mode().Perm()&0077 != 0 {
				t.Errorf("report is accessible to other users: %v %v", info, err)
			}
		})
	}
}
