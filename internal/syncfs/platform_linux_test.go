//go:build linux

package syncfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxRootReplacementAndAncestorLinks(t *testing.T) {
	root := testRoot(t)
	old := root.Path + "-old"
	if err := os.Rename(root.Path, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(old) })
	if err := os.Mkdir(root.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Validate(root); !errors.Is(err, ErrRootChanged) {
		t.Fatalf("replaced root accepted: %v", err)
	}
	put(t, filepath.Join(old, "source"), "safe")
	if err := os.Mkdir(filepath.Join(old, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Identify(filepath.Join(link, "nested")); err == nil {
		t.Fatal("accepted ancestor alias")
	}
	if _, err := Hash(context.Background(), filepath.Join(link, "source")); err == nil {
		t.Fatal("hash followed ancestor alias")
	}
	if err := RenameNoReplace(filepath.Join(link, "source"), filepath.Join(root.Path, "moved")); err == nil {
		t.Fatal("rename followed ancestor alias")
	}
	if err := Trash(filepath.Join(link, "source")); err == nil {
		t.Fatal("trash followed ancestor alias")
	}
	if data, err := os.ReadFile(filepath.Join(old, "source")); err != nil || string(data) != "safe" {
		t.Fatalf("source damaged: %q %v", data, err)
	}
}

func TestLinuxWatchRealRecursiveChanges(t *testing.T) {
	root := testRoot(t)
	nested := filepath.Join(root.Path, "existing", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	notifications := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := Watch(ctx, root, func() { notifications <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	await := func() {
		t.Helper()
		select {
		case <-notifications:
		case <-time.After(12 * time.Second):
			t.Fatal("no native change notification")
		}
	}
	put(t, filepath.Join(nested, "file"), "one")
	await()
	incoming := filepath.Join(t.TempDir(), "incoming")
	if err := os.MkdirAll(filepath.Join(incoming, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root.Path, "moved")
	if err := os.Rename(incoming, moved); err != nil {
		t.Fatal(err)
	}
	await()
	put(t, filepath.Join(moved, "child", "new"), "two")
	await()
	stop()
	stop()
}

func TestLinuxTrashPreservesFilesDirectoriesAndMetadata(t *testing.T) {
	root := testRoot(t)
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("XDG_DATA_HOME", data)
	for _, name := range []string{"file with % space", "directory"} {
		source := filepath.Join(root.Path, name)
		if name == "directory" {
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(source, "child"), "keep")
		} else {
			put(t, source, "keep")
		}
		before, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		if err = Trash(source); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(source); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source remains: %v", err)
		}
		files, err := os.ReadDir(filepath.Join(data, "Trash", "files"))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, file := range files {
			trashed := filepath.Join(data, "Trash", "files", file.Name())
			after, err := os.Stat(trashed)
			if err != nil || !os.SameFile(before, after) {
				continue
			}
			found = true
			if name == "directory" {
				trashed = filepath.Join(trashed, "child")
			}
			body, err := os.ReadFile(trashed)
			if err != nil || string(body) != "keep" {
				t.Fatalf("trash damaged contents: %q %v", body, err)
			}
			metadata, err := os.ReadFile(filepath.Join(data, "Trash", "info", file.Name()+".trashinfo"))
			if err != nil || !strings.Contains(string(metadata), "[Trash Info]\nPath=/") || !strings.Contains(string(metadata), "\nDeletionDate=") {
				t.Fatalf("missing restorable metadata: %q %v", metadata, err)
			}
		}
		if !found {
			t.Fatal("original inode not in trash")
		}
	}
	if err := os.Chmod(filepath.Join(data, "Trash"), 0777); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root.Path, "must-survive")
	put(t, source, "safe")
	if err := Trash(source); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("unsafe trash accepted: %v", err)
	}
	if body, err := os.ReadFile(source); err != nil || string(body) != "safe" {
		t.Fatalf("failed trash lost source: %q %v", body, err)
	}
}

func TestLinuxCasefoldDirectoryIsRefused(t *testing.T) {
	root := testRoot(t)
	f, err := openNoFollow(root.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	flags, err := unix.IoctlGetInt(int(f.Fd()), unix.FS_IOC_GETFLAGS)
	if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EOPNOTSUPP) {
		t.Skip("filesystem has no directory casefold ioctl")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = unix.IoctlSetInt(int(f.Fd()), unix.FS_IOC_SETFLAGS, flags|0x40000000); err != nil {
		t.Skipf("test filesystem does not enable ext4 casefold: %v", err)
	}
	if _, err = Identify(root.Path); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("casefold directory advertised as case sensitive: %v", err)
	}
}
