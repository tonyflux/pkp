//go:build windows || (darwin && cgo)

package lanshare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

func newTestManager(t *testing.T, cloud Cloud) *Manager {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{ConfigDir: dir, Cloud: cloud, AllowLoopback: true, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m
}

func localSpace(t *testing.T, m *Manager, prepare bool) (Space, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "inside.txt"), []byte("authorized bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := m.AddSpace(SpaceRequest{Name: "Private local files", Kind: "local", LocalPath: root, AllowPrepare: prepare})
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func authenticated(t *testing.T, m *Manager) string {
	t.Helper()
	id := randomID()
	m.mu.Lock()
	m.running, m.host, m.address = true, "127.0.0.1:17891", "http://127.0.0.1:17891"
	m.interfaceID = "lo@127.0.0.1"
	m.sessions[id] = &session{LastSeen: time.Now(), Generation: m.generation}
	m.mu.Unlock()
	return id
}

func apiRequest(t *testing.T, m *Manager, method, path, cookie string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:17891"+path, bytes.NewReader(data))
	if method == http.MethodPost {
		r.Header.Set("Origin", "http://127.0.0.1:17891")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-PKP-Request", "1")
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "pkp_lan", Value: cookie})
	}
	w := httptest.NewRecorder()
	m.serveHTTP(w, r)
	return w
}

func TestDefaultOffPasswordSaltAndRestart(t *testing.T) {
	m := newTestManager(t, nil)
	if m.Status().Running || m.Status().HasPassword {
		t.Fatal("new service must be off and password-free")
	}
	if err := m.Start(StartRequest{InterfaceID: "lo@127.0.0.1"}); err == nil {
		t.Fatal("started without password and authorization")
	}
	const password = "an independent LAN password"
	if err := m.SetPassword(password); err != nil {
		t.Fatal(err)
	}
	firstSalt := append([]byte(nil), m.password.Salt...)
	if err := m.SetPassword(password); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(firstSalt, m.password.Salt) {
		t.Fatal("password salt was reused")
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(password)) {
		t.Fatal("plaintext password persisted")
	}
	dir := filepath.Dir(m.dir)
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{ConfigDir: dir, AllowLoopback: true, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Status().Running || !reopened.Status().HasPassword || len(reopened.sessions) != 0 {
		t.Fatal("restart restored listener or sessions, or lost password")
	}
}

func TestAuthenticationCSRFAndPublicMetadata(t *testing.T) {
	m := newTestManager(t, nil)
	s, _ := localSpace(t, m, false)
	sessionID := authenticated(t, m)
	for _, path := range []string{"/app.js", "/player.js", "/gestures.js", "/gallery.js", "/select.js", "/locales.js"} {
		asset := apiRequest(t, m, "GET", path, "", nil)
		if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Content-Type"), "javascript") {
			t.Fatalf("visitor module unavailable at %s: %d %s", path, asset.Code, asset.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{"/api/spaces", "/api/files?space=" + s.ID, "/api/file?space=" + s.ID + "&id=forged", "/api/tasks?space=" + s.ID, "/api/content?space=" + s.ID + "&id=forged"} {
		w := apiRequest(t, m, "GET", path, "", nil)
		if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), s.Name) {
			t.Fatalf("metadata before authentication at %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Host = "attacker.example" },
		func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
		func(r *http.Request) { r.Header.Del("X-PKP-Request") },
		func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:17891/api/prepare", strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: "pkp_lan", Value: sessionID})
		r.Header.Set("Origin", "http://127.0.0.1:17891")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-PKP-Request", "1")
		mutate(r)
		w := httptest.NewRecorder()
		m.serveHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("CSRF request accepted: %d", w.Code)
		}
	}
	w := apiRequest(t, m, "GET", "/api/spaces", sessionID, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), s.Name) {
		t.Fatalf("authorized spaces: %s", w.Body.String())
	}
	for _, secret := range []string{"localPath", "accountId", "rootId", s.LocalPath} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("source secret exposed: %s", secret)
		}
	}
}

func TestHomeScreenAssetsBeforeLogin(t *testing.T) {
	m := newTestManager(t, nil)
	s, _ := localSpace(t, m, false)
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("local test file: %+v %v", entries, err)
	}
	m.mu.Lock()
	m.running, m.host = true, "127.0.0.1:17891"
	m.mu.Unlock()
	assets := []struct {
		path     string
		typeName string
		size     int
	}{
		{"/manifest.webmanifest", "application/manifest+json", 0},
		{"/icon-192.png", "image/png", 192},
		{"/icon-512.png", "image/png", 512},
		{"/apple-touch-icon.png", "image/png", 180},
	}
	for _, asset := range assets {
		t.Run(asset.path, func(t *testing.T) {
			w := apiRequest(t, m, http.MethodGet, asset.path, "", nil)
			typ, _, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
			if w.Code != http.StatusOK || err != nil || typ != asset.typeName || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("public asset: status=%d headers=%v error=%v", w.Code, w.Header(), err)
			}
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "manifest-src 'self'") {
				t.Fatal("manifest blocked by CSP")
			}
			if asset.size != 0 {
				icon, err := png.Decode(w.Body)
				if err != nil {
					t.Fatal(err)
				}
				if icon.Bounds().Dx() != asset.size || icon.Bounds().Dy() != asset.size {
					t.Fatalf("unexpected icon dimensions: %v", icon.Bounds())
				}
			} else {
				var manifest map[string]json.RawMessage
				if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]string{
					"id": "/", "scope": "/", "start_url": "/", "display": "standalone",
				} {
					var got string
					if err := json.Unmarshal(manifest[key], &got); err != nil || got != want {
						t.Fatalf("manifest %s: %q, expected %q (%v)", key, got, want, err)
					}
				}
				var icons []map[string]string
				if err := json.Unmarshal(manifest["icons"], &icons); err != nil || len(icons) != 2 {
					t.Fatalf("manifest icons: %+v %v", icons, err)
				}
				for i, icon := range icons {
					wantSize := []string{"192x192", "512x512"}[i]
					if icon["src"] != assets[i+1].path || icon["sizes"] != wantSize || icon["type"] != "image/png" {
						t.Fatalf("unexpected manifest icon: %+v", icon)
					}
				}
			}
			for _, header := range []string{"Host", "Origin"} {
				r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17891"+asset.path, nil)
				if header == "Host" {
					r.Host = "attacker.example"
				} else {
					r.Header.Set(header, "http://attacker.example")
				}
				blocked := httptest.NewRecorder()
				m.serveHTTP(blocked, r)
				if blocked.Code != http.StatusForbidden || blocked.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("foreign %s accepted: status=%d headers=%v", header, blocked.Code, blocked.Header())
				}
			}
		})
	}
	for _, path := range []string{
		"/api/files?space=" + s.ID,
		"/api/file?space=" + s.ID + "&id=" + entries[0].ID,
		"/api/content?space=" + s.ID + "&id=" + entries[0].ID,
	} {
		w := apiRequest(t, m, http.MethodGet, path, "", nil)
		if w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "authorized bytes") {
			t.Fatalf("private file exposed at %s: status=%d headers=%v body=%s", path, w.Code, w.Header(), w.Body.String())
		}
	}
}

func TestLocalOpaqueIDsRejectLinksAndRootReplacement(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("never disclose"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside-link")
	linkErr := os.Symlink(outside, link)
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "inside.txt" {
		t.Fatalf("linked entry exposed: %+v (link error %v)", entries, linkErr)
	}
	for _, forged := range []string{"../secret.txt", "%2e%2e%2fsecret.txt", outside, "inside.txt"} {
		if _, err := m.resolve(context.Background(), s.ID, forged); err == nil {
			t.Fatalf("accepted forged ID %q", forged)
		}
	}
	id := entries[0].ID
	if err := os.Rename(filepath.Join(root, "inside.txt"), filepath.Join(root, "retained.txt")); err != nil {
		t.Fatal(err)
	}
	if linkErr == nil {
		if err := os.Symlink(outside, filepath.Join(root, "inside.txt")); err != nil {
			t.Fatal(err)
		}
		if f, _, err := m.original(context.Background(), s.ID, id, ""); err == nil {
			f.Close()
			t.Fatal("replacement symlink opened")
		}
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root + "-old") })
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.list(context.Background(), s.ID, ""); err == nil {
		t.Fatal("same-path replacement root reauthorized")
	}
}

func TestOpenLocalPinnedHandleSurvivesReplacement(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file")
	if err = os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	f, token, err := OpenLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(f)
	if err != nil || string(contents) != "original" {
		t.Fatalf("handle redirected: %q %v", contents, err)
	}
	fresh, err := LocalVersion(path)
	if err != nil || token == fresh {
		t.Fatal("replacement kept identity token")
	}
}

func TestRangeAndActiveContentIsolation(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	if err := os.WriteFile(filepath.Join(root, "attack.html"), []byte("<script>fetch('/api/spaces')</script>"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	for _, entry := range entries {
		r := httptest.NewRequest("GET", "http://127.0.0.1:17891/api/content?space="+s.ID+"&id="+entry.ID, nil)
		r.AddCookie(&http.Cookie{Name: "pkp_lan", Value: cookie})
		r.Header.Set("Range", "bytes=0-3")
		w := httptest.NewRecorder()
		m.serveHTTP(w, r)
		if w.Code != http.StatusPartialContent || w.Body.Len() != 4 {
			t.Fatalf("range failed: %d %s", w.Code, w.Body.String())
		}
		if entry.Name == "attack.html" && (!strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") || w.Header().Get("Content-Type") != "application/octet-stream") {
			t.Fatal("active HTML could execute in service origin")
		}
	}
}

func TestPasswordAndSpaceRevocationCloseConnections(t *testing.T) {
	m := newTestManager(t, nil)
	a, _ := localSpace(t, m, false)
	b, _ := localSpace(t, m, false)
	cookie := authenticated(t, m)
	add := func(space string) (net.Conn, context.Context) {
		server, client := net.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		response := &activeResponse{Session: cookie, Space: space, Conn: server, Cancel: cancel}
		m.mu.Lock()
		m.responses[response] = struct{}{}
		m.mu.Unlock()
		t.Cleanup(func() { server.Close(); client.Close(); cancel() })
		return client, ctx
	}
	ca, ctxA := add(a.ID)
	_, ctxB := add(b.ID)
	if err := m.UpdateSpace(SpaceUpdate{ID: a.ID, Name: a.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if ctxA.Err() == nil || ctxB.Err() != nil {
		t.Fatal("single-space revocation affected wrong scope")
	}
	_ = ca.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := ca.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("revocation left stream open: %v", err)
	}
	if err := m.SetPassword("replacement password"); err != nil {
		t.Fatal(err)
	}
	if ctxB.Err() == nil {
		t.Fatal("password reset left existing stream active")
	}
	w := apiRequest(t, m, "GET", "/api/spaces", cookie, nil)
	if w.Code != 401 {
		t.Fatal("old cookie survived password reset")
	}
}

type controlledCloud struct {
	mu                sync.Mutex
	identity          string
	allowed           bool
	version           string
	calls             int
	replaceAfterProof bool
	started           chan struct{}
	release           chan struct{}
	cancelled         chan struct{}
}

func newCloud() *controlledCloud {
	return &controlledCloud{identity: "user-A", allowed: true, version: "version-1", started: make(chan struct{}, 8), release: make(chan struct{}, 8), cancelled: make(chan struct{}, 8)}
}
func (c *controlledCloud) AccountIdentity(context.Context, string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.allowed {
		return "", errUnavailable
	}
	return c.identity, nil
}
func (c *controlledCloud) ValidateRoot(ctx context.Context, a, r, identity string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.allowed || c.identity != identity {
		return errUnavailable
	}
	return nil
}
func (c *controlledCloud) List(ctx context.Context, a, r, p, identity string) ([]Entry, error) {
	src, err := c.Resolve(ctx, a, r, "file", identity)
	return []Entry{src.Entry}, err
}
func (c *controlledCloud) Resolve(ctx context.Context, a, r, id, identity string) (Source, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.allowed || c.identity != identity {
		return Source{}, errUnavailable
	}
	return Source{Entry: Entry{ID: "file", Name: "file.txt", Version: c.version, Size: 4, MIME: "text/plain"}}, nil
}
func (c *controlledCloud) Prepare(ctx context.Context, a, r, id, version, destination, checkpoint, identity string, progress func(int64, int64)) (string, error) {
	c.mu.Lock()
	if identity != c.identity {
		c.mu.Unlock()
		return "", errUnavailable
	}
	c.calls++
	c.mu.Unlock()
	c.started <- struct{}{}
	select {
	case <-ctx.Done():
		c.cancelled <- struct{}{}
		return "", ctx.Err()
	case <-c.release:
	}
	if err := os.WriteFile(destination, []byte("data"), 0600); err != nil {
		return "", err
	}
	progress(4, 4)
	token, err := LocalVersion(destination)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	replace := c.replaceAfterProof
	c.mu.Unlock()
	if replace {
		if err = os.WriteFile(destination, []byte("evil"), 0600); err != nil {
			return "", err
		}
	}
	return token, nil
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("expected task signal")
	}
}
func waitState(t *testing.T, m *Manager, id, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		actual := m.tasks[id].State
		m.mu.Unlock()
		if actual == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.mu.Lock()
	actual := m.tasks[id].Task
	m.mu.Unlock()
	t.Fatalf("wanted %s, got %+v", state, actual)
}

func TestPreparationDeduplicatesAndDetachesLastSubscriber(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	first, second := authenticated(t, m), authenticated(t, m)
	req := prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "original"}
	task, err := m.prepare(context.Background(), req, first)
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	joined, err := m.prepare(context.Background(), req, second)
	if err != nil || joined.ID != task.ID {
		t.Fatal("duplicate task was not reused", err)
	}
	if err := m.cancelForSession(context.Background(), s.ID, task.ID, first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cloud.cancelled:
		t.Fatal("one subscriber cancelled another's work")
	default:
	}
	if err := m.cancelForSession(context.Background(), s.ID, task.ID, second); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.cancelled)
	waitState(t, m, task.ID, "cancelled")
	cloud.mu.Lock()
	calls := cloud.calls
	cloud.mu.Unlock()
	if calls != 1 {
		t.Fatalf("duplicate download count %d", calls)
	}
}

func TestStopRetainsStartedOriginalAndHoldsNextStage(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := authenticated(t, m)
	task, err := m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "compatible"}, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	if err = m.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cloud.cancelled:
		t.Fatal("stop cancelled started computer download")
	default:
	}
	cloud.release <- struct{}{}
	waitState(t, m, task.ID, "held")
	src, err := m.resolve(context.Background(), s.ID, "file")
	if err != nil || !src.Entry.Ready {
		t.Fatal("finished original was not retained", err)
	}
	before, err := os.ReadFile(src.LocalPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ClearCache(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(src.LocalPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("clear cache deleted retained original")
	}
	cloud.mu.Lock()
	cloud.identity = "different-user"
	cloud.mu.Unlock()
	if _, err = m.resolve(context.Background(), s.ID, "file"); err == nil {
		t.Fatal("same device ID reauthorized another account")
	}
}

func TestPreparePermissionHoldResumeAndSourceRevocation(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	task, err := m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "original"}, cookie)
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	if err = m.UpdateSpace(SpaceUpdate{ID: s.ID, Name: s.Name, Enabled: true, AllowPrepare: false}); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.cancelled)
	waitState(t, m, task.ID, "held")
	if err = m.ResumeTask(task.ID); !errors.Is(err, errPrepareDisabled) {
		t.Fatalf("resume bypassed preparation permission: %v", err)
	}
	if err = m.UpdateSpace(SpaceUpdate{ID: s.ID, Name: s.Name, Enabled: true, AllowPrepare: true}); err != nil {
		t.Fatal(err)
	}
	if err = m.ResumeTask(task.ID); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	cloud.mu.Lock()
	cloud.allowed = false
	cloud.mu.Unlock()
	waitSignal(t, cloud.cancelled)
	waitState(t, m, task.ID, "held")
}

func TestCacheOwnershipAndActiveReaderPin(t *testing.T) {
	m := newTestManager(t, nil)
	s, _ := localSpace(t, m, true)
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	dir, err := m.newOutputDirectory()
	if err != nil {
		t.Fatal(err)
	}
	rel := dir + "/compatible.mp4"
	path := filepath.Join(m.dir, rel)
	if err = os.WriteFile(path, []byte("cache"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := LocalVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	key := resourceKey(s.ID, entry.ID, entry.Version)
	m.mu.Lock()
	delete(m.pending, dir)
	m.cache[key] = &cacheFile{Path: rel, Token: token, Size: 5, MIME: "video/mp4", Kind: "remux", Used: time.Now()}
	m.mu.Unlock()
	src, err := m.resolve(context.Background(), s.ID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, release, err := m.openCompatible(s.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := m.ClearCache()
	if err != nil || cleared.Skipped != 1 || cleared.RemovedBytes != 0 {
		t.Fatalf("active copy evicted: %+v %v", cleared, err)
	}
	release()
	const budget int64 = 1 << 20
	if err = m.reserve(budget); err != nil {
		t.Fatal(err)
	}
	retained, readErr := os.ReadFile(path)
	m.release(budget)
	if readErr != nil || string(retained) != "cache" {
		t.Fatal("preparing another file evicted an unrelated copy")
	}
	cleared, err = m.ClearCache()
	if err != nil || cleared.RemovedBytes != 5 {
		t.Fatalf("owned unused copy not cleared: %+v %v", cleared, err)
	}
	source, _, err := m.original(context.Background(), s.ID, entry.ID, entry.Version)
	if err != nil {
		t.Fatal("cache clear touched source", err)
	}
	source.Close()
}

func TestCopyReservationsRespectDiskBudget(t *testing.T) {
	m := newTestManager(t, nil)
	free, err := freeBytes(m.copyIdentity.Path)
	if err != nil {
		t.Fatal(err)
	}
	const capacity int64 = 128 << 20
	const reservation int64 = 32 << 20
	const headroom int64 = 16 << 20
	if free < capacity {
		t.Skip("insufficient disk space for reservation accounting")
	}
	m.mu.Lock()
	m.minFree = free - capacity
	m.mu.Unlock()
	for range 2 {
		if err := m.reserve(reservation); err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	reserved := m.reserved
	m.mu.Unlock()
	if reserved != 2*reservation {
		t.Fatalf("repeated reservations not accounted: %d", reserved)
	}
	// Allow ordinary filesystem activity without depending on exact free-byte snapshots.
	if budget := m.outputBudget(headroom); budget < 32<<20 || budget > 64<<20 {
		t.Fatalf("disk budget ignored reservations or packet headroom: %d", budget)
	}
	for _, bytes := range []int64{-1, 96 << 20, 1<<63 - 1} {
		if err := m.reserve(bytes); !errors.Is(err, errDiskFull) {
			t.Fatalf("invalid or overcommitted reservation %d: %v", bytes, err)
		}
	}
	if budget := m.outputBudget(1<<63 - 1); budget != 0 {
		t.Fatalf("oversized packet headroom produced budget %d", budget)
	}
	if budget := m.outputBudget(-1); budget != 0 {
		t.Fatalf("negative packet headroom produced budget %d", budget)
	}
	m.release(2 * reservation)
	if err := m.reserve(96 << 20); err != nil {
		t.Fatalf("released reservations still consume disk budget: %v", err)
	}
	m.release(96 << 20)
}

func TestCopyReservationAndBudgetExceedEightGiB(t *testing.T) {
	m := newTestManager(t, nil)
	const bytes int64 = 9 << 30
	const headroom int64 = 16 << 20
	free, err := freeBytes(m.copyIdentity.Path)
	if err != nil {
		t.Fatal(err)
	}
	if free < bytes+headroom+(256<<20) {
		t.Skip("requires more than 9 GiB of free disk; no output bytes are allocated")
	}
	if budget := m.outputBudget(headroom); budget < bytes {
		t.Fatalf("sufficient disk was capped below 9 GiB: %d", budget)
	}
	if err := m.reserve(bytes); err != nil {
		t.Fatalf("sufficient disk rejected a reservation above 8 GiB: %v", err)
	}
	m.release(bytes)
}

func TestHardLinkDirectoryEntriesHaveDistinctOpaqueIDs(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	if err := os.Link(filepath.Join(root, "inside.txt"), filepath.Join(root, "alias.txt")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID == entries[1].ID {
		t.Fatalf("hard links conflated: %+v", entries)
	}
	for _, entry := range entries {
		resolved, err := m.resolve(context.Background(), s.ID, entry.ID)
		if err != nil || resolved.Entry.Name != entry.Name {
			t.Fatalf("entry identity selected the other name: %+v %v", resolved, err)
		}
	}
}

func TestReplacementReservationPreservesPreviousCopy(t *testing.T) {
	m := newTestManager(t, nil)
	dir, err := m.newOutputDirectory()
	if err != nil {
		t.Fatal(err)
	}
	rel := dir + "/compatible.mp4"
	path := filepath.Join(m.dir, rel)
	if err = os.WriteFile(path, []byte("last valid copy"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := LocalVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.pending, dir)
	m.cache["same-source-version"] = &cacheFile{Path: rel, Token: token, Size: 15, Used: time.Now()}
	m.mu.Unlock()
	unpin := m.pinCurrentCopy("same-source-version")
	defer unpin()
	m.mu.Lock()
	m.minFree = 1<<63 - 1
	m.mu.Unlock()
	if budget := m.outputBudget(16 << 20); budget != 0 {
		t.Fatalf("disk reserve exhausted but output budget is %d", budget)
	}
	if err = m.reserve(1); !errors.Is(err, errDiskFull) {
		t.Fatalf("replacement ignored exhausted disk reserve: %v", err)
	}
	cleared, err := m.ClearCache()
	if err != nil || cleared.Skipped != 1 || cleared.RemovedBytes != 0 {
		t.Fatalf("pinned previous copy cleared: %+v %v", cleared, err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != "last valid copy" {
		t.Fatal("failed replacement evicted last valid copy")
	}
}

func TestRecoveryRemovesOnlyOwnedIncompleteOutputs(t *testing.T) {
	m := newTestManager(t, nil)
	dir, err := m.newOutputDirectory()
	if err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(m.dir, dir, "partial")
	if err = os.WriteFile(owned, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(m.dir, "cache", "user-kept")
	if err = os.WriteFile(foreign, []byte("not owned"), 0600); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.recoverPending()
	m.mu.Unlock()
	if _, err = os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned incomplete output survived recovery")
	}
	if bytes, err := os.ReadFile(foreign); err != nil || string(bytes) != "not owned" {
		t.Fatal("recovery removed unowned content")
	}
}

func TestSourceRemovalInterruptsAnExistingStream(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	response := &activeResponse{Session: cookie, Conn: server, Cancel: cancel, Context: ctx}
	m.mu.Lock()
	m.responses[response] = struct{}{}
	m.mu.Unlock()
	defer func() { cancel(); response.Watch.Wait(); m.mu.Lock(); delete(m.responses, response); m.mu.Unlock() }()
	if err = m.bindResponse(response, s.ID, entries[0].ID, entries[0].Version); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(root, "inside.txt"), filepath.Join(t.TempDir(), "moved.txt")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream continued after source left authorized root")
	}
}

func TestPreparationDisabledCannotBeBypassedByDirectAPI(t *testing.T) {
	m := newTestManager(t, nil)
	s, _ := localSpace(t, m, false)
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	w := apiRequest(t, m, "POST", "/api/prepare", cookie, prepareRequest{SpaceID: s.ID, FileID: entries[0].ID, Kind: "compatible"})
	if w.Code != 403 || !strings.Contains(w.Body.String(), "prepare_disabled") {
		t.Fatalf("preparation bypass: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(t, m, "GET", "/api/content?space="+s.ID+"&id="+entries[0].ID, cookie, nil)
	if w.Code != 200 || w.Body.String() != "authorized bytes" {
		t.Fatal("preparation permission incorrectly revoked original reading")
	}
}

func TestEquivalentBrowserPlansJoinOnePreparation(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, true)
	entries, err := m.list(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	token, err := LocalVersion(filepath.Join(root, entry.Name))
	if err != nil {
		t.Fatal(err)
	}
	info := lanmedia.Info{Container: "matroska", VideoCodec: "h264", AudioCodec: "aac", Width: 1280, Height: 720, Duration: 1, BitDepth: 8, PixelFormat: "yuv420p"}
	caps := lanmedia.Capabilities{H264: true, AAC: true, MP4: true}
	plan, err := lanmedia.MakePlan(info, caps)
	if err != nil {
		t.Fatal(err)
	}
	cookieA, cookieB := authenticated(t, m), authenticated(t, m)
	task := &taskRecord{Task: Task{ID: randomID(), SpaceID: s.ID, FileID: entry.ID, Kind: "compatible", State: "converting"}, Version: entry.Version, RequestKey: resourceKey(s.ID, entry.ID, entry.Version) + "/compatible/" + planKey(plan), Sessions: map[string]bool{cookieA: true}}
	m.mu.Lock()
	m.mediaInfo[resourceKey(s.ID, entry.ID, entry.Version)+"/original/"+token] = info
	m.tasks[task.ID] = task
	m.mu.Unlock()
	// The second browser also supports unrelated codecs; output requirements are unchanged.
	caps.HEVC, caps.VP9, caps.AV1 = true, true, true
	joined, err := m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: entry.ID, Kind: "compatible", Capabilities: caps}, cookieB)
	if err != nil || joined.ID != task.ID {
		t.Fatalf("equivalent output got duplicate preparation: %+v %v", joined, err)
	}
	m.mu.Lock()
	attached := task.Sessions[cookieA] && task.Sessions[cookieB]
	m.mu.Unlock()
	if !attached {
		t.Fatal("reused preparation lost a subscriber")
	}
}

func TestUnchangedDirectoryPollDoesNotRewriteState(t *testing.T) {
	m := newTestManager(t, nil)
	s, _ := localSpace(t, m, false)
	if _, err := m.list(context.Background(), s.ID, ""); err != nil {
		t.Fatal(err)
	}
	first, err := LocalVersion(filepath.Join(m.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.list(context.Background(), s.ID, ""); err != nil {
		t.Fatal(err)
	}
	second, err := LocalVersion(filepath.Join(m.dir, "state.json"))
	if err != nil || first != second {
		t.Fatal("unchanged directory polling rewrote persistent state", err)
	}
}

func TestPinnedReaderRejectsSameSizeRewrites(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file")
	if err = os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	file, token, err := OpenLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	reader := revocableFile{File: file, ctx: context.Background(), token: token}
	if n, err := reader.Read(make([]byte, 8)); n != 0 || !errors.Is(err, errUnavailable) {
		t.Fatalf("rewritten bytes escaped pinned version: n=%d err=%v", n, err)
	}
}

func TestLoginCookieAndFailureThrottle(t *testing.T) {
	m := newTestManager(t, nil)
	const password = "1"
	if err := m.SetPassword(password); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPassword(""); !errors.Is(err, errInvalid) {
		t.Fatalf("empty password: %v", err)
	}
	authenticated(t, m)
	valid := apiRequest(t, m, "POST", "/api/login", "", map[string]string{"password": password})
	if valid.Code != 200 {
		t.Fatal(valid.Body.String())
	}
	cookies := valid.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatal("missing cookie protections")
	}
	for i := range 8 {
		denied := apiRequest(t, m, "POST", "/api/login", "", map[string]string{"password": "wrong"})
		if denied.Code != 401 {
			t.Fatalf("attempt %d: %d", i, denied.Code)
		}
	}
	limited := apiRequest(t, m, "POST", "/api/login", "", map[string]string{"password": "wrong"})
	if limited.Code != 429 {
		t.Fatalf("password guessing not rate limited: %d", limited.Code)
	}
}

func TestListenerBindsOnlyExplicitAddressAndStopRevokes(t *testing.T) {
	m := newTestManager(t, nil)
	localSpace(t, m, false)
	if err := m.SetPassword("independent password"); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	if err = m.Start(StartRequest{InterfaceID: "lo@127.0.0.1", Port: port}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	bound := m.listener.Addr().(*net.TCPAddr)
	m.mu.Unlock()
	if !bound.IP.Equal(net.ParseIP("127.0.0.1")) || bound.IP.IsUnspecified() {
		t.Fatal("service bound an unselected interface", bound)
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(m.Status().Address + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err = m.Stop(); err != nil {
		t.Fatal(err)
	}
	if m.Status().Running || m.Status().Address != "" {
		t.Fatal("stopped server advertises an address")
	}
	if _, err = m.selectedInterface("0@0.0.0.0"); err == nil {
		t.Fatal("wildcard interface accepted")
	}
}

func TestCompletedOriginalRequiresAdapterIdentityEvidence(t *testing.T) {
	cloud := newCloud()
	cloud.replaceAfterProof = true
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	task, err := m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "original"}, cookie)
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	cloud.release <- struct{}{}
	waitState(t, m, task.ID, "held")
	src, err := m.resolve(context.Background(), s.ID, "file")
	if err != nil {
		t.Fatal(err)
	}
	if src.Entry.Ready {
		t.Fatal("same-size replacement after completion proof was published")
	}
	m.mu.Lock()
	count := len(m.originals)
	m.mu.Unlock()
	if count != 0 {
		t.Fatal("unproven replacement entered retained-original map")
	}
}

func TestCacheCleanupRejectsLinkAndOriginalPaths(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	sourcePath := filepath.Join(root, "inside.txt")
	token, err := LocalVersion(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.cache["forged"] = &cacheFile{Path: "originals/inside.txt", Token: token, Size: 16}
	m.mu.Unlock()
	cleared, err := m.ClearCache()
	if err != nil || cleared.Skipped != 1 {
		t.Fatalf("non-cache manifest path accepted: %+v %v", cleared, err)
	}
	dir, err := m.newOutputDirectory()
	if err != nil {
		t.Fatal(err)
	}
	rel := dir + "/compatible.mp4"
	if err = os.Symlink(sourcePath, filepath.Join(m.dir, rel)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	m.mu.Lock()
	delete(m.pending, dir)
	m.cache["linked"] = &cacheFile{Path: rel, Token: token, Size: 16}
	m.mu.Unlock()
	cleared, err = m.ClearCache()
	if err != nil || cleared.Skipped != 2 || cleared.RemovedBytes != 0 {
		t.Fatalf("cleanup followed a link: %+v %v", cleared, err)
	}
	if contents, err := os.ReadFile(filepath.Join(s.LocalPath, "inside.txt")); err != nil || string(contents) != "authorized bytes" {
		t.Fatal("cleanup touched source through link")
	}
}
