package services

import (
	"slices"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/index"
)

// Verification: a shell of empty folders A/B/C is one item
// carrying its nested count, and trashing A takes all three to the bin;
// special folders are never listed nor trashed; nothing is deleted for good.
func TestEmptyFolderShellGoesInOneStep(t *testing.T) {
	tree := newFakeTree()
	tree.add("A", "A", "", true)
	tree.add("B", "B", "A", true)
	tree.add("C", "C", "B", true)
	tree.add("D", "D", "", true)
	tree.add("DD", "DD", "D", true)
	tree.add("x", "x.mkv", "DD", false)
	tree.add("P", "My Pack", "", true)
	tree.files["P"].special = true
	tree.add("H", "H", "", true)
	tree.add("ht", "old.mkv", "H", false)
	tree.files["ht"].trashed = true
	tree.add("R", "R", "", true)
	svc, account, sent := newRemoveDrive(t, tree, nil)
	checkTree(t, tree)
	recent := indexedFolder("R", "", "R")
	recent.FolderType, recent.CreatedTime = "NORMAL", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if err := activeIndex(svc).store.Upsert([]index.Row{recent}); err != nil {
		t.Fatal(err)
	}

	page, err := svc.ScanEmptyFolders(account, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || len(page.Folders) != 3 {
		t.Fatalf("page = %+v, want A (with B, C), H and R", page)
	}
	a, h, r := page.Folders[0], page.Folders[1], page.Folders[2]
	if a.ID != "A" || a.Path != "全部文件 / A" || a.Nested != 2 || a.Recent || a.HasTrashed {
		t.Errorf("A = %+v", a)
	}
	if h.ID != "H" || !h.HasTrashed || h.Recent {
		t.Errorf("H = %+v", h)
	}
	if r.ID != "R" || !r.Recent || r.HasTrashed {
		t.Errorf("R = %+v", r)
	}

	rep, err := svc.RemoveEmptyFolders(account, []string{"A", "P"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Removed, []string{"A"}) {
		t.Errorf("removed = %v", rep.Removed)
	}
	if got := failedReasons(rep); len(got) != 1 || got["P"] != "My Pack：特殊文件夹，未处理" {
		t.Errorf("failed = %v", got)
	}
	if !tree.file("A").trashed || tree.file("P").trashed {
		t.Error("server side: A should be in the bin, My Pack untouched")
	}
	for _, id := range []string{"A", "B", "C"} {
		if row, _ := indexRow(t, svc, id); !row.Trashed {
			t.Errorf("index %s still live", id)
		}
	}
	if b := sent.of(":batchTrash"); len(b) != 1 || !slices.Equal(b[0], []string{"A"}) {
		t.Errorf("batchTrash calls = %v", b)
	}
	if page, _ := svc.ScanEmptyFolders(account, ""); page.Total != 2 {
		t.Errorf("after: %+v, want H and R left", page)
	}
}

// Verification: a folder that got something since the scan — directly or
// deep inside — is kept with 「already non-empty, unprocessed」; the rest go ahead.
func TestFolderFilledSinceScanIsKept(t *testing.T) {
	tree := newFakeTree()
	tree.add("E", "E", "", true)
	tree.add("X", "X", "", true)
	tree.add("Y", "Y", "X", true)
	tree.add("K", "K", "", true)
	tree.add("f", "f.txt", "", false)
	svc, account, _ := newRemoveDrive(t, tree, nil)
	checkTree(t, tree)
	page, err := svc.ScanEmptyFolders(account, "")
	if err != nil || page.Total != 4 || len(page.Folders) != 3 {
		t.Fatalf("scan = %+v, %v", page, err)
	}
	tree.put("e1", "new.mkv", "E", false)
	tree.put("y1", "deep.mkv", "Y", false)

	rep, err := svc.RemoveEmptyFolders(account, []string{"E", "X", "K", "f"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Removed, []string{"K"}) {
		t.Errorf("removed = %v", rep.Removed)
	}
	want := map[string]string{
		"E": "E：已经不是空的，未处理",
		"X": "X：已经不是空的，未处理",
		"f": "f.txt：不是文件夹，未处理",
	}
	got := failedReasons(rep)
	for id, why := range want {
		if got[id] != why {
			t.Errorf("failed %s = %q, want %q", id, got[id], why)
		}
	}
	if tree.file("E").trashed || tree.file("X").trashed || !tree.file("K").trashed {
		t.Error("only K should be in the bin")
	}
}
