package index

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeLister scripts ListFiles answers. Whole-tree requests are keyed by
// page token; trash listings (filters contain "trashed") by the trash key.
type fakeLister struct {
	mu    sync.Mutex
	pages map[string]*drive.FileList // key: page token, or "trash:"+token
	errs  map[string]error
	calls []drive.ListFilesParams
}

func (f *fakeLister) ListFiles(_ context.Context, p drive.ListFilesParams) (*drive.FileList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	key := p.PageToken
	if p.Filters != nil {
		if _, ok := p.Filters["trashed"]; ok {
			key = "trash:" + p.PageToken
		}
	}
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	page, ok := f.pages[key]
	if !ok {
		return nil, errors.New("fake: unexpected page " + key)
	}
	return page, nil
}

func (f *fakeLister) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func version(destroy string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"last_destroy_time":"` + destroy + `"}`))
}

func df(id, parent, name string, trashed bool) drive.File {
	f := drive.File{Kind: drive.KindFile, ID: id, ParentID: parent, Name: name, Trashed: trashed, ModifiedTime: "2026-01-01T00:00:00+08:00"}
	if trashed {
		// One purge time for the whole fixture: a trashed child of a trashed
		// parent reads as inherited, as on the server.
		f.DeleteTime = "2026-01-16T00:00:00+08:00"
	}
	return f
}

func rowOf(f drive.File) Row {
	return Row{ID: f.ID, ParentID: f.ParentID, Kind: f.Kind, Name: f.Name, Trashed: f.Trashed, Starred: f.Starred,
		DeleteTime: f.DeleteTime, ModifiedTime: f.ModifiedTime, Category: "other"}
}

func newSyncer(t *testing.T, lister Lister) (*Syncer, *Store, *[]Progress) {
	t.Helper()
	store := openTemp(t)
	var seen []Progress
	var mu sync.Mutex
	s := NewSyncer(store, lister, rowOf, func(p Progress) {
		mu.Lock()
		seen = append(seen, p)
		mu.Unlock()
	}, quiet())
	return s, store, &seen
}

func TestFirstSyncWalksEverythingAndStoresCursor(t *testing.T) {
	lister := &fakeLister{pages: map[string]*drive.FileList{
		"":       {Files: []drive.File{df("a", "", "a", false), df("b", "", "b", false)}, NextPageToken: "p2", SyncTime: "T1", Version: version("D0")},
		"p2":     {Files: []drive.File{df("c", "a", "c", false)}, NextPageToken: "", SyncTime: "T2", Version: version("D0")},
		"trash:": {},
	}}
	s, store, progress := newSyncer(t, lister)
	if p := s.Progress(); p.Phase != "idle" || p.Ready {
		t.Fatalf("fresh progress = %+v", s.Progress())
	}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n, _ := store.Count(); n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
	for key, want := range map[string]string{MetaCursor: "T2", MetaDestroy: "D0"} {
		if got, _ := store.Meta(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got, _ := store.Meta(MetaFullAt); got == "" {
		t.Error("full_at not recorded")
	}
	if p := s.Progress(); !p.Ready || p.Phase != "idle" || p.Count != 3 || p.Error != "" {
		t.Errorf("progress after full = %+v", p)
	}
	if lister.calls[0].ParentID != drive.WholeTree || lister.calls[0].PageToken != "" || lister.calls[0].Filters != nil {
		t.Errorf("first request = %+v, want whole tree, no filters", lister.calls[0])
	}
	if lister.calls[0].ThumbnailSize != drive.ThumbnailMedium {
		t.Error("thumbnails are only returned with thumbnail_size set")
	}
	sawFull := false
	for _, p := range *progress {
		if p.Phase == "full" && p.Done == 2 {
			sawFull = true
		}
	}
	if !sawFull {
		t.Errorf("no full-phase progress with done=2 among %+v", *progress)
	}
}

// An incremental pass is one request when nothing touched the bin, and one
// more (the bin relist) when it did: a destroyed entry keeps appearing in
// the feed as trashed, so the bin is the only place to learn it is gone.
func TestIncrementalResumesAtCursorAndTracksTrash(t *testing.T) {
	lister := &fakeLister{pages: map[string]*drive.FileList{
		"":       {Files: []drive.File{df("a", "", "a", false), df("b", "", "b", false)}, SyncTime: "T2", Version: version("D0")},
		"trash:": {},
	}}
	s, store, _ := newSyncer(t, lister)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Quiet pass: nothing trashed, destroy unchanged.
	calls := lister.count()
	lister.pages[drive.SyncCursor("T2")] = &drive.FileList{Files: []drive.File{df("a", "", "a2", false)}, SyncTime: "T2b", Version: version("D0")}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatalf("quiet incremental: %v", err)
	}
	if got := lister.count() - calls; got != 1 {
		t.Errorf("quiet incremental made %d requests, want 1", got)
	}
	last := lister.calls[len(lister.calls)-1]
	if last.PageToken != drive.SyncCursor("T2") || last.ParentID != drive.WholeTree || last.Filters != nil {
		t.Errorf("incremental request = %+v", last)
	}
	if a, _, _ := store.Get("a"); a.Name != "a2" {
		t.Errorf("a = %+v, want the renamed row", a)
	}

	// A trashed row in the feed triggers the bin relist.
	calls = lister.count()
	lister.pages[drive.SyncCursor("T2b")] = &drive.FileList{Files: []drive.File{df("b", "", "b", true)}, SyncTime: "T3", Version: version("D0")}
	lister.pages["trash:"] = &drive.FileList{Files: []drive.File{df("b", "", "b", true)}}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if got := lister.count() - calls; got != 2 {
		t.Errorf("incremental with a trashed row made %d requests, want 2 (feed + bin)", got)
	}
	b, _, _ := store.Get("b")
	if !b.Trashed {
		t.Error("b should be trashed after the incremental page")
	}
	if _, ok, _ := store.Get("a"); !ok {
		t.Error("a must be untouched")
	}
	if got, _ := store.Meta(MetaCursor); got != "T3" {
		t.Errorf("cursor = %q, want T3", got)
	}
}

// The feed never says "destroyed": the entry is re-emitted as trashed with
// a newer sort key. The bin relist that follows is what removes it.
func TestDestroyedEntryLeavesViaBinRelist(t *testing.T) {
	lister := &fakeLister{pages: map[string]*drive.FileList{
		"":       {Files: []drive.File{df("live", "", "x", false), df("t1", "", "t1", true), df("t2", "", "t2", true), df("t1c", "t1", "child", true)}, SyncTime: "T1", Version: version("D0")},
		"trash:": {Files: []drive.File{df("t1", "", "t1", true), df("t2", "", "t2", true)}},
	}}
	s, store, _ := newSyncer(t, lister)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.Count(); n != 4 {
		t.Fatalf("count after full = %d, want 4 (descendants of trashed folders are kept)", n)
	}

	lister.pages[drive.SyncCursor("T1")] = &drive.FileList{Files: []drive.File{df("t1", "", "t1", true)}, SyncTime: "T2", Version: version("D0")}
	lister.pages["trash:"] = &drive.FileList{Files: []drive.File{df("t2", "", "t2", true)}}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, ok, _ := store.Get("t1"); ok {
		t.Error("t1 was destroyed on the server and must leave the index")
	}
	if _, ok, _ := store.Get("t1c"); ok {
		t.Error("t1's descendant goes with it")
	}
	if _, ok, _ := store.Get("t2"); !ok {
		t.Error("t2 is still in the bin and must stay")
	}
	if _, ok, _ := store.Get("live"); !ok {
		t.Error("live rows are never touched by the trash reconcile")
	}
}

func TestDestroyChangeRelistsTrashAndDropsMissingRows(t *testing.T) {
	lister := &fakeLister{pages: map[string]*drive.FileList{
		"":       {Files: []drive.File{df("live", "", "x", false), df("t1", "", "t1", true), df("t2", "", "t2", true)}, SyncTime: "T1", Version: version("D0")},
		"trash:": {Files: []drive.File{df("t1", "", "t1", true), df("t2", "", "t2", true)}},
	}}
	s, store, _ := newSyncer(t, lister)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Meta(MetaDestroy); got != "D0" {
		t.Errorf("destroy after full = %q, want D0", got)
	}
	lister.pages[drive.SyncCursor("T1")] = &drive.FileList{Files: nil, SyncTime: "T1", Version: version("D1")}
	lister.pages["trash:"] = &drive.FileList{Files: []drive.File{df("t2", "", "t2", true)}}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, ok, _ := store.Get("t1"); ok {
		t.Error("t1 was destroyed on the server and must leave the index")
	}
	if _, ok, _ := store.Get("t2"); !ok {
		t.Error("t2 is still in the bin and must stay")
	}
	if _, ok, _ := store.Get("live"); !ok {
		t.Error("live rows are never touched by the trash reconcile")
	}
	if got, _ := store.Meta(MetaDestroy); got != "D1" {
		t.Errorf("destroy = %q, want D1", got)
	}
	trash := lister.calls[len(lister.calls)-1]
	if trash.Filters == nil || trash.Filters["trashed"]["eq"] != true || trash.ParentID != drive.WholeTree {
		t.Errorf("trash relist request = %+v", trash)
	}
}

func TestMidWalkFailureKeepsCursorAndReportsError(t *testing.T) {
	lister := &fakeLister{
		pages: map[string]*drive.FileList{
			"": {Files: []drive.File{df("a", "", "a", false)}, NextPageToken: "p2", SyncTime: "T1"},
		},
		errs: map[string]error{"p2": errors.New("boom")},
	}
	s, store, _ := newSyncer(t, lister)
	err := s.Sync(context.Background())
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
	if got, _ := store.Meta(MetaCursor); got != "" {
		t.Errorf("cursor advanced to %q on a failed walk", got)
	}
	if got, _ := store.Meta(MetaFullAt); got != "" {
		t.Error("full_at must stay empty on a failed full walk")
	}
	p := s.Progress()
	if p.Error != "boom" || p.Ready || p.Phase != "idle" {
		t.Errorf("progress = %+v", p)
	}
	// The first page's rows stay, and the walk is kept to resume.
	if n, _ := store.Count(); n != 1 {
		t.Errorf("count = %d", n)
	}
	if raw, _ := store.Meta(MetaWalk); raw == "" {
		t.Error("a failed full walk must keep its checkpoint")
	}

	// Cancellation surfaces as the context error and stops before listing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := lister.count()
	if err := s.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled sync err = %v", err)
	}
	if lister.count() != calls {
		t.Error("a cancelled walk must not hit the server")
	}
}

func TestRebuildClearsThenWalks(t *testing.T) {
	lister := &fakeLister{pages: map[string]*drive.FileList{
		"":       {Files: []drive.File{df("a", "", "a", false)}, SyncTime: "T1"},
		"trash:": {},
	}}
	s, store, _ := newSyncer(t, lister)
	if err := store.Upsert([]Row{{ID: "stale", Kind: drive.KindFile, Name: "stale"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get("stale"); ok {
		t.Error("rebuild must drop rows the walk did not see")
	}
	if n, _ := store.Count(); n != 1 {
		t.Errorf("count = %d", n)
	}
	if !s.Progress().Ready {
		t.Error("rebuild completes a full walk")
	}
}

func TestFullWalkRemovesRowsNotSeen(t *testing.T) {
	lister := &fakeLister{pages: map[string]*drive.FileList{
		"":       {Files: []drive.File{df("a", "", "a", false)}, SyncTime: "T1"},
		"trash:": {},
	}}
	s, store, _ := newSyncer(t, lister)
	if err := store.Upsert([]Row{{ID: "ghost", Kind: drive.KindFile, Name: "ghost"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get("ghost"); ok {
		t.Error("a full walk converges on the server: unseen rows go")
	}
}
