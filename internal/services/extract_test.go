package services

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

const archiveGCID = "0123456789ABCDEF0123456789ABCDEF01234567"

// archiveFile answers GET /drive/v1/files/<id> for the stand-in drive: an
// archive "arc", a folder "dst" under the root, and "gone" in the bin.
func archiveFile(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/drive/v1/files/arc":
		io.WriteString(w, `{"kind":"drive#file","id":"arc","parent_id":"src","name":"海岸素材.zip","size":"2048","hash":"`+archiveGCID+`","phase":"PHASE_TYPE_COMPLETE"}`)
	case "/drive/v1/files/dst":
		io.WriteString(w, `{"kind":"drive#folder","id":"dst","parent_id":"","name":"影视","phase":"PHASE_TYPE_COMPLETE"}`)
	case "/drive/v1/files/gone":
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"file_in_recycle_bin","error_code":9,"error_description":"in the bin"}`)
	default:
		return false
	}
	return true
}

// A listing the service timed out on is asked again before the sheet hears
// of it, and a missing password, a wrong one and a good one come back as
// three different outcomes; the members keep what extraction needs.
func TestArchiveContentsRetriesTimeoutsAndTellsPasswordsApart(t *testing.T) {
	defer func(p time.Duration) { archiveRetryPause = p }(archiveRetryPause)
	archiveRetryPause = 0
	var mu sync.Mutex
	calls := map[string]int{}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) || archiveFile(w, r) {
			return
		}
		if r.URL.Path != "/decompress/v1/list" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body struct {
			FileID   string `json:"file_id"`
			GCID     string `json:"gcid"`
			Password string `json:"password"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.FileID != "arc" || body.GCID != archiveGCID {
			t.Errorf("list body = %+v, want the archive's id and gcid", body)
		}
		mu.Lock()
		calls[body.Password]++
		n := calls[body.Password]
		mu.Unlock()
		switch {
		case body.Password == "" && n == 1, body.Password == "slow":
			io.WriteString(w, `{"status":"FILE_LIST_TIMEOUT","files":[]}`)
		case body.Password == "":
			io.WriteString(w, `{"status":"PASS_WORD_EMPTY","status_text":"Please enter the zip password","files":[]}`)
		case body.Password == "bad":
			io.WriteString(w, `{"status":"PASS_WORD_ERROR","files":[]}`)
		default:
			io.WriteString(w, `{"status":"OK","files":[
				{"index":0,"filename":"原片","filesize":"0","kind":"drive#folder","path":"原片/"},
				{"index":3,"filename":"封面.jpg","filesize":"398458880","mime_type":"image/jpeg","kind":"drive#file","path":"封面.jpg"}]}`)
		}
	})

	got, err := svc.ArchiveContents("", "arc", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ArchiveNeedsPassword || len(got.Items) != 0 || calls[""] != 2 {
		t.Errorf("no password: %+v after %d calls, want password after one retry", got, calls[""])
	}
	if got, _ := svc.ArchiveContents("", "arc", "bad"); got.Status != ArchiveWrongPassword {
		t.Errorf("wrong password: status %q", got.Status)
	}
	got, err = svc.ArchiveContents("", "arc", "good")
	if err != nil {
		t.Fatal(err)
	}
	want := []ArchiveItem{
		{Index: 0, Name: "原片", Folder: true, Path: "原片/", Category: "folder"},
		{Index: 3, Name: "封面.jpg", Size: 398458880, Path: "封面.jpg", Category: "image"},
	}
	if got.Status != ArchiveOK || !reflect.DeepEqual(got.Items, want) {
		t.Errorf("good password: %+v, want ok with %+v", got, want)
	}
	if got, _ := svc.ArchiveContents("", "arc", "slow"); got.Status != ArchiveTimeout || calls["slow"] != archiveListTries {
		t.Errorf("always timing out: %q after %d calls, want timeout after %d", got.Status, calls["slow"], archiveListTries)
	}
}

// An extraction checks its destination first: a folder that is gone or
// already holds the folder the extraction would make stops it before any
// task exists. Otherwise the picks, the password and the destination go to
// the service as it expects them.
func TestExtractArchiveChecksTheDestinationThenSendsThePicks(t *testing.T) {
	var mu sync.Mutex
	var sent []map[string]any
	children := `{"files":[{"kind":"drive#folder","id":"x","parent_id":"dst","name":"海岸素材"}]}`
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) || archiveFile(w, r) {
			return
		}
		switch r.URL.Path {
		case "/drive/v1/files":
			mu.Lock()
			io.WriteString(w, children)
			mu.Unlock()
		case "/decompress/v1/decompress":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			sent = append(sent, body)
			mu.Unlock()
			io.WriteString(w, `{"status":"OK","task_id":"VOtask","files_num":7,"redirect_link":""}`)
		default:
			http.NotFound(w, r)
		}
	})
	req := ExtractRequest{FileID: "arc", Password: "pw", ParentID: "dst", Picks: []ArchivePick{
		{Index: 3, Path: "封面.jpg"},
		{Folder: true, Path: "原片/"},
	}}

	got, err := svc.ExtractArchive("", ExtractRequest{FileID: "arc", ParentID: "gone"})
	if err != nil || got.Status != ExtractDestinationGone {
		t.Fatalf("trashed destination: %+v, %v", got, err)
	}
	got, err = svc.ExtractArchive("", req)
	if err != nil || got.Status != ExtractNameTaken || got.Folder != "海岸素材" {
		t.Fatalf("taken name: %+v, %v", got, err)
	}
	if len(sent) != 0 {
		t.Fatalf("a task was requested despite the checks: %v", sent)
	}

	mu.Lock()
	children = `{"files":[{"kind":"drive#file","id":"y","parent_id":"dst","name":"海岸素材.zip"}]}`
	mu.Unlock()
	got, err = svc.ExtractArchive("", req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ExtractStarted || got.TaskID != "VOtask" || got.Files != 7 {
		t.Errorf("started: %+v", got)
	}
	if len(sent) != 1 {
		t.Fatalf("requests = %d, want 1", len(sent))
	}
	body := sent[0]
	wantFiles := []any{
		map[string]any{"index": 3.0, "kind": "drive#file", "path": "封面.jpg"},
		map[string]any{"index": 0.0, "kind": "drive#folder", "path": "原片/"},
	}
	if body["gcid"] != archiveGCID || body["password"] != "pw" || body["parent_id"] != "dst" || body["default_parent"] != false ||
		!reflect.DeepEqual(body["parent_full_path"], []any{"影视"}) || !reflect.DeepEqual(body["files"], wantFiles) {
		t.Errorf("decompress body = %v", body)
	}

	// Every item ticked asks for the whole archive.
	if _, err := svc.ExtractArchive("", ExtractRequest{FileID: "arc"}); err != nil {
		t.Fatal(err)
	}
	if files := sent[1]["files"]; !reflect.DeepEqual(files, []any{}) || sent[1]["parent_id"] != "" {
		t.Errorf("whole archive to the root: files %v, parent %v", files, sent[1]["parent_id"])
	}
}

// The task's phase becomes what the sheet shows: done with the folder it
// made, failed with the service's reason, running with when to ask again.
func TestExtractProgressNamesTheOutcome(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/decompress/v1/progress" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("task_id") {
		case "run":
			io.WriteString(w, `{"progress":42,"expires_in":5,"phase":"PHASE_TYPE_RUNNING","file_id":"","params":{}}`)
		case "done":
			io.WriteString(w, `{"progress":100,"expires_in":999,"phase":"PHASE_TYPE_COMPLETE","file_id":"VNfolder","params":{}}`)
		case "fail":
			io.WriteString(w, `{"progress":0,"expires_in":999,"phase":"PHASE_TYPE_ERROR","params":{"error_detail":"file_space_not_enough"}}`)
		}
	})
	for id, want := range map[string]ExtractState{
		"run":  {Phase: ExtractRunning, Progress: 42, Next: 5},
		"done": {Phase: ExtractDone, Progress: 100, Next: 2, FileID: "VNfolder"},
		"fail": {Phase: ExtractFailed, Next: 2, Error: "file_space_not_enough"},
	} {
		got, err := svc.ExtractProgress("", id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got != want {
			t.Errorf("%s: %+v, want %+v", id, got, want)
		}
	}
}
