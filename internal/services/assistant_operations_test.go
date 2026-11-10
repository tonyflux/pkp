package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/upload"
)

func TestAssistantRejectsForbiddenAndMalformedOperationsWithoutRequests(t *testing.T) {
	var requests atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if authAndTokenRoutes(w, r) {
			return
		}
		io.WriteString(w, `{}`)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := drive.DeviceIDFor("user@example.com")
	requests.Store(0)
	cases := []AssistantOperationRequest{
		{Action: "empty_trash"}, {Action: "permanent_delete", IDs: []string{"f1"}},
		{Action: "RemoveFiles", IDs: []string{"f1"}}, {Action: "DeleteTasks", IDs: []string{"f1"}},
		{}, {Action: "star"}, {Action: "star", IDs: []string{"f1", ""}},
		{Action: "star", IDs: []string{"f1", "f1"}}, {Action: "star", IDs: []string{"../f1"}},
		{Action: "star", IDs: []string{"f1"}, ParentID: "dest"},
		{Action: "create_folder", Name: "../secret"}, {Action: "create_folder", Name: "."},
		{Action: "create_folder", Name: "new", IDs: []string{"f1"}},
		{Action: "copy", IDs: []string{"f1"}, ParentID: "https://private.invalid"},
	}
	for _, req := range cases {
		if _, err := svc.AssistantOperate(account, req); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
	if _, err := svc.AssistantOperate("", AssistantOperationRequest{Action: "create_folder", Name: "new"}); err == nil {
		t.Error("accepted implicit active account")
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid operations made %d requests", requests.Load())
	}
}

func TestAssistantPreflightsWholeBatchBeforeAnyWrite(t *testing.T) {
	var writes atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			writes.Add(1)
			io.WriteString(w, `{}`)
			return
		}
		switch r.URL.Path {
		case "/drive/v1/files/f1":
			io.WriteString(w, `{"id":"f1","kind":"drive#file","name":"first"}`)
		case "/drive/v1/files/stale":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"not_found","error_description":"https://private.invalid?token=secret"}`)
		case "/drive/v1/files":
			io.WriteString(w, `{"files":[]}`)
		case "/drive/v1/tasks/cloud":
			io.WriteString(w, `{"id":"cloud","type":"transcoding","phase":"PHASE_TYPE_ERROR"}`)
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
			io.WriteString(w, `{}`)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := drive.DeviceIDFor("user@example.com")
	for _, req := range []AssistantOperationRequest{
		{Action: "star", IDs: []string{"f1", "stale"}},
		{Action: "restore", IDs: []string{"f1"}},
		{Action: "remove_cloud_task", IDs: []string{"cloud"}},
		{Action: "remove_download", IDs: []string{"u1"}},
		{Action: "remove_upload", IDs: []string{"d1"}},
	} {
		if _, err := svc.AssistantOperate(account, req); err == nil {
			t.Errorf("accepted stale/wrong-domain target %+v", req)
		} else if strings.Contains(err.Error(), "private.invalid") || strings.Contains(err.Error(), "secret") {
			t.Errorf("leaked error %v", err)
		}
	}
	if _, err := svc.AssistantOperate("missing-account", AssistantOperationRequest{Action: "star", IDs: []string{"f1"}}); !errors.Is(err, errNoSuchAccount) {
		t.Errorf("wrong account: %v", err)
	}
	if writes.Load() != 0 {
		t.Fatalf("preflight failure allowed %d writes", writes.Load())
	}
}

func TestAssistantCopiesRejectRecursiveAndTrashedDestinations(t *testing.T) {
	var writes atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
		parent := ""
		if id == "child" {
			parent = "source"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "name": id, "kind": drive.KindFolder, "parent_id": parent, "trashed": id == "binned"})
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := drive.DeviceIDFor("user@example.com")
	for _, destination := range []string{"source", "child", "binned"} {
		if _, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: "copy", IDs: []string{"source"}, ParentID: destination}); err == nil {
			t.Errorf("accepted destination %q", destination)
		}
	}
	if writes.Load() != 0 {
		t.Fatalf("invalid copy made %d writes", writes.Load())
	}
}

func TestAssistantNamedAccountRemainsPinnedAndReportsPartialFailure(t *testing.T) {
	var svc *DriveService
	var other *accountSession
	var pinned *accountSession
	var switched atomic.Bool
	var written []string
	svc = newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if strings.HasPrefix(r.URL.Path, "/drive/v1/files/") {
			id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
			if switched.CompareAndSwap(false, true) {
				svc.mu.Lock()
				svc.current = other
				svc.mu.Unlock()
			}
			fmt.Fprintf(w, `{"id":%q,"kind":"drive#file","name":%q}`, id, id)
			return
		}
		if r.URL.Path == "/drive/v1/files:star" {
			// If the operation follows active(), this loses its client now.
			var body struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			written = append(written, body.IDs...)
			if body.IDs[0] == "f2" {
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":"denied","error_description":"/Users/private/token https://private.invalid?token=secret"}`)
				return
			}
			io.WriteString(w, `{}`)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		io.WriteString(w, `{}`)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	pinned, _ = svc.active()
	other = &accountSession{key: "other", rec: accountRecord{Key: "other"}}
	svc.mu.Lock()
	svc.accounts = append(svc.accounts, other)
	svc.mu.Unlock()
	result, err := svc.AssistantOperate(pinned.key, AssistantOperationRequest{Action: "star", IDs: []string{"f1", "f2"}})
	if err != nil || len(result.Results) != 2 || result.Results[0].Error != "" || result.Results[1].Error == "" {
		t.Fatalf("result %+v, error %v", result, err)
	}
	if strings.Join(written, ",") != "f1,f2" {
		t.Fatalf("real side effects %v", written)
	}
	data, _ := json.Marshal(result)
	for _, secret := range []string{"private.invalid", "/Users/", "token=secret"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("receipt leaked %q", secret)
		}
	}
}

func TestAssistantCloudRemovalPreservesFilesAndRetryUsesBackendHash(t *testing.T) {
	const hash = "0123456789ABCDEF0123456789ABCDEF01234567"
	var removed, retried atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		switch {
		case r.URL.Path == "/drive/v1/tasks/cloud":
			fmt.Fprintf(w, `{"id":"cloud","name":"task","type":"offline","phase":"PHASE_TYPE_ERROR","params":{"url":"magnet:?xt=urn:btih:%s&dn=secret"},"reference_resource":{"parent_id":"dest"}}`, hash)
		case r.URL.Path == "/drive/v1/tasks/http":
			io.WriteString(w, `{"id":"http","type":"offline","phase":"PHASE_TYPE_ERROR","params":{"url":"https://private.invalid/file"}}`)
		case r.URL.Path == "/drive/v1/files/dest":
			io.WriteString(w, `{"id":"dest","kind":"drive#folder","name":"saved"}`)
		case r.URL.Path == "/drive/v1/tasks" && r.Method == http.MethodDelete:
			if r.URL.Query().Get("delete_files") != "false" || r.URL.Query().Get("task_ids") != "cloud" {
				t.Errorf("unsafe removal query %v", r.URL.Query())
			}
			removed.Add(1)
			io.WriteString(w, `{}`)
		case r.URL.Path == "/drive/v1/files" && r.Method == http.MethodPost:
			var body struct {
				Parent string `json:"parent_id"`
				URL    struct {
					URL string `json:"url"`
				} `json:"url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Parent != "dest" || body.URL.URL != "magnet:?xt=urn:btih:"+hash {
				t.Errorf("retry did not preserve backend target/hash: %+v", body)
			}
			retried.Add(1)
			io.WriteString(w, `{"task":{"id":"new","phase":"PHASE_TYPE_RUNNING"},"file":{"id":"saved"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			io.WriteString(w, `{}`)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := drive.DeviceIDFor("user@example.com")
	idx := svc.byKey(account).index()
	if idx == nil {
		t.Fatal("account index unavailable")
	}
	for _, action := range []string{"remove_cloud_task", "retry_cloud_task"} {
		got, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: action, IDs: []string{"cloud"}})
		if err != nil || len(got.Results) != 1 || got.Results[0].Error != "" {
			t.Fatalf("%s: %+v %v", action, got, err)
		}
		select {
		case <-idx.inboxNow:
			if action != "retry_cloud_task" {
				t.Errorf("%s woke the arrival watcher without adding a task", action)
			}
		default:
			if action == "retry_cloud_task" {
				t.Error("successful cloud retry did not wake the account's arrival watcher")
			}
		}
		encoded, _ := json.Marshal(got)
		if strings.Contains(string(encoded), hash) {
			t.Error("receipt exposed retry hash")
		}
	}
	if _, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: "retry_cloud_task", IDs: []string{"http"}}); !errors.Is(err, errAssistantRetryHash) {
		t.Errorf("HTTP retry capability error: %v", err)
	}
	if removed.Load() != 1 || retried.Load() != 1 {
		t.Fatalf("side effects removal=%d retry=%d", removed.Load(), retried.Load())
	}
}

func TestAssistantCloudAndShareOwnersUsePinnedSessionIdentity(t *testing.T) {
	const hash = "0123456789ABCDEF0123456789ABCDEF01234567"
	for _, identity := range []struct {
		name    string
		record  string
		token   string
		refresh bool
	}{
		{name: "legacy record", token: "u"},
		{name: "legacy identity populated by refresh", refresh: true},
		{name: "token overrides stale record", record: "foreign", token: "u"},
		{name: "record fallback", record: "u"},
	} {
		t.Run(identity.name, func(t *testing.T) {
			var writes atomic.Int64
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				switch {
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/drive/v1/tasks/"):
					id := strings.TrimPrefix(r.URL.Path, "/drive/v1/tasks/")
					owner := "u"
					if id == "foreign" {
						owner = "foreign"
					}
					fmt.Fprintf(w, `{"id":%q,"user_id":%q,"name":%q,"type":"offline","phase":"PHASE_TYPE_ERROR","params":{"url":"magnet:?xt=urn:btih:%s"}}`, id, owner, id, hash)
				case r.URL.Path == "/drive/v1/share/list":
					io.WriteString(w, `{"data":[{"share_id":"owned","title":"owned","share_status":"OK","user_info":{"user_id":"u"}},{"share_id":"foreign","title":"foreign","share_status":"OK","user_info":{"user_id":"foreign"}}]}`)
				case r.URL.Path == "/drive/v1/tasks" && r.Method == http.MethodDelete,
					r.URL.Path == "/drive/v1/share:batchDelete":
					writes.Add(1)
					io.WriteString(w, `{}`)
				case r.URL.Path == "/drive/v1/files" && r.Method == http.MethodPost:
					writes.Add(1)
					io.WriteString(w, `{"task":{"id":"new","phase":"PHASE_TYPE_RUNNING"},"file":{"id":"saved"}}`)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					io.WriteString(w, `{}`)
				}
			})
			t.Cleanup(func() { _ = svc.ServiceShutdown() })
			pinned, err := svc.active()
			if err != nil {
				t.Fatal(err)
			}
			tok, err := pinned.tokens.Current()
			if err != nil {
				t.Fatal(err)
			}
			tok.UserID = identity.token
			if identity.refresh {
				tok.AccessToken = ""
			}
			if err := pinned.tokens.Seed(tok); err != nil {
				t.Fatal(err)
			}
			pinned.mu.Lock()
			pinned.rec.UserID = identity.record
			pinned.mu.Unlock()
			other := seedSession(t, svc, "other@example.com")
			otherToken, err := other.tokens.Current()
			if err != nil {
				t.Fatal(err)
			}
			otherToken.UserID = "foreign"
			if err := other.tokens.Seed(otherToken); err != nil {
				t.Fatal(err)
			}
			other.mu.Lock()
			other.rec.UserID = "foreign"
			other.mu.Unlock()
			for _, action := range []string{"retry_cloud_task", "remove_cloud_task", "cancel_share"} {
				before := writes.Load()
				for _, ids := range [][]string{{"foreign"}, {"owned", "foreign"}} {
					if _, err := svc.AssistantOperate(pinned.key, AssistantOperationRequest{Action: action, IDs: ids}); !errors.Is(err, errAssistantTarget) {
						t.Errorf("%s accepted foreign owner %v: %v", action, ids, err)
					}
				}
				if writes.Load() != before {
					t.Fatalf("%s wrote before validating all owners", action)
				}
				result, err := svc.AssistantOperate(pinned.key, AssistantOperationRequest{Action: action, IDs: []string{"owned"}})
				if err != nil || len(result.Results) != 1 || result.Results[0].Error != "" || writes.Load() != before+1 {
					t.Fatalf("%s rejected pinned owner: %+v, error %v, writes %d", action, result, err, writes.Load()-before)
				}
			}
		})
	}
}

func assistantJournalTasks(t *testing.T, tasks any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.json")
	data, err := json.Marshal(map[string]any{"version": 1, "tasks": tasks})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAssistantResourcesPageAllLocalTransfersWithoutSecretsOrOtherAccounts(t *testing.T) {
	var cloudPages []string
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path == "/drive/v1/tasks" {
			cloudPages = append(cloudPages, r.URL.Query().Get("page_token"))
			if r.URL.Query().Get("limit") != "40" {
				t.Errorf("cloud page limit %s", r.URL.Query().Get("limit"))
			}
			if r.URL.Query().Get("page_token") == "" {
				io.WriteString(w, `{"tasks":[{"id":"cloud1","name":"https://private.invalid/?token=secret","type":"offline","phase":"PHASE_TYPE_ERROR","message":"password=secret","params":{"url":"magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567","error_detail":"secret"},"icon_link":"https://private.invalid/icon"}],"next_page_token":"second"}`)
			} else {
				io.WriteString(w, `{"tasks":[{"id":"cloud2","name":"second","type":"offline","phase":"PHASE_TYPE_COMPLETE"}]}`)
			}
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		io.WriteString(w, `{}`)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	var uploads []upload.Task
	var downloads []download.Task
	for n := 1; n <= 45; n++ {
		uploads = append(uploads, upload.Task{ID: fmt.Sprint("u", n), Name: fmt.Sprint("upload", n), State: upload.StateFailed, Path: "/Users/private/upload", Error: "password=secret", SavedTo: "/Users/private", Size: 100, Done: 25})
		downloads = append(downloads, download.Task{ID: fmt.Sprint("d", n), Name: fmt.Sprint("download", n), State: download.StatePaused, Path: "/Users/private/download", Origin: "private", Error: "https://private.invalid/secret", Size: 100, Done: 50})
	}
	s.queue().Open(assistantJournalTasks(t, uploads), quiet())
	s.downloadQueue().Open(assistantJournalTasks(t, downloads), quiet())
	other := &accountSession{key: "other", rec: accountRecord{Key: "other"}, uploads: upload.New(nil, 1, nil), downloads: download.New(nil, download.Options{})}
	other.queue().Open(assistantJournalTasks(t, []upload.Task{{ID: "u1", Name: "other-account-secret", State: upload.StateDone}}), quiet())
	svc.mu.Lock()
	svc.accounts = append(svc.accounts, other)
	svc.current = other
	svc.mu.Unlock()
	seen := map[string]bool{}
	token := ""
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber > 6 {
			t.Fatal("pagination failed to terminate")
		}
		page, err := svc.AssistantResources(s.key, "transfers", token)
		if err != nil || len(page.Items) > 40 {
			t.Fatalf("page %d: %+v %v", pageNumber, page, err)
		}
		encoded, _ := json.Marshal(page.Items)
		for _, secret := range []string{"private.invalid", "/Users/", "password", "other-account-secret", "0123456789ABCDEF0123456789ABCDEF01234567"} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("resource page leaked %q", secret)
			}
		}
		for _, item := range page.Items {
			key := item.Kind + ":" + item.ID
			if seen[key] {
				t.Errorf("duplicate %s", key)
			}
			seen[key] = true
		}
		if page.NextPageToken == "" {
			break
		}
		if _, err := svc.AssistantResources(other.key, "transfers", page.NextPageToken); err == nil {
			t.Error("accepted another account's continuation")
		}
		token = page.NextPageToken
	}
	if len(seen) != 92 || strings.Join(cloudPages, ",") != ",second" {
		t.Fatalf("lost resources: %d rows, cloud pages %v", len(seen), cloudPages)
	}
}

func TestAssistantResourcesLocalTransfersSurviveCloudFailure(t *testing.T) {
	for _, counts := range [][2]int{{0, 0}, {1, 1}, {39, 0}, {40, 0}, {0, 40}, {39, 2}, {41, 41}} {
		t.Run(fmt.Sprintf("uploads%d_downloads%d", counts[0], counts[1]), func(t *testing.T) {
			var cloudRequests atomic.Int64
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				if r.URL.Path != "/drive/v1/tasks" || r.Method != http.MethodGet {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				cloudRequests.Add(1)
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"invalid_argument","error_description":"cloud unavailable"}`)
			})
			t.Cleanup(func() { _ = svc.ServiceShutdown() })
			s, _ := svc.active()
			var uploads []upload.Task
			var downloads []download.Task
			for n := 1; n <= counts[0]; n++ {
				uploads = append(uploads, upload.Task{ID: fmt.Sprint("u", n), Name: "upload", State: upload.StateFailed})
			}
			for n := 1; n <= counts[1]; n++ {
				downloads = append(downloads, download.Task{ID: fmt.Sprint("d", n), Name: "download", State: download.StatePaused})
			}
			s.queue().Open(assistantJournalTasks(t, uploads), quiet())
			s.downloadQueue().Open(assistantJournalTasks(t, downloads), quiet())
			seen := map[string]bool{}
			token := ""
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > 4 {
					t.Fatal("pagination failed to reach cloud stage")
				}
				page, err := svc.AssistantResources(s.key, "transfers", token)
				if err != nil {
					if len(seen) != counts[0]+counts[1] || len(page.Items) != 0 || cloudRequests.Load() != 1 {
						t.Fatalf("cloud failed before local catalog completed: seen=%d page=%+v cloud=%d error=%v", len(seen), page, cloudRequests.Load(), err)
					}
					break
				}
				if len(page.Items) == 0 || len(page.Items) > assistantResourceLimit || page.NextPageToken == "" || cloudRequests.Load() != 0 {
					t.Fatalf("local page depended on cloud or lost continuation: %+v cloud=%d", page, cloudRequests.Load())
				}
				for _, item := range page.Items {
					key := item.Kind + ":" + item.ID
					if seen[key] || (item.Kind != "upload" && item.Kind != "download") {
						t.Fatalf("unexpected local item %+v", item)
					}
					seen[key] = true
				}
				token = page.NextPageToken
			}
			for _, target := range []struct {
				kind, id, action string
			}{{"upload", "u1", "remove_upload"}, {"download", "d1", "remove_download"}} {
				if !seen[target.kind+":"+target.id] {
					continue
				}
				result, err := svc.AssistantOperate(s.key, AssistantOperationRequest{Action: target.action, IDs: []string{target.id}})
				if err != nil || len(result.Results) != 1 || result.Results[0].Error != "" {
					t.Fatalf("local control after cloud failure: %+v %v", result, err)
				}
			}
			if cloudRequests.Load() != 1 {
				t.Fatalf("local control made cloud requests: %d", cloudRequests.Load())
			}
		})
	}
}

func TestAssistantLocalRemovalChangesOnlyNamedQueue(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		t.Errorf("local removal unexpectedly called %s", r.URL.Path)
		io.WriteString(w, `{}`)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	finished := filepath.Join(t.TempDir(), "finished.txt")
	if err := os.WriteFile(finished, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.downloadQueue().Open(assistantJournalTasks(t, []download.Task{{ID: "d1", Name: "finished.txt", State: download.StateDone, Path: finished}}), quiet())
	got, err := svc.AssistantOperate(s.key, AssistantOperationRequest{Action: "remove_download", IDs: []string{"d1"}})
	if err != nil || len(got.Results) != 1 || got.Results[0].Error != "" || len(s.downloadQueue().List()) != 0 {
		t.Fatalf("removal %+v %v, queue %+v", got, err, s.downloadQueue().List())
	}
	if data, err := os.ReadFile(finished); err != nil || string(data) != "keep me" {
		t.Fatalf("finished download destroyed: %q %v", data, err)
	}
}

func TestAssistantCreatesFolderAndConfirmsRestore(t *testing.T) {
	var restored atomic.Bool
	var created, untrashed atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		switch r.URL.Path {
		case "/drive/v1/files":
			if r.Method == http.MethodPost {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["name"] != "new" || body["kind"] != drive.KindFolder {
					t.Errorf("folder body %v", body)
				}
				created.Add(1)
				io.WriteString(w, `{"file":{"id":"created","name":"new(1)","kind":"drive#folder"}}`)
			} else {
				io.WriteString(w, `{"files":[{"id":"binned","name":"old","kind":"drive#file","trashed":true}]}`)
			}
		case "/drive/v1/files:batchUntrash":
			restored.Store(true)
			untrashed.Add(1)
			io.WriteString(w, `{}`)
		case "/drive/v1/files:batchGet":
			if restored.Load() {
				io.WriteString(w, `{"files":[{"id":"binned","name":"restored","kind":"drive#file","trashed":false}]}`)
			} else {
				io.WriteString(w, `{"files":[]}`)
			}
		default:
			t.Errorf("unexpected restore/create request %s", r.URL.Path)
			io.WriteString(w, `{}`)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := drive.DeviceIDFor("user@example.com")
	made, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: "create_folder", Name: "new"})
	if err != nil || len(made.Results) != 1 || made.Results[0].ID != "created" || made.Results[0].Name != "new(1)" || made.Results[0].Error != "" {
		t.Fatalf("folder result %+v %v", made, err)
	}
	back, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: "restore", IDs: []string{"binned"}})
	if err != nil || len(back.Results) != 1 || back.Results[0].Error != "" || back.Results[0].ID != "binned" {
		t.Fatalf("restore result %+v %v", back, err)
	}
	if created.Load() != 1 || untrashed.Load() != 1 || !restored.Load() {
		t.Fatalf("side effects: created=%d restored=%d", created.Load(), untrashed.Load())
	}
}

func TestAssistantShareCancellationFindsLaterPageWithoutReturningSecrets(t *testing.T) {
	var cancelled atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		switch r.URL.Path {
		case "/drive/v1/share/list":
			if r.URL.Query().Get("page_token") == "" {
				io.WriteString(w, `{"data":[{"share_id":"s1","title":"first","share_status":"OK","share_url":"https://private.invalid","pass_code":"secret"}],"next_page_token":"second"}`)
			} else {
				io.WriteString(w, `{"data":[{"share_id":"s2","title":"second","share_status":"OK","share_url":"https://private.invalid","pass_code":"secret"}]}`)
			}
		case "/drive/v1/share:batchDelete":
			var body struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.Join(body.IDs, ",") != "s2" {
				t.Errorf("cancelled wrong share %v", body.IDs)
			}
			cancelled.Add(1)
			io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected share request %s", r.URL.Path)
			io.WriteString(w, `{}`)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := drive.DeviceIDFor("user@example.com")
	first, err := svc.AssistantResources(account, "shares", "")
	if err != nil || len(first.Items) != 1 || first.NextPageToken == "" || first.Items[0].Kind != "share" {
		t.Fatalf("first share page %+v %v", first, err)
	}
	second, err := svc.AssistantResources(account, "shares", first.NextPageToken)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "s2" || second.NextPageToken != "" {
		t.Fatalf("second share page %+v %v", second, err)
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "private.invalid") || strings.Contains(string(encoded), "secret") {
		t.Errorf("share catalog leaked secrets: %s", encoded)
	}
	if _, err := svc.AssistantResources(account, "trash", first.NextPageToken); err == nil {
		t.Error("accepted cross-catalog cursor")
	}
	got, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: "cancel_share", IDs: []string{"s2"}})
	if err != nil || len(got.Results) != 1 || got.Results[0].Error != "" || cancelled.Load() != 1 {
		t.Fatalf("cancel share %+v %v, writes=%d", got, err, cancelled.Load())
	}
	if _, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: "cancel_share", IDs: []string{"s2", "gone"}}); err == nil {
		t.Error("accepted stale batch share target")
	}
	if cancelled.Load() != 1 {
		t.Error("stale share batch partially cancelled links")
	}
}

func TestAssistantRejectsInapplicableLocalTaskStates(t *testing.T) {
	var requests atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		requests.Add(1)
		io.WriteString(w, `{}`)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	s.downloadQueue().Open(assistantJournalTasks(t, []download.Task{{ID: "d1", Name: "done", State: download.StateDone}}), quiet())
	s.queue().Open(assistantJournalTasks(t, []upload.Task{{ID: "u1", Name: "done", State: upload.StateDone}}), quiet())
	for _, action := range []string{"pause_download", "resume_download", "restart_download", "cancel_download", "retry_upload", "cancel_upload"} {
		id := "d1"
		if strings.HasSuffix(action, "_upload") {
			id = "u1"
		}
		if _, err := svc.AssistantOperate(s.key, AssistantOperationRequest{Action: action, IDs: []string{id}}); !errors.Is(err, errAssistantState) {
			t.Errorf("%s accepted finished task: %v", action, err)
		}
	}
	if requests.Load() != 0 || len(s.queue().List()) != 1 || len(s.downloadQueue().List()) != 1 {
		t.Fatal("state rejection changed queues or called API")
	}
}

func TestAssistantTrashCatalogKeepsOverflowAccessibleAfterRestoreAndReload(t *testing.T) {
	for _, restored := range []int{1, 40} {
		t.Run(fmt.Sprint(restored), func(t *testing.T) {
			var removed, requests atomic.Int64
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				if r.URL.Path != "/drive/v1/files" || r.URL.Query().Get("parent_id") != "*" {
					t.Errorf("trash read %s %v", r.URL.Path, r.URL.Query())
				}
				requests.Add(1)
				var files []map[string]any
				// Whole-tree API pages may exceed the requested limit.
				for n := int(removed.Load()) + 1; n <= 45; n++ {
					files = append(files, map[string]any{"id": fmt.Sprint("f", n), "kind": drive.KindFolder, "trashed": true, "name": fmt.Sprint("folder", n), "thumbnail_link": "https://private.invalid", "hash": "secret"})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
			})
			t.Cleanup(func() { _ = svc.ServiceShutdown() })
			account := drive.DeviceIDFor("user@example.com")
			first, err := svc.AssistantResources(account, "trash", "")
			if err != nil || len(first.Items) != 40 || first.NextPageToken == "" {
				t.Fatalf("first trash page %+v %v", first, err)
			}
			removed.Store(int64(restored))
			// Cursor JSON roundtrips through storage, with no service-local cache.
			stored, err := json.Marshal(first.NextPageToken)
			if err != nil {
				t.Fatal(err)
			}
			var continuation string
			if err := json.Unmarshal(stored, &continuation); err != nil {
				t.Fatal(err)
			}
			reloaded := &DriveService{accounts: []*accountSession{{key: account}}}
			second, err := reloaded.AssistantResources(account, "trash", continuation)
			if err != nil || len(second.Items) != 5 || second.NextPageToken != "" || second.Items[0].ID != "f41" || second.Items[0].Kind != "trash_folder" || !second.Items[0].Folder {
				t.Fatalf("second trash page %+v %v", second, err)
			}
			for n, item := range second.Items {
				if item.ID != fmt.Sprint("f", n+41) {
					t.Errorf("lost overflow row %d: %+v", n, item)
				}
			}
			if requests.Load() != 1 {
				t.Errorf("continuation re-downloaded mutable listing: %d requests", requests.Load())
			}
			encoded, _ := json.Marshal(first)
			if strings.Contains(string(encoded), "private.invalid") || strings.Contains(string(encoded), "secret") {
				t.Errorf("trash catalog leaked secrets: %s", encoded)
			}
			if len(first.NextPageToken) > assistantCursorLimit {
				t.Error("overflow inflated bounded continuation")
			}
		})
	}
}

func TestAssistantUploadBatchValidatesAllLocalSourcesBeforeRetry(t *testing.T) {
	var writes atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		writes.Add(1)
		io.WriteString(w, `{}`)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	valid := filepath.Join(t.TempDir(), "valid.txt")
	if err := os.WriteFile(valid, []byte("valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.queue().Open(assistantJournalTasks(t, []upload.Task{
		{ID: "u1", Name: "valid.txt", State: upload.StateFailed, Path: valid},
		{ID: "u2", Name: "gone.txt", State: upload.StateFailed, Path: filepath.Join(t.TempDir(), "missing")},
	}), quiet())
	if _, err := svc.AssistantOperate(s.key, AssistantOperationRequest{Action: "retry_upload", IDs: []string{"u1", "u2"}}); !errors.Is(err, errAssistantTarget) {
		t.Errorf("batch did not reject missing source: %v", err)
	}
	if rows := s.queue().List(); rows[0].State != upload.StateFailed || writes.Load() != 0 {
		t.Fatalf("valid prefix retried before batch validation: %+v requests=%d", rows, writes.Load())
	}
}

func TestAssistantPausedCloudTaskIsListedAndRemovedWithoutFiles(t *testing.T) {
	var removed atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		task := `{"id":"paused","name":"Authorization: Bearer secret-token","type":"offline","phase":"PHASE_TYPE_PAUSED"}`
		switch {
		case r.URL.Path == "/drive/v1/tasks" && r.Method == http.MethodGet:
			fmt.Fprintf(w, `{"tasks":[%s]}`, task)
		case r.URL.Path == "/drive/v1/tasks/paused":
			io.WriteString(w, task)
		case r.URL.Path == "/drive/v1/tasks" && r.Method == http.MethodDelete:
			if r.URL.Query().Get("delete_files") != "false" || r.URL.Query().Get("task_ids") != "paused" {
				t.Errorf("unsafe paused removal: %v", r.URL.Query())
			}
			removed.Add(1)
			io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected paused task request %s %s", r.Method, r.URL.Path)
			io.WriteString(w, `{}`)
		}
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := drive.DeviceIDFor("user@example.com")
	page, err := svc.AssistantResources(account, "transfers", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].State != drive.PhasePaused {
		t.Fatalf("paused task disappeared: %+v %v", page, err)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "Bearer") {
		t.Errorf("authorization credential leaked to AI: %s", encoded)
	}
	if _, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: "retry_cloud_task", IDs: []string{"paused"}}); !errors.Is(err, errAssistantState) {
		t.Errorf("paused retry accepted: %v", err)
	}
	result, err := svc.AssistantOperate(account, AssistantOperationRequest{Action: "remove_cloud_task", IDs: []string{"paused"}})
	if err != nil || len(result.Results) != 1 || result.Results[0].Error != "" || removed.Load() != 1 {
		t.Fatalf("paused removal %+v %v requests=%d", result, err, removed.Load())
	}
	encoded, _ = json.Marshal(result)
	if strings.Contains(string(encoded), "secret-token") {
		t.Errorf("authorization credential leaked in receipt: %s", encoded)
	}
}

func TestAssistantRejectsExtremeRemotePageWithoutSilentlyTruncating(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		var files []map[string]any
		for n := 1; n <= 41; n++ {
			name := fmt.Sprint("file", n)
			if n == 41 {
				name = strings.Repeat("x", assistantCursorLimit)
			}
			files = append(files, map[string]any{"id": fmt.Sprint("f", n), "name": name, "kind": drive.KindFile, "trashed": true})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	account := drive.DeviceIDFor("user@example.com")
	page, err := svc.AssistantResources(account, "trash", "")
	if !errors.Is(err, errAssistantPageTooLarge) || len(page.Items) != 0 || page.NextPageToken != "" {
		t.Fatalf("extreme overflow silently truncated: %d rows, continuation=%d bytes, error=%v", len(page.Items), len(page.NextPageToken), err)
	}
}
