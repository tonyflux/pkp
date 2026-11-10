package upload

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
)

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// restartBackend finishes "done.bin" through a local OSS stand-in, rejects
// "fail.bin", and holds every other file until its job is cancelled — which
// is what quitting the app does to an upload in flight.
type restartBackend struct{ endpoint string }

func (b restartBackend) CreateUpload(ctx context.Context, p drive.UploadParams) (*drive.UploadSlot, error) {
	switch p.Name {
	case "done.bin":
		return &drive.UploadSlot{File: drive.File{ID: "f-done"}, OSS: &drive.OSSCredentials{
			Endpoint: b.endpoint, Bucket: "bucket", Key: "object", AccessKeyID: "k", AccessKeySecret: "s",
		}}, nil
	case "fail.bin":
		return nil, errors.New("server said no")
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (restartBackend) EnsureFolder(context.Context, string, string) (*drive.File, error) {
	return nil, errors.New("unexpected folder create")
}

func (restartBackend) Trash(context.Context, []string) error { return nil }

func TestJournalSurvivesRestart(t *testing.T) {
	oss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("ETag", `"stored"`)
	}))
	defer oss.Close()

	dir := t.TempDir()
	journal := filepath.Join(dir, "uploads.json")
	src := map[string]string{}
	for _, name := range []string{"done.bin", "fail.bin", "slow.bin", "later.bin", "new.bin"} {
		src[name] = filepath.Join(dir, name)
		if err := os.WriteFile(src[name], []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	events := make(chan Task, 64)
	q := New(restartBackend{endpoint: oss.URL}, 1, func(tk Task) { events <- tk })
	q.Open(journal, quietLog)
	add := func(q *Queue, name string) Task {
		t.Helper()
		task, err := q.Add(src[name], Target{SavedTo: "全部文件"})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	waitFor(t, events, add(q, "done.bin").ID, func(tk Task) bool { return tk.State == StateDone })
	waitFor(t, events, add(q, "fail.bin").ID, func(tk Task) bool { return tk.State == StateFailed })
	slow := add(q, "slow.bin")
	waitFor(t, events, slow.ID, func(tk Task) bool { return tk.State == StateUploading })
	later := add(q, "later.bin") // behind slow.bin: one file at a time
	q.Close()

	// The journal is all a new session has.
	q2 := New(restartBackend{endpoint: oss.URL}, 1, func(tk Task) { events <- tk })
	q2.Open(journal, quietLog)
	got := map[string]Task{}
	for _, tk := range q2.List() {
		got[tk.Name] = tk
	}
	want := map[string]string{
		"done.bin":  StateDone,
		"fail.bin":  StateFailed,
		"slow.bin":  StateInterrupted, // running when the app quit
		"later.bin": StateInterrupted, // still queued when the app quit
	}
	for name, state := range want {
		if got[name].State != state {
			t.Errorf("%s came back %q, want %q", name, got[name].State, state)
		}
	}
	if got["done.bin"].FileID != "f-done" || got["fail.bin"].Error != "server said no" || got["slow.bin"].SavedTo != "全部文件" {
		t.Errorf("records lost their details: %+v", got)
	}

	// New jobs must not reuse an id from the earlier session.
	if fresh := add(q2, "new.bin"); seqOf(fresh.ID) <= seqOf(later.ID) {
		t.Errorf("new job id %s collides with the restored ones (last %s)", fresh.ID, later.ID)
	}

	// Retrying looks at the file again: a vanished file is refused, not queued.
	if err := os.Remove(src["later.bin"]); err != nil {
		t.Fatal(err)
	}
	if err := q2.Retry(later.ID); err == nil {
		t.Error("retrying an upload whose file is gone must fail")
	}
	if err := q2.Retry(slow.ID); err != nil {
		t.Fatalf("retrying an interrupted upload: %v", err)
	}
	waitFor(t, events, slow.ID, func(tk Task) bool { return tk.State == StateQueued || tk.State == StateUploading })
	q2.Close()
}

func TestJournalUnreadableIsMovedAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uploads.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := New(restartBackend{}, 1, nil)
	q.Open(path, quietLog)
	if n := len(q.List()); n != 0 {
		t.Fatalf("restored %d jobs from garbage", n)
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("the unreadable journal was not kept aside: %v", err)
	}
	q.Close()
	if tasks, _, err := readJournal(path); err != nil || len(tasks) != 0 {
		t.Fatalf("after close the journal should be valid and empty, got %d tasks, err %v", len(tasks), err)
	}
}

func TestRecordsKeepNewestFinishedOnly(t *testing.T) {
	rs := []record{
		{Task: Task{ID: "u1", State: StateDone, UpdatedAt: "2026-10-12T01:00:00Z", Done: 5, Speed: 9}},
		{Task: Task{ID: "u2", State: StateFailed, UpdatedAt: "2026-09-01T00:00:00Z", Done: 7}, Resume: &resumeState{FileID: "p2"}},
		{Task: Task{ID: "u3", State: StateDone, UpdatedAt: "2026-10-12T03:00:00Z"}},
		{Task: Task{ID: "u4", State: StateDone, UpdatedAt: "2026-10-12T02:00:00Z"}},
	}
	got := records(rs, 2)
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
		if r.Speed != 0 || (r.Resume == nil && r.Done != 0) {
			t.Errorf("%s kept byte counters: %+v", r.ID, r.Task)
		}
	}
	// The oldest finished upload goes; a failure stays however old it is,
	// and with its slot, how much of it is already up.
	if want := []string{"u2", "u3", "u4"}; len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Errorf("kept %v, want %v", ids, want)
	}
	if got[0].Done != 7 || got[0].Resume == nil || got[0].Resume.FileID != "p2" {
		t.Errorf("a resumable failure lost its slot or its sent bytes: %+v", got[0])
	}
}

// The same rule for uploads: the assistant's saved card names u<number>,
// and a restart must never give that number to another file.
func TestQueueDoesNotReuseIDsAfterRestart(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "uploads.json")
	src := filepath.Join(dir, "fail.bin")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	q := New(restartBackend{}, 1, nil)
	q.Open(journal, quietLog)
	first, err := q.Add(src, Target{})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "u1" {
		t.Fatalf("first id %q", first.ID)
	}
	if err := q.Remove(first.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	q.Close()

	restored := New(restartBackend{}, 1, nil)
	restored.Open(journal, quietLog)
	defer restored.Close()
	next, err := restored.Add(src, Target{})
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != "u2" {
		t.Fatalf("restart gave %q: the removed u1 must not come back", next.ID)
	}
}
