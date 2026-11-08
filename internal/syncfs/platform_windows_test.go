//go:build windows

package syncfs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDriveOpenAndMissingChild(t *testing.T) {
	dir := t.TempDir()
	h, err := winOpen(dir, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		t.Fatal("ordinary drive-letter directory rejected:", err)
	}
	windows.CloseHandle(h)
	h, err = winOpen(filepath.Join(dir, "missing"), windows.FILE_READ_ATTRIBUTES)
	if err == nil {
		windows.CloseHandle(h)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing child: %v, want os.ErrNotExist", err)
	}
}

func TestWindowsDriveOpenRejectsJunctions(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("not authorized"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junction")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Fatalf("create directory junction: %v: %s", err, output)
	}
	defer os.Remove(link)
	for _, path := range []string{link, filepath.Join(link, "secret.txt")} {
		_, h, err := winInfo(path)
		if err == nil {
			windows.CloseHandle(h)
			t.Fatalf("safe open followed junction %q", path)
		}
	}
}
