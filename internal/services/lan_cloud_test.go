package services

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/lanshare"
	"github.com/tonyflux/pkp/internal/syncfs"
)

func TestLANCloudScopeAndCompletedOriginal(t *testing.T) {
	body := bytes.Repeat([]byte("original bytes\x00"), 20000)
	digest := md5.Sum(body)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.bin", time.Time{}, bytes.NewReader(body))
	}))
	defer cdn.Close()
	var mu sync.Mutex
	parent := "inside"
	files := map[string]drive.File{
		"root":    {ID: "root", Kind: drive.KindFolder, Name: "Shared", UserID: "u"},
		"inside":  {ID: "inside", ParentID: "root", Kind: drive.KindFolder, Name: "Folder", UserID: "u"},
		"outside": {ID: "outside", Kind: drive.KindFolder, Name: "Private", UserID: "u"},
		"clip":    {ID: "clip", ParentID: "inside", Kind: drive.KindFile, Name: "clip.bin", UserID: "u", Size: drive.Int64(len(body)), Phase: drive.PhaseComplete, ModifiedTime: "2026-11-01T00:00:00Z", MD5Checksum: hex.EncodeToString(digest[:]), ThumbnailLink: "https://private.example/cover"},
	}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/drive/v1/files" {
			if r.URL.Query().Get("thumbnail_size") != string(drive.ThumbnailMedium) {
				t.Errorf("thumbnail size: %q", r.URL.Query().Get("thumbnail_size"))
			}
			out := []drive.File{}
			for _, f := range files {
				if f.ID == "clip" {
					f.ParentID = parent
				}
				if f.ParentID == r.URL.Query().Get("parent_id") {
					out = append(out, f)
				}
			}
			_ = json.NewEncoder(w).Encode(drive.FileList{Files: out})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
		f, ok := files[id]
		if !ok {
			http.Error(w, `{"error":"not_found"}`, 404)
			return
		}
		if id == "clip" {
			f.ParentID = parent
			f.Links = map[string]drive.Link{"application/octet-stream": {URL: cdn.URL, Expire: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}
		}
		_ = json.NewEncoder(w).Encode(f)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	c := &lanCloud{drive: svc}
	ctx := context.Background()
	if err := c.ValidateRoot(ctx, s.key, "root", "u"); err != nil {
		t.Fatal(err)
	}
	listed, err := c.List(ctx, s.key, "root", "inside", "u")
	if err != nil || len(listed) != 1 || listed[0].Thumbnail != "https://private.example/cover" {
		t.Fatalf("cloud thumbnail listing: %+v %v", listed, err)
	}
	if _, err := c.Resolve(ctx, "", "root", "clip", "u"); !errors.Is(err, errLANSource) {
		t.Fatalf("empty account borrowed foreground: %v", err)
	}
	if _, err := c.Resolve(ctx, s.key, "root", "clip", "previous-user"); !errors.Is(err, errLANSource) {
		t.Fatalf("rebound account accepted: %v", err)
	}
	// Changing the foreground must not change the account authorized by a space.
	svc.mu.Lock()
	svc.current = nil
	svc.mu.Unlock()
	source, err := c.Resolve(ctx, s.key, "root", "clip", "u")
	if err != nil || source.Entry.Ready {
		t.Fatalf("unexpected initial source: %+v %v", source, err)
	}
	dir := t.TempDir()
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "prepared.bin")
	checkpoint := filepath.Join(dir, "checkpoint")
	if err := os.Mkdir(checkpoint, 0700); err != nil {
		t.Fatal(err)
	}
	preparedToken, err := c.Prepare(ctx, s.key, "root", "clip", source.Entry.Version, original, checkpoint, "u", nil)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := lanshare.LocalVersion(original); err != nil || current != preparedToken {
		t.Fatalf("prepared identity changed: %v", err)
	}
	got, err := os.ReadFile(original)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("prepared original differs: %v", err)
	}
	// An ordinary desktop download can be reused only after checksum verification.
	q, err := s.liveDownloads()
	if err != nil {
		t.Fatal(err)
	}
	task, err := q.Add(download.Source{FileID: "clip", Name: "desktop.bin", Size: int64(len(body)), Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	waitDownload(t, svc, task.ID, downloaded)
	source, err = c.Resolve(ctx, s.key, "root", "clip", "u")
	if err != nil || !source.Entry.Ready || source.LocalVersion == "" {
		t.Fatalf("verified desktop original unavailable: %+v %v", source, err)
	}
	if err := os.WriteFile(source.LocalPath, bytes.Repeat([]byte("x"), len(body)), 0600); err != nil {
		t.Fatal(err)
	}
	source, err = c.Resolve(ctx, s.key, "root", "clip", "u")
	if err != nil || source.Entry.Ready {
		t.Fatalf("same-size replaced original accepted: %+v %v", source, err)
	}
	mu.Lock()
	parent = "outside"
	mu.Unlock()
	if _, err := c.Resolve(ctx, s.key, "root", "clip", "u"); !errors.Is(err, errLANScope) {
		t.Fatalf("moved-out file remained authorized: %v", err)
	}
	if _, err := c.List(ctx, s.key, "root", "outside", "u"); !errors.Is(err, errLANScope) {
		t.Fatalf("outside folder listed: %v", err)
	}
	if got, err := os.ReadFile(original); err != nil || !bytes.Equal(got, body) {
		t.Fatal("revocation changed retained original")
	}
	mu.Lock()
	parent = "inside"
	hashless := files["clip"]
	hashless.MD5Checksum = ""
	files["clip"] = hashless
	mu.Unlock()
	source, err = c.Resolve(ctx, s.key, "root", "clip", "u")
	if err != nil {
		t.Fatal(err)
	}
	hashlessPath := filepath.Join(dir, "hashless-original.bin")
	hashlessCheckpoint := filepath.Join(dir, "hashless-checkpoint")
	if err := os.Mkdir(hashlessCheckpoint, 0700); err != nil {
		t.Fatal(err)
	}
	preparedToken, err = c.Prepare(ctx, s.key, "root", "clip", source.Entry.Version, hashlessPath, hashlessCheckpoint, "u", nil)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := lanshare.LocalVersion(hashlessPath); err != nil || current != preparedToken {
		t.Fatalf("hashless proof changed: %v", err)
	}
	if got, err := os.ReadFile(hashlessPath); err != nil || !bytes.Equal(got, body) {
		t.Fatal("hashless preparation differs from received bytes")
	}
}

func TestLANCloudRejectsForeignAndCyclicAncestry(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
		switch id {
		case "foreign":
			io.WriteString(w, `{"id":"foreign","kind":"drive#file","user_id":"other","parent_id":"root"}`)
		case "cycle":
			io.WriteString(w, `{"id":"cycle","kind":"drive#folder","user_id":"u","parent_id":"cycle"}`)
		default:
			io.WriteString(w, `{"files":[]}`)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	c := &lanCloud{drive: svc}
	for _, id := range []string{"foreign", "cycle", "*"} {
		if _, err := c.Resolve(context.Background(), s.key, "root", id, "u"); err == nil {
			t.Fatalf("unauthorized %q accepted", id)
		}
	}
}

func TestLANVisitorPreparationDownloadsOriginalsAfterDeviceRenumbering(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("regression for persisted Darwin mount-session device numbers")
	}
	var picture bytes.Buffer
	if err := jpeg.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 32, 24)), nil); err != nil {
		t.Fatal(err)
	}
	bodies := map[string][]byte{
		"normal": bytes.Repeat([]byte("original binary\x00"), 600000),
		"image":  picture.Bytes(),
		"text":   bytes.Repeat([]byte("original text 原件\n"), 100),
	}
	var transfers, fetches atomic.Int32
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/")
		body, ok := bodies[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Range") == "" {
			t.Error("download engine did not request original byte ranges")
		}
		transfers.Add(1)
		http.ServeContent(w, r, id, time.Unix(1, 0), bytes.NewReader(body))
	}))
	defer cdn.Close()
	files := map[string]drive.File{
		"root":    {ID: "root", Kind: drive.KindFolder, UserID: "u"},
		"private": {ID: "private", Kind: drive.KindFolder, UserID: "u"},
		"normal":  {ID: "normal", Name: "original.bin", MimeType: "application/octet-stream"},
		"image":   {ID: "image", Name: "wallhaven.jpg", MimeType: "image/jpeg"},
		"text":    {ID: "text", Name: "notes.txt", MimeType: "text/plain"},
	}
	for id, body := range bodies {
		f := files[id]
		f.ParentID, f.Kind, f.UserID = "root", drive.KindFile, "u"
		f.Phase, f.ModifiedTime, f.Size = drive.PhaseComplete, "2026-11-01T00:00:00Z", drive.Int64(len(body))
		if id != "image" {
			sum := md5.Sum(body)
			f.MD5Checksum = hex.EncodeToString(sum[:])
		}
		files[id] = f
	}
	var mu sync.Mutex
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		mu.Lock()
		f, ok := files[strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")]
		mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("usage") == string(drive.UsageFetch) {
			fetches.Add(1)
			if f.ID == "normal" {
				f.Links = map[string]drive.Link{"application/octet-stream": {URL: cdn.URL + "/" + f.ID, Expire: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}
			} else {
				f.WebContentLink = cdn.URL + "/" + f.ID
			}
		}
		_ = json.NewEncoder(w).Encode(f)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account, _ := svc.active()
	cloud := &lanCloud{drive: svc}
	opts := lanshare.Options{ConfigDir: t.TempDir(), OriginalDir: t.TempDir(), Cloud: cloud, AllowLoopback: true, MinFreeBytes: 1}
	manager, err := lanshare.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if manager != nil {
			_ = manager.Close()
		}
	})
	space, err := manager.AddSpace(lanshare.SpaceRequest{Name: "Originals", Kind: "cloud", AccountID: account.key, RootID: "root", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	var address string
	var visitor *http.Client
	request := func(method, path string, body any, out any) []byte {
		t.Helper()
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, address+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost {
			req.Header.Set("Origin", address)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-PKP-Request", "1")
		}
		response, err := visitor.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		got, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: HTTP %d %q %v", method, path, response.StatusCode, got, err)
		}
		if out != nil {
			if err := json.Unmarshal(got, out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return got
	}
	start := func() {
		t.Helper()
		if err := manager.SetPassword("isolated visitor test password"); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		if err := manager.Start(lanshare.StartRequest{InterfaceID: "lo@127.0.0.1", Port: port}); err != nil {
			t.Fatal(err)
		}
		address = manager.Status().Address
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		visitor = &http.Client{Jar: jar, Timeout: 10 * time.Second}
		request(http.MethodPost, "/api/login", map[string]string{"password": "isolated visitor test password"}, nil)
	}
	prepare := func(id string) {
		t.Helper()
		before := fetches.Load()
		var task lanshare.Task
		request(http.MethodPost, "/api/prepare", map[string]string{"spaceId": space.ID, "fileId": id, "kind": "original"}, &task)
		deadline := time.Now().Add(20 * time.Second)
		for task.State != "ready" {
			if task.State == "held" || task.State == "failed" || task.State == "cancelled" || time.Now().After(deadline) {
				t.Fatalf("original %s did not become downloadable: %+v", id, task)
			}
			time.Sleep(10 * time.Millisecond)
			var tasks []lanshare.Task
			request(http.MethodGet, "/api/tasks?space="+url.QueryEscape(space.ID), nil, &tasks)
			for _, current := range tasks {
				if current.ID == task.ID {
					task = current
					break
				}
			}
		}
		if task.Done != int64(len(bodies[id])) || fetches.Load()-before < 2 {
			t.Fatalf("original %s bypassed complete Queue.Receive/source recheck: %+v", id, task)
		}
		got := request(http.MethodGet, "/api/content?space="+url.QueryEscape(space.ID)+"&id="+id+"&download=1", nil, nil)
		if !bytes.Equal(got, bodies[id]) {
			t.Fatalf("visitor received different original bytes for %s", id)
		}
	}
	start()
	prepare("normal")
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(opts.ConfigDir, "lan-share", "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	var identity syncfs.Identity
	if err := json.Unmarshal(saved["originalIdentity"], &identity); err != nil {
		t.Fatal(err)
	}
	device, inode, ok := strings.Cut(identity.File, ":")
	dev, err := strconv.ParseUint(device, 10, 64)
	if !ok || err != nil {
		t.Fatalf("unexpected persisted identity %q", identity.File)
	}
	identity.File = strconv.FormatUint(dev+1, 10) + ":" + inode
	saved["originalIdentity"], err = json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err = lanshare.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	start()
	before := transfers.Load()
	if got := request(http.MethodGet, "/api/content?space="+url.QueryEscape(space.ID)+"&id=normal&download=1", nil, nil); !bytes.Equal(got, bodies["normal"]) || transfers.Load() != before {
		t.Fatal("reopening discarded the completed original mapping or downloaded it again")
	}
	prepare("image")
	prepare("text")
	if len(account.downloadQueue().List()) != 0 {
		t.Fatal("visitor preparations leaked into desktop download history")
	}
	mu.Lock()
	original := files["text"]
	mu.Unlock()
	for _, test := range []struct {
		name, parent, modified string
		missing                bool
		want                   error
	}{
		{name: "changed", parent: "root", modified: "2026-11-02T00:00:00Z", want: download.ErrChanged},
		{name: "outside-scope", parent: "private", modified: original.ModifiedTime, want: errLANScope},
		{name: "inaccessible", missing: true, want: errLANSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			mu.Lock()
			changed := original
			changed.ParentID, changed.ModifiedTime = test.parent, test.modified
			files["text"] = changed
			if test.missing {
				delete(files, "text")
			}
			mu.Unlock()
			before := transfers.Load()
			destination := filepath.Join(t.TempDir(), "must-not-publish.txt")
			_, err := cloud.Prepare(context.Background(), account.key, "root", "text", lanVersion(&original), destination, destination+".checkpoint", "u", nil)
			if !errors.Is(err, test.want) {
				t.Fatalf("unauthorized source preparation = %v, want %v", err, test.want)
			}
			if transfers.Load() != before {
				t.Fatal("invalid source transferred bytes")
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid source published an original: %v", err)
			}
		})
	}
}
