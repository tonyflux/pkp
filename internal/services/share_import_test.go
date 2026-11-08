package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/stream"
)

func TestShareInspectGatesAndRootPaginationStayReadOnly(t *testing.T) {
	var mutations atomic.Int32
	var rootReads atomic.Int32
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("share read exposed account authorization")
		}
		switch r.URL.Path {
		case "/drive/v1/share":
			rootReads.Add(1)
			switch r.URL.Query().Get("pass_code") {
			case "":
				io.WriteString(w, `{"share_status":"PASS_CODE_EMPTY","files":[{"id":"hidden"}]}`)
			case "wrong":
				io.WriteString(w, `{"share_status":"PASS_CODE_ERROR"}`)
			case "expired":
				io.WriteString(w, `{"share_status":"EXPIRED"}`)
			case "right":
				if r.URL.Query().Get("page_token") == "next" {
					if r.URL.Query().Get("pass_code_token") != "capability" {
						t.Error("root pagination lost share capability")
					}
					io.WriteString(w, `{"share_status":"OK","files":[{"id":"second","name":"next.txt"}]}`)
				} else {
					io.WriteString(w, `{"share_status":"OK","title":"Example","user_info":{"nickname":"Owner"},"file_num":"2","pass_code_token":"capability","next_page_token":"next","files":[{"id":"first","name":"clip.mp4","medias":[{"video":{"width":1920,"height":1080,"duration":12}}]}]}`)
				}
			}
		case "/drive/v1/share/detail":
			if r.URL.Query().Get("parent_id") != "folder" {
				t.Error("wrong share parent")
			}
			io.WriteString(w, `{"share_status":"OK","files":[],"next_page_token":"folder-next"}`)
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := svc.current.key
	for _, gate := range []struct{ code, want string }{{"", "PASS_CODE_EMPTY"}, {"wrong", "PASS_CODE_ERROR"}, {"expired", "EXPIRED"}} {
		share, err := svc.InspectShare(account, "https://mypikpak.com/s/example", gate.code)
		if err != nil || share.Status != gate.want || len(share.Entries) != 0 || share.PassCodeToken != "" {
			t.Fatalf("gate %q: %+v %v", gate.code, share, err)
		}
	}
	share, err := svc.InspectShare(account, "https://mypikpak.com/s/example?password=right", "")
	if err != nil || share.Title != "Example" || share.Owner != "Owner" || share.FileCount != 2 || len(share.Entries) != 1 || share.Entries[0].Width != 1920 || share.Entries[0].Duration != 12 {
		t.Fatalf("share metadata: %+v %v", share, err)
	}
	page, err := svc.ListShareFiles(account, share.ShareID, share.PassCodeToken, "", share.NextPageToken)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != "second" || page.PassCodeToken != "capability" || page.NextPageToken != "" {
		t.Fatalf("root page: %+v %v", page, err)
	}
	folder, err := svc.ListShareFiles(account, share.ShareID, page.PassCodeToken, "folder", "")
	if err != nil || folder.PassCodeToken != "capability" || folder.NextPageToken != "folder-next" {
		t.Fatalf("folder page: %+v %v", folder, err)
	}
	corrected, err := svc.InspectShare(account, "https://mypikpak.com/s/example?pass_code=wrong", "right")
	if err != nil || corrected.Status != "OK" || corrected.URL != "https://mypikpak.com/s/example?pass_code=right" {
		t.Fatalf("explicit code did not override linked code: %+v %v", corrected, err)
	}
	reads := rootReads.Load()
	if _, err := svc.InspectShare("", "https://mypikpak.com/s/example", ""); err == nil {
		t.Error("implicit active account accepted")
	}
	if _, err := svc.InspectShare(account, "https://evil.example/s/example", ""); err == nil {
		t.Error("foreign share accepted")
	}
	if rootReads.Load() != reads || mutations.Load() != 0 {
		t.Error("inspection performed an unexpected request or mutation")
	}
}

func TestShareRestoreStaysWithChosenAccountAndConfirmsMapping(t *testing.T) {
	var writes, taskReads, targetReads atomic.Int32
	var targetDevice, taskDevice string
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		switch r.URL.Path {
		case "/drive/v1/share/file_info":
			if r.Header.Get("Authorization") != "" {
				t.Error("membership read leaked auth")
			}
			fmt.Fprintf(w, `{"share_status":"OK","file_info":{"id":%q,"kind":"drive#file","phase":"PHASE_TYPE_COMPLETE"}}`, r.URL.Query().Get("file_id"))
		case "/drive/v1/files/target":
			io.WriteString(w, `{"id":"target","kind":"drive#folder"}`)
		case "/drive/v1/share/restore":
			writes.Add(1)
			targetDevice = r.Header.Get("X-Device-Id")
			var body drive.RestoreShareParams
			var raw map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				t.Error(err)
			}
			_ = json.Unmarshal(raw["parent_id"], &body.ParentID)
			_ = json.Unmarshal(raw["file_ids"], &body.FileIDs)
			if body.ParentID != "target" || len(body.FileIDs) != 2 || len(raw["to"]) != 0 || string(raw["specify_parent_id"]) != "true" {
				t.Errorf("selection/destination changed: %v", raw)
			}
			io.WriteString(w, `{"restore_status":"RESTORE_START","file_id":"target","restore_task_id":"restore-task"}`)
		case "/drive/v1/tasks/restore-task":
			taskReads.Add(1)
			taskDevice = r.Header.Get("X-Device-Id")
			io.WriteString(w, `{"id":"restore-task","phase":"PHASE_TYPE_COMPLETE","params":{"trace_file_ids":"{\"source-b\":\"new-b\",\"source-a\":\"new-a\"}"}}`)
		case "/drive/v1/files/new-a", "/drive/v1/files/new-b":
			// A completed cloud task can precede replica visibility.
			if targetReads.Add(1) == 2 {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"error":"not_found"}`)
				return
			}
			fmt.Fprintf(w, `{"id":%q,"kind":"drive#file","parent_id":"target"}`, strings.TrimPrefix(r.URL.Path, "/drive/v1/files/"))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	chosen := svc.current.key
	other := seedSession(t, svc, "other@example.com").key
	req := RestoreShareRequest{ShareID: "example", ParentID: "target", FileIDs: []string{"source-a", "source-b"}}
	result, err := svc.RestoreShare(chosen, req)
	if err != nil || result.Status != "pending" || result.TaskID != "restore-task" || writes.Load() != 1 || targetDevice != chosen {
		t.Fatalf("accepted restore: %+v %v writes=%d device=%q", result, err, writes.Load(), targetDevice)
	}
	// Reordering the same pending selection must not create another copy.
	req.FileIDs[0], req.FileIDs[1] = req.FileIDs[1], req.FileIDs[0]
	duplicate, err := svc.RestoreShare(chosen, req)
	if err != nil || duplicate.TaskID != result.TaskID || writes.Load() != 1 {
		t.Fatalf("duplicate pending restore: %+v %v", duplicate, err)
	}
	if _, err := svc.ShareRestoreStatus(other, result.TaskID); err == nil || taskReads.Load() != 0 {
		t.Error("other account could poll restore task")
	}
	if _, err := svc.ShareRestoreStatus("", result.TaskID); err == nil {
		t.Error("status followed active account")
	}
	result, err = svc.ShareRestoreStatus(chosen, result.TaskID)
	if err != nil || result.Status != "pending" {
		t.Fatalf("replica lag was terminal or false success: %+v %v", result, err)
	}
	result, err = svc.ShareRestoreStatus(chosen, result.TaskID)
	if err != nil || result.Status != "complete" || strings.Join(result.FileIDs, ",") != "new-a,new-b" || result.ParentID != "target" || taskDevice != chosen || svc.current.key != other {
		t.Fatalf("confirmed restore: %+v %v, task device=%q", result, err, taskDevice)
	}
	if targetReads.Load() != 3 {
		t.Fatalf("validated destinations were fetched again: %d reads", targetReads.Load())
	}
}

func TestShareRestoreConcurrentCompletionNeverRegressesVerifiedProgress(t *testing.T) {
	var aReads, bReads atomic.Int32
	blocked, release := make(chan struct{}), make(chan struct{})
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		switch r.URL.Path {
		case "/drive/v1/share/file_info":
			fmt.Fprintf(w, `{"share_status":"OK","file_info":{"id":%q,"kind":"drive#file"}}`, r.URL.Query().Get("file_id"))
		case "/drive/v1/share/restore":
			io.WriteString(w, `{"restore_status":"RESTORE_START","restore_task_id":"task"}`)
		case "/drive/v1/tasks/task":
			io.WriteString(w, `{"id":"task","phase":"PHASE_TYPE_COMPLETE","params":{"trace_file_ids":"{\"source-a\":\"new-a\",\"source-b\":\"new-b\"}"}}`)
		case "/drive/v1/files/new-a":
			aReads.Add(1)
			io.WriteString(w, `{"id":"new-a","kind":"drive#file"}`)
		case "/drive/v1/files/new-b":
			if bReads.Add(1) == 1 {
				close(blocked)
				<-release
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"error":"not_found"}`)
				return
			}
			io.WriteString(w, `{"id":"new-b","kind":"drive#file"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	account := svc.current.key
	result, err := svc.RestoreShare(account, RestoreShareRequest{ShareID: "example", FileIDs: []string{"source-a", "source-b"}})
	if err != nil || result.Status != "pending" {
		t.Fatalf("accepted restore: %+v %v", result, err)
	}
	type pollResult struct {
		result ShareRestoreResult
		err    error
	}
	done := make(chan pollResult, 1)
	go func() {
		result, err := svc.ShareRestoreStatus(account, "task")
		done <- pollResult{result, err}
	}()
	select {
	case <-blocked:
	case early := <-done:
		t.Fatalf("first poll did not reach replica lag: %+v", early)
	case <-time.After(5 * time.Second):
		t.Fatal("first poll did not reach restored file verification")
	}
	result, err = svc.ShareRestoreStatus(account, "task")
	if err != nil || result.Status != "complete" || strings.Join(result.FileIDs, ",") != "new-a,new-b" {
		t.Fatalf("second poll did not complete: %+v %v", result, err)
	}
	close(release)
	select {
	case late := <-done:
		if late.err != nil || late.result.Status != "complete" || strings.Join(late.result.FileIDs, ",") != "new-a,new-b" {
			t.Fatalf("late replica lag regressed completion: %+v", late)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late poll did not settle")
	}
	if aReads.Load() != 1 || bReads.Load() != 2 {
		t.Fatalf("verified progress was repeated: a=%d b=%d", aReads.Load(), bReads.Load())
	}
}

func TestShareRestoreRejectsInvalidSelectionsBeforeWriting(t *testing.T) {
	var writes atomic.Int32
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		io.WriteString(w, `{"share_status":"OK","file_info":{"id":"not-the-selected-file","kind":"drive#file"}}`)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := svc.current.key
	for _, ids := range [][]string{nil, {"file", "file"}, {"a,b"}, {"../file"}, {"file"}} {
		if _, err := svc.RestoreShare(account, RestoreShareRequest{ShareID: "example", FileIDs: ids}); err == nil {
			t.Errorf("invalid selection accepted: %v", ids)
		}
	}
	if writes.Load() != 0 {
		t.Fatal("invalid selection wrote to cloud")
	}
}

func TestShareRestoreRejectsUnprovenReceiptsAndFailedTasks(t *testing.T) {
	for _, scenario := range []struct{ name, receipt, task string }{
		{"wrong destination", `{"restore_status":"RESTORE_COMPLETE","file_id":"elsewhere","params":{"trace_file_ids":"{\"source\":\"new\"}"}}`, ""},
		{"unknown receipt", `{"restore_status":"RESTORE_UNKNOWN","file_id":"target"}`, ""},
		{"wrong source mapping", `{"restore_status":"RESTORE_COMPLETE","file_id":"target","params":{"trace_file_ids":"{\"other\":\"new\"}"}}`, ""},
		{"task failed", `{"restore_status":"RESTORE_START","file_id":"target","restore_task_id":"task"}`, `{"id":"task","phase":"PHASE_TYPE_ERROR","params":{"error_detail":"file_space_not_enough"}}`},
		{"task mapping extra source", `{"restore_status":"RESTORE_START","file_id":"target","restore_task_id":"task"}`, `{"id":"task","phase":"PHASE_TYPE_COMPLETE","params":{"trace_file_ids":"{\"source\":\"new\",\"extra\":\"other\"}"}}`},
		{"restored outside destination", `{"restore_status":"RESTORE_COMPLETE","file_id":"target","params":{"trace_file_ids":"{\"source\":\"new\"}"}}`, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var writes atomic.Int32
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				switch r.URL.Path {
				case "/drive/v1/files/target":
					io.WriteString(w, `{"id":"target","kind":"drive#folder"}`)
				case "/drive/v1/share/file_info":
					io.WriteString(w, `{"share_status":"OK","file_info":{"id":"source","kind":"drive#file"}}`)
				case "/drive/v1/share/restore":
					writes.Add(1)
					io.WriteString(w, scenario.receipt)
				case "/drive/v1/tasks/task":
					io.WriteString(w, scenario.task)
				case "/drive/v1/files/new":
					io.WriteString(w, `{"id":"new","kind":"drive#file","parent_id":"elsewhere"}`)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
			})
			t.Cleanup(func() { _ = svc.ServiceShutdown() })
			account := svc.current.key
			req := RestoreShareRequest{ShareID: "example", ParentID: "target", FileIDs: []string{"source"}}
			result, err := svc.RestoreShare(account, req)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.task != "" {
				if result.Status != "pending" {
					t.Fatalf("accepted task reported finished: %+v", result)
				}
				result, err = svc.ShareRestoreStatus(account, result.TaskID)
			}
			if err != nil || result.Status != "error" || result.Message == "" {
				t.Fatalf("unproven restore claimed success: %+v %v", result, err)
			}
			if scenario.name == "task failed" && !strings.Contains(result.Message, "file_space_not_enough") {
				t.Error("task failure detail hidden")
			}
			// Closing and reopening loses UI state, not the accepted receipt.
			req.PassCodeToken = "refreshed-capability"
			reopened, err := svc.RestoreShare(account, req)
			if err != nil || reopened.Status != result.Status || reopened.TaskID != result.TaskID || reopened.Message != result.Message || writes.Load() != 1 {
				t.Fatalf("reopening replayed an accepted write: %+v %v writes=%d", reopened, err, writes.Load())
			}
		})
	}
}

func TestShareLostReceiptRemainsPendingWithoutDuplicateMutation(t *testing.T) {
	var writes atomic.Int32
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path == "/drive/v1/share/file_info" {
			io.WriteString(w, `{"share_status":"OK","file_info":{"id":"source","kind":"drive#file"}}`)
			return
		}
		if r.URL.Path == "/drive/v1/share/restore" {
			writes.Add(1)
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		t.Errorf("unexpected route %s", r.URL.Path)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	req := RestoreShareRequest{ShareID: "example", FileIDs: []string{"source"}}
	for range 2 {
		result, err := svc.RestoreShare(svc.current.key, req)
		if err != nil || result.Status != "pending" || result.TaskID != "" || result.Message == "" {
			t.Fatalf("lost receipt: %+v %v", result, err)
		}
	}
	if writes.Load() != 1 {
		t.Fatalf("lost receipt triggered %d writes", writes.Load())
	}
}

func TestShareRestoreCapacityRetainsUncertainAndFailedWrites(t *testing.T) {
	for _, scenario := range []string{"lost receipt", "unknown receipt", "mapping error", "task error"} {
		t.Run(scenario, func(t *testing.T) {
			var writes atomic.Int32
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				switch r.URL.Path {
				case "/drive/v1/share/file_info":
					io.WriteString(w, `{"share_status":"OK","file_info":{"id":"source","kind":"drive#file"}}`)
				case "/drive/v1/share/restore":
					writes.Add(1)
					if scenario == "lost receipt" {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = connection.Close()
						return
					}
					var req struct {
						ShareID string `json:"share_id"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					switch scenario {
					case "unknown receipt":
						io.WriteString(w, `{"restore_status":"RESTORE_UNKNOWN"}`)
					case "mapping error":
						io.WriteString(w, `{"restore_status":"RESTORE_COMPLETE","params":{"trace_file_ids":"{\"wrong\":\"new\"}"}}`)
					case "task error":
						fmt.Fprintf(w, `{"restore_status":"RESTORE_START","restore_task_id":%q}`, req.ShareID)
					}
				default:
					if strings.HasPrefix(r.URL.Path, "/drive/v1/tasks/") {
						fmt.Fprintf(w, `{"id":%q,"phase":"PHASE_TYPE_ERROR","message":"failed"}`, strings.TrimPrefix(r.URL.Path, "/drive/v1/tasks/"))
					} else {
						t.Errorf("unexpected route %s", r.URL.Path)
					}
				}
			})
			t.Cleanup(func() { _ = svc.ServiceShutdown() })
			account := svc.current.key
			var first ShareRestoreResult
			for i := range 64 {
				result, err := svc.RestoreShare(account, RestoreShareRequest{ShareID: fmt.Sprintf("share-%d", i), FileIDs: []string{"source"}})
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "task error" {
					result, err = svc.ShareRestoreStatus(account, result.TaskID)
					if err != nil {
						t.Fatal(err)
					}
				}
				want := "error"
				if scenario == "lost receipt" {
					want = "pending"
				}
				if result.Status != want {
					t.Fatalf("receipt status = %q, want %q", result.Status, want)
				}
				if i == 0 {
					first = result
				}
			}
			if _, err := svc.RestoreShare(account, RestoreShareRequest{ShareID: "overflow", FileIDs: []string{"source"}}); !errors.Is(err, errShareCapacity) {
				t.Fatalf("uncertain receipts evicted for capacity: %v", err)
			}
			reopened, err := svc.RestoreShare(account, RestoreShareRequest{ShareID: "share-0", FileIDs: []string{"source"}})
			if err != nil || reopened.Status != first.Status || reopened.TaskID != first.TaskID || reopened.Message != first.Message || writes.Load() != 64 {
				t.Fatalf("capacity permitted replay: %+v %v writes=%d", reopened, err, writes.Load())
			}
		})
	}
}

func TestShareRestoreCapacityEvictsOnlyOneVerifiedReceipt(t *testing.T) {
	var writes atomic.Int32
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		switch r.URL.Path {
		case "/drive/v1/share/file_info":
			io.WriteString(w, `{"share_status":"OK","file_info":{"id":"source","kind":"drive#file"}}`)
		case "/drive/v1/share/restore":
			writes.Add(1)
			io.WriteString(w, `{"restore_status":"RESTORE_COMPLETE","params":{"trace_file_ids":"{\"source\":\"new\"}"}}`)
		case "/drive/v1/files/new":
			io.WriteString(w, `{"id":"new","kind":"drive#file"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := svc.current.key
	for i := range 65 {
		result, err := svc.RestoreShare(account, RestoreShareRequest{ShareID: fmt.Sprintf("share-%d", i), FileIDs: []string{"source"}})
		if err != nil || result.Status != "complete" {
			t.Fatalf("verified restore %d: %+v %v", i, result, err)
		}
	}
	// Exactly one of the original64 receipts may have been displaced.
	if len(svc.shareImports.restores) != 64 || writes.Load() != 65 {
		t.Fatalf("completed receipt cache not bounded: size=%d writes=%d", len(svc.shareImports.restores), writes.Load())
	}
	for key, tracked := range svc.shareImports.restores {
		if tracked.request.ShareID == "share-64" {
			continue
		}
		result, err := svc.RestoreShare(account, tracked.request)
		if err != nil || result.Status != "complete" || writes.Load() != 65 {
			t.Fatalf("retained verified receipt %s replayed: %+v %v", key, result, err)
		}
		break
	}
}

type shareRestoreTransport func(*http.Request) (*http.Response, error)

func (f shareRestoreTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestShareRestorePreflightReadsHaveIndependentAccountDeadlines(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if !authAndTokenRoutes(w, r) {
			t.Errorf("unexpected real API request %s", r.URL.Path)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	session := svc.current
	client, err := session.api()
	if err != nil {
		t.Fatal(err)
	}
	cfg := client.Config()
	var previous time.Time
	var reads, writes int
	cfg.HTTPClient = &http.Client{Transport: shareRestoreTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > driveTimeout {
			t.Errorf("missing bounded account deadline: %v %v", deadline, ok)
		}
		if !previous.IsZero() && !deadline.After(previous) {
			t.Error("independent API read inherited the previous request's deadline")
		}
		previous = deadline
		if r.Header.Get("X-Device-Id") != session.key {
			t.Error("preflight switched account identity")
		}
		var body string
		switch r.URL.Path {
		case "/drive/v1/files/target":
			body = `{"id":"target","kind":"drive#folder"}`
		case "/drive/v1/share/file_info":
			reads++
			if r.Header.Get("Authorization") != "" {
				t.Error("membership read leaked account credentials")
			}
			body = fmt.Sprintf(`{"share_status":"OK","file_info":{"id":%q,"kind":"drive#file"}}`, r.URL.Query().Get("file_id"))
		case "/drive/v1/share/restore":
			writes++
			if reads != 200 {
				t.Errorf("mutation preceded full selection validation: %d reads", reads)
			}
			body = `{"restore_status":"RESTORE_START","file_id":"target","restore_task_id":"task"}`
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	replacement, err := drive.New(cfg, session.tokens)
	if err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	session.client = replacement
	session.mu.Unlock()
	req := RestoreShareRequest{ShareID: "example", ParentID: "target"}
	for i := range 200 {
		req.FileIDs = append(req.FileIDs, fmt.Sprintf("source-%d", i))
	}
	result, err := svc.RestoreShare(session.key, req)
	if err != nil || result.Status != "pending" || reads != 200 || writes != 1 {
		t.Fatalf("large selection preflight: %+v %v reads=%d writes=%d", result, err, reads, writes)
	}
}

func TestSharePreviewUsesOwnedRangeProxyWithoutChangingPlayback(t *testing.T) {
	body := strings.Repeat("sample text\n", textPreviewLimit/8)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("preview sent account credentials to CDN")
		}
		if r.URL.Path == "/default.m3u8" {
			t.Error("share preview chose a manifest over original media")
		}
		http.ServeContent(w, r, "content", time.Unix(0, 0), strings.NewReader(body))
	}))
	defer origin.Close()
	files := map[string]drive.File{
		"video":       {ID: "video", Name: "clip.mp4", MimeType: "video/mp4", Medias: []drive.Media{{IsDefault: true, Link: drive.Link{URL: origin.URL + "/default.m3u8"}}, {IsOrigin: true, Link: drive.Link{URL: origin.URL + "/original.mp4"}}}},
		"audio":       {ID: "audio", Name: "audio.flac", MimeType: "audio/flac", WebContentLink: origin.URL},
		"image":       {ID: "image", Name: "image.txt", MimeType: "image/png", WebContentLink: origin.URL},
		"pdf":         {ID: "pdf", Name: "doc.bin", MimeType: "application/pdf", WebContentLink: origin.URL},
		"text":        {ID: "text", Name: "not-a-video.mp4", MimeType: "text/plain", WebContentLink: origin.URL},
		"unsupported": {ID: "unsupported", Name: "app.exe", WebContentLink: origin.URL},
		"running":     {ID: "running", Name: "clip.mp4", Phase: drive.PhaseRunning, WebContentLink: origin.URL},
	}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/drive/v1/share/file_info" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected preview read %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(drive.SharedFile{Status: "OK", FileInfo: files[r.URL.Query().Get("file_id")]})
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	proxy := stream.NewServer(quiet())
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	svc.player = &PlayerService{stream: proxy, account: "existing-account", fileID: "existing-playing-file"}
	account := svc.current.key
	for _, kind := range []string{"video", "audio", "image", "pdf", "text"} {
		preview, err := svc.PreviewShareFile(context.Background(), account, "example", "capability", kind)
		if err != nil || preview.Kind != kind || !strings.HasPrefix(preview.URL, "http://127.0.0.1:") || strings.Contains(preview.URL, origin.URL) {
			t.Fatalf("%s preview: %+v %v", kind, preview, err)
		}
		if kind == "text" && (preview.Text == nil || !preview.Text.Truncated || preview.Text.Encoding != "utf-8" || len(preview.Text.Text) > textPreviewLimit) {
			t.Fatalf("unbounded text preview: %+v", preview.Text)
		}
		req, _ := http.NewRequest(http.MethodGet, preview.URL, nil)
		req.Header.Set("Range", "bytes=0-3")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusPartialContent || len(data) != 4 {
			t.Fatalf("range preview: status=%d bytes=%d", response.StatusCode, len(data))
		}
		if kind == "pdf" && response.Header.Get("Content-Type") != "application/pdf" {
			t.Error("PDF MIME lost")
		}
		svc.ClosePreview(context.Background(), preview.URL)
		response, err = http.Get(preview.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Error("closed share preview remained available")
		}
	}
	for _, id := range []string{"unsupported", "running"} {
		if _, err := svc.PreviewShareFile(context.Background(), account, "example", "", id); err == nil {
			t.Errorf("unreadable %s preview accepted", id)
		}
	}
	if svc.player.account != "existing-account" || svc.player.fileID != "existing-playing-file" {
		t.Error("share preview disrupted native playback")
	}
}

func TestShareMappingRejectsAmbiguousOrUnselectedFiles(t *testing.T) {
	for _, trace := range []string{
		`null`, `[]`, `{}`, `{"source":""}`, `{"source":"new","extra":"other"}`, `{"source":"new","source":"other"}`,
		`{"other":"new"}`, `{"source":"new","second":"new"}`, `{"source":"../new"}`, `{"source":12}`, `{"source":"new"} {}`,
	} {
		if _, err := restoredShareIDs(trace, []string{"source"}); err == nil {
			t.Errorf("ambiguous mapping accepted: %s", trace)
		}
	}
	ids, err := restoredShareIDs(`{"second":"new-2","first":"new-1"}`, []string{"first", "second"})
	if err != nil || strings.Join(ids, ",") != "new-1,new-2" {
		t.Fatalf("selection order lost: %v %v", ids, err)
	}
}
