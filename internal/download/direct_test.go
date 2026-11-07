//go:build (darwin && cgo) || windows

package download

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/syncfs"
)

type versionedBackend struct {
	*fakeBackend
	vmu     sync.Mutex
	version string
}

func (b *versionedBackend) GetFile(ctx context.Context, id string, p drive.GetFileParams) (*drive.File, error) {
	f, err := b.fakeBackend.GetFile(ctx, id, p)
	if err != nil {
		return nil, err
	}
	b.vmu.Lock()
	f.ModifiedTime = b.version
	b.vmu.Unlock()
	f.Phase = drive.PhaseComplete
	return f, nil
}

func TestDirectReceiveResumesRangesButRefusesSameSizeChangedVersion(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{holds: map[string]bool{"1": true}})
	b := &versionedBackend{fakeBackend: &fakeBackend{url: cdn.URL}, version: "2026-10-19T10:00:00Z"}
	var manual atomic.Int32
	q := New(b, Options{Parallel: 1, Threads: 1, OnChange: func(Task) { manual.Add(1) }})
	t.Cleanup(q.Close)
	journal := filepath.Join(t.TempDir(), "manual.json")
	q.Open(journal, quietLog)
	req := DirectRequest{ID: "download-op", FileID: "f", Path: filepath.Join(t.TempDir(), "exact.part"), Checkpoint: filepath.Join(t.TempDir(), "op"), Size: int64(len(payload))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Receive(ctx, req, nil) }()
	cdn.waitHeld(t, 1)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("direct receive did not stop")
	}
	q.Close()
	partial := req.Path + PartialExt
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("cancel discarded resumable bytes: %v", err)
	}
	b.vmu.Lock()
	b.version = "2026-10-19T11:00:00Z"
	b.vmu.Unlock()
	q2 := New(b, Options{Parallel: 1, Threads: 4})
	t.Cleanup(q2.Close)
	before, _, _ := cdn.stats()
	if err := q2.Receive(context.Background(), req, nil); !errors.Is(err, ErrChanged) {
		t.Fatalf("same-size changed version = %v", err)
	}
	after, _, _ := cdn.stats()
	if after != before {
		t.Fatal("changed version transferred bytes into old partial")
	}
	b.vmu.Lock()
	b.version = "2026-10-19T10:00:00Z"
	b.vmu.Unlock()
	if err := q2.Receive(context.Background(), req, nil); err != nil {
		t.Fatalf("resume = %v", err)
	}
	got, err := os.ReadFile(req.Path)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("resumed file differs: %v", err)
	}
	if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial not atomically placed: %v", err)
	}
	if len(q.List()) != 0 || len(q2.List()) != 0 || manual.Load() != 0 {
		t.Fatal("direct download leaked into manual list/events")
	}
	if rs, err := readJournal(journal); err != nil || len(rs) != 0 {
		t.Fatalf("direct job leaked into manual journal: %v %v", rs, err)
	}
}

func TestDirectReceiveDoesNotOverwriteDestinationAppearingDuringTransfer(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{holds: map[string]bool{"1": true}})
	b := &versionedBackend{fakeBackend: &fakeBackend{url: cdn.URL}, version: "version-1"}
	q := New(b, Options{Parallel: 1, Threads: 1})
	t.Cleanup(q.Close)
	req := DirectRequest{ID: "race", FileID: "f", Path: filepath.Join(t.TempDir(), "exact-name"), Checkpoint: filepath.Join(t.TempDir(), "op"), Size: int64(len(payload))}
	done := make(chan error, 1)
	go func() { done <- q.Receive(context.Background(), req, nil) }()
	cdn.waitHeld(t, 1)
	want := []byte("user content created while downloading")
	if err := os.WriteFile(req.Path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	cdn.free()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrExist) {
			t.Fatalf("concurrent destination = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("receive did not finish")
	}
	got, err := os.ReadFile(req.Path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("concurrent user's file overwritten: %v", err)
	}
	got, err = os.ReadFile(req.Path + PartialExt)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("complete staging bytes not preserved: %v", err)
	}
}

func TestDirectReceiveVerificationPrecedesPublication(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{})
	backend := &versionedBackend{fakeBackend: &fakeBackend{url: cdn.URL}, version: "complete-version"}
	queue := New(backend, Options{Parallel: 1, Threads: 2})
	t.Cleanup(queue.Close)
	rejected := errors.New("completion proof rejected")
	request := DirectRequest{ID: "verified-original", FileID: "f", Path: filepath.Join(t.TempDir(), "original"), Checkpoint: filepath.Join(t.TempDir(), "checkpoint"), Size: int64(len(payload))}
	request.VerifyComplete = func(file *os.File) error {
		got, err := io.ReadAll(io.NewSectionReader(file, 0, int64(len(payload))))
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("verification observed incomplete bytes: %v", err)
		}
		if _, err := os.Stat(request.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("original published before verification")
		}
		return rejected
	}
	if err := queue.Receive(context.Background(), request, nil); !errors.Is(err, rejected) {
		t.Fatalf("verification failure lost: %v", err)
	}
	if _, err := os.Stat(request.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected original published")
	}
	request.VerifyComplete = func(file *os.File) error {
		got, err := io.ReadAll(io.NewSectionReader(file, 0, int64(len(payload))))
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("resumed proof differs: %v", err)
		}
		return nil
	}
	if err := queue.Receive(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(request.Path)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("verified original differs: %v", err)
	}
}

func TestDirectReceiveRevalidatesDestinationAfterQueueWait(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{holds: map[string]bool{"1": true}})
	backend := &versionedBackend{fakeBackend: &fakeBackend{url: cdn.URL}, version: "stable"}
	queue := New(backend, Options{Parallel: 1, Threads: 1})
	t.Cleanup(queue.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- queue.Receive(ctx, DirectRequest{ID: "occupy", FileID: "f", Path: filepath.Join(t.TempDir(), "first"), Checkpoint: filepath.Join(t.TempDir(), "checkpoint"), Size: int64(len(payload))}, nil)
	}()
	cdn.waitHeld(t, 1)
	selected := filepath.Join(t.TempDir(), "selected-disk")
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := syncfs.Identify(selected)
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan struct{})
	secondDone := make(chan error, 1)
	var once sync.Once
	request := DirectRequest{ID: "selected-disk", FileID: "f", Path: filepath.Join(selected, "original"), Checkpoint: filepath.Join(selected, "checkpoint"), Size: int64(len(payload)), ValidateDestination: func() error { return syncfs.Validate(identity) }}
	go func() { secondDone <- queue.Receive(ctx, request, func(Task) { once.Do(func() { close(queued) }) }) }()
	select {
	case <-queued:
	case <-ctx.Done():
		t.Fatal("download did not queue")
	}
	if err := os.Rename(selected, selected+"-detached"); err != nil {
		t.Fatal(err)
	}
	cdn.free()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first download did not finish")
	}
	select {
	case err := <-secondDone:
		if err == nil {
			t.Fatal("missing destination accepted")
		}
	case <-ctx.Done():
		t.Fatal("queued download did not settle")
	}
	if _, err := os.Stat(selected); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disconnected root recreated", err)
	}
}
