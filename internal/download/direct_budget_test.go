//go:build (darwin && cgo) || windows

package download

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectAndManualDownloadsShareAccountConnectionBudget(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{pace: time.Millisecond})
	b := &versionedBackend{fakeBackend: &fakeBackend{url: cdn.URL}, version: "stable"}
	m := newMeter()
	q := New(b, Options{Parallel: 3, Threads: MaxThreads, HTTPClient: m.client()})
	t.Cleanup(q.Close)
	if _, err := q.Add(Source{FileID: "manual", Name: "manual.bin", Dir: t.TempDir(), Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for _, id := range []string{"sync-a", "sync-b"} {
		req := DirectRequest{ID: id, FileID: id, Path: filepath.Join(t.TempDir(), "stage"), Checkpoint: filepath.Join(t.TempDir(), "op"), Size: int64(len(payload))}
		go func() { done <- q.Receive(ctx, req, nil) }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	peak := m.most
	m.mu.Unlock()
	if peak > AccountConnections || peak <= MaxThreads {
		t.Fatalf("shared connections peaked at %d, want concurrent files within account budget %d", peak, AccountConnections)
	}
	if tasks := q.List(); len(tasks) != 1 || tasks[0].FileID != "manual" {
		t.Fatalf("transfer center contains direct downloads: %+v", tasks)
	}
}
