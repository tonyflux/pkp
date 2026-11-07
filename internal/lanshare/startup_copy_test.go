//go:build windows || (darwin && cgo)

package lanshare

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

func TestStartupCopyPlaybackAndSourceIsolation(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	space, root := localSpace(t, m, false)
	cookie := authenticated(t, m)
	ctx := context.Background()
	list := func() PreparationEntry {
		t.Helper()
		entries, err := m.ListPreparationEntries(ctx, space.ID, "")
		if err != nil || len(entries) != 1 {
			t.Fatalf("listing: %+v %v", entries, err)
		}
		return entries[0]
	}
	entry := list()
	path := filepath.Join(root, entry.Name)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	caps := lanmedia.Capabilities{H264: true, AAC: true, MP4: true}
	info := lanmedia.Info{Container: "mp4", MIME: "video/mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 12, BitDepth: 8, PixelFormat: "yuv420p", MP4IndexBytes: 5 << 20}
	seedOriginal := func(entry Entry) {
		t.Helper()
		token, err := LocalVersion(path)
		if err != nil {
			t.Fatal(err)
		}
		m.mu.Lock()
		m.mediaInfo[resourceKey(space.ID, entry.ID, entry.Version)+"/original/"+token] = info
		m.mu.Unlock()
	}
	seedOriginal(entry.Entry)
	playback := func(variant string, optimize ...bool) playbackResult {
		t.Helper()
		response := apiRequest(t, m, http.MethodPost, "/api/playback", cookie, playbackRequest{SpaceID: space.ID, FileID: entry.ID, Variant: variant, OptimizeStartup: len(optimize) > 0 && optimize[0], Capabilities: caps})
		var result playbackResult
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Plan == nil {
			t.Fatalf("playback %q: %d %s", variant, response.Code, response.Body.String())
		}
		return result
	}
	assertStartup := func(result playbackResult) {
		t.Helper()
		if result.Variant != "original" || result.Plan.Kind != "direct" || !result.Plan.CanOptimizeStartup || result.Plan.Info.MP4IndexBytes != info.MP4IndexBytes || result.Plan.Info.VideoCodec != info.VideoCodec || result.Plan.Info.AudioCodec != info.AudioCodec {
			t.Fatalf("large-index original did not play directly: %+v", result)
		}
	}
	assertDownload := func(want []byte) {
		t.Helper()
		response := apiRequest(t, m, http.MethodGet, "/api/content?space="+space.ID+"&id="+entry.ID+"&variant=original&download=1", cookie, nil)
		if response.Code != http.StatusOK || response.Body.String() != string(want) || response.Header().Get("Content-Type") != "application/octet-stream" {
			t.Fatalf("original download changed: %d %q", response.Code, response.Body.String())
		}
	}
	assertStartup(playback(""))
	startup := playback("original", true)
	if startup.Plan.Kind != "remux" || startup.Plan.Reason != "startup_index" {
		t.Fatalf("explicit optimization missing: %+v", startup)
	}
	if len(m.Tasks()) != 0 || list().DesktopCompatibleReady {
		t.Fatal("planning automatically prepared a copy")
	}
	// Guests without preparation permission can still play the original immediately.
	fallback := playback("original")
	if fallback.Variant != "original" || fallback.Plan.Kind != "direct" || fallback.Plan.Info.MP4IndexBytes != info.MP4IndexBytes {
		t.Fatalf("original fallback unavailable: %+v", fallback)
	}
	denied := apiRequest(t, m, http.MethodPost, "/api/prepare", cookie, prepareRequest{SpaceID: space.ID, FileID: entry.ID, Kind: "compatible", OptimizeStartup: true, Capabilities: caps})
	if denied.Code != http.StatusForbidden || len(m.Tasks()) != 0 {
		t.Fatalf("guest preparation permission bypassed: %d %s", denied.Code, denied.Body.String())
	}
	assertDownload(original)

	// Keep the real worker occupied so queuing is deterministic without invoking FFmpeg.
	blockerSpace, err := m.AddSpace(SpaceRequest{Name: "Worker blocker", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.PrepareDesktop(ctx, blockerSpace.ID, "file", "original"); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	key := resourceKey(space.ID, entry.ID, entry.Version)
	startupKey := planKey(*startup.Plan)
	legacy := *startup.Plan
	legacy.Reason = "container"
	legacyKey := planKey(legacy)
	if startupKey == legacyKey {
		t.Fatal("startup copy can reuse a legacy remux layout")
	}
	compactInfo := info
	compactInfo.MP4IndexBytes = 1 << 20
	publish := func(plan string, taskID string) string {
		t.Helper()
		dir, err := m.newOutputDirectory()
		if err != nil {
			t.Fatal(err)
		}
		rel := dir + "/compatible.mp4"
		body := []byte("separately owned playback copy")
		if err = os.WriteFile(filepath.Join(m.dir, rel), body, 0600); err != nil {
			t.Fatal(err)
		}
		token, err := LocalVersion(filepath.Join(m.dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.pending, dir)
		m.cache[key] = &cacheFile{Path: rel, Token: token, Size: int64(len(body)), MIME: "video/mp4", Kind: "remux", PlanKey: plan, Used: time.Now()}
		m.mediaInfo[key+"/compatible/"+token] = compactInfo
		if task := m.tasks[taskID]; task != nil {
			task.State, task.Done, task.OutputToken = "ready", task.Total, token
		} else {
			m.tasks[taskID] = &taskRecord{Task: Task{ID: taskID, SpaceID: space.ID, FileID: entry.ID, Kind: "compatible", State: "ready"}, Version: entry.Version, RequestKey: key + "/compatible/" + plan, OutputToken: token, Capabilities: caps, Created: time.Now(), Sessions: make(map[string]bool)}
		}
		return token
	}
	publish(legacyKey, "legacy-layout")
	before := len(m.Tasks())
	task, err := m.prepare(ctx, prepareRequest{SpaceID: space.ID, FileID: entry.ID, Kind: "compatible", OptimizeStartup: true, Capabilities: caps}, "")
	if err != nil || task.ID == "legacy-layout" || task.State != "queued" || len(m.Tasks()) != before+1 {
		t.Fatalf("explicit desktop copy was not newly queued: %+v %v", task, err)
	}
	m.mu.Lock()
	queued := m.tasks[task.ID]
	requestMatches := queued.RequestKey == key+"/compatible/"+startupKey
	m.mu.Unlock()
	if _, err = m.ClearCache(); err != nil {
		t.Fatal(err)
	}
	startupToken := publish(startupKey, task.ID)
	if !requestMatches {
		t.Fatal("desktop request did not retain its startup layout plan")
	}
	assertCompatible := func(wantIndex int64) {
		t.Helper()
		assertStartup(playback(""))
		result := playback("compatible")
		if result.Variant != "compatible" || result.Plan.Kind != "direct" || result.Plan.CanOptimizeStartup || !result.Entry.CompatibleReady || result.Plan.Info.MP4IndexBytes != wantIndex || result.Plan.Info.VideoCodec != info.VideoCodec || result.Plan.Info.AudioCodec != info.AudioCodec {
			t.Fatalf("prepared playback copy not selected: %+v", result)
		}
		if !list().DesktopCompatibleReady {
			t.Fatal("desktop readiness lost its current copy proof")
		}
	}
	assertCompatible(compactInfo.MP4IndexBytes)
	count := len(m.Tasks())
	reused, err := m.prepare(ctx, prepareRequest{SpaceID: space.ID, FileID: entry.ID, Kind: "compatible", OptimizeStartup: true, Capabilities: caps}, "")
	if err != nil || reused.ID != task.ID || reused.State != "ready" || len(m.Tasks()) != count {
		t.Fatalf("prepared copy not reused: %+v %v", reused, err)
	}
	fallback = playback("original")
	if fallback.Variant != "original" || fallback.Plan.Kind != "direct" || fallback.Plan.Info.MP4IndexBytes != info.MP4IndexBytes {
		t.Fatalf("copy replaced the explicit original: %+v", fallback)
	}
	assertDownload(original)

	// A genuine copy may still have a large index; it must not offer an endless copy cycle.
	m.mu.Lock()
	m.mediaInfo[key+"/compatible/"+startupToken] = info
	m.mu.Unlock()
	assertCompatible(info.MP4IndexBytes)
	if len(m.Tasks()) != count {
		t.Fatal("playing a prepared copy submitted another conversion")
	}
	cleared, err := m.ClearCache()
	if err != nil || cleared.RemovedBytes == 0 || cleared.Skipped != 0 {
		t.Fatalf("clear copy: %+v %v", cleared, err)
	}
	if list().DesktopCompatibleReady {
		t.Fatal("cleared cache retained compatible readiness")
	}
	assertStartup(playback(""))
	assertDownload(original)

	publish(startupKey, task.ID)
	assertCompatible(compactInfo.MP4IndexBytes)
	replacement := []byte("replacement original with different bytes and size")
	if err = os.WriteFile(path, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	previousVersion := entry.Version
	entry = list()
	if entry.Version == previousVersion || entry.CompatibleReady || entry.DesktopCompatibleReady {
		t.Fatalf("replaced source inherited the old copy: %+v", entry)
	}
	seedOriginal(entry.Entry)
	result := playback("")
	assertStartup(result)
	if result.Entry.Version != entry.Version || result.Entry.CompatibleReady || len(m.Tasks()) != count {
		t.Fatalf("replacement playback reused or regenerated the old copy: %+v", result)
	}
	assertDownload(replacement)
}
