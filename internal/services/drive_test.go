package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

// newDriveService wires a service against a stand-in API, already signed in.
func newDriveService(t *testing.T, handler http.HandlerFunc) *DriveService {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	svc := NewDriveService(DriveOptions{
		ConfigDir:   t.TempDir(),
		APIBaseURL:  srv.URL,
		AuthBaseURL: srv.URL,
		NoIndexSync: true,
		Logger:      quiet(),
	}, nil)
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	seedSession(t, svc, "user@example.com")
	return svc
}

// seedSession signs svc into account as a session resumed from its
// credentials file would be — from a refresh token, with no password in
// memory — and puts it on screen.
func seedSession(t *testing.T, svc *DriveService, account string) *accountSession {
	t.Helper()
	key := drive.DeviceIDFor(account)
	s := svc.byKey(key)
	if s == nil {
		s = &accountSession{key: key, rec: accountRecord{Key: key, Login: account, DeviceID: key}}
		svc.mu.Lock()
		svc.accounts = append(svc.accounts, s)
		svc.mu.Unlock()
	}
	tokens := svc.newTokenManager(s, key, svc.profile(), svc.credentialsStore(key))
	if err := tokens.Seed(drive.Token{Username: account, RefreshToken: "seed"}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	tok, err := tokens.Token(context.Background())
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	s.mu.Lock()
	s.rec.UserID = tok.UserID
	s.mu.Unlock()
	if err := svc.buildSession(s, tokens); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	svc.mu.Lock()
	svc.current = s
	svc.mu.Unlock()
	if err := svc.saveAccounts(); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return s
}

// activeIndex is the index of the account on screen, nil without one.
func activeIndex(svc *DriveService) *indexSession {
	s, err := svc.active()
	if err != nil {
		return nil
	}
	return s.index()
}

func authAndTokenRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/v1/auth/token", "/v1/auth/signin":
		io.WriteString(w, `{"token_type":"Bearer","access_token":"a","refresh_token":"r","expires_in":7200,"sub":"u"}`)
		return true
	case "/v1/shield/captcha/init":
		io.WriteString(w, `{"captcha_token":"c","expires_in":300}`)
		return true
	}
	return false
}

// Favourites and the recycle bin are views over the whole drive, not folders.
// Listing them against a parent id would quietly return the root folder.
// Before the index has completed a walk, List goes to the server for them.
func TestLibraryViewsQueryTheWholeTree(t *testing.T) {
	var queries []url.Values
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		queries = append(queries, r.URL.Query())
		io.WriteString(w, `{"files":[]}`)
	})

	if svc.IndexProgress().Ready {
		t.Fatal("a fresh index must not claim to be ready")
	}
	page, err := svc.List(ListRequest{View: "starred"})
	if err != nil {
		t.Fatalf("starred: %v", err)
	}
	if !page.Live || page.Total != -1 {
		t.Errorf("starred page = %+v, want live with unknown total", page)
	}
	if _, err := svc.List(ListRequest{View: "trash"}); err != nil {
		t.Fatalf("trash: %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(queries))
	}

	for i, want := range []struct {
		trashed any
		tag     any
	}{{false, "STAR"}, {true, nil}} {
		q := queries[i]
		if q.Get("parent_id") != "*" {
			t.Errorf("request %d parent_id = %q, want *", i, q.Get("parent_id"))
		}
		var filters map[string]map[string]any
		if err := json.Unmarshal([]byte(q.Get("filters")), &filters); err != nil {
			t.Fatalf("request %d filters %q: %v", i, q.Get("filters"), err)
		}
		if filters["trashed"]["eq"] != want.trashed {
			t.Errorf("request %d trashed filter = %v, want %v", i, filters["trashed"], want.trashed)
		}
		if want.tag != nil && filters["system_tag"]["in"] != want.tag {
			t.Errorf("request %d system_tag filter = %v, want %v", i, filters["system_tag"], want.tag)
		}
	}
}

// The whole-drive file view before the index is ready walks the
// tree from the server: folders are dropped, and a page holding only
// folders is read past rather than shown as an empty page.
func TestFilesViewLiveWalksTheTreeWithoutFolders(t *testing.T) {
	var queries []url.Values
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		queries = append(queries, r.URL.Query())
		if r.URL.Query().Get("page_token") == "" {
			io.WriteString(w, `{"files":[{"kind":"drive#folder","id":"d1","name":"A"}],"next_page_token":"p2"}`)
			return
		}
		io.WriteString(w, `{"files":[{"kind":"drive#folder","id":"d2","name":"B"},{"kind":"drive#file","id":"f1","name":"clip.mp4","parent_id":"d1"}],"next_page_token":"p3"}`)
	})

	page, err := svc.List(ListRequest{View: "files", Category: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Live || len(page.Entries) != 1 || page.Entries[0].ID != "f1" || page.NextPageToken != "p3" {
		t.Fatalf("page = %+v, want f1 alone with the second page's token", page)
	}
	if page.Entries[0].Path != "" {
		t.Errorf("live path = %q, want none (ancestors unknown without the index)", page.Entries[0].Path)
	}
	if len(queries) != 2 || queries[1].Get("page_token") != "p2" {
		t.Fatalf("requests = %v", queries)
	}
	if queries[0].Get("parent_id") != "*" || !strings.Contains(queries[0].Get("filters"), `"trashed":{"eq":false}`) {
		t.Errorf("first request = %v, want the live whole tree", queries[0])
	}
}

// Once a full walk has completed, listings and search come from the index
// and never touch the server.
func TestListServesFromIndexWhenReady(t *testing.T) {
	var requests atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		requests.Add(1)
		io.WriteString(w, `{"files":[]}`)
	})
	s := activeIndex(svc)
	if s == nil {
		t.Fatal("index did not open")
	}
	rows := []index.Row{
		{ID: "dir", ParentID: "", Kind: drive.KindFolder, Name: "My Saint", Category: "folder", ModifiedTime: "2026-01-02T00:00:00Z"},
		{ID: "clip", ParentID: "dir", Kind: drive.KindFile, Name: "aRE38bwCAfw.mp4", MimeType: "video/mp4",
			Phase: drive.PhaseComplete, Category: "video", Size: 10, ModifiedTime: "2026-01-01T00:00:00Z"},
		{ID: "doc", ParentID: "", Kind: drive.KindFile, Name: "notes.txt", Category: "document", ModifiedTime: "2026-01-03T00:00:00Z"},
	}
	if err := s.store.Upsert(rows); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetMeta(index.MetaFullAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if !svc.IndexProgress().Ready {
		t.Fatal("index should be ready after full_at is set")
	}

	page, err := svc.List(ListRequest{View: "all", Category: "all", Sort: "name", Asc: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Live || page.Total != 2 || len(page.Entries) != 2 {
		t.Fatalf("root page = %+v", page)
	}
	if page.Entries[0].ID != "dir" || !page.Entries[0].Folder || page.Entries[1].ID != "doc" {
		t.Errorf("root entries = %+v", page.Entries)
	}
	if page.Entries[0].Path != "" {
		t.Error("folder views carry no path")
	}

	page, err = svc.List(ListRequest{View: "all", ParentID: "dir", Category: "video"})
	if err != nil || len(page.Entries) != 1 || !page.Entries[0].Playable {
		t.Errorf("folder page = %+v err=%v, want the playable clip", page, err)
	}

	page, err = svc.List(ListRequest{View: "search", Query: "b"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].ID != "clip" || page.Entries[0].Path != "全部文件 / My Saint" {
		t.Errorf("search page = %+v", page.Entries)
	}
	page, _ = svc.List(ListRequest{View: "search", Query: "  "})
	if len(page.Entries) != 0 || page.Live {
		t.Errorf("blank search = %+v, want an empty indexed page", page)
	}

	page, err = svc.List(ListRequest{View: "files", Category: "all", Sort: "modified"})
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	if page.Live || page.Total != 2 || len(page.Entries) != 2 || page.Entries[0].ID != "doc" || page.Entries[1].ID != "clip" {
		t.Fatalf("files page = %+v, want every file of the drive and no folder", page)
	}
	if page.Entries[0].Path != "全部文件" || page.Entries[1].Path != "全部文件 / My Saint" {
		t.Errorf("files paths = %q, %q", page.Entries[0].Path, page.Entries[1].Path)
	}

	if n := requests.Load(); n != 0 {
		t.Errorf("indexed listings made %d server requests", n)
	}

	// A move lands the entry in its new folder without a walk, and the
	// destination picker sees the folders the index holds.
	if err := svc.MoveFiles([]string{"clip"}, ""); err != nil {
		t.Fatal(err)
	}
	page, _ = svc.List(ListRequest{View: "all"})
	if page.Total != 3 {
		t.Errorf("root total after moving clip out of dir = %d, want 3", page.Total)
	}
	page, _ = svc.List(ListRequest{View: "all", ParentID: "dir"})
	if page.Total != 0 {
		t.Errorf("dir still lists %d entries after the move", page.Total)
	}
	folders, err := svc.ListFolders("")
	if err != nil || len(folders) != 1 || folders[0].ID != "dir" {
		t.Errorf("root folders = %+v err=%v, want only dir", folders, err)
	}
}

// A folder made here shows in the indexed listing at once, under the name
// the server gave it, without waiting for the next sync pass.
func TestCreatedFolderListsWithoutASyncPass(t *testing.T) {
	var sent map[string]any
	var listed atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/drive/v1/files" {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			io.WriteString(w, `{"file":{"kind":"drive#folder","id":"new","name":"新建文件夹(1)","parent_id":"dir","modified_time":"2026-01-05T00:00:00Z"}}`)
			return
		}
		listed.Add(1)
		io.WriteString(w, `{"files":[]}`)
	})
	s := activeIndex(svc)
	if s == nil {
		t.Fatal("index did not open")
	}
	if err := s.store.Upsert([]index.Row{{ID: "dir", Kind: drive.KindFolder, Name: "Projects", Category: "folder", ModifiedTime: "2026-01-02T00:00:00Z"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetMeta(index.MetaFullAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	entry, err := svc.CreateFolder("", "dir", "新建文件夹")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if sent["kind"] != drive.KindFolder || sent["name"] != "新建文件夹" || sent["parent_id"] != "dir" {
		t.Errorf("request body = %v, want a folder named New Folder under dir", sent)
	}
	if entry.ID != "new" || entry.Name != "新建文件夹(1)" || !entry.Folder {
		t.Errorf("entry = %+v, want the folder as the server named it", entry)
	}

	page, err := svc.List(ListRequest{View: "all", ParentID: "dir"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Live || len(page.Entries) != 1 || page.Entries[0].ID != "new" {
		t.Errorf("dir page = %+v, want the new folder from the index", page)
	}
	if n := listed.Load(); n != 0 {
		t.Errorf("listing went to the server %d times", n)
	}
}

// One malformed line must not discard the rest of a batch, and the user has
// to be told which line failed.
func TestAddOfflineTasksReportsEachLine(t *testing.T) {
	var created atomic.Int64
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		link, _ := body["url"].(map[string]any)
		if strings.Contains(link["url"].(string), "broken") {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_argument","error_description":"bad magnet"}`)
			return
		}
		n := created.Add(1)
		io.WriteString(w, `{"task":{"id":"task-`+string(rune('0'+n))+`","name":"clip"},"file":{"id":"f","name":"clip"}}`)
	})

	results, err := svc.AddOfflineTasks("", []string{
		"magnet:?xt=urn:btih:aaa",
		"  ", // blank lines are dropped, not reported as failures
		"magnet:?xt=urn:btih:broken",
		"magnet:?xt=urn:btih:bbb",
	}, "dest")
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3 (blank line dropped)", len(results))
	}
	if !results[0].OK || results[0].TaskID == "" {
		t.Errorf("first line = %+v, want success with a task id", results[0])
	}
	if results[1].OK || !strings.Contains(results[1].Error, "bad magnet") {
		t.Errorf("second line = %+v, want the server's reason", results[1])
	}
	if !results[2].OK {
		t.Errorf("third line = %+v, want it submitted despite the earlier failure", results[2])
	}
}

// The inspector shows duration and resolution. They live on the media
// variants, not on the file, and a document legitimately has neither.
func TestFileDetailLiftsMediaFacts(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if got := r.URL.Query().Get("usage"); got != string(drive.UsageAll) {
			t.Errorf("usage = %q, want ALL", got)
		}
		io.WriteString(w, `{"kind":"drive#file","id":"f1","name":"trip.mp4","size":"1331691520",
			"mime_type":"video/mp4","phase":"PHASE_TYPE_COMPLETE","starred":false,"tags":[{"id":"","name":"STAR","type":0}],
			"created_time":"2026-10-01T10:00:00.000Z","hash":"ABC",
			"medias":[{"media_id":"m0","video":{"duration":"0"}},
			          {"media_id":"m1","video":{"duration":"768","width":3840,"height":2160}}]}`)
	})

	detail, err := svc.FileDetail("f1")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Duration != 768 || detail.Width != 3840 || detail.Height != 2160 {
		t.Errorf("media facts = %v/%dx%d, want 768s at 3840x2160", detail.Duration, detail.Width, detail.Height)
	}
	if !detail.Starred || detail.Category != "video" || !detail.Playable {
		t.Errorf("entry = %+v", detail.Entry)
	}
	if detail.CreatedTime == "" || detail.Hash != "ABC" {
		t.Errorf("created/hash = %q/%q", detail.CreatedTime, detail.Hash)
	}
}

// Selecting an entry in the recycle bin must still describe it. The server
// refuses a direct read of anything trashed (400 file_in_recycle_bin, error
// code 9 like a captcha rejection), so the bin listing has to answer.
func TestFileDetailDescribesRecycledEntry(t *testing.T) {
	bin := map[string]string{
		"f1": `{"kind":"drive#folder","id":"f1","name":"Acme handoff","trashed":true,
			"created_time":"2026-09-30T20:44:30.038+08:00","modified_time":"2026-09-30T20:46:33.364+08:00",
			"delete_time":"2026-10-15T20:46:33.364+08:00","hash":"H1"}`,
		"f2": `{"kind":"drive#file","id":"f2","name":"other.mp4","trashed":true,"hash":"H2"}`,
	}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if strings.HasPrefix(r.URL.Path, "/drive/v1/files/") {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"file_in_recycle_bin","error_code":9,"error_description":"File or folder is already in recycler"}`)
			return
		}
		var filters map[string]map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters)
		if r.URL.Query().Get("parent_id") != "*" || filters["trashed"]["eq"] != true {
			io.WriteString(w, `{"files":[]}`)
			return
		}
		var rows []string
		for id, row := range bin {
			if want, ok := filters["id"]["eq"]; !ok || want == id {
				rows = append(rows, row)
			}
		}
		io.WriteString(w, `{"files":[`+strings.Join(rows, ",")+`]}`)
	})

	detail, err := svc.FileDetail("f1")
	if err != nil {
		t.Fatalf("detail of a trashed folder: %v", err)
	}
	if detail.ID != "f1" || detail.Name != "Acme handoff" || !detail.Folder || !detail.Trashed {
		t.Errorf("entry = %+v", detail.Entry)
	}
	if detail.CreatedTime != "2026-09-30T20:44:30.038+08:00" || detail.Hash != "H1" {
		t.Errorf("created/hash = %q/%q", detail.CreatedTime, detail.Hash)
	}

	delete(bin, "f1") // destroyed or restored elsewhere in the meantime
	if _, err := svc.FileDetail("f1"); err == nil {
		t.Error("an entry gone from the bin must not be described")
	}
}

// The filter chips and the fallback icon both key on the category, and an
// offline task frequently lands with no MIME type at all.
func TestCategoryFallsBackToExtension(t *testing.T) {
	for name, want := range map[string]string{
		"trip.MKV":      "video",
		"notes.pdf":     "document",
		"bundle.tar.gz": "archive",
		"song.flac":     "audio",
		"shot.HEIC":     "image",
		"unknown.bin":   "other",
	} {
		got := categoryOf(drive.File{Kind: drive.KindFile, Name: name})
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := categoryOf(drive.File{Kind: drive.KindFolder, Name: "Movies"}); got != "folder" {
		t.Errorf("folder = %q", got)
	}
	// A real MIME type wins over a misleading extension.
	if got := categoryOf(drive.File{Kind: drive.KindFile, Name: "clip.txt", MimeType: "video/mp4"}); got != "video" {
		t.Errorf("mime override = %q, want video", got)
	}
}

// Offline tasks land with no MIME type; the play button must still appear
// for a video or audio extension, and never for an unfinished task.
func TestPlayableFallsBackToExtension(t *testing.T) {
	for name, want := range map[string]bool{
		"clip.mov":   true,
		"song.flac":  true,
		"shot.png":   false,
		"notes.pdf":  false,
		"binary.bin": false,
	} {
		if got := isPlayable(drive.File{Kind: drive.KindFile, Name: name, Phase: drive.PhaseComplete}); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if isPlayable(drive.File{Kind: drive.KindFile, Name: "clip.mp4", Phase: "PHASE_TYPE_RUNNING"}) {
		t.Error("unfinished task reported playable")
	}
	if isPlayable(drive.File{Kind: drive.KindFolder, Name: "clips.mp4"}) {
		t.Error("folder reported playable")
	}
}

// The panel must never show mojibake as if it were the file, and a UTF-8
// sequence cut at the fetch limit must not make a UTF-8 file "unknown".
func TestDecodeTextNamesTheEncoding(t *testing.T) {
	utf8Cut := append([]byte("你好"), []byte("世")[:2]...) // Truncate the final multibyte character.
	utf16le := append([]byte{0xFF, 0xFE}, 'h', 0, 'i', 0)
	utf16be := []byte{0xFE, 0xFF, 0, 'h', 0, 'i'}
	gbk := []byte{0xC4, 0xE3, 0xBA, 0xC3} // A Chinese greeting encoded in GBK.

	for name, tc := range map[string]struct {
		raw      []byte
		text     string
		encoding string
	}{
		"plain":   {[]byte("hello"), "hello", "utf-8"},
		"bom":     {append([]byte{0xEF, 0xBB, 0xBF}, "hi"...), "hi", "utf-8"},
		"cut":     {utf8Cut, "你好", "utf-8"},
		"utf16le": {utf16le, "hi", "utf-16le"},
		"utf16be": {utf16be, "hi", "utf-16be"},
		"gbk":     {gbk, "", "unknown"},
		"empty":   {nil, "", "utf-8"},
	} {
		text, encoding := decodeText(tc.raw)
		if text != tc.text || encoding != tc.encoding {
			t.Errorf("%s: got %q/%s, want %q/%s", name, text, encoding, tc.text, tc.encoding)
		}
	}
}

// Only a finished file with real bytes can be previewed; the kind decides
// which panel, and the MIME type outranks the extension.
func TestPreviewKind(t *testing.T) {
	for name, tc := range map[string]struct {
		file drive.File
		want string
	}{
		"png":          {drive.File{Kind: drive.KindFile, Name: "a.PNG"}, "image"},
		"srt":          {drive.File{Kind: drive.KindFile, Name: "a.srt"}, "text"},
		"pdf":          {drive.File{Kind: drive.KindFile, Name: "a.pdf"}, "pdf"},
		"mime pdf":     {drive.File{Kind: drive.KindFile, Name: "a.bin", MimeType: "application/pdf"}, "pdf"},
		"mime text":    {drive.File{Kind: drive.KindFile, Name: "a.bin", MimeType: "text/plain"}, "text"},
		"mime wins":    {drive.File{Kind: drive.KindFile, Name: "a.txt", MimeType: "image/png"}, "image"},
		"presentation": {drive.File{Kind: drive.KindFile, Name: "a.PPTX", MimeType: "application/vnd.openxmlformats-officedocument.presentationml.presentation"}, ""},
		"video":        {drive.File{Kind: drive.KindFile, Name: "a.mp4", MimeType: "video/mp4"}, ""},
		"folder":       {drive.File{Kind: drive.KindFolder, Name: "pics"}, ""},
		"not finished": {drive.File{Kind: drive.KindFile, Name: "a.png", Phase: "PHASE_TYPE_RUNNING"}, ""},
	} {
		if got := previewOf(tc.file); got != tc.want {
			t.Errorf("%s: preview = %q, want %q", name, got, tc.want)
		}
	}
}

// ReadText must stop at the limit and say so, rather than pulling a whole
// log file into the page.
func TestReadTextIsBounded(t *testing.T) {
	big := strings.Repeat("line of text\n", textPreviewLimit/10)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "big.log", time.Unix(0, 0), strings.NewReader(big))
	}))
	defer origin.Close()

	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		io.WriteString(w, `{"kind":"drive#file","id":"f1","name":"big.log","phase":"PHASE_TYPE_COMPLETE",
			"web_content_link":"`+origin.URL+`/big.log"}`)
	})

	preview, err := svc.ReadText(context.Background(), "f1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !preview.Truncated {
		t.Error("Truncated = false, want true for a file past the limit")
	}
	if len(preview.Text) > textPreviewLimit || len(preview.Text) < textPreviewLimit-16 {
		t.Errorf("text length = %d, want about %d", len(preview.Text), textPreviewLimit)
	}
	if preview.Encoding != "utf-8" {
		t.Errorf("encoding = %s, want utf-8", preview.Encoding)
	}
}

// Every account keeps its own index open: listings follow the account on
// screen, switching back finds the first account's rows without a new walk,
// and removing an account closes its index and deletes the file.
func TestAccountsKeepTheirOwnIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		io.WriteString(w, `{"files":[]}`)
	}))
	t.Cleanup(srv.Close)
	svc := NewDriveService(DriveOptions{
		ConfigDir:   t.TempDir(),
		APIBaseURL:  srv.URL,
		AuthBaseURL: srv.URL,
		Logger:      quiet(),
	}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	// signIn seeds a session and waits for the loop's first full walk,
	// which is what makes the index serve listings.
	signIn := func(account string) *accountSession {
		t.Helper()
		s := seedSession(t, svc, account)
		deadline := time.Now().Add(5 * time.Second)
		for !svc.IndexProgress().Ready {
			if time.Now().After(deadline) {
				t.Fatalf("index for %s never became ready: %+v", account, svc.IndexProgress())
			}
			time.Sleep(10 * time.Millisecond)
		}
		return s
	}
	listed := func() int {
		t.Helper()
		page, err := svc.List(ListRequest{View: "all"})
		if err != nil || page.Live {
			t.Fatalf("listing = %+v err=%v, want an indexed page", page, err)
		}
		return page.Total
	}

	a := signIn("a@example.com")
	row := index.Row{ID: "doc", Kind: drive.KindFile, Name: "notes.txt", Category: "document", ModifiedTime: "2026-01-03T00:00:00Z"}
	if err := a.index().store.Upsert([]index.Row{row}); err != nil {
		t.Fatal(err)
	}
	if n := listed(); n != 1 {
		t.Fatalf("first account lists %d rows, want 1", n)
	}

	b := signIn("b@example.com")
	if n := listed(); n != 0 {
		t.Errorf("second account lists %d rows, want none of the first's", n)
	}
	if p := svc.IndexProgress(); p.Account != b.key {
		t.Errorf("progress names %q, want the account on screen %q", p.Account, b.key)
	}
	if _, err := a.index().store.Count(); err != nil {
		t.Errorf("first account's index closed while another was on screen: %v", err)
	}

	if _, err := svc.SwitchAccount(a.key); err != nil {
		t.Fatal(err)
	}
	if n := listed(); n != 1 {
		t.Errorf("back on the first account: %d rows, want its 1", n)
	}

	store := a.index().store
	if _, err := svc.RemoveAccount(a.key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Count(); err == nil {
		t.Error("removed account's index is still open")
	}
	if _, err := os.Stat(svc.accountFile(a.key, "index-", ".sqlite")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("removed account's index file: %v, want it gone", err)
	}
	if st := svc.Status(); st.AccountID != b.key {
		t.Errorf("on screen after removing it: %q, want the other account %q", st.AccountID, b.key)
	}
	if n := listed(); n != 0 {
		t.Errorf("remaining account lists %d rows, want its own none", n)
	}
}

// A session the server stops renewing — access token rejected, refresh token
// retired, no password to fall back on — must end, whether a UI call or the
// background index walk finds out first. Leaving the client up kept the app
// on the file list, failing every call with "sign in again".
func TestDeadSessionEnds(t *testing.T) {
	for name, trigger := range map[string]func(*DriveService){
		"ui call": func(svc *DriveService) {
			if _, err := svc.Quota(); !errors.Is(err, drive.ErrPasswordRequired) {
				t.Errorf("quota err = %v, want ErrPasswordRequired", err)
			}
		},
		"index walk": func(svc *DriveService) { svc.RequestSync("") },
	} {
		t.Run(name, func(t *testing.T) {
			var dead atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if dead.Load() {
					if r.URL.Path == "/v1/auth/token" {
						w.WriteHeader(http.StatusBadRequest)
						io.WriteString(w, `{"error":"invalid_grant","error_description":"refresh token expired"}`)
					} else {
						w.WriteHeader(http.StatusUnauthorized)
						io.WriteString(w, `{"error":"unauthenticated","error_code":16}`)
					}
					return
				}
				if authAndTokenRoutes(w, r) {
					return
				}
				io.WriteString(w, `{"files":[]}`)
			}))
			t.Cleanup(srv.Close)
			svc := NewDriveService(DriveOptions{
				ConfigDir:   t.TempDir(),
				APIBaseURL:  srv.URL,
				AuthBaseURL: srv.URL,
				Logger:      quiet(),
			}, nil)
			t.Cleanup(func() { _ = svc.ServiceShutdown() })
			seedSession(t, svc, "user@example.com")
			deadline := time.Now().Add(5 * time.Second)
			for !svc.IndexProgress().Ready {
				if time.Now().After(deadline) {
					t.Fatal("index never became ready")
				}
				time.Sleep(10 * time.Millisecond)
			}

			dead.Store(true)
			trigger(svc)
			for svc.Status().Configured {
				if time.Now().After(deadline) {
					t.Fatal("session still signed in after the refresh token died")
				}
				time.Sleep(10 * time.Millisecond)
			}

			status := svc.Status()
			if !status.NeedsPassword || status.Account != "user@example.com" {
				t.Errorf("status = %+v, want a password prompt for the account", status)
			}
			// Its index goes; its uploads stay listed but cannot be acted on
			// until it is signed back in.
			s, _ := svc.active()
			deadline = time.Now().Add(5 * time.Second)
			for s.index() != nil {
				if time.Now().After(deadline) {
					t.Fatal("index outlived the session")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := s.liveQueue(); !errors.Is(err, drive.ErrPasswordRequired) {
				t.Errorf("acting on the uploads: %v, want ErrPasswordRequired", err)
			}
		})
	}
}
