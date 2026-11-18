//go:build linux

package lanmedia

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxToolsFollowResolvedExecutable(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "AppDir", "usr", "lib", "pkp", "pkp")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "pkp")
	if err := os.Symlink(exe, launcher); err != nil {
		t.Fatal(err)
	}
	writeTools := func(dir string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			if err := os.WriteFile(filepath.Join(dir, name), nil, mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	beside := filepath.Dir(exe)
	helpers := filepath.Join(beside, "Helpers")
	writeTools(beside, 0o755)
	writeTools(helpers, 0o755)
	// The launcher directory is not the private runtime directory.
	writeTools(root, 0o755)
	p := newProcessor(launcher)
	if p.ffmpeg != filepath.Join(helpers, "ffmpeg") || p.ffprobe != filepath.Join(helpers, "ffprobe") {
		t.Fatalf("private helpers not selected: %+v", p)
	}
	if err := os.Remove(filepath.Join(helpers, "ffprobe")); err != nil {
		t.Fatal(err)
	}
	p = newProcessor(launcher)
	if p.ffmpeg != filepath.Join(beside, "ffmpeg") || p.ffprobe != filepath.Join(beside, "ffprobe") {
		t.Fatalf("incomplete helper pair must not mix tool directories: %+v", p)
	}
	if err := os.WriteFile(filepath.Join(helpers, "ffprobe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p = newProcessor(launcher)
	if p.ffmpeg != filepath.Join(beside, "ffmpeg") || p.ffprobe != filepath.Join(beside, "ffprobe") {
		t.Fatalf("non-executable helper must not hide working tools: %+v", p)
	}
}

func TestLinuxSystemToolsIgnoreWorkingDirectoryAndPATH(t *testing.T) {
	root := t.TempDir()
	untrusted := filepath.Join(root, "untrusted")
	if err := os.Mkdir(untrusted, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(untrusted, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(untrusted)
	t.Setenv("PATH", untrusted)
	p := newProcessor(filepath.Join(root, "usr", "lib", "pkp", "pkp"))
	if p.ffmpeg == "" && p.ffprobe == "" {
		// A machine without the native dependencies must report unavailable.
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			info, err := os.Stat(filepath.Join("/usr/bin", name))
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return
			}
		}
		t.Fatal("installed system tools were not found")
	}
	if p.ffmpeg != "/usr/bin/ffmpeg" || p.ffprobe != "/usr/bin/ffprobe" {
		t.Fatalf("untrusted working directory or PATH used: %+v", p)
	}
}
