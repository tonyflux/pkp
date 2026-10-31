package download

import "testing"

func TestPlanCoversFile(t *testing.T) {
	for _, tc := range []struct {
		size    int64
		threads int
		want    int
	}{
		{0, 8, 0},
		{1, 8, 1},
		{minPart - 1, 8, 1},
		{2*minPart + 5, 8, 2},
		{37<<20 + 123, 8, 8},
		{37<<20 + 123, 4, 4},
		{1 << 30, 1, 1},
	} {
		parts := plan(tc.size, tc.threads)
		if len(parts) != tc.want {
			t.Errorf("plan(%d, %d) = %d ranges, want %d", tc.size, tc.threads, len(parts), tc.want)
			continue
		}
		var at int64
		for i, p := range parts {
			if p.Start != at || p.Pos != p.Start || p.End <= p.Start || p.busy {
				t.Errorf("plan(%d, %d)[%d] = %+v after %d", tc.size, tc.threads, i, *p, at)
			}
			if i < len(parts)-1 && p.End%align != 0 {
				t.Errorf("plan(%d, %d)[%d] ends off the %d grid: %d", tc.size, tc.threads, i, align, p.End)
			}
			at = p.End
		}
		if at != tc.size {
			t.Errorf("plan(%d, %d) covers %d bytes", tc.size, tc.threads, at)
		}
	}
}

func TestNextSplitsLargestBusyPart(t *testing.T) {
	a := &attempt{j: &job{Task: Task{Size: 32 << 20}}}
	a.j.parts = plan(32<<20, 2)
	first, second := a.next(), a.next()
	if first == nil || second == nil || first == second || !first.busy || !second.busy {
		t.Fatalf("free ranges handed out as %v, %v", first, second)
	}
	first.Pos = first.End - 3<<20 // 3 MiB left, the second has 16

	third := a.next()
	if third == nil || third.Start%align != 0 || third.Start != second.End || third.End != 32<<20 || !third.busy {
		t.Fatalf("split off %+v from %+v", third, second)
	}
	if second.End-second.Pos < minSplit || third.End-third.Pos < minSplit {
		t.Fatalf("a half under %d: %+v, %+v", minSplit, *second, *third)
	}
	if got := left(a.j.parts); got != 3<<20+16<<20 {
		t.Fatalf("splitting changed what is left: %d", got)
	}

	// Ranges too short to halve stay with their threads.
	for _, p := range a.j.parts {
		p.Pos = p.End - (2*minSplit - 1)
	}
	if p := a.next(); p != nil {
		t.Fatalf("split a range under %d: %+v", 2*minSplit, *p)
	}
	// A range a thread let go is handed out again first.
	second.busy = false
	if p := a.next(); p != second {
		t.Fatalf("handed out %+v, not the free range", p)
	}
	// One connection for the whole file: nothing to share.
	second.busy = false
	a.single.Store(true)
	if p := a.next(); p != nil {
		t.Fatalf("handed out %+v without ranges", *p)
	}
}
