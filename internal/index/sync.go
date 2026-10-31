package index

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
)

// fullInterval bounds how stale a full walk may get. Entries another client
// destroys without going through the recycle bin never show up in an
// incremental page, so only a full walk converges on them.
const fullInterval = 7 * 24 * time.Hour

// Progress is what the UI shows while a walk is running.
type Progress struct {
	Phase string `json:"phase"` // "idle" | "full" | "incremental"
	Done  int    `json:"done"`  // rows upserted so far in this walk
	// Percent is how much of a full walk is done, 0–99, against the size
	// of the index an earlier walk left; 0 when there is none (first build)
	// and outside a full walk.
	Percent int `json:"percent"`
	// Reached is the sort key (an RFC 3339 time, oldest first) before which
	// a full walk has read every entry; "" when unknown or not walking.
	Reached string `json:"reached"`
	Ready   bool   `json:"ready"` // a full walk has completed at least once for this account
	Count   int    `json:"count"` // rows in the index
	Error   string `json:"error"` // last sync error, "" when none
	// SyncedAt is the RFC3339 UTC time of the last completed walk.
	SyncedAt string `json:"syncedAt"`
}

// Lister is the one method of *drive.Client the syncer needs.
type Lister interface {
	ListFiles(ctx context.Context, params drive.ListFilesParams) (*drive.FileList, error)
}

// Syncer keeps a Store in step with the server. One walk runs at a time;
// Sync and Rebuild return immediately when another is in flight.
type Syncer struct {
	store      *Store
	lister     Lister
	log        *slog.Logger
	toRow      func(drive.File) Row
	onProgress func(Progress)

	running  sync.Mutex // held for the duration of a walk
	mu       sync.Mutex // guards progress
	progress Progress
}

// NewSyncer wires a syncer. toRow converts a listing entry into the stored
// shape (the caller owns the category rule); onProgress may be nil.
func NewSyncer(store *Store, lister Lister, toRow func(drive.File) Row, onProgress func(Progress), log *slog.Logger) *Syncer {
	if log == nil {
		log = slog.Default()
	}
	s := &Syncer{store: store, lister: lister, log: log, toRow: toRow, onProgress: onProgress}
	s.progress = s.snapshot("idle", 0, "")
	return s
}

// Progress reports the current state. Ready is read from the store each
// time, so a caller deciding whether to trust the index sees the truth
// even if the store was written outside a walk; the rest is the last
// snapshot a walk published.
func (s *Syncer) Progress() Progress {
	s.mu.Lock()
	p := s.progress
	s.mu.Unlock()
	p.Ready = s.Ready()
	return p
}

// Ready reports whether a full walk has completed for this store.
func (s *Syncer) Ready() bool {
	fullAt, err := s.store.Meta(MetaFullAt)
	return err == nil && fullAt != ""
}

// Sync runs one incremental pass, escalating to a full walk when the index
// has never completed one or the last one is older than fullInterval.
func (s *Syncer) Sync(ctx context.Context) error {
	if !s.running.TryLock() {
		return nil
	}
	defer s.running.Unlock()

	cursor, err := s.store.Meta(MetaCursor)
	if err != nil {
		return s.finish(err)
	}
	if cursor == "" {
		return s.finish(s.walkAndReconcile(ctx, "", true))
	}
	if err := s.walkAndReconcile(ctx, drive.SyncCursor(cursor), false); err != nil {
		return s.finish(err)
	}
	if s.fullDue() {
		return s.finish(s.walkAndReconcile(ctx, "", true))
	}
	return s.finish(nil)
}

func (s *Syncer) walkAndReconcile(ctx context.Context, pageToken string, full bool) error {
	var result walkResult
	var err error
	if full {
		result, err = s.fullWalk(ctx)
	} else {
		result, err = s.walk(ctx, pageToken)
	}
	if err != nil {
		return err
	}
	return s.reconcileTrash(ctx, result)
}

// Rebuild empties the index and walks the whole tree again. Unlike Sync it
// waits for a running walk rather than skipping: the user asked for it.
func (s *Syncer) Rebuild(ctx context.Context) error {
	s.running.Lock()
	defer s.running.Unlock()
	if err := s.store.Clear(); err != nil {
		return s.finish(err)
	}
	return s.finish(s.walkAndReconcile(ctx, "", true))
}

// finish records the outcome of a walk and returns to idle.
func (s *Syncer) finish(err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
		s.log.Warn("index: sync failed", "error", err)
	}
	s.publish("idle", 0, msg)
	return err
}

// walkResult is what a completed walk reports back for the trash reconcile.
type walkResult struct {
	version string // FileList.Version of the last page
	trashed int    // rows with trashed=true seen during the walk
}

// walk is an incremental pass: the whole tree from pageToken, one page
// after another, upserting each. The cursor advances only when the pass
// completes, so a failed pass replays from the same high-water mark;
// upserts are idempotent so that is safe.
func (s *Syncer) walk(ctx context.Context, pageToken string) (walkResult, error) {
	s.publish("incremental", 0, "")

	done := 0
	var result walkResult
	var lastSyncTime string
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		list, err := s.lister.ListFiles(ctx, drive.ListFilesParams{
			ParentID:      drive.WholeTree,
			PageToken:     pageToken,
			ThumbnailSize: drive.ThumbnailMedium,
		})
		if err != nil {
			return result, err
		}
		rows := make([]Row, 0, len(list.Files))
		for _, f := range list.Files {
			rows = append(rows, s.toRow(f))
			if f.Trashed {
				result.trashed++
			}
		}
		if err := s.store.Upsert(rows); err != nil {
			return result, err
		}
		done += len(rows)
		result.version = list.Version
		if list.SyncTime != "" {
			lastSyncTime = list.SyncTime
		}
		s.publish("incremental", done, "")
		if list.NextPageToken == "" {
			break
		}
		pageToken = list.NextPageToken
	}

	if err := s.finishPass(lastSyncTime, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return result, err
	}
	s.log.Info("index: walk complete", "phase", "incremental", "rows", done, "trashed", result.trashed)
	return result, nil
}

// finishPass records a completed walk: the cursor moves to syncTime (the
// drive's high-water mark, when the server gave one) and synced_at to now.
func (s *Syncer) finishPass(syncTime, now string) error {
	if syncTime != "" {
		if err := s.store.SetMeta(MetaCursor, syncTime); err != nil {
			return err
		}
	}
	return s.store.SetMeta(MetaSyncedAt, now)
}

// reconcileTrash drops rows the server destroyed permanently. Destruction
// is not observable from the feed: a destroyed entry keeps being listed as
// trashed (its sort key even advances, so it shows up again incrementally)
// while the recycle bin listing and GetFile no longer know it, and
// last_destroy_time moves only some time later. So whenever a walk saw any
// trashed row, or the destroy mark moved, the bin is relisted (one request
// for a few dozen top-level items) and any top-level trashed row that is
// no longer there goes, with its descendants.
func (s *Syncer) reconcileTrash(ctx context.Context, result walkResult) error {
	destroy := drive.DestroyTime(result.version)
	stored, err := s.store.Meta(MetaDestroy)
	if err != nil {
		return err
	}
	if result.trashed == 0 && destroy == stored && destroy != "" {
		return nil
	}
	var keep []string
	pageToken := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		list, err := s.lister.ListFiles(ctx, drive.ListFilesParams{
			ParentID:      drive.WholeTree,
			PageToken:     pageToken,
			Filters:       drive.NewFilters().Eq("trashed", true),
			ThumbnailSize: drive.ThumbnailMedium,
		})
		if err != nil {
			return err
		}
		rows := make([]Row, 0, len(list.Files))
		for _, f := range list.Files {
			rows = append(rows, s.toRow(f))
			keep = append(keep, f.ID)
		}
		if err := s.store.Upsert(rows); err != nil {
			return err
		}
		if list.NextPageToken == "" {
			break
		}
		pageToken = list.NextPageToken
	}
	gone, err := s.store.DeleteTrashedNotIn(keep)
	if err != nil {
		return err
	}
	if gone > 0 {
		s.log.Info("index: destroyed rows removed", "rows", gone)
	}
	return s.store.SetMeta(MetaDestroy, destroy)
}

// fullDue says whether the safety-net full walk is owed.
func (s *Syncer) fullDue() bool {
	fullAt, err := s.store.Meta(MetaFullAt)
	if err != nil || fullAt == "" {
		return true
	}
	at, err := time.Parse(time.RFC3339, fullAt)
	if err != nil {
		return true
	}
	return time.Since(at) > fullInterval
}

// snapshot builds a Progress from the store's current state.
func (s *Syncer) snapshot(phase string, done int, errMsg string) Progress {
	fullAt, _ := s.store.Meta(MetaFullAt)
	syncedAt, _ := s.store.Meta(MetaSyncedAt)
	count, _ := s.store.Count()
	return Progress{Phase: phase, Done: done, Ready: fullAt != "", Count: count, Error: errMsg, SyncedAt: syncedAt}
}

func (s *Syncer) publish(phase string, done int, errMsg string) {
	s.setProgress(s.snapshot(phase, done, errMsg))
}

func (s *Syncer) setProgress(p Progress) {
	s.mu.Lock()
	s.progress = p
	s.mu.Unlock()
	if s.onProgress != nil {
		s.onProgress(p)
	}
}
