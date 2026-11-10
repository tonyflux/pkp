package foldersync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func completedFixture(t *testing.T) (*Manager, *memoryCloud, Pair, Node, string) {
	t.Helper()
	m, backend, root := newTestManager(t)
	body := []byte("verified sync original")
	node := Node{ID: "film", ParentID: "root", Name: "film.bin", Size: int64(len(body)), Version: "cloud-v1"}
	backend.nodes[node.ID], backend.bytes[node.ID] = node, body
	path := filepath.Join(root, node.Name)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	pair := pairFromPreview(t, m, previewConfig(root))
	return m, backend, pair, node, path
}

func TestCompletedFilesRequireAccountOwnerAndCurrentCloudIdentity(t *testing.T) {
	m, _, _, node, _ := completedFixture(t)
	files := m.CompletedFiles("account", "server-owner", node)
	if len(files) != 1 || files[0].RelativePath != "film.bin" || len(files[0].Hash) != 64 {
		t.Fatalf("completed original missing: %+v", files)
	}
	for _, query := range []struct {
		name, account, owner string
		node                 Node
	}{
		{"different account", "other-account", "server-owner", node},
		{"rebound identity", "account", "other-owner", node},
		{"missing account", "", "server-owner", node},
		{"missing identity", "account", "", node},
		{"new cloud version", "account", "server-owner", func() Node { n := node; n.Version = "cloud-v2"; return n }()},
		{"different cloud file", "account", "server-owner", func() Node { n := node; n.ID = "another-file"; return n }()},
		{"moved cloud file", "account", "server-owner", func() Node { n := node; n.ParentID = "private"; return n }()},
		{"incomplete cloud file", "account", "server-owner", func() Node { n := node; n.Phase = "PHASE_TYPE_RUNNING"; return n }()},
	} {
		t.Run(query.name, func(t *testing.T) {
			if got := m.CompletedFiles(query.account, query.owner, query.node); len(got) != 0 {
				t.Fatalf("unproven original offered: %+v", got)
			}
		})
	}
}

func TestCompletedFilesFollowBaselineCommitsWithoutControllingSync(t *testing.T) {
	m, backend, pair, node, local := completedFixture(t)
	if err := m.Pause(context.Background(), pair.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := m.CompletedFiles("account", "server-owner", node); len(got) != 1 || !m.List()[0].Paused {
		t.Fatal("reading a completed original changed or required unpausing its relationship")
	}
	updated := node
	updated.Version = "cloud-v2"
	backend.nodes[node.ID] = updated
	if err := m.Pause(context.Background(), pair.ID, false); err != nil {
		t.Fatal(err)
	}
	m.reconcile(m.pairs[pair.ID])
	if got := m.CompletedFiles("account", "server-owner", node); len(got) != 0 {
		t.Fatal("previous baseline generation remained reusable")
	}
	if got := m.CompletedFiles("account", "server-owner", updated); len(got) != 1 {
		t.Fatal("new committed baseline was not indexed")
	}
	if err := m.Remove(context.Background(), pair.ID); err != nil {
		t.Fatal(err)
	}
	if got := m.CompletedFiles("account", "server-owner", updated); len(got) != 0 {
		t.Fatal("removed relationship still supplied candidates")
	}
	if body, err := os.ReadFile(local); err != nil || string(body) != "verified sync original" {
		t.Fatalf("relationship removal changed the original: %q %v", body, err)
	}
}

func TestCompletedFilesExcludeUncommittedAndInternalPaths(t *testing.T) {
	m, _, pair, node, _ := completedFixture(t)
	r := m.pairs[pair.ID]
	m.mu.Lock()
	r.record.Operations = []*operation{{ID: "pending", Path: "film.bin", Stage: "done"}}
	err := m.saveBaselineLocked(r)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := m.CompletedFiles("account", "server-owner", node); len(got) != 0 {
		t.Fatal("uncommitted publication was offered as an original")
	}
	m.mu.Lock()
	r.record.Operations = nil
	b := r.record.Baseline["film.bin"]
	local, cloud := *b.Local, *b.Cloud
	local.Path, cloud.Path = ".pkp-sync/staged.bin", ".pkp-sync/staged.bin"
	delete(r.record.Baseline, "film.bin")
	r.record.Baseline[local.Path] = baseline{Local: &local, Cloud: &cloud}
	err = m.saveBaselineLocked(r)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := m.CompletedFiles("account", "server-owner", node); len(got) != 0 {
		t.Fatal("staging bytes were offered by a baseline")
	}
}
