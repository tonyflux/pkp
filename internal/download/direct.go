package download

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/syncfs"
)

var (
	errDirectCheckpoint = i18n.New("传输检查点与当前操作不一致")
	errDirectActive     = i18n.New("传输操作正在进行")
)

// DirectRequest receives into an exact, caller-owned unique staging path.
// Path and Checkpoint must be absolute and stable for ID. Checkpoint is a
// private operation directory; Path's parent must already exist. The caller
// validates root/symlink ownership before every call and owns final cleanup.
type DirectRequest struct {
	ID, FileID, Path, Checkpoint string
	Size                         int64
	// VerifyComplete observes the engine's still-open, fully received file
	// before publication. An error keeps it partial; the callback must not close it.
	VerifyComplete func(*os.File) error
	// ValidateDestination runs after the queue slot is acquired, before any
	// checkpoint directory can be created beneath a caller-selected disk.
	ValidateDestination func() error
}

type fileVersion struct {
	ID, Created, Modified, Hash, MD5, Phase string
	Size                                    int64
}

func versionOf(f *drive.File) fileVersion {
	return fileVersion{ID: f.ID, Created: f.CreatedTime, Modified: f.ModifiedTime, Hash: f.Hash, MD5: f.MD5Checksum, Phase: f.Phase, Size: int64(f.Size)}
}

func (v fileVersion) known() bool { return v.Modified != "" || v.Hash != "" || v.MD5 != "" }

type directJob struct {
	checkpoint string
	progress   func(Task)
	verify     func(*os.File) error
	version    fileVersion
	etag       string
	file       string
	prepared   bool
	complete   bool
}

type directRecord struct {
	ID, FileID, Path string
	Version          fileVersion
	ETag             string
	File             string
	Prepared         bool
	Complete         bool
	Parts            []part
}

// Receive shares this account's FIFO file slots, link refreshes, HTTP client
// and connection tokens with manual downloads. It never enters their list,
// journal or events. Cancellation preserves its isolated resumable checkpoint.
func (q *Queue) Receive(ctx context.Context, req DirectRequest, onProgress func(Task)) error {
	if req.ID == "" || req.FileID == "" || req.Size < 0 || !filepath.IsAbs(req.Path) || !filepath.IsAbs(req.Checkpoint) {
		return errDirectCheckpoint
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	j := &job{Task: Task{ID: req.ID, FileID: req.FileID, Name: filepath.Base(req.Path), Path: req.Path, Size: req.Size, State: StateQueued, UpdatedAt: stamp()}, cancel: cancel,
		direct: &directJob{checkpoint: filepath.Clean(req.Checkpoint), progress: onProgress, verify: req.VerifyComplete}}
	q.mu.Lock()
	if q.closing {
		q.mu.Unlock()
		return errClosed
	}
	for other := range q.direct {
		if other.ID == j.ID || other.Path == j.Path || other.direct.checkpoint == j.direct.checkpoint {
			q.mu.Unlock()
			return errDirectActive
		}
	}
	for _, other := range q.jobs {
		if other.Path == j.Path {
			q.mu.Unlock()
			return errDirectActive
		}
	}
	q.direct[j] = struct{}{}
	q.waiting = append(q.waiting, j)
	q.running.Add(1)
	q.directRunning.Add(1)
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		delete(q.direct, j)
		q.mu.Unlock()
		q.running.Done()
		q.directRunning.Done()
	}()
	q.emit(j, true)
	if err := q.acquire(ctx, j); err != nil {
		q.finish(j, context.Cause(ctx))
		return context.Cause(ctx)
	}
	defer q.release()
	q.mu.Lock()
	threads := q.threads
	j.State, j.UpdatedAt = StateDownloading, stamp()
	j.lastAt = time.Now()
	q.mu.Unlock()
	q.emit(j, true)
	var err error
	if req.ValidateDestination != nil {
		err = req.ValidateDestination()
	}
	if err == nil {
		err = q.restoreDirect(j)
	}
	if err == nil {
		err = q.download(ctx, j, threads)
	}
	if err != nil && ctx.Err() != nil {
		err = errors.Join(err, context.Cause(ctx))
	}
	q.finish(j, err)
	return err
}

func (q *Queue) restoreDirect(j *job) error {
	if err := os.MkdirAll(j.direct.checkpoint, 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(j.direct.checkpoint, "direct.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var r directRecord
	if json.Unmarshal(data, &r) != nil || r.ID != j.ID || r.FileID != j.FileID || r.Path != j.Path || r.Version.Size != j.Size || !validParts(r.Parts, j.Size) {
		return errDirectCheckpoint
	}
	j.direct.version, j.direct.etag = r.Version, r.ETag
	j.direct.prepared, j.direct.complete = r.Prepared, r.Complete
	j.direct.file = r.File
	if r.Prepared {
		j.parts = make([]*part, len(r.Parts))
		for i := range r.Parts {
			j.parts[i] = &r.Parts[i]
		}
		j.Done = j.written()
	}
	return nil
}

func validParts(parts []part, size int64) bool {
	ordered := slices.Clone(parts)
	sort.Slice(ordered, func(a, b int) bool { return ordered[a].Start < ordered[b].Start })
	end := int64(0)
	for _, p := range ordered {
		if p.Start < end || p.Start < 0 || p.Start > p.Pos || p.Pos > p.End || p.End > size {
			return false
		}
		end = p.End
	}
	return true
}

// saveDirect checkpoints only bytes already flushed to disk; the part lock
// prevents advancing a position past that durability point until it is saved.
func (q *Queue) saveDirect(j *job, f *os.File) error {
	j.pmu.Lock()
	defer j.pmu.Unlock()
	if f != nil {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	r := directRecord{ID: j.ID, FileID: j.FileID, Path: j.Path, Version: j.direct.version, ETag: j.direct.etag, File: j.direct.file, Prepared: j.direct.prepared, Complete: j.direct.complete, Parts: make([]part, len(j.parts))}
	for i, p := range j.parts {
		r.Parts[i] = *p
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := filepath.Join(j.direct.checkpoint, "direct.json")
	out, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return commitDirectRecord(path+".tmp", path)
}

func directCreate(path string, size int64) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_ = sparse(f)
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func directReopen(path string, size int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return nil, errDirectCheckpoint
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	now, err := f.Stat()
	if err != nil || !os.SameFile(info, now) {
		_ = f.Close()
		return nil, errDirectCheckpoint
	}
	return f, nil
}

func (q *Queue) linkFor(ctx context.Context, j *job) (string, time.Time, int64, error) {
	if j.direct == nil {
		return q.link(ctx, j.FileID)
	}
	f, err := q.backend.GetFile(ctx, j.FileID, drive.GetFileParams{Usage: drive.UsageFetch})
	link, expire, size, err := fileLink(f, err)
	if err != nil {
		return "", time.Time{}, 0, err
	}
	v := versionOf(f)
	if f.IsFolder() || f.ID != j.FileID || size != j.Size || (f.Phase != "" && f.Phase != drive.PhaseComplete) {
		return "", time.Time{}, 0, ErrChanged
	}
	if j.direct.version.ID != "" {
		if v != j.direct.version || !v.known() {
			return "", time.Time{}, 0, ErrChanged
		}
	} else {
		j.direct.version = v
	}
	return link, expire, size, nil
}

func (q *Queue) downloadDirect(ctx context.Context, j *job, threads int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	link, expire, size, err := q.linkFor(ctx, j)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(j.Path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return os.ErrExist
		}
		return err
	}
	if j.direct.complete {
		return errDirectCheckpoint
	}
	partial := j.Path + PartialExt
	need := size - j.Done
	if free, err := freeSpace(filepath.Dir(j.Path)); err == nil && free < uint64(need)+spaceMargin {
		return i18n.Errorf("磁盘空间不足：还需 %s，可用 %s", humanBytes(need), humanBytes(int64(free)))
	}
	var f *os.File
	if j.direct.prepared {
		id, identityErr := syncfs.FileIdentity(partial)
		if identityErr != nil {
			return identityErr
		}
		if id != j.direct.file || id == "" {
			return errDirectCheckpoint
		}
		f, err = directReopen(partial, size)
	} else {
		// The durable intent precedes creating even a zero-byte partial file.
		if err := q.saveDirect(j, nil); err != nil {
			return err
		}
		f, err = directCreate(partial, size)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	id, err := syncfs.FileIdentity(partial)
	if err != nil {
		return err
	}
	if j.direct.prepared && id != j.direct.file {
		return errDirectCheckpoint
	}
	if !j.direct.prepared {
		j.direct.file, j.direct.prepared = id, true
		j.parts = plan(size, threads)
		if err := q.saveDirect(j, f); err != nil {
			return err
		}
	}
	a := &attempt{q: q, j: j, f: f, size: size, threads: threads, link: link, expire: usable(expire), verdict: make(chan struct{})}
	if err := a.run(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.direct.version.known() {
		if _, _, _, err := q.linkFor(ctx, j); err != nil {
			return err
		}
	} else {
		return ErrChanged
	}
	if j.direct.verify != nil {
		if err := j.direct.verify(f); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if id, err := syncfs.FileIdentity(partial); err != nil || id != j.direct.file {
		return errors.Join(errDirectCheckpoint, err)
	}
	if err := syncfs.RenameNoReplace(partial, j.Path); err != nil {
		return err
	}
	j.direct.complete = true
	if err := q.saveDirect(j, nil); err != nil {
		return err
	}
	return nil
}
