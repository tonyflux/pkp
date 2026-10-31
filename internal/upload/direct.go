package upload

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
)

// ErrUnknown means slot creation may have succeeded without a usable response.
// Reconcile the cloud operation before supplying a recovered Slot; never retry
// the operation under a new ID merely because Send returned this error.
var ErrUnknown = i18n.New("上传创建结果待确认，请先核实云端状态")

var (
	errDirectCheckpoint = i18n.New("传输检查点与当前操作不一致")
	errDirectActive     = i18n.New("传输操作正在进行")
	errDirectExpired    = i18n.New("上传授权已失效，请先核实云端状态")
)

// DirectRequest sends an immutable caller-owned regular file. ID and Checkpoint
// belong to exactly one operation and must survive interruption; Checkpoint is
// a private directory, not a manual-upload checkpoint. SaveSlot must durably
// save the grant and its file ID before returning nil. An expired recovered
// grant is not replaced or trashed: the caller must first reconcile its file.
type DirectRequest struct {
	ID, Path, ParentID, Name, Checkpoint string
	Slot                                 *drive.UploadSlot
	SaveSlot                             func(*drive.UploadSlot) error
}

type directJob struct {
	checkpoint string
	progress   func(Task)
}

type directRecord struct {
	ID, Path, ParentID, Name string
	Size                     int64
	ModTime                  time.Time
	Creating                 bool
	Slot                     *drive.UploadSlot
	Complete                 bool
}

// Send shares this account queue's FIFO file slots and OSS executor, but never
// enters List, the manual journal, or OnChange. Cancelling retains all recovery
// evidence and never trashes a cloud version. The caller owns final cleanup.
func (q *Queue) Send(ctx context.Context, req DirectRequest, onProgress func(Task)) (string, error) {
	if req.ID == "" || req.Name == "" || req.SaveSlot == nil || !filepath.IsAbs(req.Path) || !filepath.IsAbs(req.Checkpoint) {
		return "", errDirectCheckpoint
	}
	info, err := regularFile(req.Path)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	j := &job{Task: Task{ID: req.ID, Path: req.Path, ParentID: req.ParentID, Name: req.Name, Size: info.Size(), State: StateQueued, UpdatedAt: stamp()}, cancel: cancel,
		direct: &directJob{checkpoint: filepath.Clean(req.Checkpoint), progress: onProgress}}
	q.mu.Lock()
	if q.closing {
		q.mu.Unlock()
		return "", context.Canceled
	}
	for other := range q.direct {
		if other.ID == req.ID || other.direct.checkpoint == j.direct.checkpoint {
			q.mu.Unlock()
			return "", errDirectActive
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
		q.finish(j, "", err)
		return "", err
	}
	defer q.release()
	q.setState(j, StateUploading)
	id, err := q.send(ctx, j, req, info)
	q.finish(j, id, err)
	return id, err
}

func (q *Queue) send(ctx context.Context, j *job, req DirectRequest, info os.FileInfo) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(req.Checkpoint, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(req.Checkpoint, "direct.json")
	r := directRecord{ID: req.ID, Path: req.Path, ParentID: req.ParentID, Name: req.Name, Size: info.Size(), ModTime: info.ModTime()}
	data, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(data, &r) != nil || r.ID != req.ID || r.Path != req.Path || r.ParentID != req.ParentID || r.Name != req.Name || r.Size != info.Size() || !r.ModTime.Equal(info.ModTime()) {
			return "", errDirectCheckpoint
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if req.Slot != nil {
		if r.Slot != nil && (r.Slot.File.ID != req.Slot.File.ID || !sameGrant(r.Slot.OSS, req.Slot.OSS)) {
			return "", errDirectCheckpoint
		}
		r.Slot = req.Slot
	}
	if r.Complete && r.Slot != nil && r.Slot.File.ID != "" && r.Slot.OSS != nil {
		if err := req.SaveSlot(r.Slot); err != nil {
			return r.Slot.File.ID, err
		}
		q.progress(j, info.Size())
		return r.Slot.File.ID, nil
	}
	if r.Slot == nil {
		if r.Creating {
			return "", ErrUnknown
		}
		r.Creating = true
		if err := saveDirectRecord(path, r); err != nil {
			return "", err
		}
		r.Slot, err = q.backend.CreateUpload(ctx, drive.UploadParams{ParentID: req.ParentID, Name: req.Name, Size: info.Size()})
		if err != nil {
			var api *drive.APIError
			if errors.As(err, &api) && api.Status >= 400 && api.Status < 500 && api.Status != 408 {
				r.Creating = false
				return "", errors.Join(err, saveDirectRecord(path, r))
			}
			return "", errors.Join(ErrUnknown, err)
		}
	}
	if r.Slot == nil || r.Slot.File.ID == "" || r.Slot.OSS == nil {
		return "", ErrUnknown
	}
	// Keep the response even if the owner's journal fails. No bytes may be
	// sent until both our recovery record and the owner's callback succeed.
	if err := saveDirectRecord(path, r); err != nil {
		return r.Slot.File.ID, errors.Join(ErrUnknown, err)
	}
	if err := req.SaveSlot(r.Slot); err != nil {
		return r.Slot.File.ID, err
	}
	slot := &resumeState{FileID: r.Slot.File.ID, Grant: *r.Slot.OSS, Size: info.Size(), ModTime: info.ModTime()}
	if !slot.fresh(time.Now()) {
		return slot.FileID, errDirectExpired
	}
	q.mu.Lock()
	j.resume, j.FileID = slot, slot.FileID
	q.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return slot.FileID, err
	}
	if err := q.transfer(ctx, j, slot, true); err != nil {
		return slot.FileID, err
	}
	after, err := regularFile(req.Path)
	if err != nil {
		return slot.FileID, err
	}
	if !slot.fits(after) || !os.SameFile(info, after) {
		return slot.FileID, errDirectCheckpoint
	}
	r.Complete = true
	if err := saveDirectRecord(path, r); err != nil {
		return slot.FileID, err
	}
	q.progress(j, info.Size())
	return slot.FileID, nil
}

func sameGrant(a, b *drive.OSSCredentials) bool {
	return a != nil && b != nil && *a == *b
}

func saveDirectRecord(path string, r directRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return commitDirectRecord(path+".tmp", path)
}
