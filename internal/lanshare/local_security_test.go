//go:build windows || (darwin && cgo)

package lanshare

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/syncfs"
)

// Model a reference persisted before staging paths were excluded from LAN shares.
func legacyLocalReference(t *testing.T, m *Manager, sid, root, rel string, directory bool) string {
	t.Helper()
	f, err := openAbsolute(filepath.Join(root, filepath.FromSlash(rel)), directory)
	if err != nil {
		t.Fatal(err)
	}
	key, _, _, err := handleIdentity(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	id := m.localID(sid, key, rel)
	m.mu.Lock()
	m.spaces[sid].References[id] = localReference{Relative: rel, Key: key}
	m.mu.Unlock()
	return id
}

func TestLocalStageListingAndLegacyReferences(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	identity, err := syncfs.Identify(root)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := syncfs.EnsureStage(identity, "test-pair")
	if err != nil {
		t.Fatal(err)
	}
	const secret = "unpublished staged bytes"
	for _, rel := range []string{".pkp-sync/chunk.part", "nested/.pkp-sync/chunk.part", ".hidden", ".pkp-sync-my-notes", "nested/owner.json"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]Entry)
	for _, entry := range entries {
		seen[entry.Name] = entry
	}
	if len(seen) != 4 || seen[".hidden"].ID == "" || seen[".pkp-sync-my-notes"].ID == "" || seen["nested"].ID == "" || seen[".pkp-sync"].ID != "" {
		t.Fatalf("incorrect reserved-name filtering: %+v", entries)
	}
	for _, name := range []string{".hidden", ".pkp-sync-my-notes"} {
		f, _, err := m.original(context.Background(), s.ID, seen[name].ID, "")
		if err != nil {
			t.Fatalf("legitimate hidden file %q unavailable: %v", name, err)
		}
		body, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(body) != secret {
			t.Fatalf("legitimate hidden file changed: %q %v", body, err)
		}
	}
	children, err := m.list(context.Background(), s.ID, seen["nested"].ID)
	if err != nil || len(children) != 1 || children[0].Name != "owner.json" {
		t.Fatalf("nested stage exposed or ordinary metadata name hidden: %+v %v", children, err)
	}
	cookie := authenticated(t, m)
	for _, rel := range []string{".pkp-sync", ".pkp-sync/owner.json", ".pkp-sync/chunk.part", "nested/.pkp-sync", "nested/.pkp-sync/chunk.part"} {
		directory := filepath.Base(rel) == ".pkp-sync"
		id := legacyLocalReference(t, m, s.ID, root, rel, directory)
		if _, err := m.resolve(context.Background(), s.ID, id); err == nil {
			t.Fatalf("resolved legacy stage reference %q", rel)
		}
		if f, _, err := m.original(context.Background(), s.ID, id, ""); err == nil {
			f.Close()
			t.Fatalf("opened legacy stage reference %q", rel)
		}
		if directory {
			if _, err := m.list(context.Background(), s.ID, id); err == nil {
				t.Fatalf("listed legacy stage reference %q", rel)
			}
		}
		for _, endpoint := range []string{"/api/file", "/api/content", "/api/content?variant=compatible"} {
			separator := "?"
			if strings.Contains(endpoint, "?") {
				separator = "&"
			}
			w := apiRequest(t, m, http.MethodGet, endpoint+separator+"space="+s.ID+"&id="+id, cookie, nil)
			if w.Code < 400 || strings.Contains(w.Body.String(), secret) {
				t.Fatalf("legacy stage served at %s (%q): %d %s", endpoint, rel, w.Code, w.Body.String())
			}
		}
	}
	if same, err := syncfs.EnsureStage(identity, "test-pair"); err != nil || same != stage {
		t.Fatalf("LAN checks disrupted stage ownership: %q %v", same, err)
	}
	if body, err := os.ReadFile(filepath.Join(stage, "chunk.part")); err != nil || string(body) != secret {
		t.Fatalf("LAN checks touched staged data: %q %v", body, err)
	}
}

func TestLocalStageRootSelectionAndLegacyRoots(t *testing.T) {
	m := newTestManager(t, nil)
	_, root := localSpace(t, m, false)
	for _, rel := range []string{".pkp-sync", ".pkp-sync/sub/deep", "nested/.pkp-sync/sub", ".pkp-sync-my-notes/sub", ".hidden/sub"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		_, err := m.AddSpace(SpaceRequest{Name: "Selected folder", Kind: "local", LocalPath: path})
		internal := strings.Contains("/"+rel+"/", "/.pkp-sync/")
		if (err != nil) != internal {
			t.Fatalf("root %q: internal=%v error=%v", rel, internal, err)
		}
		if !internal {
			continue
		}
		identity, err := syncfs.Identify(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := openAbsolute(identity.Path, true)
		if err != nil {
			t.Fatal(err)
		}
		key, _, volume, err := handleIdentity(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		legacy := spaceRecord{Identity: identity, HandleKey: key, Volume: volume}
		if f, err := openRoot(legacy); err == nil {
			f.Close()
			t.Fatalf("legacy selected staging root accepted: %q", rel)
		}
	}
	alias := filepath.Join(root, "stage-alias")
	if os.Symlink(filepath.Join(root, ".pkp-sync"), alias) == nil {
		if _, err := m.AddSpace(SpaceRequest{Name: "Ancestor alias", Kind: "local", LocalPath: filepath.Join(alias, "sub")}); err == nil {
			t.Fatal("canonicalized staging ancestor selected as root")
		}
	}
	identity, err := syncfs.Identify(root)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.CaseSensitive {
		if _, err := m.AddSpace(SpaceRequest{Name: "Case alias", Kind: "local", LocalPath: filepath.Join(root, ".PKP-SYNC", "sub")}); err == nil {
			t.Fatal("case-insensitive staging alias selected as root")
		}
	}
}

func TestLocalStageRenamesInvalidateHandlesAndReferences(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("initial listing: %+v %v", entries, err)
	}
	path := filepath.Join(root, "inside.txt")
	f, err := openAbsolute(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stage := filepath.Join(root, ".pkp-sync")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(stage, "chunk.part")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalHandle(f, m.spaces[s.ID].Identity); err == nil {
		t.Fatal("already opened file renamed into staging remained authorized")
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if file, _, err := m.original(context.Background(), s.ID, entries[0].ID, ""); err == nil {
		file.Close()
		t.Fatal("old reference authorized a replacement after moving original to staging")
	}
	if body, err := os.ReadFile(moved); err != nil || string(body) != "authorized bytes" {
		t.Fatalf("stage rename check changed source: %q %v", body, err)
	}
}
