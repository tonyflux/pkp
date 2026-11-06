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
	"time"
)

func historyList(t *testing.T, m *Manager, sid, visitor string) []Task {
	t.Helper()
	response := apiRequest(t, m, http.MethodGet, "/api/tasks?space="+sid, visitor, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("task list: %d %s", response.Code, response.Body.String())
	}
	var tasks []Task
	if err := json.Unmarshal(response.Body.Bytes(), &tasks); err != nil {
		t.Fatal(err)
	}
	return tasks
}

func TestVisitorTaskHistorySingleBulkAndSessionScope(t *testing.T) {
	m := newTestManager(t, nil)
	space, root := localSpace(t, m, false)
	other, _ := localSpace(t, m, false)
	first, second := authenticated(t, m), authenticated(t, m)
	entries, err := m.list(context.Background(), space.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	otherEntries, err := m.list(context.Background(), other.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	cacheDir, err := m.newOutputDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(m.dir, cacheDir, "compatible.mp4")
	if err = os.WriteFile(cachePath, []byte("compatible bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	cacheToken, err := LocalVersion(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	key := resourceKey(space.ID, entry.ID, entry.Version)
	m.mu.Lock()
	delete(m.pending, cacheDir)
	m.cache[key] = &cacheFile{Path: cacheDir + "/compatible.mp4", Token: cacheToken, Size: 16, Used: time.Now()}
	m.originals[key] = &preparedFile{Path: filepath.Join(root, "inside.txt"), Token: entry.Version}
	for _, state := range []string{"ready", "failed", "cancelled", "held", "queued", "downloading", "converting"} {
		m.tasks[state] = &taskRecord{Task: Task{ID: state, SpaceID: space.ID, FileID: entry.ID, Name: entry.Name, Kind: "original", State: state}, Version: entry.Version}
	}
	m.tasks["other"] = &taskRecord{Task: Task{ID: "other", SpaceID: other.ID, FileID: otherEntries[0].ID, Kind: "original", State: "cancelled"}, Version: otherEntries[0].Version}
	m.mu.Unlock()
	if len(historyList(t, m, space.ID, first)) != 7 {
		t.Fatal("missing initial task history")
	}
	remove := apiRequest(t, m, http.MethodPost, "/api/tasks/remove", first, map[string]string{"spaceId": space.ID, "taskId": "cancelled"})
	if remove.Code != http.StatusOK || !strings.Contains(remove.Body.String(), "cancelled") {
		t.Fatalf("single remove: %d %s", remove.Code, remove.Body.String())
	}
	// A fresh GET, as on page reload, must not restore this visitor's record.
	if len(historyList(t, m, space.ID, first)) != 6 || len(historyList(t, m, space.ID, second)) != 7 {
		t.Fatal("dismissal was not session-specific or did not survive a new listing")
	}
	clear := apiRequest(t, m, http.MethodPost, "/api/tasks/clear", first, map[string]string{"spaceId": space.ID})
	if clear.Code != http.StatusOK {
		t.Fatalf("bulk remove: %d %s", clear.Code, clear.Body.String())
	}
	remaining := historyList(t, m, space.ID, first)
	if len(remaining) != 4 {
		t.Fatalf("active history changed: %+v", remaining)
	}
	for _, task := range remaining {
		if terminalTask(task.State) {
			t.Fatalf("terminal record survived clear: %+v", task)
		}
	}
	if len(historyList(t, m, other.ID, first)) != 1 || len(historyList(t, m, space.ID, second)) != 7 {
		t.Fatal("clear leaked into another space or visitor")
	}
	m.mu.Lock()
	if len(m.tasks) != 8 || m.originals[key] == nil || m.cache[key] == nil {
		t.Fatal("cleanup removed shared tasks or retained mappings")
	}
	m.mu.Unlock()
	for path, expected := range map[string]string{filepath.Join(root, "inside.txt"): "authorized bytes", cachePath: "compatible bytes"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("retained bytes changed: %s %q %v", path, data, err)
		}
	}
	content := apiRequest(t, m, http.MethodGet, "/api/content?space="+space.ID+"&id="+entry.ID, first, nil)
	if content.Code != http.StatusOK || content.Body.String() != "authorized bytes" {
		t.Fatalf("original no longer readable: %d %s", content.Code, content.Body.String())
	}
}

type historyUnavailableCloud struct {
	*controlledCloud
	resolveMu sync.Mutex
	resolves  int
}

func (c *historyUnavailableCloud) Resolve(context.Context, string, string, string, string) (Source, error) {
	c.resolveMu.Lock()
	c.resolves++
	c.resolveMu.Unlock()
	return Source{}, errUnavailable
}

func TestVisitorHistoryUnavailableSourceAndCancellation(t *testing.T) {
	cloud := &historyUnavailableCloud{controlledCloud: newCloud()}
	m := newTestManager(t, cloud)
	space, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := localSpace(t, m, false)
	first, second, stranger := authenticated(t, m), authenticated(t, m), authenticated(t, m)
	m.mu.Lock()
	m.tasks["held"] = &taskRecord{Task: Task{ID: "held", SpaceID: space.ID, FileID: "missing", State: "held"}, Sessions: map[string]bool{first: true, second: true}}
	m.tasks["finished"] = &taskRecord{Task: Task{ID: "finished", SpaceID: space.ID, FileID: "missing", State: "failed"}}
	m.mu.Unlock()
	post := func(route, sid, id, visitor string) int {
		return apiRequest(t, m, http.MethodPost, route, visitor, map[string]string{"spaceId": sid, "taskId": id}).Code
	}
	if post("/api/tasks/remove", space.ID, "held", first) == http.StatusOK || post("/api/cancel", space.ID, "held", stranger) != http.StatusForbidden || post("/api/cancel", other.ID, "held", first) != http.StatusForbidden {
		t.Fatal("active removal or unauthorized cancellation accepted")
	}
	if post("/api/cancel", space.ID, "held", first) != http.StatusOK {
		t.Fatal("unavailable-source interest could not be withdrawn")
	}
	m.mu.Lock()
	state := m.tasks["held"].State
	m.mu.Unlock()
	if state != "held" {
		t.Fatal("one visitor cancelled another visitor's task")
	}
	if post("/api/cancel", space.ID, "held", second) != http.StatusOK || post("/api/tasks/remove", space.ID, "held", first) != http.StatusOK || post("/api/tasks/remove", space.ID, "finished", first) != http.StatusOK {
		t.Fatal("unavailable-source terminal history could not be dismissed")
	}
	if len(historyList(t, m, space.ID, first)) != 0 {
		t.Fatal("dismissed records returned")
	}
	cloud.resolveMu.Lock()
	resolves := cloud.resolves
	cloud.resolveMu.Unlock()
	if resolves != 0 {
		t.Fatalf("cleanup or filtered listing revalidated per-file source %d times", resolves)
	}
	if post("/api/tasks/remove", other.ID, "finished", first) != http.StatusForbidden || post("/api/tasks/clear", "forged", "", first) != http.StatusForbidden || post("/api/tasks/clear", space.ID, "", "") != http.StatusUnauthorized {
		t.Fatal("unauthorized cleanup scope accepted")
	}
	cloud.mu.Lock()
	cloud.identity = "another-account"
	cloud.mu.Unlock()
	if post("/api/tasks/clear", space.ID, "", first) == http.StatusOK {
		t.Fatal("changed account accepted for cleanup")
	}
}

func TestVisitorHistoryMutationProtection(t *testing.T) {
	m := newTestManager(t, nil)
	space, _ := localSpace(t, m, false)
	visitor := authenticated(t, m)
	for _, route := range []string{"/api/tasks/remove", "/api/tasks/clear"} {
		for _, missing := range []string{"Origin", "Content-Type", "X-PKP-Request"} {
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:17891"+route, strings.NewReader(`{"spaceId":"`+space.ID+`","taskId":"finished"}`))
			request.AddCookie(&http.Cookie{Name: "pkp_lan", Value: visitor})
			request.Header.Set("Origin", "http://127.0.0.1:17891")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-PKP-Request", "1")
			request.Header.Del(missing)
			response := httptest.NewRecorder()
			m.serveHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s accepted without %s: %d", route, missing, response.Code)
			}
		}
	}
	m.mu.Lock()
	m.spaces[space.ID].Enabled = false
	m.mu.Unlock()
	response := apiRequest(t, m, http.MethodPost, "/api/tasks/clear", visitor, map[string]string{"spaceId": space.ID})
	if response.Code != http.StatusForbidden {
		t.Fatal("disabled space history was mutable")
	}
}
