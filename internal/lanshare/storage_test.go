package lanshare

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/syncfs"
)

func storageTestDirectory(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func storageTestCopy(t *testing.T, m *Manager, key string) (*cacheFile, string) {
	t.Helper()
	dir, err := m.newOutputDirectory()
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	identity := m.pendingIdentities[dir]
	m.mu.Unlock()
	name := dir + "/compatible.mp4"
	path, ok := m.cacheLocation(name, identity, false)
	if !ok {
		t.Fatal("allocation does not resolve")
	}
	if err := os.WriteFile(path, []byte("owned-copy"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := LocalVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	record := &cacheFile{Path: name, Directory: identity, Token: token, Size: 10, Used: time.Now()}
	m.mu.Lock()
	m.cache[key] = record
	delete(m.pending, dir)
	delete(m.pendingIdentities, dir)
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return record, path
}

func TestStorageSelectionsPersistWithoutMovingResources(t *testing.T) {
	config := storageTestDirectory(t)
	m, err := New(Options{ConfigDir: config, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	legacy, legacyPath := storageTestCopy(t, m, "legacy")
	originals, copies := storageTestDirectory(t), storageTestDirectory(t)
	if err := m.SetStorageDirectory("original", originals); err != nil {
		t.Fatal(err)
	}
	if err := m.SetStorageDirectory("copies", copies); err != nil {
		t.Fatal(err)
	}
	_, newPath := storageTestCopy(t, m, "new")
	if filepath.Dir(filepath.Dir(newPath)) != copies {
		t.Fatalf("new copy outside selection: %s", newPath)
	}
	m.mu.Lock()
	cached, f := m.cachedLocked("legacy")
	m.mu.Unlock()
	if f == nil || cached != legacy {
		t.Fatal("selection discarded old copy")
	}
	f.Close()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{ConfigDir: config, OriginalDir: storageTestDirectory(t), MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	status := m.Status()
	if status.OriginalDir != originals || status.CopyDir != copies {
		t.Fatalf("selections lost: %+v", status)
	}
	for _, key := range []string{"legacy", "new"} {
		m.mu.Lock()
		_, f = m.cachedLocked(key)
		m.mu.Unlock()
		if f == nil {
			t.Fatalf("%s not reusable after restart", key)
		}
		f.Close()
	}
	unrelated := filepath.Join(copies, "my-download.mp4")
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := m.ClearCache()
	if err != nil || result.RemovedBytes != 20 {
		t.Fatalf("cleanup: %+v %v", result, err)
	}
	for _, path := range []string{legacyPath, newPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned copy retained: %s %v", path, err)
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("cleanup deleted unrelated download", err)
	}
}

func TestStorageChangeBlocksAllocationsButNotReaders(t *testing.T) {
	m := newTestManager(t, nil)
	first, second := storageTestDirectory(t), storageTestDirectory(t)
	record, path := storageTestCopy(t, m, "reading")
	m.mu.Lock()
	record.Readers++
	m.mu.Unlock()
	if err := m.SetStorageDirectory("copies", first); err != nil {
		t.Fatal("reader blocked settings", err)
	}
	result, err := m.ClearCache()
	if err != nil || result.Skipped != 1 {
		t.Fatalf("reader cleanup: %+v %v", result, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "downloading", "converting"} {
		m.mu.Lock()
		m.tasks["active"] = &taskRecord{Task: Task{State: state}}
		m.mu.Unlock()
		if err := m.SetStorageDirectory("copies", second); !errors.Is(err, errStorageBusy) {
			t.Fatalf("%s: %v", state, err)
		}
		if m.Status().CopyDir != first {
			t.Fatal("busy update changed selection")
		}
	}
	m.mu.Lock()
	delete(m.tasks, "active")
	m.reserved = 1
	m.mu.Unlock()
	if err := m.SetStorageDirectory("original", second); !errors.Is(err, errStorageBusy) {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.reserved = 0
	record.Readers--
	m.mu.Unlock()
	if err := m.SetStorageDirectory("copies", second); err != nil {
		t.Fatal(err)
	}
	result, err = m.ClearCache()
	if err != nil || result.RemovedBytes != 10 {
		t.Fatalf("old root cleanup: %+v %v", result, err)
	}
}

func TestStorageRejectsMissingReplacedAndLinkedRoots(t *testing.T) {
	m := newTestManager(t, nil)
	parent := storageTestDirectory(t)
	missing := filepath.Join(parent, "missing")
	if err := m.SetStorageDirectory("copies", missing); !errors.Is(err, errStorageUnavailable) {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("selection created missing directory")
	}
	selected := filepath.Join(parent, "selected")
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.SetStorageDirectory("copies", selected); err != nil {
		t.Fatal(err)
	}
	record, path := storageTestCopy(t, m, "copy")
	if err := os.Rename(selected, selected+"-detached"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.newOutputDirectory(); !errors.Is(err, errStorageUnavailable) {
		t.Fatal("missing disk allocation", err)
	}
	if _, err := os.Stat(selected); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing disk recreated")
	}
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.newOutputDirectory(); !errors.Is(err, errStorageUnavailable) {
		t.Fatal("replacement accepted", err)
	}
	if err := m.reserve(1); !errors.Is(err, errStorageUnavailable) {
		t.Fatal("replacement reserved", err)
	}
	if budget := m.outputBudget(16 << 20); budget != 0 {
		t.Fatalf("replacement storage produced output budget %d", budget)
	}
	// A replaced allocation directory is not ours even with a hard link to the old bytes.
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(selected+"-detached", filepath.Base(filepath.Dir(path)), filepath.Base(path)), path); err != nil {
		t.Fatal(err)
	}
	result, err := m.ClearCache()
	if err != nil || result.Skipped != 1 {
		t.Fatalf("replacement cleanup: %+v %v", result, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("replacement bytes deleted", err)
	}
	m.mu.Lock()
	_, f := m.cachedLocked("copy")
	m.mu.Unlock()
	if f != nil {
		f.Close()
		t.Fatal("replacement copy reused", record)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(selected, link); err != nil {
		t.Fatal(err)
	}
	if err := m.SetStorageDirectory("original", link); !errors.Is(err, errStorageUnavailable) {
		t.Fatal("linked root accepted", err)
	}
}

func TestOriginalTaskResumeRemainsBoundToOriginalRoot(t *testing.T) {
	config := storageTestDirectory(t)
	m, err := New(Options{ConfigDir: config, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	first, second := storageTestDirectory(t), storageTestDirectory(t)
	if err := m.SetStorageDirectory("original", first); err != nil {
		t.Fatal(err)
	}
	task := &taskRecord{Task: Task{ID: "held", State: "held"}}
	if dir, err := m.taskOriginalStorage(task); err != nil || dir != first {
		t.Fatalf("new allocation: %s %v", dir, err)
	}
	task.Original = filepath.Join(first, "retained.txt")
	task.Checkpoint = task.Original + ".checkpoint"
	if err := os.WriteFile(task.Original, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := LocalVersion(task.Original)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.tasks[task.ID] = task
	m.originals["old"] = &preparedFile{Path: task.Original, Token: token, Root: task.OriginalRoot}
	m.mu.Unlock()
	if err := m.SetStorageDirectory("original", second); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{ConfigDir: config, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if dir, err := m.taskOriginalStorage(m.tasks["held"]); err != nil || dir != first {
		t.Fatalf("resume redirected: %s %v", dir, err)
	}
	if dir, err := m.taskOriginalStorage(&taskRecord{}); err != nil || dir != second {
		t.Fatalf("new task misplaced: %s %v", dir, err)
	}
	if _, err := m.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(task.Original); err != nil {
		t.Fatal("original deleted", err)
	}
	if err := os.Rename(first, first+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(first, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.taskOriginalStorage(m.tasks["held"]); !errors.Is(err, errStorageUnavailable) {
		t.Fatal("resume accepted replacement", err)
	}
}

func TestLegacyRelativeCachePathsResolveOnlyAgainstConfigRoot(t *testing.T) {
	config := storageTestDirectory(t)
	m, err := New(Options{ConfigDir: config, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	record, path := storageTestCopy(t, m, "legacy")
	m.mu.Lock()
	record.Directory = syncfs.Identity{}
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetStorageDirectory("copies", storageTestDirectory(t)); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{ConfigDir: config, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, f := m.cachedLocked("legacy")
	m.mu.Unlock()
	if f == nil {
		t.Fatal("legacy relative path lost")
	}
	f.Close()
	if _, err := m.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy path not cleaned", err)
	}
}

func TestStorageSelectionSurvivesDisconnectedDiskRestart(t *testing.T) {
	config, parent := storageTestDirectory(t), storageTestDirectory(t)
	selected := filepath.Join(parent, "disk")
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{ConfigDir: config, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetStorageDirectory("original", selected); err != nil {
		t.Fatal(err)
	}
	if err := m.SetStorageDirectory("copies", selected); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(selected, selected+"-old"); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{ConfigDir: config, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if status := m.Status(); status.CopyDir != selected || status.OriginalDir != selected {
		t.Fatalf("fallback after restart: %+v", status)
	}
	if _, err := m.originalStorage(); err == nil {
		t.Fatal("original fallback")
	}
	if _, err := m.newOutputDirectory(); err == nil {
		t.Fatal("copy fallback")
	}
	if _, err := os.Stat(selected); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disk root recreated")
	}
}

func TestLegacyOriginalCheckpointUsesSavedRootNotNewSelection(t *testing.T) {
	config, old := storageTestDirectory(t), storageTestDirectory(t)
	identity, err := syncfs.Identify(old)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{ConfigDir: config, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.originalIdentity, m.originalsDir = identity, old
	m.tasks["legacy"] = &taskRecord{Task: Task{ID: "legacy", State: "held"}, Original: filepath.Join(old, "file"), Checkpoint: filepath.Join(old, "file.checkpoint")}
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{ConfigDir: config, OriginalDir: storageTestDirectory(t), MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.SetStorageDirectory("original", storageTestDirectory(t)); err != nil {
		t.Fatal(err)
	}
	if dir, err := m.taskOriginalStorage(m.tasks["legacy"]); err != nil || dir != old {
		t.Fatalf("legacy resume root: %s %v", dir, err)
	}
	data, err := os.ReadFile(filepath.Join(config, "lan-share", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved persisted
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Tasks["legacy"].OriginalRoot.Path != old {
		t.Fatal("legacy root not persisted")
	}
}

func TestCloudPreparationUsesSelectedRootAndReusesPreviousOriginal(t *testing.T) {
	cloud := newCloud()
	config := storageTestDirectory(t)
	m, err := New(Options{ConfigDir: config, Cloud: cloud, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	space, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	first, second := storageTestDirectory(t), storageTestDirectory(t)
	if err := m.SetStorageDirectory("original", first); err != nil {
		t.Fatal(err)
	}
	task := &taskRecord{Task: Task{ID: "prepare", SpaceID: space.ID, FileID: "file", Kind: "original", State: "queued"}, Version: "version-1", Desktop: true}
	cloud.release <- struct{}{}
	if err := m.runTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(task.Original) != first || task.OriginalRoot.Path != first {
		t.Fatalf("original allocated outside selection: %+v", task)
	}
	if err := m.SetStorageDirectory("original", second); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{ConfigDir: config, Cloud: cloud, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	source, err := m.resolve(context.Background(), space.ID, "file")
	if err != nil || !source.Entry.Ready || source.LocalPath != task.Original {
		t.Fatalf("old original not reused: %+v %v", source, err)
	}
	if _, err := m.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(task.Original); err != nil {
		t.Fatal("cleanup deleted original", err)
	}
}
