package index

import (
	"slices"
	"testing"
)

// statsRows: d/ (f1 3, f2 4, e/ (f3 10, f4 trashed 100)), g/ (f5 1), f6 2
// at the root; f3 is an image.
func statsRows(t *testing.T) *Store {
	s := openTemp(t)
	f3 := file("f3", "e", "f3", 10)
	f3.Category = "image"
	f4 := file("f4", "e", "f4", 100)
	f4.Trashed = true
	upsert(t, s,
		folder("d", "", "d"), file("f1", "d", "f1", 3), file("f2", "d", "f2", 4),
		folder("e", "d", "e"), f3, f4,
		folder("g", "", "g"), file("f5", "g", "f5", 1),
		file("f6", "", "f6", 2),
	)
	return s
}

func TestSummarizeAndLargest(t *testing.T) {
	s := statsRows(t)
	sum, err := s.Summarize("d")
	if err != nil {
		t.Fatal(err)
	}
	want := []CategoryCount{{"image", 1, 10}, {"video", 2, 7}}
	if sum.Files != 3 || sum.Folders != 1 || sum.Size != 17 || !slices.Equal(sum.Categories, want) {
		t.Errorf("summary of d = %+v", sum)
	}
	if sum, _ := s.Summarize(""); sum.Files != 5 || sum.Folders != 3 || sum.Size != 20 {
		t.Errorf("summary of the drive = %+v", sum)
	}
	if sum, _ := s.Summarize("g"); sum.Files != 1 || sum.Folders != 0 || sum.Categories == nil {
		t.Errorf("summary of g = %+v", sum)
	}

	got, err := s.Largest("", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "d" || got[0].Total != 17 || got[1].ID != "f6" || got[1].Total != 2 {
		t.Errorf("largest = %+v", got)
	}
	if got, _ := s.Largest("d", 5); len(got) != 3 || got[0].ID != "e" || got[0].Total != 10 || got[1].ID != "f2" {
		t.Errorf("largest in d = %+v", got)
	}
}

func TestWithinAndChildren(t *testing.T) {
	s := statsRows(t)
	got, err := s.Within("d", []string{"f1", "f3", "f4", "f5", "d", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"f1", "f3"}) {
		t.Errorf("within d = %v", got)
	}
	got, _ = s.Within("", []string{"f1", "f4", "f6", "d"})
	slices.Sort(got)
	if !slices.Equal(got, []string{"d", "f1", "f6"}) {
		t.Errorf("within the root = %v", got)
	}

	rows, err := s.Children("d", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(rows); !slices.Equal(got, []string{"e", "f1", "f2"}) {
		t.Errorf("children of d = %v", got)
	}
	if rows, _ := s.Children("e", 10); len(rows) != 1 {
		t.Errorf("children of e (one trashed) = %v", ids(rows))
	}
	if rows, _ := s.Children("d", 1); len(rows) != 2 {
		t.Errorf("limit 1 reads %d, want limit+1", len(rows))
	}
}

// A cover leads with pictures, then other files, then subfolders, newest
// first in each; trashed entries are neither shown nor counted.
func TestCovers(t *testing.T) {
	s := openTemp(t)
	thumb := func(r Row) Row { r.ThumbnailLink = "https://thumb/" + r.ID; return r }
	gone := thumb(file("e5", "p", "e5", 1))
	gone.Trashed = true
	sub := folder("s9", "p", "s9")
	sub.ModifiedTime = "2026-02-01T00:00:00Z" // newest of all, still last
	upsert(t, s, folder("p", "", "p"), folder("q", "", "q"), sub, gone,
		file("a1", "p", "a1", 1), thumb(file("b2", "p", "b2", 1)), file("c3", "p", "c3", 1), thumb(file("d4", "p", "d4", 1)))

	got, err := s.Covers([]string{"p", "q", "p"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if p := got["p"]; p.Children != 5 || !slices.Equal(ids(p.Items), []string{"d4", "b2", "c3"}) {
		t.Errorf("cover of p = %d %v", p.Children, ids(p.Items))
	}
	if q, ok := got["q"]; !ok || q.Children != 0 || len(q.Items) != 0 {
		t.Errorf("cover of the empty q = %+v (present %v)", q, ok)
	}
	all, _ := s.Covers([]string{"p"}, 10)
	if !slices.Equal(ids(all["p"].Items), []string{"d4", "b2", "c3", "a1", "s9"}) {
		t.Errorf("whole cover order of p = %v", ids(all["p"].Items))
	}
}
