package syncfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testRoot(t *testing.T) Identity {
	t.Helper()
	id, err := Identify(t.TempDir())
	if errors.Is(err, ErrUnsupported) {
		t.Skip("filesystem is explicitly unsupported")
	}
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestOverlapsUsesComponentsAndAliases(t *testing.T) {
	a := Identity{Path: "/资料/É", Volume: "one", File: "1", CaseSensitive: false}
	for _, path := range []string{"/资料/e\u0301/项目", "/资料/é", "/资料"} {
		b := Identity{Path: path, Volume: "one", File: "2", CaseSensitive: false}
		if !Overlaps(a, b) || !Overlaps(b, a) {
			t.Fatalf("missed overlap %q", path)
		}
	}
	if Overlaps(a, Identity{Path: "/资料/É集", CaseSensitive: false}) {
		t.Fatal("string prefix is not ancestry")
	}
	if !Overlaps(a, Identity{Path: "/alias", Volume: "one", File: "1"}) {
		t.Fatal("same directory identity bypassed overlap")
	}
	if Overlaps(Identity{Path: "/A", CaseSensitive: true}, Identity{Path: "/a", CaseSensitive: true}) {
		t.Fatal("case-sensitive paths merged")
	}
	if !Overlaps(Identity{Path: "/"}, Identity{Path: "/nested"}) {
		t.Fatal("filesystem root is ancestor")
	}
}

func TestWindowsNamesAreNotSilentlySanitized(t *testing.T) {
	for _, name := range []string{"CON", "con.txt", "AUX.data", "COM1", "LPT9.log", "COM¹", "bad.", "bad ", "a:b", "a\\b", "a?b", "a\x01b", strings.Repeat("界", 256)} {
		if NameError(name, "windows") == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, name := range []string{".env", "CONSOLE", "LPT10", strings.Repeat("界", 200)} {
		if err := NameError(name, "windows"); err != nil {
			t.Fatalf("rejected legal name %q: %v", name, err)
		}
	}
	id := Identity{CaseSensitive: false, UnicodeSensitive: false}
	if CollisionKey("É.txt", id) != CollisionKey("e\u0301.TXT", id) {
		t.Fatal("Unicode/case collision missed")
	}
}

func TestScanRetainsDotfilesEmptyDirectoriesAndSkipsLinks(t *testing.T) {
	root := testRoot(t)
	put(t, filepath.Join(root.Path, ".env"), "SECRET=x")
	put(t, filepath.Join(root.Path, "editing.swp"), "temporary")
	put(t, filepath.Join(root.Path, ".DS_Store"), "control")
	if err := os.Mkdir(filepath.Join(root.Path, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	put(t, filepath.Join(outside, "never-read"), "outside")
	linked := os.Symlink(outside, filepath.Join(root.Path, "link")) == nil
	entries, skipped, err := Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Entry{}
	for _, entry := range entries {
		found[entry.Path] = entry
		if strings.Contains(entry.Path, "never-read") {
			t.Fatal("followed symlink")
		}
	}
	if len(found) != 2 || found[".env"].Hash == "" || !found["empty"].Directory {
		t.Fatalf("wrong snapshot: %+v", entries)
	}
	if len(skipped) < 2 || (linked && len(skipped) != 3) {
		t.Fatalf("wrong skips: %+v", skipped)
	}
	cached, _, err := Scan(context.Background(), root, found)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 2 {
		t.Fatal(cached)
	}
	before := found[".env"]
	put(t, filepath.Join(root.Path, ".env"), "SECRET=y")
	when, err := time.Parse(time.RFC3339Nano, before.ModTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(filepath.Join(root.Path, ".env"), when, when); err != nil {
		t.Fatal(err)
	}
	updated, _, err := Scan(context.Background(), root, found)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range updated {
		if entry.Path == ".env" && entry.Hash == before.Hash {
			t.Fatal("restored mtime reused stale hash")
		}
	}
}

func TestStageNeverAdoptsUserDirectory(t *testing.T) {
	root := testRoot(t)
	path := filepath.Join(root.Path, ".pkp-sync")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(path, "mine"), "must survive")
	if _, err := EnsureStage(root, "pair"); !errors.Is(err, ErrStageOccupied) {
		t.Fatalf("adopted user directory: %v", err)
	}
	if _, _, err := Scan(context.Background(), root, nil); !errors.Is(err, ErrStageOccupied) {
		t.Fatalf("silently excluded user directory: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(path, "mine"))
	if err != nil || string(data) != "must survive" {
		t.Fatal("user content changed")
	}
}

func TestOwnedStageBoundToPairAndRoot(t *testing.T) {
	root := testRoot(t)
	path, err := EnsureStage(root, "pair-one")
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(path, "operation.part"), "incomplete")
	if _, err = EnsureStage(root, "pair-two"); !errors.Is(err, ErrStageOccupied) {
		t.Fatalf("pair ownership ignored: %v", err)
	}
	entries, _, err := Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging fed into sync: %+v", entries)
	}
	ownerPath := filepath.Join(path, markerName)
	data, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	put(t, ownerPath, strings.ReplaceAll(string(data), root.File, "different-root"))
	if _, _, err = Scan(context.Background(), root, nil); !errors.Is(err, ErrStageOccupied) {
		t.Fatalf("root marker not checked: %v", err)
	}
}

func TestRelativePathsAndReplacedRootsBlockWrites(t *testing.T) {
	root := testRoot(t)
	for _, rel := range []string{"", "../outside", "a/../b", "/outside", "a//b", "a/./b", "a\\..\\b"} {
		if _, err := ValidateRelative(root, rel); err == nil {
			t.Fatalf("accepted %q", rel)
		}
	}
	outside := t.TempDir()
	if os.Symlink(outside, filepath.Join(root.Path, "link")) == nil {
		if _, err := ValidateRelative(root, "link/new"); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("accepted linked parent: %v", err)
		}
		if _, err := Identify(filepath.Join(root.Path, "link")); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("accepted linked root: %v", err)
		}
	}
	changed := root
	changed.Volume = "different-volume"
	if _, err := ValidateRelative(changed, "new"); !errors.Is(err, ErrRootChanged) {
		t.Fatalf("different mounted volume accepted: %v", err)
	}
	changed = root
	changed.File = "replaced-directory"
	if err := Validate(changed); !errors.Is(err, ErrRootChanged) {
		t.Fatalf("replacement accepted: %v", err)
	}
}

func TestDarwinPersistedRootSurvivesDeviceRenumbering(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin device numbers are mount-session identifiers")
	}
	root := testRoot(t)
	device, inode, ok := strings.Cut(root.File, ":")
	dev, err := strconv.ParseUint(device, 10, 64)
	if !ok || err != nil {
		t.Fatalf("unexpected Darwin identity %q", root.File)
	}
	ino, err := strconv.ParseUint(inode, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	legacy := root
	legacy.File = strconv.FormatUint(dev+1, 10) + ":" + inode
	if err := Validate(legacy); err != nil {
		t.Fatalf("same volume UUID and inode refused after remount: %v", err)
	}
	alias := legacy
	alias.Path = filepath.Join(filepath.Dir(root.Path), "unrelated-alias")
	if !Overlaps(root, alias) {
		t.Fatal("old device number hid the same root from overlap detection")
	}
	stage, err := EnsureStage(legacy, "persisted-pair")
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(stage, "retained.part"), "retained partial bytes")
	if _, err := EnsureStage(root, "persisted-pair"); err != nil {
		t.Fatalf("persisted stage owner rejected the unchanged root: %v", err)
	}
	if _, err := EnsureStage(root, "different-pair"); !errors.Is(err, ErrStageOccupied) {
		t.Fatalf("device renumbering weakened pair ownership: %v", err)
	}
	for _, changed := range []Identity{
		{Path: root.Path, Volume: "another-volume", File: legacy.File},
		{Path: root.Path, Volume: root.Volume, File: device + ":" + strconv.FormatUint(ino+1, 10)},
		{Path: root.Path, Volume: root.Volume, File: "not-a-device:" + inode},
		{Path: root.Path, Volume: root.Volume, File: legacy.File + ":extra"},
	} {
		changed.CaseSensitive, changed.UnicodeSensitive = root.CaseSensitive, root.UnicodeSensitive
		if err := Validate(changed); !errors.Is(err, ErrRootChanged) {
			t.Fatalf("different or malformed identity accepted: %+v %v", changed, err)
		}
	}
	old := root.Path + "-original"
	if err := os.Rename(root.Path, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(old) })
	if err := os.Mkdir(root.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Validate(legacy); !errors.Is(err, ErrRootChanged) {
		t.Fatalf("replacement root accepted: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(old, ".pkp-sync", "retained.part")); err != nil || string(got) != "retained partial bytes" {
		t.Fatalf("root validation changed retained checkpoint: %q %v", got, err)
	}
}

func TestRenameNeverClobbersExistingFileOrDirectory(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from")
	to := filepath.Join(dir, "to")
	put(t, from, "new")
	put(t, to, "old")
	if err := RenameNoReplace(from, to); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected existing target: %v", err)
	}
	data, err := os.ReadFile(to)
	if err != nil || string(data) != "old" {
		t.Fatal("clobbered target")
	}
	if _, err = os.Stat(from); err != nil {
		t.Fatal("source lost")
	}
	targetDir := filepath.Join(dir, "directory")
	if err = os.Mkdir(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = RenameNoReplace(from, targetDir); err == nil {
		t.Fatal("replaced directory")
	}
	if err = RenameNoReplace(from, filepath.Join(dir, "free")); err != nil {
		t.Fatal(err)
	}
}

type mutationContext struct {
	context.Context
	once   sync.Once
	mutate func()
}

func (ctx *mutationContext) Err() error { ctx.once.Do(ctx.mutate); return ctx.Context.Err() }

func TestHashRejectsConcurrentSameSizeWrite(t *testing.T) {
	root := testRoot(t)
	path := filepath.Join(root.Path, "changing")
	put(t, path, strings.Repeat("a", 512*1024))
	ctx := &mutationContext{Context: context.Background(), mutate: func() { put(t, path, strings.Repeat("b", 512*1024)) }}
	if _, err := Hash(ctx, path); !errors.Is(err, ErrUnstable) {
		t.Fatalf("accepted changing file: %v", err)
	}
}

func TestScanRefusesPartialSnapshotWhenDirectoryChanges(t *testing.T) {
	root := testRoot(t)
	put(t, filepath.Join(root.Path, "first"), "content")
	var calls int
	ctx := &scanMutationContext{Context: context.Background(), run: func() {
		calls++
		if calls == 2 {
			put(t, filepath.Join(root.Path, "late"), "arrived after enumeration")
		}
	}}
	entries, _, err := Scan(ctx, root, nil)
	if !errors.Is(err, ErrUnstable) || entries != nil {
		t.Fatalf("published partial scan: %+v %v", entries, err)
	}
}

type scanMutationContext struct {
	context.Context
	run func()
}

func (ctx *scanMutationContext) Err() error { ctx.run(); return ctx.Context.Err() }

func TestArbitrarySyncLookingUserNameIsNotExcluded(t *testing.T) {
	root := testRoot(t)
	put(t, filepath.Join(root.Path, ".pkp-sync-my-notes"), "user data")
	entries, _, err := Scan(context.Background(), root, nil)
	if err != nil || len(entries) != 1 || entries[0].Name != ".pkp-sync-my-notes" {
		t.Fatalf("hid user file: %+v %v", entries, err)
	}
}

func TestIsStagePathUsesExactComponents(t *testing.T) {
	for _, tc := range []struct {
		path      string
		sensitive bool
		internal  bool
	}{
		{".pkp-sync", true, true},
		{"root/.pkp-sync/owner.json", true, true},
		{"root/.PKP-SYNC/chunk", false, true},
		{"root/.PKP-SYNC/chunk", true, false},
		{".pkp-sync-my-notes", false, false},
		{"root/.pkp-sync-my-notes/.hidden", false, false},
		{"root/.hidden", false, false},
		{"root/owner.json", false, false},
	} {
		path := filepath.FromSlash(tc.path)
		identity := Identity{CaseSensitive: tc.sensitive}
		if got := IsStagePath(path, identity); got != tc.internal {
			t.Errorf("IsStagePath(%q, sensitive=%v) = %v", path, tc.sensitive, got)
		}
		absolute := filepath.Join(string(filepath.Separator), path)
		if got := IsStagePath(absolute, identity); got != tc.internal {
			t.Errorf("IsStagePath(%q, sensitive=%v) = %v", absolute, tc.sensitive, got)
		}
	}
}

func TestFileIdentityDetectsReplacedPartial(t *testing.T) {
	root := testRoot(t)
	path := filepath.Join(root.Path, "partial")
	put(t, path, "one")
	before, err := FileIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root.Path, "replacement")
	put(t, replacement, "two")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = RenameNoReplace(replacement, path); err != nil {
		t.Fatal(err)
	}
	after, err := FileIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("replaced partial kept identity")
	}
}

func TestInspectAbsenceDoesNotHideDisconnectedRoot(t *testing.T) {
	root := testRoot(t)
	entry, err := Inspect(context.Background(), root, "missing")
	if err != nil || entry != nil {
		t.Fatalf("missing candidate: %+v %v", entry, err)
	}
	changed := root
	changed.Volume = "disconnected"
	if _, err = Inspect(context.Background(), changed, "missing"); !errors.Is(err, ErrRootChanged) {
		t.Fatalf("disconnected root looked empty: %v", err)
	}
}

func TestUnicodeAliasesBlockOnlyAffectedPaths(t *testing.T) {
	root := testRoot(t)
	put(t, filepath.Join(root.Path, "é"), "one")
	put(t, filepath.Join(root.Path, "safe"), "safe")
	alternate := filepath.Join(root.Path, "e\u0301")
	f, err := os.OpenFile(alternate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		t.Skip("volume already rejects canonical aliases")
	}
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("two")
	f.Close()
	entries, skipped, err := Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || len(skipped) != 2 {
		t.Fatalf("safe sibling lost or aliases unblocked: %+v %+v", entries, skipped)
	}
}

func TestNativeWatchStopsIdempotently(t *testing.T) {
	root := testRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	stop, err := Watch(ctx, root, func() {})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	stop()
	stop()
}
