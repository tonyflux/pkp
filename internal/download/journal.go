package download

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"
)

const (
	// journalDelay coalesces a burst of state changes — a folder of small
	// files finishing one after another — into one write.
	journalDelay = time.Second
	// keepDone bounds the finished history kept across restarts. Failed,
	// paused, cancelled and interrupted records stay until cleared.
	keepDone = 1000
	// journalVersion is bumped when the file's meaning changes.
	journalVersion = 1
)

type journalFile struct {
	Version int      `json:"version"`
	Tasks   []record `json:"tasks"`
}

// record is one job as the journal keeps it: the task, and the ranges an
// unfinished download still has to fetch.
type record struct {
	Task
	Parts []part `json:"parts,omitempty"`
}

// journal is where an opened queue keeps its records between sessions.
type journal struct {
	path    string
	log     *slog.Logger
	pending bool
	// closed is set by the queue's final write; nothing writes after it, so
	// a late job cannot clobber what the next session has already read.
	closed bool
}

// Open restores the records an earlier session saved at path and keeps the
// file current from now on. Downloads that were queued or running when that
// session ended come back interrupted, with what they had on disk: nothing
// restarts on its own. A file that cannot be read is moved aside rather than
// overwritten.
func (q *Queue) Open(path string, log *slog.Logger) {
	rs, err := readJournal(path)
	if err != nil {
		log.Warn("download: journal unreadable, starting empty", "path", path, "error", err)
		if err := os.Rename(path, path+".bad"); err != nil {
			log.Warn("download: could not move the unreadable journal aside", "error", err)
		}
		rs = nil
	}

	q.mu.Lock()
	for _, r := range rs {
		t := r.Task
		n := seqOf(t.ID)
		if n == 0 || q.jobs[t.ID] != nil {
			continue
		}
		if t.Active() {
			t.State = StateInterrupted
		}
		t.Speed, t.Threads, t.Resumable = 0, 0, false
		j := &job{Task: t}
		if r.Parts != nil {
			j.parts = make([]*part, len(r.Parts))
			for i := range r.Parts {
				j.parts[i] = &r.Parts[i]
			}
		}
		q.jobs[t.ID] = j
		q.seq = max(q.seq, n)
	}
	q.mu.Unlock()

	q.jmu.Lock()
	q.journal = &journal{path: path, log: log}
	q.jmu.Unlock()
}

// touchJournal schedules a write. It must not be called while holding mu:
// the write takes jmu, then mu.
func (q *Queue) touchJournal() {
	q.jmu.Lock()
	defer q.jmu.Unlock()
	if q.journal == nil || q.journal.closed || q.journal.pending {
		return
	}
	q.journal.pending = true
	time.AfterFunc(journalDelay, q.saveJournal)
}

func (q *Queue) saveJournal() {
	q.jmu.Lock()
	defer q.jmu.Unlock()
	q.writeJournalLocked()
}

// writeJournalLocked writes the records now; jmu must be held.
func (q *Queue) writeJournalLocked() {
	jn := q.journal
	if jn == nil || jn.closed {
		return
	}
	jn.pending = false
	if err := writeJournal(jn.path, records(q.snapshot(), keepDone)); err != nil {
		jn.log.Warn("download: journal not saved", "path", jn.path, "error", err)
	}
}

// snapshot is every job as the journal keeps it, in the order added.
func (q *Queue) snapshot() []record {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]record, 0, len(q.jobs))
	for _, j := range q.jobs {
		r := record{Task: j.Task}
		j.pmu.Lock()
		if j.parts != nil {
			r.Parts = make([]part, len(j.parts))
			for i, p := range j.parts {
				r.Parts[i] = *p
			}
		}
		j.pmu.Unlock()
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool { return seqOf(out[a].ID) < seqOf(out[b].ID) })
	return out
}

// records is what the journal keeps: every job's last state without its
// speed and connections, and only the newest keep finished downloads, so the
// file and the list stay bounded.
func records(rs []record, keep int) []record {
	var done []int
	for i := range rs {
		if rs[i].State == StateDone {
			done = append(done, i)
		}
	}
	drop := map[int]bool{}
	if len(done) > keep {
		// RFC 3339 in UTC, as stamp writes it, sorts as text.
		sort.SliceStable(done, func(a, b int) bool { return rs[done[a]].UpdatedAt > rs[done[b]].UpdatedAt })
		for _, i := range done[keep:] {
			drop[i] = true
		}
	}
	out := make([]record, 0, len(rs)-len(drop))
	for i, r := range rs {
		if drop[i] {
			continue
		}
		r.Speed, r.Threads = 0, 0
		out = append(out, r)
	}
	return out
}

func readJournal(path string) ([]record, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f journalFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Version != journalVersion {
		return nil, fmt.Errorf("download: journal version %d is not %d", f.Version, journalVersion)
	}
	return f.Tasks, nil
}

// writeJournal replaces path atomically: a crash mid-write leaves the
// previous journal, never half of a new one. Owner-only, like the rest of
// the account's files.
func writeJournal(path string, rs []record) error {
	data, err := json.Marshal(journalFile{Version: journalVersion, Tasks: rs})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
