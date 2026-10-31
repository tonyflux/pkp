package upload

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// journalDelay coalesces a burst of state changes — a folder of small
	// files finishing one after another — into one write.
	journalDelay = time.Second
	// keepDone bounds the finished history kept across restarts. Failed,
	// cancelled and interrupted records stay until the user clears them.
	keepDone = 1000
	// journalVersion is bumped when the file's meaning changes.
	journalVersion = 1
)

type journalFile struct {
	Version int      `json:"version"`
	Tasks   []record `json:"tasks"`
}

// record is one job as the journal keeps it: the task, and the slot an
// unfinished upload can continue in. The slot holds an OSS grant, which is
// why it lives here and not in the Task the webview gets.
type record struct {
	Task
	Resume *resumeState `json:"resume,omitempty"`
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
// file current from now on. Uploads that were still queued or running when
// that session ended come back interrupted: nothing restarts on its own. A
// file that cannot be read is moved aside rather than overwritten. The OSS
// checkpoints of resumable uploads live in a directory beside it.
func (q *Queue) Open(path string, log *slog.Logger) {
	rs, err := readJournal(path)
	if err != nil {
		log.Warn("upload: journal unreadable, starting empty", "path", path, "error", err)
		if err := os.Rename(path, path+".bad"); err != nil {
			log.Warn("upload: could not move the unreadable journal aside", "error", err)
		}
		rs = nil
	}

	q.mu.Lock()
	q.checkpoints = strings.TrimSuffix(path, filepath.Ext(path)) + ".resume"
	for _, r := range rs {
		t := r.Task
		n := seqOf(t.ID)
		if n == 0 || q.jobs[t.ID] != nil {
			continue
		}
		if t.Active() {
			t.State = StateInterrupted
		}
		q.jobs[t.ID] = &job{Task: t, resume: r.Resume}
		q.seq = max(q.seq, n)
	}
	// Checkpoints of jobs without a kept slot are left over from a crash
	// or a removal that did not get to clean up.
	var stale []string
	if entries, err := os.ReadDir(q.checkpoints); err == nil {
		for _, e := range entries {
			if j := q.jobs[e.Name()]; j == nil || j.resume == nil {
				stale = append(stale, filepath.Join(q.checkpoints, e.Name()))
			}
		}
	}
	q.mu.Unlock()
	for _, dir := range stale {
		_ = os.RemoveAll(dir)
	}

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
		jn.log.Warn("upload: journal not saved", "path", jn.path, "error", err)
	}
}

// snapshot is every job as the journal keeps it, in the order added.
func (q *Queue) snapshot() []record {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]record, 0, len(q.jobs))
	for _, j := range q.jobs {
		out = append(out, record{Task: q.view(j), Resume: j.resume})
	}
	sort.Slice(out, func(a, b int) bool { return seqOf(out[a].ID) < seqOf(out[b].ID) })
	return out
}

// records is what the journal keeps: every job's last state without the
// speed, and without the byte count unless a kept slot makes it mean
// something after a restart; only the newest keep finished uploads stay,
// so the file and the list remain bounded.
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
		r.Speed = 0
		if r.Resume == nil {
			r.Done = 0
		}
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
		return nil, fmt.Errorf("upload: journal version %d is not %d", f.Version, journalVersion)
	}
	return f.Tasks, nil
}

// writeJournal replaces path atomically: a crash mid-write leaves the
// previous journal, never half of a new one. Owner-only: kept slots carry
// OSS grants.
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
