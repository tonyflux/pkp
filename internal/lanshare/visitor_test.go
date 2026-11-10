//go:build windows || (darwin && cgo)

package lanshare

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

func TestVisitorSnapshotRevisionAndTaskScope(t *testing.T) {
	m := newTestManager(t, nil)
	space, root := localSpace(t, m, true)
	cookie := authenticated(t, m)
	query := "/api/state?space=" + space.ID + "&files=1&tasks=all"
	first := apiRequest(t, m, http.MethodGet, query, cookie, nil)
	var result visitorState
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &result) != nil || result.Files == nil || result.Files.Entries == nil || len(*result.Files.Entries) != 1 {
		t.Fatalf("first snapshot: %d %s", first.Code, first.Body.String())
	}
	entry := (*result.Files.Entries)[0]
	revision := result.Files.Revision
	m.mu.Lock()
	m.tasks["ready"] = &taskRecord{Task: Task{ID: "ready", SpaceID: space.ID, FileID: entry.ID, State: "ready", Kind: "original"}, Version: entry.Version}
	m.tasks["held"] = &taskRecord{Task: Task{ID: "held", SpaceID: space.ID, FileID: entry.ID, State: "held", Kind: "original"}, Version: entry.Version}
	m.mu.Unlock()
	unchanged := apiRequest(t, m, http.MethodGet, query+"&revision="+revision, cookie, nil)
	var same visitorState
	if unchanged.Code != 200 || json.Unmarshal(unchanged.Body.Bytes(), &same) != nil || same.Files.Entries != nil || same.Tasks == nil || len(*same.Tasks) != 2 {
		t.Fatalf("unchanged: %d %s", unchanged.Code, unchanged.Body.String())
	}
	active := apiRequest(t, m, http.MethodGet, "/api/state?space="+space.ID+"&tasks=active", cookie, nil)
	var running visitorState
	if active.Code != 200 || json.Unmarshal(active.Body.Bytes(), &running) != nil || running.Tasks == nil || len(*running.Tasks) != 1 || (*running.Tasks)[0].ID != "held" {
		t.Fatalf("active scope: %d %s", active.Code, active.Body.String())
	}
	if err := os.WriteFile(filepath.Join(root, entry.Name), []byte("changed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := apiRequest(t, m, http.MethodGet, query+"&revision="+revision, cookie, nil)
	var next visitorState
	if changed.Code != 200 || json.Unmarshal(changed.Body.Bytes(), &next) != nil || next.Files.Revision == revision || next.Files.Entries == nil {
		t.Fatalf("changed source hidden by revision: %d %s", changed.Code, changed.Body.String())
	}
	if err := m.UpdateSpace(SpaceUpdate{ID: space.ID, Name: space.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	denied := apiRequest(t, m, http.MethodGet, query+"&revision="+revision, cookie, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("revision bypassed revocation: %d %s", denied.Code, denied.Body.String())
	}
}

func TestVisitorPlaybackChecksCurrentSourceAndDoesNotPrepare(t *testing.T) {
	m := newTestManager(t, nil)
	space, root := localSpace(t, m, true)
	cookie := authenticated(t, m)
	entries, err := m.list(context.Background(), space.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	token, err := LocalVersion(filepath.Join(root, entry.Name))
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.mediaInfo[resourceKey(space.ID, entry.ID, entry.Version)+"/original/"+token] = lanmedia.Info{Container: "mp4", MIME: "video/mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 12, BitDepth: 8}
	m.mu.Unlock()
	// An unrelated cold probe must not block already-proven codec metadata.
	m.probeSlot <- struct{}{}
	defer func() { <-m.probeSlot }()
	request := playbackRequest{SpaceID: space.ID, FileID: entry.ID, Capabilities: lanmedia.Capabilities{H264: true, AAC: true, MP4: true}}
	w := apiRequest(t, m, http.MethodPost, "/api/playback", cookie, request)
	var playback playbackResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &playback) != nil || playback.Plan == nil || playback.Plan.Kind != "direct" || playback.Variant != "original" || playback.Entry.Version != entry.Version {
		t.Fatalf("direct playback: %d %s", w.Code, w.Body.String())
	}
	if len(m.Tasks()) != 0 {
		t.Fatal("planning submitted preparation")
	}
	request.Capabilities.H264 = false
	w = apiRequest(t, m, http.MethodPost, "/api/playback", cookie, request)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &playback) != nil || playback.Plan.Kind == "direct" || len(m.Tasks()) != 0 {
		t.Fatalf("unsupported browser silently prepared or played: %d %s", w.Code, w.Body.String())
	}
	if err := m.UpdateSpace(SpaceUpdate{ID: space.ID, Name: space.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if w := apiRequest(t, m, http.MethodPost, "/api/playback", cookie, request); w.Code != 403 {
		t.Fatalf("playback cache survived revocation: %d %s", w.Code, w.Body.String())
	}
}

func TestContentUsesCurrentProbedMediaTypeWithoutChangingDownloads(t *testing.T) {
	m := newTestManager(t, nil)
	space, root := localSpace(t, m, false)
	entries, err := m.list(context.Background(), space.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	token, err := LocalVersion(filepath.Join(root, entry.Name))
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.mediaInfo[resourceKey(space.ID, entry.ID, entry.Version)+"/original/"+token] = lanmedia.Info{Container: "mp4", MIME: "video/mp4"}
	m.mu.Unlock()
	cookie := authenticated(t, m)
	endpoint := "/api/content?space=" + space.ID + "&id=" + entry.ID
	response := apiRequest(t, m, http.MethodGet, endpoint, cookie, nil)
	if response.Code != 200 || response.Header().Get("Content-Type") != "video/mp4" || response.Body.String() != "authorized bytes" {
		t.Fatalf("probed media response: %d %s", response.Code, response.Body.String())
	}
	download := apiRequest(t, m, http.MethodGet, endpoint+"&download=1", cookie, nil)
	if download.Header().Get("Content-Type") != "application/octet-stream" || download.Body.String() != "authorized bytes" {
		t.Fatal("download bytes or disposition changed")
	}
	if err = os.WriteFile(filepath.Join(root, entry.Name), []byte("new bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := apiRequest(t, m, http.MethodGet, endpoint, cookie, nil)
	if changed.Header().Get("Content-Type") == "video/mp4" {
		t.Fatal("new file identity inherited old media type")
	}
}

type responseCloseProbe struct {
	net.Conn
	mu     sync.Mutex
	closed bool
}

func (c *responseCloseProbe) Close() error { c.mu.Lock(); c.closed = true; c.mu.Unlock(); return nil }

type cancelledResponseCloud struct {
	*controlledCloud
	entered chan struct{}
}

func (c *cancelledResponseCloud) ValidateRoot(ctx context.Context, _, _, _ string) error {
	close(c.entered)
	<-ctx.Done()
	return ctx.Err()
}
func TestCompletedResponseDoesNotCloseReusableConnection(t *testing.T) {
	cloud := &cancelledResponseCloud{controlledCloud: newCloud(), entered: make(chan struct{})}
	m := newTestManager(t, cloud)
	m.mu.Lock()
	m.spaces["scope"] = &spaceRecord{Space: Space{ID: "scope", SpaceRequest: SpaceRequest{Kind: "cloud", AccountID: "account"}, Enabled: true}, AccountIdentity: "user-A"}
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	conn := &responseCloseProbe{}
	response := &activeResponse{Space: "scope", Context: ctx, Cancel: cancel, Conn: conn}
	response.Watch.Add(1)
	go m.watchResponse(response)
	select {
	case <-cloud.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not start")
	}
	cancel()
	response.Watch.Wait()
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if closed {
		t.Fatal("normal response completion closed keep-alive connection")
	}
}
