package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

// sentLog records the batch writes a service sent: path and ids.
type sentLog struct {
	mu   sync.Mutex
	sent []sentRequest
}

type sentRequest struct {
	path string
	ids  []string
}

func (l *sentLog) of(path string) [][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out [][]string
	for _, s := range l.sent {
		if strings.HasSuffix(s.path, path) {
			out = append(out, s.ids)
		}
	}
	return out
}

func (l *sentLog) writes() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sent)
}

// newRemoveDrive is a signed-in service over tree whose index holds the
// tree as it stands (see indexTree), and the log of what it sent.
func newRemoveDrive(t *testing.T, tree *fakeTree, hashes map[string]string) (*DriveService, string, *sentLog) {
	t.Helper()
	fastTasks(t)
	log := &sentLog{}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/drive/v1/files:") {
			body, _ := io.ReadAll(r.Body)
			var req struct {
				IDs []string `json:"ids"`
			}
			_ = json.Unmarshal(body, &req)
			log.mu.Lock()
			log.sent = append(log.sent, sentRequest{path: r.URL.Path, ids: req.IDs})
			log.mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		tree.serve(w, r)
	})
	s, err := svc.active()
	if err != nil {
		t.Fatal(err)
	}
	indexTree(t, svc, tree, hashes)
	return svc, s.key, log
}

// indexTree writes tree into the index of the account on screen, as a walk
// would see it — what is inside a trashed folder is trashed with it, with
// the folder's purge time — and marks the index ready.
func indexTree(t *testing.T, svc *DriveService, tree *fakeTree, hashes map[string]string) {
	t.Helper()
	tree.mu.Lock()
	var rows []index.Row
	for _, f := range tree.files {
		file := drive.File{ID: f.id, Name: f.name, ParentID: f.parent, Kind: drive.KindFile, Size: drive.Int64(f.size),
			Phase: drive.PhaseComplete, Hash: hashes[f.id], CreatedTime: "2026-01-01T00:00:00Z", ModifiedTime: "2026-01-01T00:00:00Z"}
		if f.folder {
			file.Kind, file.FolderType = drive.KindFolder, "NORMAL"
			if f.special {
				file.FolderType = "DOWNLOAD"
			}
		}
		for up := f; up != nil; up = tree.files[up.parent] {
			if up.trashed {
				file.Trashed, file.DeleteTime = true, "2026-11-01T00:00:00Z+"+up.id
				break
			}
		}
		rows = append(rows, toRow(file))
	}
	tree.mu.Unlock()
	fillIndex(t, svc, rows)
}

func indexRow(t *testing.T, svc *DriveService, id string) (index.Row, bool) {
	t.Helper()
	row, ok, err := activeIndex(svc).store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return row, ok
}

func failedReasons(rep RemoveReport) map[string]string {
	out := map[string]string{}
	for _, f := range rep.Failed {
		out[f.ID] = f.Name + "：" + f.Error
	}
	return out
}

func noDeletes(t *testing.T, tree *fakeTree) {
	t.Helper()
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if len(tree.deletes) != 0 {
		t.Errorf("batchDelete was called: %v", tree.deletes)
	}
}

// Verification: an entry renamed, moved or deleted since the page showed
// it is reported with the reason and never sent; the rest go ahead. Moving
// to the bin never deletes anything for good.
func TestRemoveFilesSkipsWhatChangedSinceThePage(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "下载", "", true)
	tree.add("G", "别处", "", true)
	for _, id := range []string{"a", "b", "c", "d"} {
		tree.add(id, id+".mp4", "F", false)
	}
	svc, account, sent := newRemoveDrive(t, tree, nil)
	tree.mu.Lock()
	tree.files["b"].name = "b2.mp4"
	tree.files["c"].parent = "G"
	delete(tree.files, "d")
	tree.mu.Unlock()

	rep, err := svc.RemoveFiles(account, []string{"a", "b", "c", "d", "a", ""}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Removed, []string{"a"}) {
		t.Errorf("removed = %v, want a", rep.Removed)
	}
	want := map[string]string{
		"b": "b2.mp4：已被改名，未处理",
		"c": "c.mp4：已被移到别的文件夹，未处理",
		"d": "d.mp4：云盘里已经没有这一项，未处理",
	}
	if got := failedReasons(rep); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("failed = %v\nwant     %v", got, want)
	}
	if !tree.file("a").trashed || tree.file("b").trashed || tree.file("c").trashed {
		t.Errorf("tree after: a=%v b=%v c=%v", tree.file("a"), tree.file("b"), tree.file("c"))
	}
	if batches := sent.of(":batchTrash"); len(batches) != 1 || !slices.Equal(batches[0], []string{"a"}) {
		t.Errorf("batchTrash calls = %v, want one with a", batches)
	}
	if row, _ := indexRow(t, svc, "a"); !row.Trashed {
		t.Error("the index still lists a as live")
	}
	if rep.Freed != 0 || rep.Stopped {
		t.Errorf("report = %+v", rep)
	}
	noDeletes(t, tree)
}

// Batching verification: more than 100 entries go 100 at a time, each batch's task
// finished before the next is sent; a refusal costs only the refused entry
// and is reported with the server's words.
func TestRemoveFilesBatchesWaitingForEachTask(t *testing.T) {
	tree := newFakeTree()
	var ids []string
	for i := range 250 {
		id := fmt.Sprintf("f%03d", i)
		tree.add(id, id+".mkv", "", false)
		ids = append(ids, id)
	}
	tree.refuse["f240"] = true
	svc, account, sent := newRemoveDrive(t, tree, nil)
	// What was still running when each batchTrash arrived. The first three
	// are the batches; then the refused one goes again entry by entry.
	var running []int
	tree.hook(func(what string) {
		if !strings.HasSuffix(what, ":batchTrash") {
			return
		}
		tree.mu.Lock()
		defer tree.mu.Unlock()
		n := 0
		for _, task := range tree.tasks {
			if task.work != nil {
				n++
			}
		}
		running = append(running, n)
	})

	rep, err := svc.RemoveFiles(account, ids, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) < 3 || running[0]+running[1]+running[2] != 0 {
		t.Errorf("tasks running as each batch was sent: %v, want none for the first three", running)
	}
	batches := sent.of(":batchTrash")
	if len(batches[0]) != 100 || len(batches[1]) != 100 || len(batches[2]) != 50 {
		t.Errorf("first batches = %d, %d, %d ids, want 100, 100, 50", len(batches[0]), len(batches[1]), len(batches[2]))
	}
	for _, b := range batches {
		if len(b) > 100 {
			t.Errorf("a batch of %d ids", len(b))
		}
	}
	if len(rep.Removed) != 249 || len(rep.Failed) != 1 {
		t.Fatalf("removed %d, failed %v", len(rep.Removed), rep.Failed)
	}
	if f := rep.Failed[0]; f.ID != "f240" || f.Name != "f240.mkv" || f.Error != "refused by the stand-in" {
		t.Errorf("failure = %+v", f)
	}
	noDeletes(t, tree)
}

// Verification: refuse more than maxRemove entries.
func TestRemoveRefusesMoreThan5000(t *testing.T) {
	tree := newFakeTree()
	svc, account, sent := newRemoveDrive(t, tree, nil)
	ids := make([]string, maxRemove+1)
	for i := range ids {
		ids[i] = fmt.Sprint(i)
	}
	for name, call := range map[string]func() error{
		"remove":     func() error { _, err := svc.RemoveFiles(account, ids, false); return err },
		"delete":     func() error { _, err := svc.RemoveFiles(account, ids, true); return err },
		"restore":    func() error { _, err := svc.RestoreFiles(account, ids); return err },
		"duplicates": func() error { _, err := svc.RemoveDuplicates(account, ids, false); return err },
		"empty":      func() error { _, err := svc.RemoveEmptyFolders(account, ids); return err },
	} {
		if err := call(); !errors.Is(err, errRemoveTooMany) || err.Error() != "一次最多处理 5000 项，请缩小范围" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if sent.writes() != 0 {
		t.Errorf("%d writes sent", sent.writes())
	}
}

// Verification: after a permanent delete the index keeps no trashed
// ghost of the entries or of anything inside them, and a sync is asked for
// (the server goes on listing destroyed entries as trashed in the
// whole-tree feed until a recycle-bin relist drops them).
func TestPermanentDeleteLeavesNoGhostRows(t *testing.T) {
	tree := newFakeTree()
	tree.add("T", "旧剧集", "", true)
	tree.add("x", "e01.mkv", "T", false)
	tree.files["x"].size = 30
	tree.files["T"].trashed = true
	tree.add("y", "copy.mkv", "", false)
	tree.files["y"].size = 7
	tree.add("z", "keep.mkv", "", false)
	svc, account, sent := newRemoveDrive(t, tree, nil)
	idx := activeIndex(svc)

	rep, err := svc.RemoveFiles(account, []string{"T", "y"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Removed, []string{"T", "y"}) || len(rep.Failed) != 0 || rep.Freed != 37 {
		t.Errorf("report = %+v, want T and y removed, 37 bytes freed", rep)
	}
	if deletes := sent.of(":batchDelete"); len(deletes) != 1 || !slices.Equal(deletes[0], []string{"T", "y"}) {
		t.Errorf("batchDelete calls = %v", deletes)
	}
	for _, id := range []string{"T", "x", "y"} {
		if _, ok := indexRow(t, svc, id); ok {
			t.Errorf("index still holds %s", id)
		}
	}
	if count, size, err := idx.store.TrashSummary(); err != nil || count != 0 || size != 0 {
		t.Errorf("trashed rows left: %d (%d bytes), %v", count, size, err)
	}
	if _, ok := indexRow(t, svc, "z"); !ok {
		t.Error("an entry not asked for left the index")
	}
	if len(idx.syncNow) != 1 {
		t.Error("no sync was asked for after the permanent delete")
	}
}

// Verification: empty recycle bin destroys what the bin lists now, reports every
// entry and the bytes freed, and the index follows.
func TestEmptyTrash(t *testing.T) {
	tree := newFakeTree()
	tree.add("T", "旧剧集", "", true)
	tree.add("x", "e01.mkv", "T", false)
	tree.files["x"].size = 30
	tree.files["T"].trashed = true
	tree.add("s", "a.zip", "", false)
	tree.files["s"].size = 5
	tree.files["s"].trashed = true
	tree.add("z", "live.mkv", "", false)
	tree.files["z"].size = 100
	svc, account, _ := newRemoveDrive(t, tree, nil)

	sum, err := svc.TrashSummary(account)
	if err != nil || sum != (TrashSummary{Count: 2, Size: 35, Ready: true}) {
		t.Errorf("summary = %+v, %v", sum, err)
	}
	rep, err := svc.EmptyTrash(account)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(rep.Removed)
	if !slices.Equal(rep.Removed, []string{"T", "s"}) || len(rep.Failed) != 0 || rep.Freed != 35 {
		t.Errorf("report = %+v", rep)
	}
	tree.mu.Lock()
	left := len(tree.files)
	tree.mu.Unlock()
	if left != 1 || tree.file("z").id != "z" {
		t.Errorf("tree keeps %d entries, want only z", left)
	}
	sum, _ = svc.TrashSummary(account)
	if sum.Count != 0 || sum.Size != 0 {
		t.Errorf("summary after = %+v", sum)
	}
	if _, ok := indexRow(t, svc, "x"); ok {
		t.Error("index still holds x")
	}
}

// restore takes entries out of the bin through the same channel; a name taken
// meanwhile comes back numbered and the index says so.
func TestRestoreFiles(t *testing.T) {
	tree := newFakeTree()
	tree.add("a", "a.mp4", "", false)
	tree.files["a"].trashed = true
	tree.add("b", "b.mp4", "", false)
	tree.files["b"].trashed = true
	tree.add("live", "live.mp4", "", false)
	svc, account, _ := newRemoveDrive(t, tree, nil)
	tree.put("b-new", "b.mp4", "", false) // takes b's name meanwhile

	rep, err := svc.RestoreFiles(account, []string{"a", "b", "live", "gone"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(rep.Removed)
	if !slices.Equal(rep.Removed, []string{"a", "b", "live"}) {
		t.Errorf("restored = %v", rep.Removed)
	}
	if got := failedReasons(rep); len(got) != 1 || got["gone"] != "：云盘里已经没有这一项，未处理" {
		t.Errorf("failed = %v", got)
	}
	if row, _ := indexRow(t, svc, "b"); row.Trashed || row.Name != "b(1).mp4" {
		t.Errorf("index b = %+v, want live as b(1).mp4", row)
	}
	if row, _ := indexRow(t, svc, "a"); row.Trashed {
		t.Error("index a still trashed")
	}
	noDeletes(t, tree)
}

// Verification: no path deletes anything for good unless it was asked
// to, whatever it is given.
func TestNothingIsDeletedForGoodUnlessAsked(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "空", "", true)
	tree.add("a", "a.mp4", "", false)
	tree.add("b", "b.mp4", "", false)
	tree.add("t", "t.mp4", "", false)
	tree.files["t"].trashed = true
	svc, account, _ := newRemoveDrive(t, tree, map[string]string{"a": strings.Repeat("A", 40), "b": strings.Repeat("A", 40)})
	if _, err := svc.ScanDuplicates(account, DupQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ScanEmptyFolders(account, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TrashSummary(account); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() (RemoveReport, error){
		func() (RemoveReport, error) { return svc.RemoveDuplicates(account, []string{"a"}, false) },
		func() (RemoveReport, error) { return svc.RemoveFiles(account, []string{"b", "F", "t"}, false) },
		func() (RemoveReport, error) { return svc.RestoreFiles(account, []string{"t", "a"}) },
		func() (RemoveReport, error) { return svc.RemoveEmptyFolders(account, []string{"F", "a", "t"}) },
	} {
		if _, err := call(); err != nil {
			t.Fatal(err)
		}
	}
	noDeletes(t, tree)
}

// One remove run per account; StopRemove ends it after the batch in
// flight, and what was not sent is neither removed nor failed.
func TestStopRemoveAfterTheBatchInFlight(t *testing.T) {
	tree := newFakeTree()
	var ids []string
	for i := range 250 {
		id := fmt.Sprintf("f%03d", i)
		tree.add(id, id+".mkv", "", false)
		ids = append(ids, id)
	}
	svc, account, sent := newRemoveDrive(t, tree, nil)
	var second error
	var once sync.Once
	tree.hook(func(what string) {
		if strings.HasSuffix(what, ":batchTrash") {
			once.Do(func() {
				_, second = svc.RemoveFiles(account, []string{"f000"}, false)
				svc.StopRemove(account)
			})
		}
	})
	rep, err := svc.RemoveFiles(account, ids, false)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(second, errRemoving) {
		t.Errorf("second run = %v, want errRemoving", second)
	}
	if !rep.Stopped || len(rep.Removed) != 100 || len(rep.Failed) != 0 {
		t.Errorf("report: stopped=%v removed=%d failed=%v", rep.Stopped, len(rep.Removed), rep.Failed)
	}
	if n := len(sent.of(":batchTrash")); n != 1 {
		t.Errorf("%d batches sent, want 1", n)
	}
	if _, err := svc.RemoveFiles(account, []string{"f200"}, false); err != nil {
		t.Errorf("a run after the stopped one: %v", err)
	}
}
