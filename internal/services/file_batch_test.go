package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
)

func batchSnapshot(tree *fakeTree, ids ...string) []BatchFileItem {
	items := make([]BatchFileItem, len(ids))
	for i, id := range ids {
		f := tree.file(id)
		items[i] = BatchFileItem{ID: id, Name: f.name, ParentID: f.parent}
	}
	return items
}

func TestFileBatchPartialRefusalAndSnapshotDrift(t *testing.T) {
	tree := newFakeTree()
	for _, id := range []string{"ok", "refused", "renamed", "moved"} {
		tree.add(id, id, "", false)
	}
	tree.refuse["refused"] = true
	svc, account, sent := newRemoveDrive(t, tree, nil)
	items := batchSnapshot(tree, "ok", "refused", "renamed", "moved")
	tree.mu.Lock()
	tree.files["renamed"].name = "new name"
	tree.files["moved"].parent = "elsewhere"
	tree.mu.Unlock()
	rep, err := svc.RunFileBatch(FileBatchRequest{ID: "run", Account: account, Action: "trash", Items: items})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"done", "failed", "failed", "failed"} {
		if rep.Items[i].State != want || rep.Items[i].ID != items[i].ID || rep.Items[i].Name != items[i].Name {
			t.Errorf("receipt %d = %+v, want %s for original snapshot", i, rep.Items[i], want)
		}
	}
	for _, ids := range sent.of(":batchTrash") {
		for _, id := range ids {
			if id == "renamed" || id == "moved" {
				t.Fatalf("drifted entry sent: %v", ids)
			}
		}
	}
	if !tree.file("ok").trashed || tree.file("refused").trashed {
		t.Fatal("partial refusal changed the wrong entries")
	}
	noDeletes(t, tree)
}

func TestFileBatchAmbiguousWriteNeverReplays(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprint(completed), func(t *testing.T) {
			tree := newFakeTree()
			tree.add("a", "a", "", false)
			tree.add("b", "b", "", false)
			writes := 0
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/drive/v1/files:batchTrash" {
					writes++
					if completed {
						tree.mu.Lock()
						tree.files["a"].trashed, tree.files["b"].trashed = true, true
						tree.mu.Unlock()
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					io.WriteString(w, `{"error":"unavailable"}`)
					return
				}
				tree.serve(w, r)
			})
			s, _ := svc.active()
			rep, err := svc.RunFileBatch(FileBatchRequest{ID: "ambiguous", Account: s.key, Action: "trash", Items: batchSnapshot(tree, "a", "b")})
			if err != nil || writes != 1 || len(rep.Items) != 2 {
				t.Fatalf("report=%+v err=%v writes=%d", rep, err, writes)
			}
			want := "unknown"
			if completed {
				want = "done"
			}
			for _, it := range rep.Items {
				if it.State != want {
					t.Errorf("receipt=%+v, want %s", it, want)
				}
			}
		})
	}
}

func TestFileBatchLateCompletionRemainsUnknown(t *testing.T) {
	tree := newFakeTree()
	tree.add("a", "a", "", false)
	fastTasks(t)
	writes := 0
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/drive/v1/tasks/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/drive/v1/files:batchTrash" {
			writes++
		}
		tree.serve(w, r)
	})
	s, _ := svc.active()
	rep, err := svc.RunFileBatch(FileBatchRequest{ID: "late", Account: s.key, Action: "trash", Items: batchSnapshot(tree, "a")})
	if err != nil || rep.Items[0].State != "unknown" || writes != 1 {
		t.Fatalf("report=%+v err=%v writes=%d", rep, err, writes)
	}
	tree.mu.Lock()
	for _, task := range tree.tasks {
		if task.work != nil {
			task.work()
			task.work = nil
		}
	}
	tree.mu.Unlock()
	if !tree.file("a").trashed || rep.Items[0].State != "unknown" {
		t.Fatal("late completion was replayed or prematurely claimed")
	}
}

func TestFileBatchStopSettlesAcceptedBatchAndKeepsUnsent(t *testing.T) {
	tree := newFakeTree()
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("f%03d", i)
		tree.add(ids[i], ids[i], "", false)
	}
	svc, account, sent := newRemoveDrive(t, tree, nil)
	var once sync.Once
	var concurrent error
	tree.hook(func(what string) {
		if strings.HasSuffix(what, ":batchTrash") {
			once.Do(func() {
				_, concurrent = svc.RunFileBatch(FileBatchRequest{ID: "other", Account: account, Action: "trash", Items: batchSnapshot(tree, ids[0])})
				svc.StopRemove(account)
			})
		}
	})
	rep, err := svc.RunFileBatch(FileBatchRequest{ID: "stop", Account: account, Action: "trash", Items: batchSnapshot(tree, ids...)})
	if err != nil || !errors.Is(concurrent, errRemoving) || !rep.Stopped || len(sent.of(":batchTrash")) != 1 {
		t.Fatalf("report=%+v err=%v concurrent=%v", rep, err, concurrent)
	}
	for i, it := range rep.Items {
		want := "done"
		if i == 100 {
			want = "unprocessed"
		}
		if it.State != want {
			t.Errorf("receipt %d=%+v, want %s", i, it, want)
		}
	}
}

func TestFileBatchMoveValidatesDestinationAndReadsBack(t *testing.T) {
	for _, destination := range []string{"target", "source", "child", "", "file", "missing"} {
		t.Run(destination, func(t *testing.T) {
			tree := newFakeTree()
			tree.add("source", "source", "", true)
			tree.add("child", "child", "source", true)
			tree.add("target", "target", "", true)
			tree.add("file", "file", "", false)
			svc, account, sent := newRemoveDrive(t, tree, nil)
			rep, err := svc.RunFileBatch(FileBatchRequest{ID: "move", Account: account, Action: "move", ParentID: destination, Items: batchSnapshot(tree, "source")})
			if err != nil {
				t.Fatal(err)
			}
			it := rep.Items[0]
			if destination == "target" {
				if it.State != "done" || it.Entry == nil || it.Entry.ParentID != "target" {
					t.Fatalf("receipt=%+v", it)
				}
				if row, _ := indexRow(t, svc, "source"); row.ParentID != "target" {
					t.Fatal("confirmed move not patched into index")
				}
			} else if it.State != "failed" || len(sent.of(":batchMove")) != 0 {
				t.Fatalf("unsafe move receipt=%+v writes=%v", it, sent.of(":batchMove"))
			}
			noDeletes(t, tree)
		})
	}
}

func TestFileBatchCopyRequiresSuccessfulTask(t *testing.T) {
	for _, phase := range []string{drive.PhaseComplete, drive.PhaseError, "missing-task"} {
		t.Run(phase, func(t *testing.T) {
			tree := newFakeTree()
			tree.add("a", "a", "", false)
			tree.add("target", "target", "", true)
			writes := 0
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/drive/v1/files:batchCopy":
					writes++
					if phase == "missing-task" {
						io.WriteString(w, `{}`)
					} else {
						io.WriteString(w, `{"task_id":"copy-task"}`)
					}
				case "/drive/v1/tasks/copy-task":
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "copy-task", "phase": phase})
				default:
					tree.serve(w, r)
				}
			})
			s, _ := svc.active()
			rep, err := svc.RunFileBatch(FileBatchRequest{ID: "copy", Account: s.key, Action: "copy", ParentID: "target", Items: batchSnapshot(tree, "a")})
			want := "unknown"
			if phase == drive.PhaseComplete {
				want = "done"
			}
			if err != nil || rep.Items[0].State != want || writes != 1 {
				t.Fatalf("report=%+v err=%v writes=%d", rep, err, writes)
			}
		})
	}
}

func TestFileBatchTargetsOriginalAccount(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("a@example.com", "pw-a", "user-a")
	api.add("b@example.com", "pw-b", "user-b")
	tree := newFakeTree()
	tree.add("a", "a", "", false)
	var tokens []string
	api.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/drive/v1/files") || strings.HasPrefix(r.URL.Path, "/drive/v1/tasks/") {
			tokens = append(tokens, r.Header.Get("Authorization"))
			tree.serve(w, r)
			return
		}
		api.serve(w, r)
	})
	fastTasks(t)
	svc := startAccounts(t, t.TempDir(), api, &memoryVault{})
	a := signIn(t, svc, SignInRequest{Login: "a@example.com", Password: "pw-a"}).Account.ID
	b := signIn(t, svc, SignInRequest{Login: "b@example.com", Password: "pw-b"}).Account.ID
	if _, err := svc.SwitchAccount(a); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.RunFileBatch(FileBatchRequest{ID: "account", Account: b, Action: "trash", Items: batchSnapshot(tree, "a")})
	if err != nil || rep.Items[0].State != "done" {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
	if len(tokens) == 0 {
		t.Fatal("no account-targeted reads or writes")
	}
	for _, token := range tokens {
		if token != "Bearer a:b@example.com" {
			t.Fatalf("request crossed accounts: %q", token)
		}
	}
}

func TestFileBatchRestoreAndPermanentDelete(t *testing.T) {
	for _, action := range []string{"restore", "delete"} {
		t.Run(action, func(t *testing.T) {
			tree := newFakeTree()
			tree.add("a", "a", "", false)
			tree.files["a"].trashed = true
			svc, account, _ := newRemoveDrive(t, tree, nil)
			rep, err := svc.RunFileBatch(FileBatchRequest{ID: action, Account: account, Action: action, Items: batchSnapshot(tree, "a")})
			if err != nil || rep.Items[0].State != "done" {
				t.Fatalf("report=%+v err=%v", rep, err)
			}
			if action == "restore" {
				if rep.Items[0].Entry == nil || rep.Items[0].Entry.Trashed {
					t.Fatal("restore lacks confirmed live source entry")
				}
				noDeletes(t, tree)
			} else if _, exists := indexRow(t, svc, "a"); exists {
				t.Fatal("permanent delete left index row")
			}
		})
	}
}

func TestFileBatchStarWaitsForReadBack(t *testing.T) {
	for _, action := range []string{"star", "unstar"} {
		for _, applied := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", action, applied), func(t *testing.T) {
				starred := action == "unstar"
				writes := 0
				svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
					if authAndTokenRoutes(w, r) {
						return
					}
					switch r.URL.Path {
					case "/drive/v1/files:batchGet":
						tags := []map[string]string{}
						if starred {
							tags = append(tags, map[string]string{"name": "STAR"})
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{map[string]any{"id": "a", "name": "a", "kind": drive.KindFile, "tags": tags}}})
					case "/drive/v1/files:star", "/drive/v1/files:unstar":
						writes++
						if applied {
							starred = action == "star"
						}
						io.WriteString(w, `{}`)
					default:
						http.NotFound(w, r)
					}
				})
				s, _ := svc.active()
				rep, err := svc.RunFileBatch(FileBatchRequest{ID: action, Account: s.key, Action: action, Items: []BatchFileItem{{ID: "a", Name: "a"}}})
				want := "unknown"
				if applied {
					want = "done"
				}
				if err != nil || rep.Items[0].State != want || writes != 1 {
					t.Fatalf("report=%+v err=%v writes=%d", rep, err, writes)
				}
				if applied && (rep.Items[0].Entry == nil || rep.Items[0].Entry.Starred != (action == "star")) {
					t.Fatal("star receipt lacks verified source state")
				}
			})
		}
	}
}

func TestFileBatchEmptyTrashUsesOnlyConfirmedSnapshot(t *testing.T) {
	tree := newFakeTree()
	tree.add("old", "old", "", false)
	tree.add("restored", "restored", "", false)
	tree.files["old"].trashed, tree.files["restored"].trashed = true, true
	svc, account, sent := newRemoveDrive(t, tree, nil)
	entries, err := svc.TrashSnapshot(account)
	if err != nil || len(entries) != 2 || sent.writes() != 0 {
		t.Fatalf("snapshot=%+v err=%v writes=%d", entries, err, sent.writes())
	}
	items := make([]BatchFileItem, len(entries))
	for i, entry := range entries {
		items[i] = BatchFileItem{ID: entry.ID, Name: entry.Name, ParentID: entry.ParentID}
	}
	tree.put("later", "later", "", false)
	tree.mu.Lock()
	tree.files["later"].trashed = true
	tree.files["restored"].trashed = false
	tree.mu.Unlock()
	rep, err := svc.RunFileBatch(FileBatchRequest{ID: "empty", Account: account, Action: "empty", Items: items})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range rep.Items {
		want := "done"
		if it.ID == "restored" {
			want = "failed"
		}
		if it.State != want {
			t.Errorf("receipt=%+v, want %s", it, want)
		}
	}
	if tree.file("later").id == "" || !tree.file("later").trashed || tree.file("restored").id == "" {
		t.Fatal("empty-trash grew its snapshot or destroyed a restored source")
	}
	for _, ids := range sent.of(":batchDelete") {
		for _, id := range ids {
			if id != "old" {
				t.Fatalf("unconfirmed permanent delete: %v", ids)
			}
		}
	}
}

func TestFileBatchRechecksSnapshotBeforeLaterBatch(t *testing.T) {
	tree := newFakeTree()
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("f%03d", i)
		tree.add(ids[i], ids[i], "", false)
	}
	items := batchSnapshot(tree, ids...)
	svc, account, sent := newRemoveDrive(t, tree, nil)
	var once sync.Once
	tree.hook(func(what string) {
		if strings.HasSuffix(what, ":batchTrash") {
			once.Do(func() {
				tree.mu.Lock()
				tree.files[ids[100]].name = "changed while first batch ran"
				tree.mu.Unlock()
			})
		}
	})
	rep, err := svc.RunFileBatch(FileBatchRequest{ID: "recheck", Account: account, Action: "trash", Items: items})
	if err != nil || rep.Items[100].State != "failed" || len(sent.of(":batchTrash")) != 1 {
		t.Fatalf("report=%+v err=%v writes=%v", rep, err, sent.of(":batchTrash"))
	}
	if tree.file(ids[100]).trashed {
		t.Fatal("later batch used stale source snapshot")
	}
}

// A legacy run whose batch reply is lost must settle the entries from the
// read-back: the server may have carried the write out, and recording it as
// not applied would leave the receipt and the index wrong.
func TestLegacyRemoveSettlesAmbiguousWriteFromReadBack(t *testing.T) {
	tree := newFakeTree()
	tree.add("a", "a", "", false)
	tree.add("b", "b", "", false)
	writes := 0
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/drive/v1/files:batchTrash" {
			writes++
			tree.mu.Lock()
			tree.files["a"].trashed, tree.files["b"].trashed = true, true
			tree.mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":"unavailable"}`)
			return
		}
		tree.serve(w, r)
	})
	s, _ := svc.active()
	indexTree(t, svc, tree, nil)
	rep, err := svc.RemoveFiles(s.key, []string{"a", "b"}, false)
	if err != nil || writes != 1 || len(rep.Removed) != 2 || len(rep.Failed) != 0 || rep.Stopped {
		t.Fatalf("report=%+v err=%v writes=%d", rep, err, writes)
	}
	if !tree.file("a").trashed || !tree.file("b").trashed {
		t.Error("the write the server applied was not settled as done")
	}
	for _, id := range []string{"a", "b"} {
		if row, ok := indexRow(t, svc, id); !ok || !row.Trashed {
			t.Errorf("%s: index row = %+v, %v; want trashed", id, row, ok)
		}
	}
}

func TestLegacyRemoveReceiptsExposeRetrySafety(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			tree := newFakeTree()
			tree.add("a", "a", "", false)
			tree.add("b", "b", "", false)
			writes := 0
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/drive/v1/files:batchTrash" {
					writes++
					w.WriteHeader(status)
					io.WriteString(w, `{"error":"refused"}`)
					return
				}
				tree.serve(w, r)
			})
			s, _ := svc.active()
			rep, err := svc.RemoveFiles(s.key, []string{"a", "b"}, false)
			want, wantWrites := "failed", 3
			if status == http.StatusServiceUnavailable {
				want, wantWrites = "unknown", 1
			}
			if err != nil || len(rep.Failed) != 2 || writes != wantWrites {
				t.Fatalf("report=%+v err=%v writes=%d", rep, err, writes)
			}
			for _, it := range rep.Failed {
				if it.State != want {
					t.Errorf("receipt=%+v, want %s", it, want)
				}
			}
			noDeletes(t, tree)
		})
	}
}
