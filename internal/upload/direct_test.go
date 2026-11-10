package upload

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
)

func TestDirectUploadPersistsSlotBeforeSendingAndResumesWithoutRecreate(t *testing.T) {
	oss, endpoint := newFakeOSS(t)
	backend := &slotBackend{endpoint: endpoint, lifetime: time.Hour}
	path, want := threeParts(t)
	var manual atomic.Int32
	q := New(backend, 1, func(Task) { manual.Add(1) })
	t.Cleanup(q.Close)
	journal := filepath.Join(t.TempDir(), "uploads.json")
	q.Open(journal, quietLog)
	checkpoint := filepath.Join(t.TempDir(), "operation")
	persistErr := errors.New("journal unavailable")
	req := DirectRequest{ID: "sync-op", Path: path, Name: "safe.tmp", ParentID: "folder", Checkpoint: checkpoint,
		SaveSlot: func(slot *drive.UploadSlot) error {
			if slot.File.ID == "" || slot.OSS == nil {
				t.Fatal("slot callback has no usable recovery identity")
			}
			return persistErr
		}}
	id, err := q.Send(context.Background(), req, nil)
	if id != "p-1" || !errors.Is(err, persistErr) {
		t.Fatalf("save failure = %q, %v", id, err)
	}
	oss.mu.Lock()
	started := oss.nextID
	oss.mu.Unlock()
	if started != 0 {
		t.Fatal("OSS transfer started before owner persisted the slot")
	}
	oss.set(func(_ string, part int) bool { return part > 1 }, nil)
	req.SaveSlot = func(*drive.UploadSlot) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := q.Send(ctx, req, nil); finished <- err }()
	oss.waitStored(t, "obj-1#1")
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("direct upload did not stop")
	}
	q.Close()
	oss.set(nil, nil)
	q2 := New(backend, 1, nil)
	t.Cleanup(q2.Close)
	if id, err = q2.Send(context.Background(), req, nil); err != nil || id != "p-1" {
		t.Fatalf("resume = %q, %v", id, err)
	}
	created, trashed := backend.state()
	if created != 1 || len(trashed) != 0 {
		t.Fatalf("created=%d trashed=%v", created, trashed)
	}
	oss.mu.Lock()
	got, firstPart := oss.objects["obj-1"], oss.received["obj-1#1"]
	oss.mu.Unlock()
	if !bytes.Equal(got, want) || firstPart != 1 {
		t.Fatalf("wrong resumed content or retransferred completed part (%d)", firstPart)
	}
	if len(q.List()) != 0 || len(q2.List()) != 0 || manual.Load() != 0 {
		t.Fatal("direct upload leaked into manual transfer center")
	}
	if rs, _, err := readJournal(journal); err != nil || len(rs) != 0 {
		t.Fatalf("manual journal contains direct job: %v %v", rs, err)
	}
}

type ambiguousBackend struct {
	*slotBackend
	calls atomic.Int32
}

func (b *ambiguousBackend) CreateUpload(context.Context, drive.UploadParams) (*drive.UploadSlot, error) {
	b.calls.Add(1)
	return nil, errors.New("response lost after server accepted creation")
}

func TestDirectUploadLostCreateResponseCannotBlindlyRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &ambiguousBackend{slotBackend: &slotBackend{}}
	req := DirectRequest{ID: "unknown", Path: path, Name: "new", Checkpoint: filepath.Join(t.TempDir(), "op"), SaveSlot: func(*drive.UploadSlot) error { return nil }}
	for range 2 {
		q := New(b, 1, nil)
		_, err := q.Send(context.Background(), req, nil)
		q.Close()
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("lost create response = %v", err)
		}
	}
	if b.calls.Load() != 1 {
		t.Fatalf("ambiguous operation created %d times", b.calls.Load())
	}
}
