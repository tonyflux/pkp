package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRevealSyncLocalFolder(t *testing.T) {
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	var opened []string
	original := revealPath
	t.Cleanup(func() { revealPath = original })
	revealPath = func(path string, selectFile bool) error {
		if selectFile {
			t.Fatal("a sync root must open as a directory, not a selected file")
		}
		opened = append(opened, path)
		return nil
	}
	svc := &DriveService{}
	if err := svc.RevealSyncLocalFolder(root); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != canonical {
		t.Fatalf("opened %v, want directory %s", opened, canonical)
	}
	file := filepath.Join(root, "not-a-folder")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(root, "missing")} {
		if err := svc.RevealSyncLocalFolder(path); err == nil {
			t.Fatalf("accepted unavailable directory %s", path)
		}
	}
	if len(opened) != 1 {
		t.Fatalf("invalid roots reached the file manager: %v", opened)
	}
	nativeErr := errors.New("file manager refused")
	revealPath = func(string, bool) error { return nativeErr }
	if err := svc.RevealSyncLocalFolder(root); !errors.Is(err, nativeErr) {
		t.Fatalf("native failure = %v, want %v", err, nativeErr)
	}
}
