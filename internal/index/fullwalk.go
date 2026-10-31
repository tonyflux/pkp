package index

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
)

// walkWorkers is how many pages a full walk keeps in flight. The server is
// the bound, not the client: 400-entry whole-tree pages took 0.5–4.6 s and
// gave 0.4 pages/s one at a time, 0.7 with 4 at once and 0.8 with 8
// (measured 2026-10-17 on a 224k-entry drive).
const walkWorkers = 4

// minSplitPages is how many pages a stretch must still look to hold before
// a free worker takes half of it: each split costs up to one page read
// twice, where the two halves meet.
const minSplitPages = 4

// keyLayout is how the server writes sort keys (2026-10-16T18:44:39.250+08:00).
const keyLayout = "2006-01-02T15:04:05.000Z07:00"

// walkState is a full walk under way. It is kept under MetaWalk after every
// page, and the rows it wrote in walk_seen, so a walk cut short (quit,
// sleep, a network failure) goes on from where it was.
//
// The whole-tree listing is ordered by a time sort key that page tokens
// carry and SyncCursor can aim anywhere, so the walk is cut into shards of
// that order, read side by side: a worker that runs out of work splits the
// shard with the most pages left in two at its middle.
type walkState struct {
	Shards  []*shard `json:"shards"`
	Trashed int      `json:"trashed"`
	Version string   `json:"version"`
	// SyncTime is the high-water mark the drive's end reported.
	SyncTime string `json:"syncTime"`
}

// shard is one stretch of the sort order, read from Token up to End ("" =
// the drive's end).
type shard struct {
	Token string `json:"token"`
	// Pos is the sort key Token resumes at; "" while not known.
	Pos string `json:"pos"`
	End string `json:"end"`
	// Step is how far (ms) the last page moved Pos: the remaining pages
	// are estimated from it, as if the stretch were evenly dense.
	Step int64 `json:"step"`
	// Parked is the open shard after reaching the drive's end while others
	// still run. It reads once more after them: entries changed meanwhile
	// (moved, renamed, restored) take a new key past that end, and a walk
	// that missed them would delete them.
	Parked bool `json:"parked,omitempty"`
}

func parseKey(key string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, key)
	return t, err == nil
}

// left is how many pages sh still looks to hold; false when unknown.
func (sh *shard) left(now time.Time) (float64, time.Time, time.Time, bool) {
	from, ok := parseKey(sh.Pos)
	if !ok || sh.Step <= 0 || sh.Parked {
		return 0, time.Time{}, time.Time{}, false
	}
	to := now
	if sh.End != "" {
		if to, ok = parseKey(sh.End); !ok {
			return 0, time.Time{}, time.Time{}, false
		}
	}
	if !to.After(from) {
		return 0, from, to, true
	}
	return float64(to.Sub(from).Milliseconds()) / float64(sh.Step), from, to, true
}

// split hands the second half of the shard with the most pages left to a
// new shard, or returns nil when none is worth splitting.
func (st *walkState) split(now time.Time) *shard {
	var best *shard
	var most float64
	var from, to time.Time
	for _, sh := range st.Shards {
		if pages, f, t, ok := sh.left(now); ok && pages > most {
			best, most, from, to = sh, pages, f, t
		}
	}
	if best == nil || most < minSplitPages {
		return nil
	}
	mid := from.Add(to.Sub(from) / 2).In(from.Location()).Format(keyLayout)
	sh := &shard{Token: drive.SyncCursor(mid), Pos: mid, End: best.End}
	best.End = mid
	st.Shards = append(st.Shards, sh)
	return sh
}

// reached is the sort key before which every entry has been read: the
// least position of the shards still open. "" while a shard's is unknown.
func (st *walkState) reached() string {
	least, at := "", time.Time{}
	for _, sh := range st.Shards {
		t, ok := parseKey(sh.Pos)
		if !ok {
			return ""
		}
		if least == "" || t.Before(at) {
			least, at = sh.Pos, t
		}
	}
	return least
}

// apply takes one page of sh into the state: where sh goes on from, or
// that it is finished.
func (st *walkState) apply(sh *shard, list *drive.FileList) {
	for _, f := range list.Files {
		if f.Trashed {
			st.Trashed++
		}
	}
	if list.Version != "" {
		st.Version = list.Version
	}
	if sh.End == "" && list.SyncTime != "" {
		st.SyncTime = list.SyncTime
	}
	key := drive.CursorTime(list.NextPageToken)
	if list.NextPageToken == "" {
		if sh.End != "" {
			st.drop(sh)
			return
		}
		// An empty last page carries no sync time: the open shard's own
		// position is the drive's end then.
		if list.SyncTime == "" && sh.Pos != "" {
			st.SyncTime = sh.Pos
		}
		if len(st.Shards) == 1 {
			st.drop(sh)
			return
		}
		sh.Parked = true
		if st.SyncTime != "" {
			sh.Token, sh.Pos = drive.SyncCursor(st.SyncTime), st.SyncTime
		}
		return
	}
	if sh.End != "" {
		k, ok1 := parseKey(key)
		e, ok2 := parseKey(sh.End)
		if ok1 && ok2 && !k.Before(e) {
			st.drop(sh)
			return
		}
	}
	sh.Step = 0
	if from, ok := parseKey(sh.Pos); ok {
		if to, ok := parseKey(key); ok && to.After(from) {
			sh.Step = to.Sub(from).Milliseconds()
		}
	}
	sh.Token, sh.Pos = list.NextPageToken, key
}

func (st *walkState) drop(sh *shard) {
	st.Shards = slices.DeleteFunc(st.Shards, func(s *shard) bool { return s == sh })
}

// loadWalk reads the walk to resume, or starts one.
func (s *Syncer) loadWalk() (*walkState, error) {
	raw, err := s.store.Meta(MetaWalk)
	if err != nil {
		return nil, err
	}
	if raw != "" {
		var st walkState
		if json.Unmarshal([]byte(raw), &st) == nil && len(st.Shards) > 0 {
			seen, _ := s.store.WalkSeen()
			s.log.Info("index: resuming full walk", "rows", seen, "shards", len(st.Shards))
			return &st, nil
		}
	}
	if err := s.store.BeginWalk(); err != nil {
		return nil, err
	}
	st := &walkState{Shards: []*shard{{}}}
	return st, s.saveWalk(st)
}

func (s *Syncer) saveWalk(st *walkState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.store.SetMeta(MetaWalk, string(raw))
}

// publishWalk reports a full walk: rows seen, how far through the sort
// order it has read, and — when an earlier walk completed, so the index's
// size stands for the drive's — the share done.
func (s *Syncer) publishWalk(st *walkState) {
	done, _ := s.store.WalkSeen()
	p := s.snapshot("full", done, "")
	if p.Ready && p.Count > 0 {
		p.Percent = min(99, 100*done/p.Count)
	}
	p.Reached = st.reached()
	s.setProgress(p)
}

// fullWalk lists the whole tree, walkWorkers pages at a time, upserting
// every page and checkpointing after it; a failure leaves the checkpoint
// for the next walk to resume. The table is never emptied: at the end
// whatever the walk did not see goes.
func (s *Syncer) fullWalk(ctx context.Context) (walkResult, error) {
	if err := ctx.Err(); err != nil {
		return walkResult{}, err
	}
	st, err := s.loadWalk()
	if err != nil {
		return walkResult{}, err
	}
	s.publishWalk(st)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type page struct {
		sh   *shard
		list *drive.FileList
		err  error
	}
	results := make(chan page)
	inflight := map[*shard]bool{}
	fetch := func(sh *shard) {
		inflight[sh] = true
		token := sh.Token
		go func() {
			if err := ctx.Err(); err != nil {
				results <- page{sh: sh, err: err}
				return
			}
			list, err := s.lister.ListFiles(ctx, drive.ListFilesParams{
				ParentID:      drive.WholeTree,
				PageToken:     token,
				ThumbnailSize: drive.ThumbnailMedium,
			})
			results <- page{sh, list, err}
		}()
	}
	dispatch := func() {
		parked := 0
		for _, sh := range st.Shards {
			switch {
			case sh.Parked:
				parked++
			case !inflight[sh] && len(inflight) < walkWorkers:
				fetch(sh)
			}
		}
		for len(inflight) < walkWorkers {
			sh := st.split(time.Now())
			if sh == nil {
				break
			}
			fetch(sh)
		}
		// Everything else is read: the drive's end once more.
		if len(inflight) == 0 && parked > 0 {
			for _, sh := range st.Shards {
				sh.Parked = false
				fetch(sh)
			}
		}
	}

	var failed error
	dispatch()
	for len(inflight) > 0 {
		r := <-results
		delete(inflight, r.sh)
		if failed != nil {
			continue // draining after a failure
		}
		if r.err == nil {
			if r.err = s.takePage(st, r.sh, r.list); r.err == nil {
				r.err = s.saveWalk(st)
			}
		}
		if r.err != nil {
			failed = r.err
			cancel()
			continue
		}
		s.publishWalk(st)
		dispatch()
	}
	if failed != nil {
		return walkResult{}, failed
	}

	now := time.Now().UTC().Format(time.RFC3339)
	gone, err := s.store.FinishWalk(now)
	if err != nil {
		return walkResult{}, err
	}
	if gone > 0 {
		s.log.Info("index: full walk removed stale rows", "rows", gone)
	}
	if err := s.finishPass(st.SyncTime, now); err != nil {
		return walkResult{}, err
	}
	seen, _ := s.store.Count()
	s.log.Info("index: walk complete", "phase", "full", "rows", seen, "trashed", st.Trashed)
	return walkResult{version: st.Version, trashed: st.Trashed}, nil
}

// takePage writes one page of sh and moves the state past it.
func (s *Syncer) takePage(st *walkState, sh *shard, list *drive.FileList) error {
	rows := make([]Row, 0, len(list.Files))
	for _, f := range list.Files {
		rows = append(rows, s.toRow(f))
	}
	if err := s.store.UpsertWalk(rows); err != nil {
		return err
	}
	st.apply(sh, list)
	return nil
}
