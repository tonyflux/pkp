//go:build windows || (darwin && cgo)

package lanshare

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparationThumbnailLocalScopeAndVersion(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 500, 300))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "picture.png")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.png"), []byte("not a png"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range entries {
		ids[e.Name] = e.ID
	}
	task, err := m.PrepareDesktop(context.Background(), s.ID, ids["picture.png"], "original")
	if err != nil || task.State != "ready" {
		t.Fatalf("ready task: %+v %v", task, err)
	}
	before := len(m.Tasks())
	thumb, err := m.PreparationThumbnail(context.Background(), s.ID, ids["picture.png"])
	if err != nil || !strings.HasPrefix(thumb, "data:image/jpeg;base64,") {
		t.Fatalf("thumbnail: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(thumb, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width != 256 || config.Height != 153 {
		t.Fatalf("thumbnail dimensions: %+v %v", config, err)
	}
	reused, err := m.PrepareDesktop(context.Background(), s.ID, ids["picture.png"], "original")
	if err != nil || reused.ID != task.ID || reused.State != "ready" {
		t.Fatalf("thumbnail changed ready task: %+v %v", reused, err)
	}
	for _, id := range []string{ids["broken.png"], ids["inside.txt"], "forged"} {
		if _, err := m.PreparationThumbnail(context.Background(), s.ID, id); err == nil {
			t.Fatalf("unexpected thumbnail for %q", id)
		}
	}
	other, _ := localSpace(t, m, false)
	if _, err := m.PreparationThumbnail(context.Background(), other.ID, ids["picture.png"]); err == nil {
		t.Fatal("cross-space ID accepted")
	}
	if err := os.Rename(path, filepath.Join(root, "previous.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PreparationThumbnail(context.Background(), s.ID, ids["picture.png"]); err == nil {
		t.Fatal("replaced file accepted")
	}
	if err := m.UpdateSpace(SpaceUpdate{ID: s.ID, Name: s.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PreparationThumbnail(context.Background(), s.ID, ids["broken.png"]); err == nil {
		t.Fatal("disabled space accepted")
	}
	if len(m.Tasks()) != before {
		t.Fatal("thumbnail created a task")
	}
}

type thumbnailCloud struct{ *controlledCloud }

func (c thumbnailCloud) List(ctx context.Context, account, root, parent, identity string) ([]Entry, error) {
	entries, err := c.controlledCloud.List(ctx, account, root, parent, identity)
	if err == nil {
		entries[0].Thumbnail = "https://private.example/cover"
	}
	return entries, err
}

func TestPreparationThumbnailCloudPrivacyAndNoDownload(t *testing.T) {
	cloud := thumbnailCloud{newCloud()}
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 || entries[0].Thumbnail != "https://private.example/cover" {
		t.Fatalf("desktop list: %+v %v", entries, err)
	}
	encoded, err := json.Marshal(entries[0])
	if err != nil || !bytes.Contains(encoded, []byte(`"thumbnail":"https://private.example/cover"`)) {
		t.Fatalf("desktop JSON: %s %v", encoded, err)
	}
	visitor := authenticated(t, m)
	response := apiRequest(t, m, "GET", "/api/files?space="+s.ID, visitor, nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), "private.example") || strings.Contains(response.Body.String(), `"thumbnail":`) || !strings.Contains(response.Body.String(), `"hasThumbnail":true`) {
		t.Fatalf("visitor listing leaked thumbnail: %d %s", response.Code, response.Body.String())
	}
	if _, err := m.PreparationThumbnail(context.Background(), s.ID, "file"); err == nil {
		t.Fatal("unready cloud original accepted")
	}
	cloud.mu.Lock()
	calls := cloud.calls
	cloud.mu.Unlock()
	if calls != 0 || len(m.Tasks()) != 0 {
		t.Fatal("thumbnail initiated preparation")
	}
}

func TestVisitorThumbnailHTTPBoundedAndScoped(t *testing.T) {
	m := newTestManager(t, nil)
	space, root := localSpace(t, m, false)
	other, _ := localSpace(t, m, false)
	img := image.NewRGBA(image.Rect(0, 0, 700, 500))
	for y := range 500 {
		for x := range 700 {
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(x), byte(y), byte(x*y), 255
		}
	}
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "picture.png")
	if err := os.WriteFile(path, original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.png"), []byte("not a png"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := m.list(context.Background(), space.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]Entry{}
	for _, entry := range entries {
		files[entry.Name] = entry
	}
	entry := files["picture.png"]
	before, err := LocalVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	server := httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	defer server.Close()
	m.mu.Lock()
	m.host = strings.TrimPrefix(server.URL, "http://")
	m.mu.Unlock()
	endpoint := func(sid string, entry Entry) string {
		return "/api/thumbnail?" + url.Values{"space": {sid}, "id": {entry.ID}, "version": {entry.Version}}.Encode()
	}
	request := func(method, endpoint, session, origin string, status int, code string) []byte {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		if session != "" {
			r.AddCookie(&http.Cookie{Name: "pkp_lan", Value: session})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if method == http.MethodPost {
			r.Header.Set("Origin", server.URL)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-PKP-Request", "1")
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %s (%v)", method, endpoint, response.StatusCode, body, err)
		}
		if status == http.StatusOK {
			if response.Header.Get("Content-Type") != "image/jpeg" {
				t.Fatalf("thumbnail MIME: %s", response.Header.Get("Content-Type"))
			}
		} else if method != http.MethodHead {
			var result map[string]string
			if json.Unmarshal(body, &result) != nil || result["error"] != code || len(result) != 1 {
				t.Fatalf("error contract: %s", body)
			}
		}
		return body
	}
	// Thumbnails must remain available even when original streaming has filled every transfer slot.
	for range cap(m.transferSlots) {
		m.transferSlots <- struct{}{}
	}
	defer func() {
		for range cap(m.transferSlots) {
			<-m.transferSlots
		}
	}()
	query := endpoint(space.ID, entry)
	body := request(http.MethodGet, query, cookie, "", http.StatusOK, "")
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "jpeg" || config.Width != 256 || config.Height != 182 || len(body) > 64<<10 || len(body) >= original.Len() {
		t.Fatalf("unbounded thumbnail: %+v %s bytes=%d original=%d err=%v", config, format, len(body), original.Len(), err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		t.Fatalf("invalid thumbnail JPEG: %v", err)
	}
	after, err := LocalVersion(path)
	unchanged, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || after != before || !bytes.Equal(unchanged, original.Bytes()) {
		t.Fatal("thumbnail modified original")
	}
	request(http.MethodGet, query, "", "", http.StatusUnauthorized, "unauthorized")
	request(http.MethodGet, query, cookie, "https://foreign.example", http.StatusForbidden, "forbidden")
	request(http.MethodHead, query, cookie, "", http.StatusBadRequest, "invalid_request")
	request(http.MethodPost, query, cookie, "", http.StatusBadRequest, "invalid_request")
	request(http.MethodGet, "/api/thumbnail?space="+space.ID+"&id="+entry.ID, cookie, "", http.StatusBadRequest, "invalid_request")
	request(http.MethodGet, endpoint(other.ID, entry), cookie, "", http.StatusForbidden, "forbidden")
	stale := entry
	stale.Version = "stale-version"
	request(http.MethodGet, endpoint(space.ID, stale), cookie, "", http.StatusConflict, "source_unavailable")
	for _, name := range []string{"inside.txt", "broken.png"} {
		request(http.MethodGet, endpoint(space.ID, files[name]), cookie, "", http.StatusConflict, "source_unavailable")
	}
	if err := os.Rename(path, filepath.Join(root, "previous.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, query, cookie, "", http.StatusConflict, "source_unavailable")
	if err := m.UpdateSpace(SpaceUpdate{ID: space.ID, Name: space.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, query, cookie, "", http.StatusForbidden, "forbidden")
	if len(m.Tasks()) != 0 {
		t.Fatal("thumbnail created a preparation task")
	}
}

func TestVisitorThumbnailCloudNotReadyDoesNotPrepare(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	space, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	response := apiRequest(t, m, http.MethodGet, "/api/thumbnail?space="+space.ID+"&id=file&version=version-1", cookie, nil)
	if response.Code != http.StatusConflict || response.Body.String() != "{\"error\":\"not_ready\"}\n" {
		t.Fatalf("unready thumbnail: %d %s", response.Code, response.Body.String())
	}
	cloud.mu.Lock()
	calls := cloud.calls
	cloud.mu.Unlock()
	if calls != 0 || len(m.Tasks()) != 0 {
		t.Fatal("thumbnail initiated cloud preparation")
	}
}

type blockedThumbnailCloud struct {
	*controlledCloud
	entered chan struct{}
}

func (c *blockedThumbnailCloud) Resolve(ctx context.Context, _, _, _, _ string) (Source, error) {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return Source{}, ctx.Err()
}

func TestVisitorThumbnailBindsBeforeGenerationAndRevokes(t *testing.T) {
	cloud := &blockedThumbnailCloud{controlledCloud: newCloud(), entered: make(chan struct{}, 1)}
	m := newTestManager(t, cloud)
	space, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- apiRequest(t, m, http.MethodGet, "/api/thumbnail?space="+space.ID+"&id=file&version=version-1", cookie, nil)
	}()
	select {
	case <-cloud.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("thumbnail did not start source authorization")
	}
	m.mu.Lock()
	bound := false
	for response := range m.responses {
		bound = response.Session == cookie && response.Space == space.ID && response.File == "file" && response.Version == "version-1"
	}
	m.mu.Unlock()
	if !bound {
		t.Fatal("thumbnail generated before response scope was bound")
	}
	if err := m.UpdateSpace(SpaceUpdate{ID: space.ID, Name: space.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response.Body.Len() != 0 {
			t.Fatalf("revoked thumbnail wrote bytes: %s", response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("space revocation did not cancel thumbnail generation")
	}
}
