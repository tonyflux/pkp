package services

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// The task list is walked page by page at the server's largest page; an
// account keeps thousands of tasks, and the first 50 were all it used to see.
func TestTasksPageThroughTheServer(t *testing.T) {
	var limits []string
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		limits = append(limits, r.URL.Query().Get("limit"))
		switch r.URL.Query().Get("page_token") {
		case "":
			io.WriteString(w, `{"tasks":[{"id":"t1","phase":"PHASE_TYPE_COMPLETE"},{"id":"t2","phase":"PHASE_TYPE_ERROR"}],"next_page_token":"p2"}`)
		case "p2":
			io.WriteString(w, `{"tasks":[{"id":"t3","phase":"PHASE_TYPE_COMPLETE"}],"next_page_token":""}`)
		default:
			t.Errorf("unexpected page token %q", r.URL.Query().Get("page_token"))
		}
	})

	var ids []string
	token := ""
	for {
		page, err := svc.Tasks("", token)
		if err != nil {
			t.Fatalf("tasks(%q): %v", token, err)
		}
		for _, task := range page.Tasks {
			ids = append(ids, task.ID)
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	if strings.Join(ids, ",") != "t1,t2,t3" {
		t.Errorf("walked %v, want every task across both pages", ids)
	}
	if strings.Join(limits, ",") != "500,500" {
		t.Errorf("limits asked = %v, want the server's largest page each time", limits)
	}
}

// Polling reads only the tasks still moving: the server filters by phase,
// every page of them is read, and its freshness window comes back to pace
// the next poll.
func TestActiveTasksFilterByPhase(t *testing.T) {
	var filters []string
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		filters = append(filters, r.URL.Query().Get("filters"))
		switch r.URL.Query().Get("page_token") {
		case "":
			io.WriteString(w, `{"tasks":[{"id":"t1","phase":"PHASE_TYPE_RUNNING","progress":40}],"next_page_token":"p2","expires_in":10}`)
		case "p2":
			io.WriteString(w, `{"tasks":[{"id":"t2","phase":"PHASE_TYPE_PENDING"}],"next_page_token":"","expires_in":10}`)
		}
	})

	page, err := svc.ActiveTasks("")
	if err != nil {
		t.Fatalf("active tasks: %v", err)
	}
	if len(page.Tasks) != 2 || page.Tasks[0].ID != "t1" || page.Tasks[1].ID != "t2" || page.Tasks[0].Progress != 40 {
		t.Errorf("tasks = %+v, want t1 and t2 from both pages", page.Tasks)
	}
	if page.ExpiresIn != 10 || page.NextPageToken != "" {
		t.Errorf("expiresIn = %d, next = %q; want 10 and the walk finished", page.ExpiresIn, page.NextPageToken)
	}
	want := `{"phase":{"in":"PHASE_TYPE_PENDING,PHASE_TYPE_RUNNING"}}`
	if len(filters) != 2 || filters[0] != want || filters[1] != want {
		t.Errorf("filters = %v, want %s on every page", filters, want)
	}
}

// A long selection is removed in requests of at most 100 ids, every id
// once. A chunk that fails stops the rest and says how far it got.
func TestRemoveTasksInChunks(t *testing.T) {
	var (
		mu      sync.Mutex
		chunks  [][]string
		failAt  = -1
		deletes []string
	)
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.Method != http.MethodDelete {
			io.WriteString(w, `{}`)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(chunks) == failAt {
			chunks = append(chunks, nil)
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_argument"}`)
			return
		}
		chunks = append(chunks, r.URL.Query()["task_ids"])
		deletes = append(deletes, r.URL.Query().Get("delete_files"))
	})

	ids := make([]string, 250)
	for i := range ids {
		ids[i] = fmt.Sprintf("task-%03d", i)
	}
	if err := svc.RemoveTasks("", ids, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	var sizes []int
	var sent []string
	for _, c := range chunks {
		sizes = append(sizes, len(c))
		sent = append(sent, c...)
	}
	if fmt.Sprint(sizes) != "[100 100 50]" || strings.Join(sent, ",") != strings.Join(ids, ",") {
		t.Errorf("chunks of %v carrying %d ids, want 100, 100, 50 carrying each id once", sizes, len(sent))
	}
	if strings.Join(deletes, ",") != "true,true,true" {
		t.Errorf("delete_files = %v, want it on every chunk", deletes)
	}

	mu.Lock()
	chunks, failAt = nil, 1
	mu.Unlock()
	err := svc.RemoveTasks("", ids, false)
	if err == nil || !strings.Contains(err.Error(), "已移除 100 项（共 250 项）") {
		t.Errorf("err = %v, want it to say how many went before the failure", err)
	}
	if len(chunks) != 2 {
		t.Errorf("%d requests, want the rest stopped after the failing chunk", len(chunks))
	}
}
