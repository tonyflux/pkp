package services

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/tonyflux/pkp/internal/index"
)

func entryIDs(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	slices.Sort(out)
	return out
}

func sorted(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

// planTreeRows is a small drive:
//
//	Movies/ (A)          a1.mkv
//	  US TV Series/ (B)        b1.mkv b2.mkv
//	    Season 1/ (C)  c1.mkv
//	  US TV Series/ (B2)       b3.mkv        (a second folder of the same name)
//	Downloads/ (D)          d1.mkv, t1.mkv (trashed)
//	  Dune/ (E)        e1.mkv e2.mkv
//	Amélie/ (G, NFD)   g1.mkv
//	r1.mkv
func planTreeRows() []index.Row {
	trashed := indexedVideo("t1", "D", "t1.mkv", 1, "2026-01-01T00:00:00Z")
	trashed.Trashed = true
	return []index.Row{
		indexedFolder("A", "", "影视"), indexedVideo("a1", "A", "a1.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedFolder("B", "A", "美剧"), indexedVideo("b1", "B", "b1.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("b2", "B", "b2.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedFolder("C", "B", "Season 1"), indexedVideo("c1", "C", "c1.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedFolder("B2", "A", "美剧"), indexedVideo("b3", "B2", "b3.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedFolder("D", "", "下载"), indexedVideo("d1", "D", "d1.mkv", 1, "2026-01-01T00:00:00Z"), trashed,
		indexedFolder("E", "D", "Dune"), indexedVideo("e1", "E", "e1.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("e2", "E", "e2.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedFolder("G", "", "Ame\u0301lie"), indexedVideo("g1", "G", "g1.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("r1", "", "r1.mkv", 1, "2026-01-01T00:00:00Z"),
	}
}

func TestPlanTreeAncestorsSiblingsSubtreesAndLayers(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	fillIndex(t, svc, planTreeRows())

	// A file deep down, and target paths: one existing (case aside, both
	// folders named US TV Series followed), one half there, one under an NFD name.
	got, err := svc.PlanTree("", "", []string{"b1"},
		[][]string{{"影视", "美剧", "season 1"}, {"影视", "纪录片", "海洋"}, {"Amélie"}})
	if err != nil {
		t.Fatal(err)
	}
	want := sorted(
		"A", "D", "G", "r1", // the scope's children
		"B", "a1", "B2", // A's children (B is b1's ancestor)
		"b1", "b2", "C", // B's children
		"b3", // B2's: a layer of the first path
		"c1", // Season 1's
		"g1", // Amélie's
	)
	if ids := entryIDs(got); !slices.Equal(ids, want) {
		t.Errorf("tree = %v, want %v", ids, want)
	}
	for _, e := range got {
		if e.ID == "c1" && e.ParentID != "C" || e.ID == "A" && e.ParentID != "" {
			t.Errorf("%s has parent %q", e.ID, e.ParentID)
		}
	}

	// A folder within a scope: the scope's live children, the folder and
	// all of it; nothing above the scope.
	got, err = svc.PlanTree("", "D", []string{"E"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(got); !slices.Equal(ids, sorted("d1", "E", "e1", "e2")) {
		t.Errorf("folder tree = %v", ids)
	}
}

func TestPlanTreeRefusals(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	rows := planTreeRows()
	rows = append(rows, indexedFolder("big", "", "big"))
	for i := range maxTree + 1 {
		rows = append(rows, indexedVideo(fmt.Sprint("v", i), "big", fmt.Sprint("v", i, ".mkv"), 1, "2026-01-01T00:00:00Z"))
	}
	fillIndex(t, svc, rows)

	for _, c := range []struct {
		scope string
		ids   []string
		want  error
	}{
		{"", []string{"b1", "nope"}, errNotIndexed}, // not synced yet: try again later
		{"D", []string{"a1"}, errOutOfScope},        // lives elsewhere
		{"D", []string{"D"}, errOutOfScope},         // the scope itself
		{"D", []string{"t1"}, errOutOfScope},        // trashed
		{"t1", nil, errNoSuchFolder},
		{"missing", nil, errNoSuchFolder},
		{"", []string{"big"}, errTreeTooLarge},
		{"big", nil, errTreeTooLarge}, // a level no plan can cover
	} {
		if _, err := svc.PlanTree("", c.scope, c.ids, nil); !errors.Is(err, c.want) {
			t.Errorf("scope %q ids %v: %v, want %v", c.scope, c.ids, err, c.want)
		}
	}
}
