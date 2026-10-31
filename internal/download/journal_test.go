package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
)

// noBackend is a queue that is never asked for a link.
type noBackend struct{}

func (noBackend) GetFile(context.Context, string, drive.GetFileParams) (*drive.File, error) {
	return nil, errors.New("unexpected link request")
}

func TestJournalRestoreRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	half := []part{{Start: 0, End: 10, Pos: 5}}
	if err := writeJournal(path, []record{
		{Task: Task{ID: "d1", FileID: "a", State: StateDone, Size: 10, Done: 10}},
		{Task: Task{ID: "d2", FileID: "b", State: StateDownloading, Size: 10, Done: 5, Speed: 9, Threads: 4}, Parts: half},
		{Task: Task{ID: "d3", FileID: "c", State: StatePaused, Size: 10, Done: 5}, Parts: half},
		{Task: Task{ID: "d4", FileID: "d", State: StateQueued, Size: 10}},
		{Task: Task{ID: "d7", FileID: "e", State: StateFailed, Size: 10, Error: "磁盘空间不足"}, Parts: []part{{End: 10}}},
		{Task: Task{ID: "x9", FileID: "f", State: StateDone}},
	}); err != nil {
		t.Fatal(err)
	}
	q := New(noBackend{}, Options{})
	q.Open(path, quietLog)
	defer q.Close()

	type want struct {
		state     string
		resumable bool
	}
	wants := map[string]want{
		"d1": {StateDone, false},
		"d2": {StateInterrupted, true}, // downloading when the app quit
		"d3": {StatePaused, true},
		"d4": {StateInterrupted, false}, // queued, nothing on disk
		"d7": {StateFailed, false},      // failed before a byte landed
	}
	got := q.List()
	if len(got) != len(wants) {
		t.Fatalf("restored %d records: %+v", len(got), got)
	}
	for _, tk := range got {
		w := wants[tk.ID]
		if tk.State != w.state || tk.Resumable != w.resumable || tk.Speed != 0 || tk.Threads != 0 {
			t.Errorf("%s came back %+v, want %+v", tk.ID, tk, w)
		}
	}
	if got[4].Error != "磁盘空间不足" || got[1].Done != 5 {
		t.Errorf("records lost their details: %+v", got)
	}
	if q.seq != 7 {
		t.Errorf("next id after d%d, want after d7", q.seq)
	}

	// Only the newest finished downloads are kept; the rest stay however old.
	rs := records([]record{
		{Task: Task{ID: "d1", State: StateDone, UpdatedAt: "2026-10-12T01:00:00Z", Speed: 3}},
		{Task: Task{ID: "d2", State: StatePaused, UpdatedAt: "2026-09-01T00:00:00Z", Done: 7, Threads: 4}, Parts: half},
		{Task: Task{ID: "d3", State: StateDone, UpdatedAt: "2026-10-12T03:00:00Z"}},
		{Task: Task{ID: "d4", State: StateDone, UpdatedAt: "2026-10-12T02:00:00Z"}},
	}, 2)
	if len(rs) != 3 || rs[0].ID != "d2" || rs[1].ID != "d3" || rs[2].ID != "d4" {
		t.Fatalf("kept %+v", rs)
	}
	if rs[0].Done != 7 || len(rs[0].Parts) != 1 || rs[0].Threads != 0 {
		t.Fatalf("a paused download lost what it had: %+v", rs[0])
	}

	// An unreadable journal is moved aside, not overwritten.
	bad := filepath.Join(t.TempDir(), "downloads.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	q2 := New(noBackend{}, Options{})
	q2.Open(bad, quietLog)
	if n := len(q2.List()); n != 0 {
		t.Fatalf("restored %d records from garbage", n)
	}
	if _, err := os.Stat(bad + ".bad"); err != nil {
		t.Fatalf("the unreadable journal was not kept aside: %v", err)
	}
	q2.Close()
	if rs, err := readJournal(bad); err != nil || len(rs) != 0 {
		t.Fatalf("after close the journal is %d records, %v", len(rs), err)
	}
}
