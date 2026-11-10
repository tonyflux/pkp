package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/foldersync"
	"github.com/tonyflux/pkp/internal/lanshare"
)

func TestLANCloudReusesPublishedSyncOriginalWithoutNewTransfers(t *testing.T) {
	body := bytes.Repeat([]byte("verified synchronized original\x00"), 256)
	var transfers atomic.Int64
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transfers.Add(1)
		http.ServeContent(w, r, "clip.bin", time.Unix(1, 0), bytes.NewReader(body))
	}))
	defer cdn.Close()
	var mu sync.Mutex
	files := map[string]drive.File{
		"root":    {ID: "root", Kind: drive.KindFolder, Name: "Shared", UserID: "u"},
		"private": {ID: "private", Kind: drive.KindFolder, Name: "Private", UserID: "u"},
		// Deliberately no server MD5: the committed sync baseline supplies SHA-256 proof.
		"clip": {ID: "clip", ParentID: "root", Kind: drive.KindFile, Name: "clip.bin", UserID: "u", Size: drive.Int64(len(body)), Phase: drive.PhaseComplete, ModifiedTime: "2026-11-01T00:00:00Z"},
	}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/drive/v1/files" {
			page := drive.FileList{Files: []drive.File{}}
			for _, file := range files {
				if file.ParentID == r.URL.Query().Get("parent_id") {
					page.Files = append(page.Files, file)
				}
			}
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		file, ok := files[strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")]
		if !ok {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		if file.ID == "clip" {
			file.Links = map[string]drive.Link{"application/octet-stream": {URL: cdn.URL, Expire: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}
		}
		_ = json.NewEncoder(w).Encode(file)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account, _ := svc.active()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := svc.SyncPreview("lan-original", foldersync.Config{Account: account.key, LocalPath: root, CloudID: "root", Mode: foldersync.Download, Name: "LAN originals"})
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.SyncStart(preview.ID)
	if err != nil || started.Pair == nil || started.Preview != nil {
		t.Fatalf("sync start: %+v %v", started, err)
	}
	pairID := started.Pair.ID
	deadline := time.Now().Add(20 * time.Second)
	for {
		pairs, err := svc.SyncPairs()
		if err != nil {
			t.Fatal(err)
		}
		if len(pairs) == 1 && pairs[0].State == "idle" && pairs[0].LastSuccess != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sync did not publish the original: %+v", pairs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := svc.SyncPause(pairID, true); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, "clip.bin")
	if got, err := os.ReadFile(local); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("sync original differs: %v", err)
	}
	beforeTransfers := transfers.Load()
	cloud := &lanCloud{drive: svc}
	ctx := context.Background()
	listed, err := cloud.List(ctx, account.key, "root", "", "u")
	if err != nil || len(listed) != 1 || !listed[0].NeedsVerification || listed[0].Ready {
		t.Fatalf("unverified sync original listed incorrectly: %+v %v", listed, err)
	}
	original, err := cloud.Resolve(ctx, account.key, "root", "clip", "u")
	if err != nil || !original.Entry.Ready || original.LocalPath != local || original.LocalVersion == "" {
		t.Fatalf("published sync original not reused: %+v %v", original, err)
	}
	if transfers.Load() != beforeTransfers {
		t.Fatal("sharing downloaded an original already supplied by synchronization")
	}
	if len(account.downloadQueue().List()) != 0 {
		t.Fatal("sync/share reuse created an ordinary desktop download")
	}
	pairs, _ := svc.SyncPairs()
	if len(pairs) != 1 || !pairs[0].Paused {
		t.Fatal("shared access changed the owner's paused synchronization")
	}
	// An unchanged mtime and size must not allow a modified local original through the proof cache.
	info, err := os.Stat(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, bytes.Repeat([]byte{'x'}, len(body)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(local, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if changed, err := cloud.Resolve(ctx, account.key, "root", "clip", "u"); err != nil || changed.Entry.Ready {
		t.Fatalf("modified sync bytes remained ready: %+v %v", changed, err)
	}
	if err := os.WriteFile(local, body, 0600); err != nil {
		t.Fatal(err)
	}
	if restored, err := cloud.Resolve(ctx, account.key, "root", "clip", "u"); err != nil || !restored.Entry.Ready {
		t.Fatalf("restored original was not reverified: %+v %v", restored, err)
	}
	other := seedSession(t, svc, "other@example.com")
	if source, err := cloud.Resolve(ctx, other.key, "root", "clip", "u"); err != nil || source.Entry.Ready {
		t.Fatalf("different account borrowed sync originals: %+v %v", source, err)
	}
	if source, err := cloud.Resolve(ctx, account.key, "root", "clip", "u"); err != nil || !source.Entry.Ready {
		t.Fatalf("foreground account switch changed the shared source: %+v %v", source, err)
	}
	mu.Lock()
	changed := files["clip"]
	changed.ModifiedTime = "2026-11-02T00:00:00Z"
	files["clip"] = changed
	mu.Unlock()
	if source, err := cloud.Resolve(ctx, account.key, "root", "clip", "u"); err != nil || source.Entry.Ready {
		t.Fatalf("stale cloud generation borrowed a previous sync original: %+v %v", source, err)
	}
	mu.Lock()
	changed.ParentID = "private"
	files["clip"] = changed
	mu.Unlock()
	if _, err := cloud.Resolve(ctx, account.key, "root", "clip", "u"); !errors.Is(err, errLANScope) {
		t.Fatalf("local sync copy bypassed live scope revocation: %v", err)
	}
	if err := svc.SyncRemove(pairID); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(local); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("removing sync deleted the original: %v", err)
	}
	if token, err := lanshare.LocalVersion(local); err != nil || token == "" {
		t.Fatalf("retained original became inaccessible: %v", err)
	}
}
