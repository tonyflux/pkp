package index

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
)

// treeLister serves a whole-tree listing the way the server does: entries
// ordered by (sort key, id), pages resuming after the token's position, and
// a token built by SyncCursor (no id) starting at its time inclusive. Trash
// listings are empty.
type treeLister struct {
	mu       sync.Mutex
	items    []treeItem
	pageSize int
	fail     func(call int) error
	// onEnd runs (under mu) once, when the open shard — the one starting
	// furthest on — first reaches the drive's end.
	onEnd    func(l *treeLister)
	ended    bool
	maxStart time.Time
	calls    int
	starts   []string
	served   map[string]bool
	running  int
	peak     int
}

type treeItem struct {
	key time.Time
	id  string
}

func (l *treeLister) sort() {
	slices.SortFunc(l.items, func(a, b treeItem) int {
		if c := a.key.Compare(b.key); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
}

// treeToken is the server's token: "<n>." (n counting pages) and the JSON
// position.
func treeToken(n int, key time.Time, id string) string {
	raw, _ := json.Marshal(map[string]any{"kind": "modified_time_asc", "data": map[string]string{"modified_time": key.Format(keyLayout), "id": id}})
	return fmt.Sprintf("%d.", n) + base64.RawURLEncoding.EncodeToString(raw)
}

func (l *treeLister) ListFiles(_ context.Context, p drive.ListFilesParams) (*drive.FileList, error) {
	if p.Filters != nil {
		return &drive.FileList{}, nil
	}
	l.mu.Lock()
	l.calls++
	call := l.calls
	l.running++
	l.peak = max(l.peak, l.running)
	var start time.Time
	var startID string
	if p.PageToken != "" {
		_, b, _ := strings.Cut(p.PageToken, ".")
		raw, _ := base64.RawURLEncoding.DecodeString(b)
		var c struct {
			Data struct {
				ModifiedTime string `json:"modified_time"`
				ID           string `json:"id"`
			} `json:"data"`
		}
		json.Unmarshal(raw, &c)
		start, _ = time.Parse(time.RFC3339Nano, c.Data.ModifiedTime)
		startID = c.Data.ID
	}
	l.starts = append(l.starts, p.PageToken)
	if start.After(l.maxStart) {
		l.maxStart = start
	}
	// Every shard but the open one answers slowly, so the open one reaches
	// the drive's end while the others still run.
	slow := start.Before(l.maxStart)
	l.mu.Unlock()
	if slow {
		time.Sleep(3 * time.Millisecond)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.running--
	if l.fail != nil {
		if err := l.fail(call); err != nil {
			return nil, err
		}
	}
	i := 0
	if p.PageToken != "" {
		i = slices.IndexFunc(l.items, func(it treeItem) bool {
			if c := it.key.Compare(start); c != 0 {
				return c > 0
			}
			return startID == "" || it.id > startID
		})
		if i < 0 {
			i = len(l.items)
		}
	}
	end := min(i+l.pageSize, len(l.items))
	list := &drive.FileList{}
	for _, it := range l.items[i:end] {
		f := df(it.id, "", it.id, false)
		f.ModifiedTime = it.key.Format(keyLayout)
		list.Files = append(list.Files, f)
		l.served[it.id] = true
	}
	if end > i {
		last := l.items[end-1]
		list.SyncTime = last.key.Format(keyLayout)
		if end < len(l.items) {
			list.NextPageToken = treeToken(call+2, last.key, last.id)
		}
	}
	if list.NextPageToken == "" && !start.Before(l.maxStart) && !l.ended && l.onEnd != nil {
		l.ended = true
		l.onEnd(l)
	}
	return list, nil
}

func denseTree(n int) *treeLister {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.FixedZone("", 8*3600))
	l := &treeLister{pageSize: 5, served: map[string]bool{}}
	for i := range n {
		l.items = append(l.items, treeItem{base.Add(time.Duration(i) * time.Second), fmt.Sprintf("d%03d", i)})
	}
	return l
}

// Pages are read several at a time; an entry another client moves while
// the walk runs — out of a stretch not yet read, to past the drive's end
// that was already reached — is still found, and nothing else is left.
func TestFullWalkReadsShardsAndCatchesEntriesMovedMeanwhile(t *testing.T) {
	l := denseTree(200)
	// A sparse tail up to two days ago, several pages long: the open shard
	// always has entries of its own to read.
	for at := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC); at.Before(time.Now().Add(-48 * time.Hour)); at = at.AddDate(0, 0, 7) {
		l.items = append(l.items, treeItem{at, "s" + at.Format("20060102")})
	}
	total := len(l.items)
	// Moved to now: past every split point and past the end already reached.
	moved, movedTo := "", time.Time{}
	l.onEnd = func(l *treeLister) {
		for i := len(l.items) - 1; i >= 0; i-- {
			if it := l.items[i]; strings.HasPrefix(it.id, "d") && !l.served[it.id] {
				moved, movedTo = it.id, time.Now().UTC()
				l.items[i].key = movedTo
				l.sort()
				return
			}
		}
	}
	s, store, _ := newSyncer(t, l)
	if err := store.Upsert([]Row{{ID: "ghost", Kind: drive.KindFile, Name: "ghost"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if moved == "" {
		t.Fatal("every entry was read before the drive's end: the scenario did not happen")
	}
	if n, _ := store.Count(); n != total {
		t.Errorf("count = %d, want %d", n, total)
	}
	if r, ok, _ := store.Get(moved); !ok || r.ModifiedTime != movedTo.Format(keyLayout) {
		t.Errorf("moved entry %s = %+v, %v; want it at its new key", moved, r, ok)
	}
	if _, ok, _ := store.Get("ghost"); ok {
		t.Error("an entry the walk did not see must go")
	}
	if l.peak < 2 {
		t.Errorf("at most %d page in flight; the walk should read shards side by side", l.peak)
	}
	if got, _ := store.Meta(MetaCursor); got != movedTo.Format(keyLayout) {
		t.Errorf("cursor = %q, want the drive's end after the move", got)
	}
	if raw, _ := store.Meta(MetaWalk); raw != "" {
		t.Errorf("checkpoint left behind: %s", raw)
	}
	if p := s.Progress(); !p.Ready || p.Count != total {
		t.Errorf("progress = %+v", p)
	}
}

// A walk cut short goes on from its checkpoint: the drive's beginning is
// not read again, and rows written before the failure still count as seen.
func TestFullWalkResumesFromCheckpoint(t *testing.T) {
	l := denseTree(60)
	l.fail = func(call int) error {
		if call == 4 {
			return errors.New("boom")
		}
		return nil
	}
	s, store, progress := newSyncer(t, l)
	if err := store.Upsert([]Row{{ID: "ghost", Kind: drive.KindFile, Name: "ghost"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background()); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
	seen, _ := store.WalkSeen()
	if seen == 0 {
		t.Fatal("the pages before the failure were not recorded")
	}
	if _, ok, _ := store.Get("ghost"); !ok {
		t.Error("an unfinished walk must not delete anything")
	}

	l.mu.Lock()
	l.fail, l.starts = nil, nil
	l.mu.Unlock()
	before := len(*progress)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(l.starts, "") {
		t.Errorf("the resumed walk read the drive from the beginning again: %d requests", len(l.starts))
	}
	if n, _ := store.Count(); n != 60 {
		t.Errorf("count = %d, want 60", n)
	}
	if _, ok, _ := store.Get("ghost"); ok {
		t.Error("ghost must go once the resumed walk completes")
	}
	// The resumed walk counts on from what was seen before.
	for _, p := range (*progress)[before:] {
		if p.Phase == "full" && p.Done < seen {
			t.Errorf("resumed progress restarted its count: %+v (seen %d before)", p, seen)
			break
		}
	}
}
