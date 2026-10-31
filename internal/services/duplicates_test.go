package services

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/index"
)

// offlineDrive is a signed-in service whose stand-in API fails the test on
// any request but signing in: scans read the index alone.
func offlineDrive(t *testing.T) *DriveService {
	t.Helper()
	return newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	})
}

func dupRow(id, parent, hash string, size int64, created string) index.Row {
	r := indexedVideo(id, parent, id+".mkv", size, "2026-04-01T00:00:00Z")
	r.Hash, r.CreatedTime = strings.Repeat(hash, 40), created
	return r
}

func copyIDs(g DupGroup) []string {
	out := make([]string, len(g.Copies))
	for i, c := range g.Copies {
		out[i] = c.ID
	}
	return out
}

func TestScanNeedsAReadyIndex(t *testing.T) {
	svc := offlineDrive(t)
	if _, err := svc.ScanDuplicates("", DupQuery{}); !errors.Is(err, errScanNotReady) {
		t.Errorf("duplicates: %v", err)
	}
	if _, err := svc.ScanEmptyFolders("", ""); !errors.Is(err, errScanNotReady) {
		t.Errorf("empty folders: %v", err)
	}
	if sum, err := svc.TrashSummary(""); err != nil || sum.Ready {
		t.Errorf("trash summary = %+v, %v; want not ready", sum, err)
	}
}

// Duplicate detection: groups by reclaimable bytes, the default keep
// (starred, played, shallowest, earliest created, smallest id) first, what
// is not content left out, a group of mismatched sizes dropped.
func TestScanDuplicates(t *testing.T) {
	svc := offlineDrive(t)
	s, err := svc.active()
	if err != nil {
		t.Fatal(err)
	}
	starred := dupRow("s2", "b", "S", 6000, "2026-05-01T00:00:00Z")
	starred.Starred = true
	thumb := dupRow("p1", "", "P", 5000, "2026-01-01T00:00:00Z")
	thumb.ThumbnailLink = "https://thumb/p1"
	zero1, zero2 := dupRow("z1", "", "Z", 0, ""), dupRow("z2", "", "Z", 0, "")
	pending1, pending2 := dupRow("u1", "", "U", 9000, ""), dupRow("u2", "", "U", 9000, "")
	pending1.Phase, pending2.Phase = "PHASE_TYPE_RUNNING", "PHASE_TYPE_RUNNING"
	binned := dupRow("t1", "", "T", 9000, "")
	binned.Trashed = true
	fillIndex(t, svc, []index.Row{
		indexedFolder("a", "", "a"), indexedFolder("b", "a", "b"),
		dupRow("s1", "", "S", 6000, "2026-01-01T00:00:00Z"), starred,
		thumb, dupRow("p2", "a", "P", 5000, "2026-05-01T00:00:00Z"),
		dupRow("d1", "b", "D", 4000, "2026-01-01T00:00:00Z"), dupRow("d2", "a", "D", 4000, "2026-05-01T00:00:00Z"),
		// c1 is the earlier instant, though not the smaller text.
		dupRow("c2", "", "C", 3000, "2026-03-01T03:00:00Z"), dupRow("c1", "", "C", 3000, "2026-03-01T08:00:00+08:00"),
		dupRow("i3", "", "I", 1000, "2026-01-01T00:00:00Z"), dupRow("i2", "", "I", 1000, "2026-01-01T00:00:00Z"),
		dupRow("i1", "", "I", 1000, "2026-01-01T00:00:00Z"),
		zero1, zero2, pending1, pending2, binned, dupRow("t2", "", "T", 9000, ""),
		dupRow("m1", "", "M", 9000, ""), dupRow("m2", "", "M", 9001, ""),
	})
	idx := activeIndex(svc)
	if err := idx.store.SetMeta(index.MetaSyncedAt, "2026-10-15T08:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := idx.store.Patch("i2", index.Patch{Name: new("x.mkv")}); err != nil {
		t.Fatal(err)
	}
	if err := idx.store.Patch("i3", index.Patch{Name: new("x.mkv")}); err != nil {
		t.Fatal(err)
	}
	if err := s.playLog().opened(playRecord{FileID: "p2", Name: "p2.mkv"}); err != nil {
		t.Fatal(err)
	}

	page, err := svc.ScanDuplicates("", DupQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, g := range page.Groups {
		order = append(order, g.Hash[:1]+":"+strings.Join(copyIDs(g), ","))
		keeps := 0
		for _, c := range g.Copies {
			if c.Keep {
				keeps++
			}
		}
		if keeps != 1 || !g.Copies[0].Keep {
			t.Errorf("group %s keeps %d, first %v", g.Hash[:1], keeps, g.Copies[0].Keep)
		}
	}
	want := []string{"S:s2,s1", "P:p2,p1", "D:d2,d1", "C:c1,c2", "I:i1,i2,i3"}
	if !slices.Equal(order, want) {
		t.Errorf("groups = %v\nwant     %v", order, want)
	}
	if page.Total != 5 || page.Copies != 6 || page.Waste != 6000+5000+4000+3000+2000 || page.Small != 0 ||
		page.SyncedAt != "2026-10-15T08:00:00Z" || page.Offset != 0 {
		t.Errorf("page figures = %+v", page)
	}
	s2 := page.Groups[0].Copies[0]
	if s2.Path != "全部文件 / a / b" || s2.Depth != 2 || !s2.Starred || s2.ParentID != "b" || s2.Size != 6000 {
		t.Errorf("s2 = %+v", s2)
	}
	if s1 := page.Groups[0].Copies[1]; s1.Path != "全部文件" || s1.Depth != 0 {
		t.Errorf("s1 = %+v", s1)
	}
	p := page.Groups[1]
	if !p.Copies[0].Played || p.Copies[1].Played || p.Thumbnail != "https://thumb/p1" || p.Category != "video" {
		t.Errorf("group P = %+v", p)
	}
	if i := page.Groups[4]; i.Name != "x.mkv" || i.Count != 3 || i.Waste != 2000 || !i.SameFolder {
		t.Errorf("group I = %+v", i)
	}

	// Same folder only: every copy of each group in one folder.
	same, err := svc.ScanDuplicates("", DupQuery{SameFolder: true})
	if err != nil {
		t.Fatal(err)
	}
	if same.Total != 2 {
		t.Errorf("same-folder groups = %d, want C and I", same.Total)
	}
	for _, g := range same.Groups {
		for _, c := range g.Copies {
			if c.ParentID != g.Copies[0].ParentID || !g.SameFolder {
				t.Errorf("group %s spans folders", g.Hash[:1])
			}
		}
	}

	// The size floor folds small groups into one figure.
	floor, err := svc.ScanDuplicates("", DupQuery{MinSize: 2500})
	if err != nil {
		t.Fatal(err)
	}
	if floor.Total != 4 || floor.Small != 1 || floor.SmallWaste != 2000 {
		t.Errorf("floor = total %d small %d/%d", floor.Total, floor.Small, floor.SmallWaste)
	}
}

// A later page pages the kept scan; a first page scans again; any remove
// run on the account drops the scan.
func TestScanDuplicatesPagesTheKeptScan(t *testing.T) {
	tree := newFakeTree()
	hashes := map[string]string{}
	for _, h := range []string{"A", "B", "C"} {
		for _, n := range []string{"1", "2"} {
			id := strings.ToLower(h) + n
			tree.add(id, id+".mkv", "", false)
			tree.files[id].size = int64(h[0])
			hashes[id] = strings.Repeat(h, 40)
		}
	}
	svc, account, _ := newRemoveDrive(t, tree, hashes)
	first, err := svc.ScanDuplicates(account, DupQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 3 || len(first.Groups) != 2 {
		t.Fatalf("first page = %+v", first)
	}
	// A new group arrives in the index; the kept scan does not see it.
	if err := activeIndex(svc).store.Upsert([]index.Row{
		dupRow("x1", "", "X", 99, ""), dupRow("x2", "", "X", 99, ""),
	}); err != nil {
		t.Fatal(err)
	}
	second, err := svc.ScanDuplicates(account, DupQuery{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 3 || len(second.Groups) != 1 || second.Offset != 2 {
		t.Errorf("second page = %+v, want the kept scan's last group", second)
	}
	again, err := svc.ScanDuplicates(account, DupQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if again.Total != 4 {
		t.Errorf("rescan total = %d, want 4", again.Total)
	}
	if _, err := svc.RemoveFiles(account, []string{"a1"}, false); err != nil {
		t.Fatal(err)
	}
	svc.dupMu.Lock()
	kept := svc.dups[account]
	svc.dupMu.Unlock()
	if kept != nil {
		t.Error("the scan survived a remove run")
	}
}

// Duplicate removal: ids covering every live copy of a group are refused as a
// whole — nothing is sent — while leaving one copy goes ahead.
func TestRemoveDuplicatesKeepsOneCopy(t *testing.T) {
	tree := newFakeTree()
	hashes := map[string]string{}
	for _, id := range []string{"h1", "h2", "g1", "g2", "g3"} {
		tree.add(id, id[:1]+".mkv", "", false)
		tree.files[id].size = 10
		hashes[id] = strings.Repeat(strings.ToUpper(id[:1]), 40)
	}
	svc, account, sent := newRemoveDrive(t, tree, hashes)

	_, err := svc.RemoveDuplicates(account, []string{"g1", "h1", "h2"}, false)
	if err == nil || !strings.Contains(err.Error(), "每组至少要保留一份") || !strings.Contains(err.Error(), "h.mkv") {
		t.Fatalf("whole group: %v", err)
	}
	if _, err := svc.RemoveDuplicates(account, []string{"g1", "h1", "h2"}, true); err == nil {
		t.Fatal("whole group, permanent: accepted")
	}
	if n := sent.writes(); n != 0 {
		t.Errorf("%d writes sent for a refused request", n)
	}
	rep, err := svc.RemoveDuplicates(account, []string{"g1", "g2", "h1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Removed, []string{"g1", "g2", "h1"}) {
		t.Errorf("removed = %v", rep.Removed)
	}
	if tree.file("g3").trashed || tree.file("h2").trashed {
		t.Error("the copy left to keep went too")
	}
	// The kept copies are now the last of their groups.
	if _, err := svc.RemoveDuplicates(account, []string{"g3"}, false); err == nil {
		t.Error("the last copy of g was accepted")
	}
	noDeletes(t, tree)
}
