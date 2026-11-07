//go:build windows || (darwin && cgo)

package lanshare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake adapter walks live metadata over HTTP, like the production cloud
// adapter. Counters measure upstream requests, not calls to taskList.
type taskProofCloud struct {
	*controlledCloud
	url string
}

type taskProofFile struct {
	ID, Parent, Version string
}

func (c *taskProofCloud) get(ctx context.Context, id string) (taskProofFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/"+id, nil)
	if err != nil {
		return taskProofFile{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return taskProofFile{}, err
	}
	defer resp.Body.Close()
	var file taskProofFile
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&file) != nil || file.ID != id {
		return taskProofFile{}, errUnavailable
	}
	return file, nil
}

func (c *taskProofCloud) ValidateRoot(ctx context.Context, account, root, identity string) error {
	if identity != "user-A" || account != "device-A" {
		return errUnavailable
	}
	_, err := c.get(ctx, root)
	return err
}

func (c *taskProofCloud) Resolve(ctx context.Context, account, root, id, identity string) (Source, error) {
	if identity != "user-A" || account != "device-A" {
		return Source{}, errUnavailable
	}
	file, err := c.get(ctx, id)
	if err != nil {
		return Source{}, err
	}
	node := file
	for n := 0; n < 4 && node.ID != root; n++ {
		if node.Parent == "" {
			return Source{}, errForbidden
		}
		node, err = c.get(ctx, node.Parent)
		if err != nil {
			return Source{}, err
		}
	}
	if node.ID != root {
		return Source{}, errForbidden
	}
	return Source{Entry: Entry{ID: file.ID, Name: id, Version: file.Version}}, nil
}

func TestTaskListProofsAreRequestAndSpaceScoped(t *testing.T) {
	var mu sync.Mutex
	files := map[string]taskProofFile{
		"root": {ID: "root"}, "other-root": {ID: "other-root"},
		"folder": {ID: "folder", Parent: "root"},
		"file":   {ID: "file", Parent: "folder", Version: "v1"},
		"second": {ID: "second", Parent: "folder", Version: "v2"},
	}
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/")
		calls[id]++
		file, ok := files[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(file)
	}))
	defer server.Close()
	cloud := &taskProofCloud{controlledCloud: newCloud(), url: server.URL}
	m := newTestManager(t, cloud)
	space, err := m.AddSpace(SpaceRequest{Name: "One", Kind: "cloud", AccountID: "device-A", RootID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.AddSpace(SpaceRequest{Name: "Two", Kind: "cloud", AccountID: "device-A", RootID: "other-root"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := authenticated(t, m), authenticated(t, m)
	dir, err := m.newOutputDirectory()
	if err != nil {
		t.Fatal(err)
	}
	rel := dir + "/compatible.mp4"
	path := filepath.Join(m.dir, rel)
	if err := os.WriteFile(path, []byte("cache"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := LocalVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.pending, dir)
	m.cache[resourceKey(space.ID, "file", "v1")] = &cacheFile{Path: rel, Token: token, Size: 5, Used: time.Now()}
	for i := range 10 {
		id := fmt.Sprintf("same-%d", i)
		m.tasks[id] = &taskRecord{Task: Task{ID: id, SpaceID: space.ID, FileID: "file", Kind: "compatible", State: "ready"}, Version: "v1", OutputToken: token}
	}
	m.tasks["bad-token"] = &taskRecord{Task: Task{ID: "bad-token", SpaceID: space.ID, FileID: "file", Kind: "compatible", State: "ready"}, Version: "v1", OutputToken: "wrong"}
	m.tasks["not-ready"] = &taskRecord{Task: Task{ID: "not-ready", SpaceID: space.ID, FileID: "file", Kind: "original", State: "ready"}, Version: "v1"}
	m.tasks["old-version"] = &taskRecord{Task: Task{ID: "old-version", SpaceID: space.ID, FileID: "file", State: "held"}, Version: "old"}
	m.tasks["second"] = &taskRecord{Task: Task{ID: "second", SpaceID: space.ID, FileID: "second", State: "held"}, Version: "v2"}
	m.tasks["other-space"] = &taskRecord{Task: Task{ID: "other-space", SpaceID: other.ID, FileID: "file", State: "held"}, Version: "v1"}
	m.sessions[a].DismissedTasks = map[string]bool{"same-0": true}
	m.mu.Unlock()
	mu.Lock()
	clear(calls)
	mu.Unlock()

	list := func(session, sid string) map[string]Task {
		t.Helper()
		w := apiRequest(t, m, "GET", "/api/tasks?space="+sid, session, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("task list: %d %s", w.Code, w.Body.String())
		}
		var tasks []Task
		if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil {
			t.Fatal(err)
		}
		result := map[string]Task{}
		for _, task := range tasks {
			result[task.ID] = task
		}
		return result
	}
	got := list(a, space.ID)
	if len(got) != 12 || got["same-0"].ID != "" || got["old-version"].ID != "" || got["second"].ID == "" || got["other-space"].ID != "" {
		t.Fatalf("wrong visible records: %+v", got)
	}
	if got["same-1"].State != "ready" || got["bad-token"].Error != "not_ready" || got["not-ready"].Error != "not_ready" {
		t.Fatalf("per-record readiness lost: %+v", got)
	}
	mu.Lock()
	if calls["file"] != 1 || calls["second"] != 1 || calls["folder"] != 2 || calls["root"] != 5 {
		t.Errorf("expected 9 metadata requests (two distinct live proofs), got %v", calls)
	}
	mu.Unlock()
	if got := list(b, space.ID); got["same-0"].ID == "" {
		t.Fatal("dismissal leaked to another session")
	}
	if got := list(b, other.ID); len(got) != 0 {
		t.Fatalf("source authorized under another root: %+v", got)
	}
	mu.Lock()
	file := files["file"]
	file.Parent = "other-root"
	files["file"] = file
	before := calls["file"]
	mu.Unlock()
	got = list(b, space.ID)
	if got["same-1"].ID != "" || got["bad-token"].ID != "" || got["second"].ID == "" {
		t.Fatalf("moved source was exposed or distinct source lost: %+v", got)
	}
	mu.Lock()
	if calls["file"] != before+1 {
		t.Errorf("subsequent request failed to recheck moved source: %v", calls)
	}
	mu.Unlock()
	if got := list(b, other.ID); got["other-space"].ID == "" {
		t.Fatal("valid source in second space was not independently authorized")
	}
	w := apiRequest(t, m, "POST", "/api/logout", b, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("logout: %d", w.Code)
	}
	w = apiRequest(t, m, "GET", "/api/tasks?space="+other.ID, b, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session listed tasks: %d %s", w.Code, w.Body.String())
	}
}
