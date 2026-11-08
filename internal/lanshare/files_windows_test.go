//go:build windows

package lanshare

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsMissingLocalFile(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "state.json"), filepath.Join(dir, "missing", "state.json")} {
		f, _, err := OpenLocal(path)
		if f != nil {
			f.Close()
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing local file %q: %v, want os.ErrNotExist", path, err)
		}
	}
}

func TestWindowsLocalJunctionsRemainBlocked(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("not authorized"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junction")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Fatalf("create directory junction: %v: %s", err, output)
	}
	defer os.Remove(link)
	parent, err := openAbsolute(root, true)
	if err != nil {
		t.Fatal("ordinary drive-letter directory rejected:", err)
	}
	defer parent.Close()
	for _, item := range []struct {
		name      string
		directory bool
	}{{"junction", true}, {"junction/secret.txt", false}} {
		f, err := openAbsolute(filepath.Join(root, filepath.FromSlash(item.name)), item.directory)
		if err == nil {
			f.Close()
			t.Fatalf("absolute open followed junction %q", item.name)
		}
		f, err = openRelative(parent, item.name, item.directory)
		if err == nil {
			f.Close()
			t.Fatalf("relative open followed junction %q", item.name)
		}
	}
	body, err := os.ReadFile(filepath.Join(outside, "secret.txt"))
	if err != nil || string(body) != "not authorized" {
		t.Fatalf("junction rejection changed outside file: %q %v", body, err)
	}
}
