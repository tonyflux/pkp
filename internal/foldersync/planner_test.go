package foldersync

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/syncfs"
)

func file(p, id, hash string) Node {
	return Node{Path: p, Name: p[strings.LastIndex(p, "/")+1:], ID: id, Hash: strings.Repeat(hash, 64), Size: 1}
}
func testPlan(mode string, l, r map[string]Node, base map[string]baseline, deletes bool) *plan {
	return makePlan(Config{ID: "pair", Account: "owner", Mode: mode, Deletes: map[string]bool{mode: deletes}}, syncfs.Identity{CaseSensitive: true}, &snapshot{Local: l, Cloud: r, Complete: true}, base)
}
func tracked(n Node) baseline {
	l, r := n, n
	l.ID = "local-" + n.ID
	r.ID = "cloud-" + n.ID
	return baseline{Local: new(l), Cloud: new(r)}
}

func TestInitialModesNeverInferDeletes(t *testing.T) {
	for _, mode := range []string{Bidirectional, Backup, Download} {
		t.Run(mode, func(t *testing.T) {
			l := map[string]Node{"local": file("local", "l", "a"), "different": file("different", "ld", "a"), "equal": file("equal", "le", "c")}
			r := map[string]Node{"cloud": file("cloud", "r", "b"), "different": file("different", "rd", "b"), "equal": file("equal", "re", "c")}
			p := testPlan(mode, l, r, nil, true)
			if p.preview.Deletes != 0 || p.preview.Conflicts != 1 {
				t.Fatalf("initial counts: %+v", p.preview)
			}
			for _, o := range p.operations {
				if o.Action == "trash" {
					t.Fatal("initial deletion")
				}
				if mode == Backup && o.Direction == "download" || mode == Download && o.Direction == "upload" {
					t.Fatal("reverse single-way write")
				}
			}
			if mode == Bidirectional && (p.preview.Uploads != 1 || p.preview.Downloads != 1) {
				t.Fatal("bidirectional did not take union")
			}
		})
	}
}
func TestTargetEditsAreNeverSilentlyOverwritten(t *testing.T) {
	for _, mode := range []string{Backup, Download} {
		t.Run(mode, func(t *testing.T) {
			b := tracked(file("x", "id", "a"))
			l, r := *b.Local, *b.Cloud
			if mode == Backup {
				r.Hash = strings.Repeat("b", 64)
			} else {
				l.Hash = strings.Repeat("b", 64)
			}
			p := testPlan(mode, map[string]Node{"x": l}, map[string]Node{"x": r}, map[string]baseline{"x": b}, true)
			if len(p.operations) != 0 || len(p.issues) != 1 || p.issues[0].Kind != "target-edited" {
				t.Fatalf("target edit not preserved: %+v", p)
			}
			if mode == Backup {
				l.Hash = strings.Repeat("c", 64)
			} else {
				r.Hash = strings.Repeat("c", 64)
			}
			p = testPlan(mode, map[string]Node{"x": l}, map[string]Node{"x": r}, map[string]baseline{"x": b}, true)
			if len(p.operations) != 0 || p.preview.Conflicts != 1 {
				t.Fatal("later source edit overwrote changed target")
			}
		})
	}
}
func TestPreservedDeletionDoesNotResurrectAcrossPasses(t *testing.T) {
	b := tracked(file("x", "id", "a"))
	base := map[string]baseline{"x": b}
	r := map[string]Node{"x": *b.Cloud}
	for range 3 {
		p := testPlan(Bidirectional, map[string]Node{}, r, base, false)
		if len(p.operations) != 0 {
			t.Fatal("retained copy resurrected source")
		}
		if p.observed["x"].Preserved != "local" {
			t.Fatal("missing durable deletion decision")
		}
		base["x"] = p.observed["x"]
	}
	p := testPlan(Bidirectional, map[string]Node{"x": file("x", "new", "b")}, r, base, false)
	if p.preview.Conflicts != 1 {
		t.Fatal("new path event reused deletion decision")
	}
}
func TestDeleteGuardBoundaries(t *testing.T) {
	for _, tc := range []struct {
		d, n        int
		empty, want bool
	}{{100, 1000, false, true}, {20, 100, false, true}, {19, 100, false, false}, {20, 101, false, false}, {99, 1000, false, false}, {1, 1, true, true}, {0, 0, true, true}, {0, 0, false, false}} {
		if got := deleteGuard(tc.d, tc.n, tc.empty); got != tc.want {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}
func TestDirectoryDeletionCountsEveryTrackedDescendant(t *testing.T) {
	base := map[string]baseline{}
	cloud := map[string]Node{}
	dir := Node{Path: "folder", Name: "folder", ID: "d", Directory: true}
	b := tracked(dir)
	base["folder"] = b
	cloud["folder"] = *b.Cloud
	for i := range 100 {
		p := fmt.Sprintf("folder/%03d", i)
		b := tracked(file(p, p, "a"))
		base[p] = b
		cloud[p] = *b.Cloud
	}
	p := testPlan(Bidirectional, map[string]Node{}, cloud, base, true)
	if p.preview.Deletes != 100 || p.preview.DeleteDirectories != 1 || p.preview.Error == "" {
		t.Fatalf("directory bypassed guard: %+v", p.preview)
	}
}
func TestRenameRequiresUniqueIdentityAndContent(t *testing.T) {
	b := tracked(file("old", "id", "a"))
	moved := *b.Local
	moved.Path = "new"
	moved.Name = "new"
	p := testPlan(Bidirectional, map[string]Node{"new": moved}, map[string]Node{"old": *b.Cloud}, map[string]baseline{"old": b}, true)
	if len(p.operations) != 1 || p.operations[0].Action != "rename" {
		t.Fatal("proven rename was not propagated")
	}
	copy := moved
	copy.Path = "copy"
	copy.Name = "copy"
	p = testPlan(Bidirectional, map[string]Node{"new": moved, "copy": copy}, map[string]Node{"old": *b.Cloud}, map[string]baseline{"old": b}, true)
	for _, o := range p.operations {
		if o.Action == "rename" {
			t.Fatal("hardlink ambiguity treated as rename")
		}
	}
	moved.Hash = strings.Repeat("b", 64)
	p = testPlan(Bidirectional, map[string]Node{"new": moved}, map[string]Node{"old": *b.Cloud}, map[string]baseline{"old": b}, true)
	for _, o := range p.operations {
		if o.Action == "rename" {
			t.Fatal("reused identity with changed content treated as rename")
		}
	}
}
func TestCollisionAndSkippedSymlinkBlockDependentDeletion(t *testing.T) {
	b := tracked(file("x", "id", "a"))
	s := &snapshot{Local: map[string]Node{}, Cloud: map[string]Node{"x": *b.Cloud}, Skipped: []syncfs.Skipped{{Path: "x", Reason: "symlink"}}, Complete: true}
	p := makePlan(Config{ID: "p", Mode: Bidirectional, Deletes: map[string]bool{Bidirectional: true}}, syncfs.Identity{}, s, map[string]baseline{"x": b})
	if len(p.operations) != 0 {
		t.Fatal("skipped symlink interpreted as deletion")
	}
	p = testPlan(Bidirectional, map[string]Node{"A": file("A", "l", "a")}, map[string]Node{"a": file("a", "r", "b")}, nil, true)
	if p.preview.Blocked {
		t.Fatal("case-sensitive volume incorrectly blocked")
	}
	s.Local = map[string]Node{"A": file("A", "l", "a")}
	s.Cloud = map[string]Node{"a": file("a", "r", "b")}
	p = makePlan(Config{ID: "p", Mode: Bidirectional}, syncfs.Identity{CaseSensitive: false}, s, nil)
	if !p.preview.Blocked || len(p.operations) != 0 {
		t.Fatal("case collision silently merged")
	}
}
func TestDeleteApprovalVersionChangesWhenTargetChanges(t *testing.T) {
	b := tracked(file("x", "id", "a"))
	base := map[string]baseline{"x": b}
	r := map[string]Node{"x": *b.Cloud}
	p := testPlan(Bidirectional, map[string]Node{}, r, base, true)
	r["x"] = file("x", b.Cloud.ID, "b")
	next := testPlan(Bidirectional, map[string]Node{}, r, base, true)
	if next.preview.ID == p.preview.ID || next.preview.Conflicts != 1 || len(next.operations) != 0 {
		t.Fatal("stale deletion approval remained usable")
	}
}
func TestPreviewIdentifiersAndKeepNamesAreStable(t *testing.T) {
	l := map[string]Node{"x": file("x", "l", "a")}
	r := map[string]Node{"x": file("x", "r", "b")}
	a, b := testPlan(Bidirectional, l, r, nil, true), testPlan(Bidirectional, l, r, nil, true)
	if a.preview.ID != b.preview.ID || a.issues[0].KeepName != b.issues[0].KeepName {
		t.Fatal("preview identity changes without content changes")
	}
}
