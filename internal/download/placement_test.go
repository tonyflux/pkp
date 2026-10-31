package download

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManualPlacementPreservesAConcurrentDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	partial := path + PartialExt
	if err := os.WriteFile(path, []byte("concurrent user's file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte("downloaded content"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := New(&fakeBackend{}, Options{})
	t.Cleanup(q.Close)
	j := &job{Task: Task{ID: "d1", Name: "report.txt", Path: path}}
	q.jobs[j.ID] = j
	if err := q.place(j, partial); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "concurrent user's file" {
		t.Fatalf("existing file lost: %q, %v", got, err)
	}
	got, err = os.ReadFile(j.Path)
	if err != nil || string(got) != "downloaded content" || j.Path == path {
		t.Fatalf("download not placed separately: %q at %q, %v", got, j.Path, err)
	}
}
