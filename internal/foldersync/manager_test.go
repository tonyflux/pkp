package foldersync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tonyflux/pkp/internal/syncfs"
)

type memoryCloud struct {
	nodes      map[string]Node
	bytes      map[string][]byte
	writes     int
	failList   error
	loseUpload bool
}

func (b *memoryCloud) AccountID() string { return "server-owner" }
func (b *memoryCloud) Root(context.Context, string) (Node, error) {
	return Node{ID: "root", Name: "root", Path: "root", Directory: true}, nil
}
func (b *memoryCloud) List(_ context.Context, id string) ([]Node, error) {
	if b.failList != nil {
		return nil, b.failList
	}
	out := []Node{}
	for _, n := range b.nodes {
		if n.ParentID == id {
			out = append(out, n)
		}
	}
	return out, nil
}
func (b *memoryCloud) Hash(_ context.Context, n Node) (string, error) {
	v, ok := b.bytes[n.ID]
	if !ok {
		return "", ErrNotFound
	}
	h := sha256.Sum256(v)
	return hex.EncodeToString(h[:]), nil
}
func (b *memoryCloud) Mkdir(_ context.Context, parent, name, id string) (Node, error) {
	b.writes++
	n := Node{ID: id, ParentID: parent, Name: name, Directory: true}
	b.nodes[id] = n
	return n, nil
}
func (b *memoryCloud) Upload(_ context.Context, r UploadRequest, _ func(int64, int64)) (Node, error) {
	b.writes++
	if b.loseUpload {
		return Node{}, ErrUnknown
	}
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return Node{}, err
	}
	n := Node{ID: r.ID, ParentID: r.ParentID, Name: r.Name, Size: int64(len(data)), Version: r.ID}
	b.nodes[n.ID] = n
	b.bytes[n.ID] = data
	return n, nil
}
func (b *memoryCloud) Download(_ context.Context, n Node, p, _, _ string, _ func(int64, int64)) error {
	b.writes++
	return os.WriteFile(p, b.bytes[n.ID], 0600)
}
func (b *memoryCloud) Rename(_ context.Context, n Node, name, _ string) (Node, error) {
	b.writes++
	n.Name = name
	n.Version += "r"
	b.nodes[n.ID] = n
	return n, nil
}
func (b *memoryCloud) Move(_ context.Context, n Node, parent, name, _ string) (Node, error) {
	b.writes++
	n.ParentID = parent
	n.Name = name
	b.nodes[n.ID] = n
	return n, nil
}
func (b *memoryCloud) Trash(_ context.Context, n Node, _ string) error {
	b.writes++
	delete(b.nodes, n.ID)
	return nil
}
func (b *memoryCloud) Trashed(_ context.Context, id string) (bool, error) {
	_, ok := b.nodes[id]
	return !ok, nil
}
func (b *memoryCloud) Get(_ context.Context, id string) (Node, error) {
	n, ok := b.nodes[id]
	if !ok {
		return Node{}, ErrNotFound
	}
	return n, nil
}
func newTestManager(t *testing.T) (*Manager, *memoryCloud, string) {
	t.Helper()
	root := t.TempDir()
	cloud := &memoryCloud{nodes: map[string]Node{}, bytes: map[string][]byte{}}
	m, err := New(Options{Dir: t.TempDir(), Backend: func(string) (Backend, error) { return cloud, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return m, cloud, root
}
func previewConfig(root string) Config {
	return Config{Account: "account", LocalPath: root, CloudID: "root", Mode: Bidirectional, Deletes: map[string]bool{Bidirectional: true}}
}
func pairFromPreview(t *testing.T, m *Manager, c Config) Pair {
	t.Helper()
	p, err := m.Preview(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	pair, changed, err := m.Start(context.Background(), p.ID)
	if err != nil || changed != nil {
		t.Fatalf("start: %v %+v", err, changed)
	}
	m.ctx = context.Background()
	return pair
}

func TestPreviewReadsOnlyAndStartRejectsStalePlan(t *testing.T) {
	m, b, root := newTestManager(t)
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := m.Preview(context.Background(), previewConfig(root))
	if err != nil {
		t.Fatal(err)
	}
	if b.writes != 0 || len(m.List()) != 0 {
		t.Fatal("preview performed writes")
	}
	if _, err = os.Lstat(filepath.Join(root, ".pkp-sync")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview created staging")
	}
	if err = os.WriteFile(filepath.Join(root, "x"), []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	_, changed, err := m.Start(context.Background(), p.ID)
	if err != nil || changed == nil || len(m.List()) != 0 || b.writes != 0 {
		t.Fatalf("stale plan executed: %v %+v", err, changed)
	}
}
func TestSuccessfulFileCommitsBaselineAndNoNormalHistory(t *testing.T) {
	m, b, root := newTestManager(t)
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("actual bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	pair := pairFromPreview(t, m, previewConfig(root))
	m.reconcile(m.pairs[pair.ID])
	p := m.List()[0]
	if p.State != "idle" || p.LastSuccess == "" || len(p.Activities) != 1 {
		t.Fatalf("untrue completion: %+v", p)
	}
	if len(b.nodes) != 1 || m.pairs[p.ID].record.Baseline["x"].Cloud == nil {
		t.Fatal("verified baseline missing")
	}
	reloaded, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.List()[0].State == "repair" {
		t.Fatal("committed baseline not reloadable")
	}
}
func TestUnknownUploadIsNotBlindlyCreatedAgain(t *testing.T) {
	m, b, root := newTestManager(t)
	b.loseUpload = true
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	pair := pairFromPreview(t, m, previewConfig(root))
	r := m.pairs[pair.ID]
	m.reconcile(r)
	first := b.writes
	if r.record.Pair.State != "unknown" {
		t.Fatalf("unknown hidden: %+v", r.record.Pair)
	}
	m.reconcile(r)
	if b.writes != first {
		t.Fatal("unknown creation retried without proof")
	}
}
func TestCorruptBaselineSurvivesAsRepairWithoutWrites(t *testing.T) {
	m, b, root := newTestManager(t)
	pair := pairFromPreview(t, m, previewConfig(root))
	r := m.pairs[pair.ID]
	p := filepath.Join(m.opts.Dir, pair.ID, "baseline-"+r.record.Version+".json")
	if err := os.WriteFile(p, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.List()) != 1 || loaded.List()[0].State != "repair" {
		t.Fatal("corrupt baseline erased pair")
	}
	loaded.ctx = context.Background()
	loaded.reconcile(loaded.pairs[pair.ID])
	if b.writes != 0 {
		t.Fatal("corrupt baseline allowed writes")
	}
}
func TestIncompleteListingNeverDeletesTrackedFiles(t *testing.T) {
	m, b, root := newTestManager(t)
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	pair := pairFromPreview(t, m, previewConfig(root))
	r := m.pairs[pair.ID]
	m.reconcile(r)
	if err := os.Remove(filepath.Join(root, "x")); err != nil {
		t.Fatal(err)
	}
	before := b.writes
	b.failList = ErrOffline
	m.reconcile(r)
	if b.writes != before || r.record.Pair.State != "offline" {
		t.Fatal("partial listing became deletion")
	}
}
func TestPausedPairAndRemoveRetainData(t *testing.T) {
	m, b, root := newTestManager(t)
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	pair := pairFromPreview(t, m, previewConfig(root))
	if err := m.Pause(context.Background(), pair.ID, true); err != nil {
		t.Fatal(err)
	}
	m.reconcile(m.pairs[pair.ID])
	if b.writes != 0 {
		t.Fatal("manual pause dispatched work")
	}
	if err := m.Remove(context.Background(), pair.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(filepath.Join(root, "x")); err != nil {
		t.Fatal("remove deleted user's file")
	}
	if len(m.List()) != 0 {
		t.Fatal("relationship retained")
	}
}
func TestDifferentDiskIdentityStopsWrites(t *testing.T) {
	m, b, root := newTestManager(t)
	pair := pairFromPreview(t, m, previewConfig(root))
	r := m.pairs[pair.ID]
	r.record.Identity.Volume = "different"
	m.reconcile(r)
	if b.writes != 0 || r.record.Pair.State != "repair" {
		t.Fatal("replaced disk accepted")
	}
}
func TestLocalRootOverlapKeepsPausedOwnership(t *testing.T) {
	m, _, root := newTestManager(t)
	pair := pairFromPreview(t, m, previewConfig(root))
	m.pairs[pair.ID].record.Pair.Paused = true
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := m.Preview(context.Background(), previewConfig(child))
	if err == nil {
		t.Fatal("paused pair released root ownership")
	}
}
func TestCompletedDownloadStageRecoversWithoutSecondTransfer(t *testing.T) {
	m, b, root := newTestManager(t)
	pair := pairFromPreview(t, m, previewConfig(root))
	r := m.pairs[pair.ID]
	n := Node{ID: "cloud", ParentID: "root", Name: "x", Path: "x", Size: 1}
	b.nodes[n.ID] = n
	b.bytes[n.ID] = []byte("x")
	n.Hash, _ = b.Hash(context.Background(), n)
	o := &operation{ID: uid(), Action: "download", Direction: "download", Path: "x", Cloud: new(n), Stage: "download-intent", ConfigVersion: configProof(r.record.Pair.Config)}
	r.record.Operations = append(r.record.Operations, o)
	stage, err := syncfs.EnsureStage(r.record.Identity, pair.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, o.ID+".part"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.execute(context.Background(), r, b, o); err != nil {
		t.Fatal(err)
	}
	if b.writes != 0 {
		t.Fatal("completed staged download transferred twice")
	}
	if data, err := os.ReadFile(filepath.Join(root, "x")); err != nil || string(data) != "x" {
		t.Fatal("staged content not installed")
	}
}

func TestRemoveKeepsUnknownWriteEvidenceAndRootOwnership(t *testing.T) {
	m, cloud, root := newTestManager(t)
	cloud.loseUpload = true
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	pair := pairFromPreview(t, m, previewConfig(root))
	r := m.pairs[pair.ID]
	m.reconcile(r)
	writes := cloud.writes
	if err := m.Remove(context.Background(), pair.ID); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown accepted write was discarded: %v", err)
	}
	if len(m.List()) != 1 || len(r.record.Operations) != 1 || cloud.writes != writes {
		t.Fatal("removal lost evidence or blindly retried an accepted create")
	}
	if got, err := os.ReadFile(filepath.Join(root, "x")); err != nil || string(got) != "original" {
		t.Fatal("removal changed source content")
	}
}

func TestMetadataChangeSchedulesReadOnlyReconciliation(t *testing.T) {
	m, cloud, root := newTestManager(t)
	pair := pairFromPreview(t, m, previewConfig(root))
	r := m.pairs[pair.ID]
	cloud.failList = ErrChanged
	m.reconcile(r)
	if r.record.Pair.State != "scanning" || cloud.writes != 0 {
		t.Fatalf("changed snapshot should be rechecked without writes: %+v", r.record.Pair)
	}
	select {
	case <-r.hints:
	default:
		t.Fatal("changed snapshot was not scheduled for fresh reconciliation")
	}
}

func TestCloudMetadataFinalizationDoesNotInvalidateVerifiedContent(t *testing.T) {
	before := file("document", "stable-id", "a")
	before.ParentID, before.Version, before.Modified = "root", "placeholder-hash", "before"
	after := before
	after.Version, after.Modified = "final-gcid", "after"
	if !cloudProof(&before, &after) {
		t.Fatal("same verified bytes and identity rejected because metadata finalized")
	}
	after.Hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if cloudProof(&before, &after) {
		t.Fatal("different actual content accepted")
	}
}

func TestReadOnlyHashCacheDoesNotCreateOrphanRelationship(t *testing.T) {
	m, _, _ := newTestManager(t)
	if err := os.Mkdir(filepath.Join(m.opts.Dir, "checks"), 0700); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(m.opts)
	if err != nil || len(loaded.List()) != 0 {
		t.Fatalf("read-only hash cache prevented clean startup: %v", err)
	}
}
