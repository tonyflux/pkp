package index

import (
	"slices"
	"strings"
	"testing"
)

func hashOf(c string) string { return strings.Repeat(c, 40) }

func copyRow(id, parent, hash string, size int64, modified string) Row {
	r := file(id, parent, id, size)
	r.Hash, r.ModifiedTime = hash, modified
	return r
}

func groupHashes(gs []DupGroup) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Hash
	}
	return out
}

func upsert(t *testing.T, s *Store, rows ...Row) {
	t.Helper()
	if err := s.Upsert(rows); err != nil {
		t.Fatal(err)
	}
}

// Duplicate verification: zero-byte files, unfinished uploads, folders and
// recycle-bin entries never make a group, and a group whose copies differ
// in size is dropped and reported.
func TestDuplicatesCountOnlyFinishedLiveFilesWithContent(t *testing.T) {
	s := openTemp(t)
	zero1, zero2 := copyRow("z1", "", hashOf("Z"), 0, "2026-01-01T00:00:00Z"), copyRow("z2", "", hashOf("Z"), 0, "2026-01-01T00:00:00Z")
	up1, up2 := copyRow("p1", "", hashOf("P"), 5, "2026-01-01T00:00:00Z"), copyRow("p2", "", hashOf("P"), 5, "2026-01-01T00:00:00Z")
	up1.Phase, up2.Phase = "PHASE_TYPE_PENDING", "PHASE_TYPE_PENDING"
	dir1, dir2 := folder("f1", "", "f1"), folder("f2", "", "f2")
	dir1.Hash, dir2.Hash, dir1.Size, dir2.Size = hashOf("F"), hashOf("F"), 9, 9
	gone := copyRow("t1", "", hashOf("T"), 7, "2026-01-01T00:00:00Z")
	gone.Trashed = true
	short1, short2 := copyRow("s1", "", "ABC", 7, "2026-01-01T00:00:00Z"), copyRow("s2", "", "ABC", 7, "2026-01-01T00:00:00Z")
	upsert(t, s,
		copyRow("a1", "", hashOf("A"), 10, "2026-01-01T00:00:00Z"), copyRow("a2", "f1", hashOf("A"), 10, "2026-01-02T00:00:00Z"),
		zero1, zero2, up1, up2, dir1, dir2, gone, copyRow("t2", "", hashOf("T"), 7, "2026-01-01T00:00:00Z"), short1, short2,
		copyRow("m1", "", hashOf("M"), 5, "2026-01-01T00:00:00Z"), copyRow("m2", "", hashOf("M"), 6, "2026-01-01T00:00:00Z"),
	)
	got, err := s.Duplicates(DupScan{})
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(groupHashes(got.Groups), hashOf("A")) {
		t.Errorf("groups = %v, want only A", groupHashes(got.Groups))
	}
	if g := got.Groups[0]; g.Size != 10 || g.Count != 2 || g.Waste != 10 || g.SameFolder {
		t.Errorf("group A = %+v", g)
	}
	if !sameIDs(got.Mismatched, hashOf("M")) {
		t.Errorf("mismatched = %v, want M", got.Mismatched)
	}
	copies, err := s.DupCopies([]string{hashOf("A"), hashOf("Z"), hashOf("T")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(ids(copies), "a1", "a2", "t2") {
		t.Errorf("copies = %v, want a1 a2 t2 (zero-byte and trashed left out)", ids(copies))
	}
}

// Duplicate filters: Same Folder Only, the size floor
// folded into Small, the three orders and a subtree scope.
func TestDuplicatesFilterSortAndScope(t *testing.T) {
	s := openTemp(t)
	upsert(t, s,
		folder("x", "", "x"), folder("y", "", "y"), folder("xx", "x", "xx"),
		// B: three copies of 100 bytes in x, waste 200.
		copyRow("b1", "x", hashOf("B"), 100, "2026-01-01T00:00:00Z"), copyRow("b2", "x", hashOf("B"), 100, "2026-01-01T00:00:00Z"),
		copyRow("b3", "x", hashOf("B"), 100, "2026-01-01T00:00:00Z"),
		// C: two copies of 1000 bytes in x/xx and y, waste 1000, newest.
		copyRow("c1", "xx", hashOf("C"), 1000, "2026-03-01T00:00:00+08:00"), copyRow("c2", "y", hashOf("C"), 1000, "2026-01-01T00:00:00Z"),
		// D: two copies of 3 bytes at the root, waste 3.
		copyRow("d1", "", hashOf("D"), 3, "2026-01-01T00:00:00Z"), copyRow("d2", "", hashOf("D"), 3, "2026-02-01T00:00:00Z"),
	)
	check := func(q DupScan, want ...string) DupResult {
		t.Helper()
		got, err := s.Duplicates(q)
		if err != nil {
			t.Fatal(err)
		}
		if !sameIDs(groupHashes(got.Groups), want...) {
			t.Errorf("%+v: groups = %v, want %v", q, groupHashes(got.Groups), want)
		}
		return got
	}
	check(DupScan{}, hashOf("C"), hashOf("B"), hashOf("D"))
	check(DupScan{Sort: "copies"}, hashOf("B"), hashOf("C"), hashOf("D"))
	check(DupScan{Sort: "modified"}, hashOf("C"), hashOf("D"), hashOf("B"))
	same := check(DupScan{SameFolder: true}, hashOf("B"), hashOf("D"))
	for _, g := range same.Groups {
		copies, err := s.DupCopies([]string{g.Hash}, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range copies {
			if c.ParentID != copies[0].ParentID {
				t.Errorf("same-folder group %s has copies in %s and %s", g.Hash, c.ParentID, copies[0].ParentID)
			}
		}
	}
	small := check(DupScan{MinSize: 100}, hashOf("C"), hashOf("B"))
	if small.Small != 1 || small.SmallWaste != 3 {
		t.Errorf("small = %d / %d, want 1 group wasting 3", small.Small, small.SmallWaste)
	}
	// Under x only B is a group (c2 is in y); C has one copy there.
	check(DupScan{Root: "x"}, hashOf("B"))
	copies, err := s.DupCopies([]string{hashOf("B"), hashOf("C")}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(ids(copies), "b1", "b2", "b3", "c1") {
		t.Errorf("copies under x = %v", ids(copies))
	}
	if _, err := s.Duplicates(DupScan{Sort: "name; DROP TABLE files"}); err == nil {
		t.Error("an unknown sort must be refused")
	}
}

// Empty folder verification: a shell of empty folders is one top-level item
// carrying its nested count; a folder holding a file (at any depth) is not
// empty; special folders never count; a trashed entry inside is flagged.
func TestEmptyFoldersTopLevelOnly(t *testing.T) {
	s := openTemp(t)
	normal := func(id, parent string) Row {
		r := folder(id, parent, id)
		r.FolderType, r.CreatedTime = "NORMAL", "2026-01-01T00:00:00Z"
		return r
	}
	pack := normal("pack", "")
	pack.FolderType = "DOWNLOAD"
	binned := file("bin1", "i", "old.mkv", 5)
	binned.Trashed = true
	upsert(t, s,
		normal("a", ""), normal("b", "a"), normal("c", "b"), // shell A/B/C
		normal("d", ""), normal("dd", "d"), file("f1", "dd", "x.mkv", 1), // D holds a file two levels down
		normal("e", ""), normal("e1", "e"), file("f2", "e", "y.mkv", 1), // E holds a file; E/e1 is empty
		pack,                                      // special, empty
		normal("i", ""), normal("j", "i"), binned, // I holds only a trashed file
	)
	got, err := s.EmptyFolders("")
	if err != nil {
		t.Fatal(err)
	}
	var top []string
	byID := map[string]EmptyFolder{}
	for _, f := range got.Folders {
		top = append(top, f.ID)
		byID[f.ID] = f
	}
	if !sameIDs(top, "a", "e1", "i") {
		t.Fatalf("top level = %v, want a e1 i", top)
	}
	if got.Total != 6 {
		t.Errorf("total = %d, want 6 (a b c e1 i j)", got.Total)
	}
	if a := byID["a"]; a.Nested != 2 || a.HasTrashed {
		t.Errorf("a = %+v, want 2 nested and no trashed", a)
	}
	if i := byID["i"]; i.Nested != 1 || !i.HasTrashed {
		t.Errorf("i = %+v, want 1 nested and a trashed entry", i)
	}
	if byID["e1"].ParentID != "e" {
		t.Errorf("e1 = %+v", byID["e1"])
	}

	// Scoped to a: its empty descendants, never a itself.
	got, err = s.EmptyFolders("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Folders) != 1 || got.Folders[0].ID != "b" || got.Folders[0].Nested != 1 || got.Total != 2 {
		t.Errorf("under a = %+v", got)
	}
}

func TestTrashSummaryTreeSizeAndDeleteTree(t *testing.T) {
	s := openTemp(t)
	dir := folder("dir", "", "dir")
	dir.Trashed, dir.DeleteTime = true, "2026-11-01T00:00:00Z"
	inside := file("in1", "dir", "a.mkv", 30)
	inside.Trashed, inside.DeleteTime = true, dir.DeleteTime
	alone := file("solo", "", "b.mkv", 12)
	alone.Trashed, alone.DeleteTime = true, "2026-11-02T00:00:00Z"
	upsert(t, s, dir, inside, alone, file("live1", "", "c.mkv", 100))

	count, size, err := s.TrashSummary()
	if err != nil || count != 2 || size != 42 {
		t.Errorf("summary = %d, %d, %v; want 2 top-level items of 42 bytes", count, size, err)
	}
	if n, err := s.TreeSize([]string{"dir", "live1"}); err != nil || n != 130 {
		t.Errorf("tree size = %d, %v; want 130", n, err)
	}
	if n, err := s.DeleteTree([]string{"dir"}); err != nil || n != 2 {
		t.Errorf("delete tree = %d, %v; want the folder and its file", n, err)
	}
	rows, err := s.Rows([]string{"dir", "in1", "solo", "live1", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(rows)
	slices.Sort(got)
	if !sameIDs(got, "live1", "solo") {
		t.Errorf("rows left = %v", got)
	}
}
