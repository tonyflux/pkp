//go:build windows || (darwin && cgo)

package lanshare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

type playbackProofCloud struct {
	*taskProofCloud
	path, token string
}

func (c *playbackProofCloud) Resolve(ctx context.Context, account, root, id, identity string) (Source, error) {
	source, err := c.taskProofCloud.Resolve(ctx, account, root, id, identity)
	if err != nil {
		return Source{}, err
	}
	source.Entry.Size, source.Entry.MIME = 4, "video/mp4"
	source.LocalPath, source.LocalVersion = c.path, c.token
	return source, nil
}

func TestPlaybackProofReuseIsRequestScoped(t *testing.T) {
	var mu sync.Mutex
	files := map[string]taskProofFile{
		"root": {ID: "root"}, "outside": {ID: "outside"},
		"folder": {ID: "folder", Parent: "root"},
		"file":   {ID: "file", Parent: "folder", Version: "v1"},
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		file, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(file)
	}))
	defer server.Close()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "original.mp4")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := LocalVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	cloud := &playbackProofCloud{taskProofCloud: &taskProofCloud{controlledCloud: newCloud(), url: server.URL}, path: path, token: token}
	m := newTestManager(t, cloud)
	space, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", RootID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	m.mu.Lock()
	m.mediaInfo[resourceKey(space.ID, "file", "v1")+"/original/"+token] = lanmedia.Info{Container: "mp4", MIME: "video/mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 24, BitDepth: 8, PixelFormat: "yuv420p"}
	m.mu.Unlock()
	query := "?space=" + space.ID + "&id=file"
	request := func(endpoint string) *httptest.ResponseRecorder {
		t.Helper()
		if endpoint == "/api/playback" {
			return apiRequest(t, m, http.MethodPost, endpoint, cookie, playbackRequest{SpaceID: space.ID, FileID: "file", Capabilities: lanmedia.Capabilities{H264: true, AAC: true, MP4: true}})
		}
		return apiRequest(t, m, http.MethodGet, endpoint+query, cookie, nil)
	}
	for _, endpoint := range []string{"/api/playback", "/api/content"} {
		mu.Lock()
		requests = 0
		mu.Unlock()
		response := request(endpoint)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", endpoint, response.Code, response.Body.String())
		}
		mu.Lock()
		count := requests
		mu.Unlock()
		if count != 4 {
			t.Errorf("%s repeated the live source walk: %d upstream requests, want 4", endpoint, count)
		}
		if endpoint == "/api/playback" {
			var playback playbackResult
			if err := json.Unmarshal(response.Body.Bytes(), &playback); err != nil || playback.Plan == nil || playback.Plan.Kind != "direct" {
				t.Fatalf("direct playback lost: %+v %v", playback, err)
			}
		}
	}
	mu.Lock()
	file := files["file"]
	file.Parent = "outside"
	files["file"] = file
	mu.Unlock()
	for _, endpoint := range []string{"/api/playback", "/api/content"} {
		response := request(endpoint)
		if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "data") {
			t.Fatalf("%s reused a stale authorization after a move: %d %s", endpoint, response.Code, response.Body.String())
		}
	}
}
