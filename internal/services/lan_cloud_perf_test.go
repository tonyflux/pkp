package services

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/lanshare"
)

func TestLANCloudProofScopeSharesAncestorsOnlyWithinOperation(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	files := map[string]drive.File{
		"root":    {ID: "root", Kind: drive.KindFolder, UserID: "u"},
		"inside":  {ID: "inside", ParentID: "root", Kind: drive.KindFolder, UserID: "u"},
		"one":     {ID: "one", Name: "one.mp4", ParentID: "inside", Kind: drive.KindFile, UserID: "u", Phase: drive.PhaseComplete, ModifiedTime: "v1"},
		"two":     {ID: "two", Name: "two.mp4", ParentID: "inside", Kind: drive.KindFile, UserID: "u", Phase: drive.PhaseComplete, ModifiedTime: "v1"},
		"outside": {ID: "outside", Kind: drive.KindFolder, UserID: "u"},
	}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/drive/v1/files:batchGet" {
			list := drive.FileList{}
			for _, id := range r.URL.Query()["ids"] {
				calls[id]++
				if f, ok := files[id]; ok {
					list.Files = append(list.Files, f)
				}
			}
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
		calls[id]++
		_ = json.NewEncoder(w).Encode(files[id])
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	c := &lanCloud{drive: svc}
	base := context.Background()
	// Without the manager sharing one scope, each adapter operation is live.
	if err := c.ValidateRoot(base, s.key, "root", "u"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "one"} {
		if _, err := c.Resolve(base, s.key, "root", id, "u"); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	before := 0
	for _, count := range calls {
		before += count
	}
	if before != 10 {
		t.Errorf("independent-operation GetFile count=%d, want 10", before)
	}
	calls = map[string]int{}
	mu.Unlock()
	ctx := c.SourceProofContext(base)
	if err := c.ValidateRoot(ctx, s.key, "root", "u"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "one"} {
		if _, err := c.Resolve(ctx, s.key, "root", id, "u"); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	for _, id := range []string{"root", "inside", "one", "two"} {
		if calls[id] != 1 {
			t.Errorf("request-scope GetFile(%s)=%d, want 1", id, calls[id])
		}
	}
	mu.Unlock()
	t.Logf("ValidateRoot + Resolve(one,two,one): independent scopes=%d GetFile, shared scope=4 GetFile", before)
	// A new request with the same IDs/account/root must not reuse evidence.
	if _, err := c.Resolve(c.SourceProofContext(ctx), s.key, "root", "one", "u"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if calls["one"] != 2 || calls["inside"] != 2 || calls["root"] != 2 {
		t.Errorf("new operation reused metadata: %v", calls)
	}
	f := files["one"]
	oldVersion := lanVersion(&f)
	f.ModifiedTime = "v2"
	files["one"] = f
	mu.Unlock()
	updated, err := c.Resolve(c.SourceProofContext(base), s.key, "root", "one", "u")
	if err != nil || updated.Entry.Version == oldVersion {
		t.Fatalf("new operation reused old cloud version: %+v %v", updated.Entry, err)
	}
	mu.Lock()
	f = files["one"]
	f.ParentID = "outside"
	files["one"] = f
	mu.Unlock()
	if _, err := c.Resolve(c.SourceProofContext(base), s.key, "root", "one", "u"); !errors.Is(err, errLANScope) {
		t.Fatalf("moved file accepted: %v", err)
	}
	mu.Lock()
	f.ParentID, f.Trashed = "inside", true
	files["one"] = f
	mu.Unlock()
	if _, err := c.Resolve(c.SourceProofContext(base), s.key, "root", "one", "u"); !errors.Is(err, errLANSource) {
		t.Fatalf("trashed file accepted: %v", err)
	}
	if _, err := c.Resolve(ctx, s.key, "outside", "two", "u"); !errors.Is(err, errLANScope) {
		t.Fatalf("old root proof authorized new root: %v", err)
	}
	// Even a cached node must reject session rebinding and cancellation.
	a, err := c.account(s.key)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.rec.UserID = "replacement"
	s.mu.Unlock()
	if _, err := c.get(ctx, a, "two"); !errors.Is(err, errLANSource) {
		t.Fatalf("cached metadata survived account change: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.get(cancelled, a, "two"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached metadata ignored cancellation: %v", err)
	}
}

func TestLANCloudListRechecksAfterPaging(t *testing.T) {
	for _, mutation := range []string{"unchanged", "move", "trash", "root-trash"} {
		t.Run(mutation, func(t *testing.T) {
			var mu sync.Mutex
			listed := false
			gets := map[string]int{}
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/drive/v1/files" {
					listed = true
					_ = json.NewEncoder(w).Encode(drive.FileList{})
					return
				}
				id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
				gets[id]++
				f := drive.File{ID: id, Kind: drive.KindFolder, UserID: "u"}
				if id == "inside" {
					f.ParentID = "root"
					if listed && mutation == "move" {
						f.ParentID = "outside"
					}
					f.Trashed = listed && mutation == "trash"
				}
				if id == "root" && listed && mutation == "root-trash" {
					f.Trashed = true
				}
				_ = json.NewEncoder(w).Encode(f)
			})
			t.Cleanup(func() { _ = svc.ServiceShutdown() })
			s, _ := svc.active()
			c := &lanCloud{drive: svc}
			ctx := c.SourceProofContext(context.Background())
			if err := c.ValidateRoot(ctx, s.key, "root", "u"); err != nil {
				t.Fatal(err)
			}
			_, err := c.List(ctx, s.key, "root", "inside", "u")
			if mutation == "unchanged" && err != nil {
				t.Fatalf("unchanged listing failed: %v", err)
			}
			if mutation != "unchanged" && err == nil {
				t.Fatal("listing published after parent authorization changed")
			}
			mu.Lock()
			defer mu.Unlock()
			if gets["inside"] != 2 {
				t.Fatalf("parent not freshly rechecked: %v", gets)
			}
			if mutation == "unchanged" {
				if gets["root"] != 2 {
					t.Fatalf("root proof not reused before listing or not fresh afterward: %v", gets)
				}
				t.Log("ValidateRoot + List(root/inside): GetFile=4 (previously 5), ListFiles=1")
			}
		})
	}
}

func TestLANCloudVerifiedProofBypassesBusyHashGate(t *testing.T) {
	body := []byte("verified original content")
	sum := md5.Sum(body)
	cloud := &drive.File{ID: "one", UserID: "u", Size: drive.Int64(len(body)), Phase: drive.PhaseComplete, ModifiedTime: "v1", MD5Checksum: hex.EncodeToString(sum[:])}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "original")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	c := &lanCloud{}
	a := lanAccount{user: "u"}
	if token, pending, err := c.completedAt(context.Background(), a, cloud, path, cloud.MD5Checksum, false, true); err != nil || token == "" || pending {
		t.Fatalf("initial verification failed: %q %v %v", token, pending, err)
	}
	// Another large original occupies the single hashing slot.
	c.proofGate <- struct{}{}
	defer func() { <-c.proofGate }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if token, pending, err := c.completedAt(ctx, a, cloud, path, cloud.MD5Checksum, false, true); err != nil || token == "" || pending {
		t.Fatalf("known proof waited behind unrelated hash: %q %v %v", token, pending, err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(body)), 0600); err != nil {
		t.Fatal(err)
	}
	if token, pending, err := c.completedAt(ctx, a, cloud, path, cloud.MD5Checksum, false, false); err != nil || token != "" || !pending {
		t.Fatalf("same-size replacement inherited proof: %q %v %v", token, pending, err)
	}
	waiting, stopWaiting := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopWaiting()
	if _, _, err := c.completedAt(waiting, a, cloud, path, cloud.MD5Checksum, false, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hash queue ignored cancellation: %v", err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, _, err := c.completedAt(cancelled, a, cloud, path, cloud.MD5Checksum, false, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled hash waiter: %v", err)
	}
}

func TestLANCloudCompletedDownloadIndexStillChecksContent(t *testing.T) {
	body := []byte("downloaded original")
	sum := md5.Sum(body)
	cloud := &drive.File{ID: "one", UserID: "u", Size: drive.Int64(len(body)), Phase: drive.PhaseComplete, ModifiedTime: "v1", MD5Checksum: hex.EncodeToString(sum[:])}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) { authAndTokenRoutes(w, r) })
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	c := &lanCloud{drive: svc}
	a, err := c.account(s.key)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "original")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	index := map[string][]download.Task{"one": {{FileID: "one", State: download.StateDone, Path: path, Size: int64(len(body))}}}
	if got, token, pending, err := c.completed(context.Background(), a, cloud, true, index); err != nil || got != path || token == "" || pending {
		t.Fatalf("indexed completed original unavailable: %q %q %v %v", got, token, pending, err)
	}
	before, err := lanshare.LocalVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(body)), 0600); err != nil {
		t.Fatal(err)
	}
	if after, err := lanshare.LocalVersion(path); err != nil || after == before {
		t.Fatalf("replacement identity did not change: %v", err)
	}
	if got, _, _, err := c.completed(context.Background(), a, cloud, true, index); err != nil || got != "" {
		t.Fatalf("download snapshot authorized replaced content: %q %v", got, err)
	}
}

func TestLANCloudResolveRechecksAfterWaitingForHash(t *testing.T) {
	for _, mutation := range []string{"move", "trash", "version", "unchanged"} {
		t.Run(mutation, func(t *testing.T) {
			body := []byte("completed desktop original")
			sum := md5.Sum(body)
			cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.ServeContent(w, r, "one", time.Time{}, bytes.NewReader(body))
			}))
			defer cdn.Close()
			var mu sync.Mutex
			file := drive.File{ID: "one", ParentID: "root", UserID: "u", Kind: drive.KindFile, Name: "one", Size: drive.Int64(len(body)), Phase: drive.PhaseComplete, ModifiedTime: "v1", MD5Checksum: hex.EncodeToString(sum[:]), Links: map[string]drive.Link{"application/octet-stream": {URL: cdn.URL, Expire: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}}
			proofReached := make(chan struct{}, 1)
			armed := false
			gets := map[string]int{}
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
				gets[id]++
				if id == "one" {
					_ = json.NewEncoder(w).Encode(file)
					return
				}
				_ = json.NewEncoder(w).Encode(drive.File{ID: id, UserID: "u", Kind: drive.KindFolder})
				if id == "root" && armed {
					select {
					case proofReached <- struct{}{}:
					default:
					}
				}
			})
			t.Cleanup(func() { _ = svc.ServiceShutdown() })
			s, _ := svc.active()
			q, err := s.liveDownloads()
			if err != nil {
				t.Fatal(err)
			}
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			task, err := q.Add(download.Source{FileID: "one", Name: "one", Size: int64(len(body)), Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			waitDownload(t, svc, task.ID, downloaded)
			c := &lanCloud{drive: svc, proofGate: make(chan struct{}, 1)}
			c.proofGate <- struct{}{}
			held := true
			defer func() {
				if held {
					<-c.proofGate
				}
			}()
			mu.Lock()
			armed = true
			gets = map[string]int{}
			mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := c.Resolve(ctx, s.key, "root", "one", "u"); result <- err }()
			select {
			case <-proofReached:
			case err := <-result:
				t.Fatalf("resolve returned before hashing slot released: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			mu.Lock()
			switch mutation {
			case "move":
				file.ParentID = "outside"
			case "trash":
				file.Trashed = true
			case "version":
				file.ModifiedTime = "v2"
			}
			mu.Unlock()
			<-c.proofGate
			held = false
			err = <-result
			if mutation == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				before := gets["one"] + gets["root"]
				mu.Unlock()
				if before != 4 {
					t.Fatalf("unknown proof must refresh both nodes: %d", before)
				}
				if _, err := c.Resolve(context.Background(), s.key, "root", "one", "u"); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				after := gets["one"] + gets["root"]
				mu.Unlock()
				if after-before != 2 {
					t.Fatalf("known proof performed extra metadata checks: %d", after-before)
				}
			} else if err == nil {
				t.Fatalf("%s during checksum wait kept old source authorized", mutation)
			}
		})
	}
}

func TestLANCloudVisitorPageDoesNotDrainOrTrustToken(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	parent := "root"
	trashRoot := false
	foreignPage := false
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/drive/v1/files" {
			token := r.URL.Query().Get("page_token")
			requests = append(requests, token)
			id, next := "one", "opaque-next"
			if token != "" {
				id, next = "two", ""
			}
			entryParent := "inside"
			if foreignPage {
				entryParent = "outside"
			}
			_ = json.NewEncoder(w).Encode(drive.FileList{Files: []drive.File{{ID: id, ParentID: entryParent, Kind: drive.KindFolder, UserID: "u"}}, NextPageToken: next})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
		f := drive.File{ID: id, Kind: drive.KindFolder, UserID: "u"}
		if id == "inside" {
			f.ParentID = parent
		}
		if id == "root" {
			f.Trashed = trashRoot
		}
		_ = json.NewEncoder(w).Encode(f)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	c := &lanCloud{drive: svc}
	entries, next, err := c.VisitorPage(context.Background(), s.key, "root", "inside", "u", "")
	if err != nil || len(entries) != 1 || entries[0].ID != "one" || next != "opaque-next" {
		t.Fatalf("first page: %+v %q %v", entries, next, err)
	}
	mu.Lock()
	if len(requests) != 1 {
		t.Errorf("first page drained upstream: %v", requests)
	}
	mu.Unlock()
	entries, next, err = c.VisitorPage(context.Background(), s.key, "root", "inside", "u", next)
	if err != nil || len(entries) != 1 || entries[0].ID != "two" || next != "" {
		t.Fatalf("second page: %+v %q %v", entries, next, err)
	}
	mu.Lock()
	if len(requests) != 2 || requests[1] != "opaque-next" {
		t.Errorf("token not forwarded: %v", requests)
	}
	parent = "outside"
	mu.Unlock()
	if _, _, err := c.VisitorPage(context.Background(), s.key, "root", "inside", "u", "opaque-next"); !errors.Is(err, errLANScope) {
		t.Fatalf("continuation token authorized moved parent: %v", err)
	}
	mu.Lock()
	parent, trashRoot = "root", true
	mu.Unlock()
	if _, _, err := c.VisitorPage(context.Background(), s.key, "root", "inside", "u", "opaque-next"); !errors.Is(err, errLANSource) {
		t.Fatalf("continuation token authorized trashed root: %v", err)
	}
	mu.Lock()
	trashRoot, foreignPage = false, true
	mu.Unlock()
	if _, _, err := c.VisitorPage(context.Background(), s.key, "root", "inside", "u", "forged-token"); !errors.Is(err, errLANSource) {
		t.Fatalf("token exposed another parent: %v", err)
	}
}
