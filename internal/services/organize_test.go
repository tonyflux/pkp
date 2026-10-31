package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/text/unicode/norm"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

// fakeTree is a drive in memory that answers the calls the organizer makes
// the way the real server does (probed 2026-10-15): renames answer at once
// and refuse an exact sibling name; batchMove, batchTrash and batchUntrash
// answer with a task id and take effect only once the task completes
// (GET /drive/v1/tasks/{id}: RUNNING, then COMPLETE); a move or restore
// onto a taken name goes ahead and numbers the entry "name(1)". Every
// request that would leave two entries of one name (compared NFC, any case)
// in a folder is recorded in clashes, and every batchDelete in deletes (the
// entries and everything below them go for good): organize tests check that
// both stay empty (checkTree). The recycle bin lists entries trashed in
// their own right, narrowed to one id or page by page.
type fakeTree struct {
	mu      sync.Mutex
	files   map[string]*fakeFile
	next    int
	clashes []string
	deletes [][]string
	// refuse makes the server refuse renaming, moving or trashing these ids.
	refuse map[string]bool
	moves  [][]string
	tasks  map[string]*fakeTask
	// before runs ahead of each write the server takes, for a test to act
	// in the middle of a run; set it with hook.
	before func(what string)
}

type fakeFile struct {
	id, name, parent string
	folder, trashed  bool
	size             int64
	// special makes a folder something other than NORMAL (folder_type).
	special bool
}

// fakeTask is a batch the server took: it runs for polls reads of its
// status, then does its work.
type fakeTask struct {
	polls int
	work  func()
}

func newFakeTree() *fakeTree {
	return &fakeTree{files: map[string]*fakeFile{}, refuse: map[string]bool{}, tasks: map[string]*fakeTask{}}
}

func (t *fakeTree) hook(fn func(what string)) {
	t.mu.Lock()
	t.before = fn
	t.mu.Unlock()
}

func (t *fakeTree) add(id, name, parent string, folder bool) {
	t.files[id] = &fakeFile{id: id, name: name, parent: parent, folder: folder}
}

// put adds an entry while a test runs.
func (t *fakeTree) put(id, name, parent string, folder bool) {
	t.mu.Lock()
	t.add(id, name, parent, folder)
	t.mu.Unlock()
}

// in is the live content of a folder, by name.
func (t *fakeTree) in(parent string) map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]string{}
	for _, f := range t.files {
		if f.parent == parent && !f.trashed {
			out[f.name] = f.id
		}
	}
	return out
}

func (t *fakeTree) file(id string) fakeFile {
	t.mu.Lock()
	defer t.mu.Unlock()
	if f := t.files[id]; f != nil {
		return *f
	}
	return fakeFile{}
}

func fold(name string) string { return norm.NFC.String(strings.ToLower(name)) }

func (t *fakeTree) taken(parent, name, id string) bool {
	for _, f := range t.files {
		if f.parent == parent && !f.trashed && f.id != id && fold(f.name) == fold(name) {
			return true
		}
	}
	return false
}

// land puts f into parent as the server would: a taken name is numbered.
func (t *fakeTree) land(f *fakeFile, parent, what string) {
	if t.taken(parent, f.name, f.id) {
		t.clashes = append(t.clashes, what+" "+f.name)
		ext := path.Ext(f.name)
		f.name = strings.TrimSuffix(f.name, ext) + "(1)" + ext
	}
	f.parent = parent
}

func (t *fakeTree) json(f *fakeFile) map[string]any {
	kind := "drive#file"
	out := map[string]any{"id": f.id, "name": f.name, "parent_id": f.parent, "trashed": f.trashed,
		"phase": "PHASE_TYPE_COMPLETE", "size": strconv.FormatInt(f.size, 10)}
	if f.folder {
		kind = "drive#folder"
		out["folder_type"] = "NORMAL"
		if f.special {
			out["folder_type"] = "DOWNLOAD"
		}
	}
	out["kind"] = kind
	return out
}

// binTop says whether f is listed by the recycle bin: trashed, and not
// inside a folder trashed with it.
func (t *fakeTree) binTop(f *fakeFile) bool {
	if !f.trashed {
		return false
	}
	p := t.files[f.parent]
	return p == nil || !p.trashed
}

// destroy removes id and everything below it.
func (t *fakeTree) destroy(id string) {
	for _, f := range t.files {
		if f.parent == id {
			t.destroy(f.id)
		}
	}
	delete(t.files, id)
}

func (t *fakeTree) task(work func()) string {
	t.next++
	id := fmt.Sprintf("task%d", t.next)
	t.tasks[id] = &fakeTask{polls: 2, work: work}
	return id
}

func (t *fakeTree) serve(w http.ResponseWriter, r *http.Request) {
	if authAndTokenRoutes(w, r) {
		return
	}
	t.mu.Lock()
	before := t.before
	t.mu.Unlock()
	if r.Method != http.MethodGet && before != nil {
		before(r.Method + " " + r.URL.Path)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var body struct {
		Name   string   `json:"name"`
		Parent string   `json:"parent_id"`
		Kind   string   `json:"kind"`
		Ignore bool     `json:"ignore_duplicated_name"`
		IDs    []string `json:"ids"`
		To     struct {
			Parent string `json:"parent_id"`
		} `json:"to"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	refused := func() {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"file_name_invalid","error_description":"refused by the stand-in"}`)
	}
	answerTask := func(work func()) {
		for _, id := range body.IDs {
			if t.refuse[id] || t.files[id] == nil {
				refused()
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": t.task(work)})
	}
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/drive/v1/files" && q.Get("parent_id") == drive.WholeTree:
		// The recycle bin, narrowed to one id or in pages.
		var filters map[string]map[string]any
		_ = json.Unmarshal([]byte(q.Get("filters")), &filters)
		files := []any{}
		if filters["trashed"]["eq"] != true {
			http.Error(w, "the stand-in only lists the recycle bin", http.StatusNotImplemented)
			return
		}
		if id, ok := filters["id"]["eq"]; ok {
			if f := t.files[fmt.Sprint(id)]; f != nil && f.trashed {
				files = append(files, t.json(f))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
			return
		}
		var rows []*fakeFile
		for _, f := range t.files {
			if t.binTop(f) {
				rows = append(rows, f)
			}
		}
		slices.SortFunc(rows, func(a, b *fakeFile) int { return strings.Compare(a.id, b.id) })
		limit, _ := strconv.Atoi(q.Get("limit"))
		from, _ := strconv.Atoi(q.Get("page_token"))
		to := min(from+limit, len(rows))
		for _, f := range rows[from:to] {
			files = append(files, t.json(f))
		}
		page := map[string]any{"files": files}
		if to < len(rows) {
			page["next_page_token"] = strconv.Itoa(to)
		}
		_ = json.NewEncoder(w).Encode(page)
	case r.Method == http.MethodGet && r.URL.Path == "/drive/v1/files":
		var rows []*fakeFile
		for _, f := range t.files {
			if f.parent == q.Get("parent_id") && !f.trashed {
				rows = append(rows, f)
			}
		}
		slices.SortFunc(rows, func(a, b *fakeFile) int { return strings.Compare(a.name+a.id, b.name+b.id) })
		limit, _ := strconv.Atoi(q.Get("limit"))
		from, _ := strconv.Atoi(q.Get("page_token"))
		to := min(from+limit, len(rows))
		files := []any{}
		for _, f := range rows[from:to] {
			files = append(files, t.json(f))
		}
		page := map[string]any{"files": files}
		if to < len(rows) {
			page["next_page_token"] = strconv.Itoa(to)
		}
		_ = json.NewEncoder(w).Encode(page)
	case r.Method == http.MethodGet && r.URL.Path == "/drive/v1/files:batchGet":
		files := []any{}
		for _, id := range q["ids"] {
			if f := t.files[id]; f != nil && !f.trashed {
				files = append(files, t.json(f))
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/drive/v1/tasks/"):
		task := t.tasks[strings.TrimPrefix(r.URL.Path, "/drive/v1/tasks/")]
		if task == nil {
			http.NotFound(w, r)
			return
		}
		phase := drive.PhaseRunning
		if task.polls--; task.polls <= 0 {
			if task.work != nil {
				task.work()
				task.work = nil
			}
			phase = drive.PhaseComplete
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t", "type": "move", "phase": phase})
	case r.Method == http.MethodPost && r.URL.Path == "/drive/v1/files":
		if body.Ignore {
			for _, f := range t.files {
				if f.parent == body.Parent && f.folder && !f.trashed && f.name == body.Name {
					_ = json.NewEncoder(w).Encode(map[string]any{"file": t.json(f)})
					return
				}
			}
		}
		if t.taken(body.Parent, body.Name, "") {
			t.clashes = append(t.clashes, "make "+body.Name)
		}
		t.next++
		f := &fakeFile{id: fmt.Sprintf("made%d", t.next), name: body.Name, parent: body.Parent, folder: true}
		t.files[f.id] = f
		_ = json.NewEncoder(w).Encode(map[string]any{"file": t.json(f)})
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/drive/v1/files/"):
		f := t.files[strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")]
		if f == nil || t.refuse[f.id] {
			refused()
			return
		}
		for _, o := range t.files {
			if o.parent == f.parent && !o.trashed && o.id != f.id && o.name == body.Name {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"file_duplicated_name","error_description":"File name cannot be repeated"}`)
				return
			}
		}
		if t.taken(f.parent, body.Name, f.id) {
			t.clashes = append(t.clashes, "rename "+f.name+" → "+body.Name)
		}
		f.name = body.Name
		_ = json.NewEncoder(w).Encode(t.json(f))
	case r.URL.Path == "/drive/v1/files:batchMove":
		ids, to := slices.Clone(body.IDs), body.To.Parent
		answerTask(func() {
			t.moves = append(t.moves, ids)
			for _, id := range ids {
				t.land(t.files[id], to, "move")
			}
		})
	case r.URL.Path == "/drive/v1/files:batchTrash":
		ids := slices.Clone(body.IDs)
		answerTask(func() {
			for _, id := range ids {
				t.files[id].trashed = true
			}
		})
	case r.URL.Path == "/drive/v1/files:batchUntrash":
		ids := slices.Clone(body.IDs)
		answerTask(func() {
			for _, id := range ids {
				if f := t.files[id]; f != nil && f.trashed {
					f.trashed = false
					t.land(f, f.parent, "restore")
				}
			}
		})
	case r.URL.Path == "/drive/v1/files:batchDelete":
		t.deletes = append(t.deletes, slices.Clone(body.IDs))
		for _, id := range body.IDs {
			t.destroy(id)
		}
		io.WriteString(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

// fastTasks makes runs read a batch's task without the real pause.
func fastTasks(t *testing.T) {
	old := taskPoll
	taskPoll = time.Millisecond
	t.Cleanup(func() { taskPoll = old })
}

// checkTree fails the test if any request doubled a name or deleted
// anything for good.
func checkTree(t *testing.T, tree *fakeTree) {
	t.Cleanup(func() {
		tree.mu.Lock()
		defer tree.mu.Unlock()
		if len(tree.deletes) != 0 {
			t.Errorf("permanent deletes: %v", tree.deletes)
		}
		if len(tree.clashes) != 0 {
			t.Errorf("requests that doubled a name: %v", tree.clashes)
		}
	})
}

// newOrganizeDrive is a signed-in service over tree, and the account key
// its runs act for.
func newOrganizeDrive(t *testing.T, tree *fakeTree) (*DriveService, string) {
	t.Helper()
	fastTasks(t)
	svc := newDriveService(t, tree.serve)
	checkTree(t, tree)
	s, err := svc.active()
	if err != nil {
		t.Fatal(err)
	}
	return svc, s.key
}

func organize(t *testing.T, svc *DriveService, account string, req OrganizeRequest) OrganizeRecord {
	t.Helper()
	rec, err := svc.Organize(account, req)
	if err != nil {
		t.Fatalf("organize: %v", err)
	}
	return rec
}

// failures maps file ids to their item's error, for the items that failed.
func failures(rec OrganizeRecord) map[string]string {
	out := map[string]string{}
	for _, it := range rec.Items {
		if it.Error != "" {
			out[it.FileID] = it.Error
		}
	}
	return out
}

func to(path ...string) []string { return append([]string{}, path...) }

func TestOrganizeFilesByType(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "待整理", "", true)
	tree.add("a", "a.mp4", "F", false)
	tree.add("b", "b.MKV", "F", false)
	tree.add("n", "notes.pdf", "F", false)
	tree.add("p", "pic.jpg", "F", false)
	tree.add("pics", "图片", "F", true)
	tree.add("o", "other.jpg", "pics", false)
	svc, account := newOrganizeDrive(t, tree)

	rec := organize(t, svc, account, OrganizeRequest{FolderID: "F", Task: "按类型", Tokens: 1200, Changes: []OrganizeChange{
		{FileID: "a", Name: "a.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "b", Name: "b.MKV", ParentID: "F", ToPath: to("视频")},
		{FileID: "n", Name: "notes.pdf", ParentID: "F", NewName: "notes 2026.pdf", ToPath: to("文档")},
		{FileID: "p", Name: "pic.jpg", ParentID: "F", ToPath: to("图片")},
	}})

	if f := failures(rec); len(f) != 0 || rec.Stopped || !rec.Finished {
		t.Fatalf("failures = %v, stopped = %v, finished = %v", f, rec.Stopped, rec.Finished)
	}
	root := tree.in("F")
	videos, docs := root["视频"], root["文档"]
	if videos == "" || docs == "" {
		t.Fatalf("folders made under F: %v", root)
	}
	// An existing image is used, not doubled; only the folders made are recorded.
	var made []string
	for _, f := range rec.Created {
		made = append(made, f.Name)
		if f.ParentID != "F" || !slices.Equal(f.Path, []string{f.Name}) {
			t.Errorf("created %+v, want directly in F", f)
		}
	}
	slices.Sort(made)
	if !slices.Equal(made, []string{"文档", "视频"}) {
		t.Errorf("created = %v, want Documents and Videos", made)
	}
	if got := tree.in(videos); len(got) != 2 || got["a.mp4"] != "a" || got["b.MKV"] != "b" {
		t.Errorf("Videos holds %v", got)
	}
	if got := tree.in(docs); got["notes 2026.pdf"] != "n" {
		t.Errorf("Documents holds %v, want notes 2026.pdf", got)
	}
	if got := tree.in("pics"); got["pic.jpg"] != "p" || got["other.jpg"] != "o" {
		t.Errorf("Pictures holds %v", got)
	}
	n := rec.Items[2]
	if !n.Done || n.AtName != "notes 2026.pdf" || n.AtParentID != docs || n.Name != "notes.pdf" || n.ParentID != "F" {
		t.Errorf("record of notes.pdf = %+v", n)
	}
	if rec.Task != "按类型" || rec.Tokens != 1200 || rec.FolderID != "F" || rec.ID == "" || rec.At == "" {
		t.Errorf("record = %+v", rec)
	}
	// The two files bound for Videos travel in one request.
	batched := false
	for _, m := range tree.moves {
		batched = batched || (len(m) == 2 && slices.Contains(m, "a") && slices.Contains(m, "b"))
	}
	if !batched {
		t.Errorf("moves = %v, want a and b in one batch", tree.moves)
	}
}

// The preview may be minutes old. Whatever changed since is left alone and
// said so; what still fits goes ahead.
func TestOrganizeChecksTheFoldersAgainBeforeActing(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "待整理", "", true)
	tree.add("v", "视频", "F", true)
	tree.add("taken", "C.MP4", "v", false) // Arrived in the destination after the preview.
	tree.add("a", "a.mp4", "F", false)
	tree.add("b", "renamed elsewhere.mp4", "F", false)
	tree.add("c", "c.mp4", "F", false)
	tree.add("d", "d.mp4", "F", false)
	svc, account := newOrganizeDrive(t, tree)

	rec := organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "a", Name: "a.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "b", Name: "b.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "c", Name: "c.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "gone", Name: "gone.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "d", Name: "d.mp4", ParentID: "F", NewName: "D:1.mp4", ToParentID: "F"},
	}})

	want := map[string]string{
		"b":    "已被改名，未处理",
		"c":    "目标位置已有同名项，未处理",
		"gone": "已被移动或删除，未处理",
	}
	got := failures(rec)
	for id, msg := range want {
		if got[id] != msg {
			t.Errorf("outcome of %s = %q, want %q", id, got[id], msg)
		}
	}
	if len(got) != len(want) {
		t.Errorf("failures = %v, want only %v", got, want)
	}
	if v := tree.in("v"); v["a.mp4"] != "a" || v["C.MP4"] != "taken" || len(v) != 2 {
		t.Errorf("Videos holds %v", v)
	}
	if f := tree.in("F"); f["c.mp4"] != "c" || f["renamed elsewhere.mp4"] != "b" || f["D:1.mp4"] != "d" {
		t.Errorf("F holds %v", f)
	}
}

// Renames that free each other's names run in an order that never names a
// taken one; a swap cannot, so it is left as it is and said so.
func TestOrganizeOrdersStepsAndLeavesSwapsAlone(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "剧集", "", true)
	tree.add("v", "视频", "F", true)
	tree.add("in-v", "a.mp4", "v", false)
	tree.add("e1", "1.mp4", "F", false)
	tree.add("e2", "2.mp4", "F", false)
	tree.add("x", "x.mp4", "F", false)
	tree.add("y", "y.mp4", "F", false)
	tree.add("m", "Movie.MP4", "F", false)
	tree.add("a", "a.mp4", "F", false)
	svc, account := newOrganizeDrive(t, tree)

	rec := organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "e1", Name: "1.mp4", ParentID: "F", NewName: "2.mp4", ToPath: to()},
		{FileID: "e2", Name: "2.mp4", ParentID: "F", NewName: "3.mp4", ToPath: to()},
		{FileID: "x", Name: "x.mp4", ParentID: "F", NewName: "y.mp4", ToPath: to()},
		{FileID: "y", Name: "y.mp4", ParentID: "F", NewName: "x.mp4", ToPath: to()},
		{FileID: "m", Name: "Movie.MP4", ParentID: "F", NewName: "Movie.mp4", ToPath: to()},
		// Its own name is taken in Videos, its new one is not: renamed here first.
		{FileID: "a", Name: "a.mp4", ParentID: "F", NewName: "b.mp4", ToPath: to("视频")},
	}})

	got := failures(rec)
	if got["x"] != probSwap() || got["y"] != probSwap() || len(got) != 2 {
		t.Fatalf("failures = %v, want the swap of x and y only", got)
	}
	f := tree.in("F")
	if f["2.mp4"] != "e1" || f["3.mp4"] != "e2" || f["x.mp4"] != "x" || f["y.mp4"] != "y" || f["Movie.mp4"] != "m" {
		t.Errorf("F holds %v", f)
	}
	if v := tree.in("v"); v["b.mp4"] != "a" || v["a.mp4"] != "in-v" {
		t.Errorf("Videos holds %v", v)
	}
}

// A plan over several levels: paths are walked layer by
// layer, existing folders joined whatever their case, missing ones made top
// down; trash and trashIfEmpty go last. No folder ever holds two entries of
// one name on the way (checkTree).
func TestOrganizeBuildsPathsLayerByLayer(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "待分类", "", true)
	tree.add("R", "raw", "F", true)
	tree.add("A", "[Group] Show", "R", true)
	tree.add("e1", "Show.S01E01.mkv", "A", false)
	tree.add("s1", "Show.S01E01.srt", "A", false)
	tree.add("e2", "show s01e02.mkv", "R", false)
	tree.add("k", "keep.txt", "R", false)
	tree.add("j", "junk.url", "R", false)
	tree.add("SH", "Show (2020)", "F", true)
	tree.add("poster", "poster.jpg", "SH", false)
	tree.add("m1", "film.2019.mkv", "F", false)
	svc, account := newOrganizeDrive(t, tree)

	season := to("Show (2020)", "Season 01")
	rec := organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "e1", Name: "Show.S01E01.mkv", ParentID: "A", NewName: "Show (2020) - S01E01.mkv", ToPath: season},
		{FileID: "s1", Name: "Show.S01E01.srt", ParentID: "A", NewName: "Show (2020) - S01E01.zh.srt", ToPath: season},
		{FileID: "e2", Name: "show s01e02.mkv", ParentID: "R", NewName: "Show (2020) - S01E02.mkv", ToPath: to("show (2020)", "SEASON 01")},
		{FileID: "m1", Name: "film.2019.mkv", ParentID: "F", NewName: "Film (2019).mkv", ToPath: to("Movies", "Film (2019)")},
		{FileID: "j", Name: "junk.url", ParentID: "R", Op: opTrash, ToPath: to("raw")},
		{FileID: "A", Folder: true, Name: "[Group] Show", ParentID: "R", Op: opTrashIfEmpty, ToPath: to("raw", "[Group] Show")},
		{FileID: "R", Folder: true, Name: "raw", ParentID: "F", Op: opTrashIfEmpty, ToPath: to("raw")},
	}})

	if got := failures(rec); len(got) != 1 || got["R"] != probNotEmpty() {
		t.Fatalf("failures = %v, want only raw kept for keep.txt", got)
	}
	seasonID := tree.in("SH")["Season 01"]
	movies := tree.in("F")["Movies"]
	film := tree.in(movies)["Film (2019)"]
	if seasonID == "" || movies == "" || film == "" {
		t.Fatalf("F holds %v, Show (2020) holds %v", tree.in("F"), tree.in("SH"))
	}
	if got := tree.in(seasonID); len(got) != 3 || got["Show (2020) - S01E01.mkv"] != "e1" || got["Show (2020) - S01E01.zh.srt"] != "s1" || got["Show (2020) - S01E02.mkv"] != "e2" {
		t.Errorf("Season 01 holds %v", got)
	}
	if got := tree.in(film); got["Film (2019).mkv"] != "m1" {
		t.Errorf("Film (2019) holds %v", got)
	}
	if a, j := tree.file("A"), tree.file("j"); !a.trashed || !j.trashed {
		t.Errorf("trashed: emptied folder %v, junk %v", a.trashed, j.trashed)
	}
	if r := tree.file("R"); r.trashed || tree.in("R")["keep.txt"] != "k" {
		t.Errorf("raw should stay with keep.txt: %+v %v", r, tree.in("R"))
	}
	// Made parents first, each recorded with its path below F.
	paths := map[string][]string{}
	seen := map[string]bool{"F": true, "SH": true}
	for _, f := range rec.Created {
		if !seen[f.ParentID] {
			t.Errorf("created %+v before its parent", f)
		}
		seen[f.ID] = true
		paths[f.ID] = f.Path
	}
	if len(rec.Created) != 3 || !slices.Equal(paths[seasonID], season) || !slices.Equal(paths[movies], to("Movies")) || !slices.Equal(paths[film], to("Movies", "Film (2019)")) {
		t.Errorf("created = %+v", rec.Created)
	}
	for _, it := range rec.Items {
		if !it.Done {
			t.Errorf("item left pending: %+v", it)
		}
	}
	if j := rec.Items[4]; !j.Trashed || j.AtParentID != "R" {
		t.Errorf("record of the trashed junk = %+v", j)
	}
}

// A folder and anything below it are never changed by one plan:
// moved, renamed or trashed together with something it holds —
// now, or where the plan puts it — both are refused. Deeper than one level
// the chain comes from the index.
func TestOrganizeRefusesAFolderAndWhatIsInIt(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "整理", "", true)
	tree.add("D", "D", "F", true)
	tree.add("sub", "sub", "D", true)
	tree.add("deep", "deep.txt", "sub", false)
	tree.add("E", "E", "F", true)
	tree.add("x", "x.txt", "E", false)
	tree.add("T", "T", "F", true)
	tree.add("y", "y.txt", "F", false)
	tree.add("ok", "ok.txt", "F", false)
	svc, account := newOrganizeDrive(t, tree)
	if err := activeIndex(svc).store.Upsert([]index.Row{
		{ID: "F", Kind: drive.KindFolder, Name: "整理"},
		{ID: "D", ParentID: "F", Kind: drive.KindFolder, Name: "D"},
		{ID: "sub", ParentID: "D", Kind: drive.KindFolder, Name: "sub"},
	}); err != nil {
		t.Fatal(err)
	}

	rec := organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "D", Folder: true, Name: "D", ParentID: "F", ToPath: to("Archive")},
		{FileID: "deep", Name: "deep.txt", ParentID: "sub", NewName: "deep 2.txt", ToParentID: "sub"},
		{FileID: "E", Folder: true, Name: "E", ParentID: "F", Op: opTrash},
		{FileID: "x", Name: "x.txt", ParentID: "E", NewName: "x 2.txt", ToPath: to("E")},
		{FileID: "T", Folder: true, Name: "T", ParentID: "F", Op: opTrash},
		{FileID: "y", Name: "y.txt", ParentID: "F", ToPath: to("T")},
		{FileID: "ok", Name: "ok.txt", ParentID: "F", NewName: "ok 2.txt", ToPath: to()},
	}})

	got := failures(rec)
	for _, id := range []string{"D", "deep", "E", "x", "T", "y"} {
		if got[id] != probNested() {
			t.Errorf("outcome of %s = %q, want %q", id, got[id], probNested())
		}
	}
	if len(got) != 6 {
		t.Errorf("failures = %v", got)
	}
	f := tree.in("F")
	if f["D"] != "D" || f["E"] != "E" || f["T"] != "T" || f["y.txt"] != "y" || f["ok 2.txt"] != "ok" || f["Archive"] != "" {
		t.Errorf("F holds %v", f)
	}
	if tree.in("sub")["deep.txt"] != "deep" || tree.in("E")["x.txt"] != "x" {
		t.Errorf("sub holds %v, E holds %v", tree.in("sub"), tree.in("E"))
	}
}

func TestOrganizeRefusesAPlanOver5000(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "整理", "", true)
	svc, account := newOrganizeDrive(t, tree)

	changes := make([]OrganizeChange, maxPlan+1)
	for i := range changes {
		changes[i] = OrganizeChange{FileID: fmt.Sprint(i), Name: fmt.Sprint(i), ParentID: "F", ToPath: to()}
	}
	if _, err := svc.Organize(account, OrganizeRequest{FolderID: "F", Changes: changes}); !errors.Is(err, errPlanTooLarge) {
		t.Fatalf("a plan of %d changes: %v, want errPlanTooLarge", len(changes), err)
	}
	if h, err := svc.OrganizeHistory(account); err != nil || len(h) != 0 {
		t.Errorf("history after a refused plan = %v, %v", h, err)
	}
}

// A path through a name held by a file, or by several entries, blocks every
// change through it; other requests no folder could carry out are refused
// on their own; names clash whatever their case and Unicode form.
func TestOrganizeRefusesWhatCannotBeDone(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "整理", "", true)
	tree.add("show", "Show", "F", false)
	tree.add("dup1", "Dup", "F", true)
	tree.add("dup2", "dup", "F", true)
	tree.add("acute", "\u00e9.txt", "F", false)
	for _, id := range []string{"a", "a2", "b", "c", "d", "e", "g", "h"} {
		tree.add(id, id+".mp4", "F", false)
	}
	svc, account := newOrganizeDrive(t, tree)

	rec := organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "a", Name: "a.mp4", ParentID: "F", ToPath: to("Show", "S1")},
		{FileID: "a2", Name: "a2.mp4", ParentID: "F", ToPath: to("show")},
		{FileID: "b", Name: "b.mp4", ParentID: "F", ToPath: to("DUP")},
		{FileID: "c", Name: "c.mp4", ParentID: "F", ToPath: to("New")},
		{FileID: "d", Name: "d.mp4", ParentID: "F", Op: "delete"},
		{FileID: "e", Name: "e.mp4", ParentID: "F", Op: opTrashIfEmpty},
		{FileID: "g", Name: "g.mp4", ParentID: "F", NewName: "E\u0301.TXT", ToPath: to()},
		{FileID: "h", Name: "h.mp4", ParentID: "F", NewName: "x/y.mp4", ToPath: to()},
	}})

	want := map[string]string{
		"a":  "「Show」已被同名文件占用，未处理",
		"a2": "「Show」已被同名文件占用，未处理", // one layer per name, as first spelt
		"b":  "这里有多个「DUP」，未处理",
		"d":  probUnknownOp(),
		"e":  probNotFolder(),
		"g":  probClash(),
		"h":  probBadName(),
	}
	got := failures(rec)
	for id, msg := range want {
		if got[id] != msg {
			t.Errorf("outcome of %s = %q, want %q", id, got[id], msg)
		}
	}
	if len(got) != len(want) {
		t.Errorf("failures = %v, want only %v", got, want)
	}
	if n := tree.in("F")["New"]; n == "" || tree.in(n)["c.mp4"] != "c" {
		t.Errorf("F holds %v", tree.in("F"))
	}
	if tree.file("e").trashed || tree.file("d").trashed {
		t.Error("a refused item was trashed")
	}
}

// Stop lets the step in flight finish and starts nothing after it; a batch
// the server refuses is retried entry by entry, so one refusal is reported
// on its own.
func TestOrganizeStopsAndReportsEachFile(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "待整理", "", true)
	tree.add("v", "视频", "F", true)
	for _, id := range []string{"a", "b", "c"} {
		tree.add(id, id+".mp4", "F", false)
	}
	tree.add("r1", "r1.txt", "F", false)
	tree.add("r2", "r2.txt", "F", false)
	tree.add("t1", "t1.txt", "F", false)
	tree.refuse["b"] = true
	svc, account := newOrganizeDrive(t, tree)

	var stopOnce sync.Once
	tree.hook(func(what string) {
		if strings.HasPrefix(what, "PATCH") {
			stopOnce.Do(func() { svc.StopOrganize(account) })
		}
	})
	rec := organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "a", Name: "a.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "b", Name: "b.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "c", Name: "c.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "r1", Name: "r1.txt", ParentID: "F", NewName: "r1 new.txt", ToPath: to()},
		{FileID: "r2", Name: "r2.txt", ParentID: "F", NewName: "r2 new.txt", ToPath: to()},
		{FileID: "t1", Name: "t1.txt", ParentID: "F", Op: opTrash},
	}})

	got := failures(rec)
	if !rec.Stopped || !rec.Finished || got["b"] != "refused by the stand-in" || got["r2"] != probStopped() || got["t1"] != probStopped() || len(got) != 3 {
		t.Fatalf("stopped = %v, failures = %v", rec.Stopped, got)
	}
	f, v := tree.in("F"), tree.in("v")
	if v["a.mp4"] != "a" || v["c.mp4"] != "c" || f["b.mp4"] != "b" || f["r1 new.txt"] != "r1" || f["r2.txt"] != "r2" || f["t1.txt"] != "t1" {
		t.Errorf("F holds %v, Videos holds %v", f, v)
	}
	if h, err := svc.OrganizeHistory(account); err != nil || len(h) != 1 || !h[0].Stopped || !h[0].Finished {
		t.Errorf("history = %+v, %v", h, err)
	}

	// The run is over: the account takes another.
	if _, err := svc.Organize(account, OrganizeRequest{FolderID: "F"}); err != nil {
		t.Errorf("a run after the stopped one: %v", err)
	}
}

// Someone else takes a name while a move is on its way: the server numbers
// the moved entry, and the record says where it really is.
func TestOrganizeReportsANameTheServerGave(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "待整理", "", true)
	tree.add("v", "视频", "F", true)
	tree.add("a", "a.mp4", "F", false)
	svc, account := newOrganizeDrive(t, tree)

	tree.hook(func(what string) {
		if strings.HasSuffix(what, ":batchMove") {
			tree.put("other", "a.mp4", "v", false)
		}
	})
	rec := organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "a", Name: "a.mp4", ParentID: "F", ToPath: to("视频")},
	}})
	it := rec.Items[0]
	if it.Error != fmtNumbered("a(1).mp4") || it.AtName != "a(1).mp4" || it.AtParentID != "v" {
		t.Errorf("record = %+v", it)
	}
	tree.mu.Lock()
	if slices.Equal(tree.clashes, []string{"move a.mp4"}) {
		tree.clashes = nil // the other client's doing, not the run's
	}
	tree.mu.Unlock()
}

func TestOrganizeOneRunPerAccount(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "待整理", "", true)
	tree.add("a", "a.mp4", "F", false)
	svc, account := newOrganizeDrive(t, tree)

	entered, release := make(chan struct{}), make(chan struct{})
	tree.hook(func(string) {
		close(entered)
		<-release
	})
	first := make(chan error, 1)
	go func() {
		_, err := svc.Organize(account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
			{FileID: "a", Name: "a.mp4", ParentID: "F", NewName: "b.mp4", ToPath: to()},
		}})
		first <- err
	}()
	<-entered
	if _, err := svc.Organize(account, OrganizeRequest{FolderID: "F"}); !errors.Is(err, errOrganizing) {
		t.Errorf("second run while one goes: %v, want errOrganizing", err)
	}
	if _, err := svc.UndoOrganize(account, "any"); !errors.Is(err, errOrganizing) {
		t.Errorf("undo while a run goes: %v, want errOrganizing", err)
	}
	tree.hook(nil)
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first run: %v", err)
	}
}

// startOrganizeDrive is a service over tree keeping its files in config, as
// the app would after a start.
func startOrganizeDrive(t *testing.T, tree *fakeTree, config string) (*DriveService, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(tree.serve))
	t.Cleanup(srv.Close)
	svc := NewDriveService(DriveOptions{ConfigDir: config, APIBaseURL: srv.URL, AuthBaseURL: srv.URL, NoIndexSync: true, Logger: quiet()}, nil)
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	return svc, seedSession(t, svc, "user@example.com").key
}

// The record outlives the app: after a restart Undo
// brings back what the run trashed, moves and renames files back, and puts
// the folders it made in the trash once they are empty, deepest first.
func TestUndoOrganizeAfterARestart(t *testing.T) {
	fastTasks(t)
	tree := newFakeTree()
	checkTree(t, tree)
	tree.add("F", "待整理", "", true)
	tree.add("a", "a.mp4", "F", false)
	tree.add("n", "n.pdf", "F", false)
	tree.add("e", "e.txt", "F", false)
	tree.add("j", "junk.url", "F", false)
	tree.add("old", "空文件夹", "F", true)
	config := t.TempDir()
	first, account := startOrganizeDrive(t, tree, config)

	done := organize(t, first, account, OrganizeRequest{FolderID: "F", Task: "整理", Changes: []OrganizeChange{
		{FileID: "a", Name: "a.mp4", ParentID: "F", NewName: "A 01.mp4", ToPath: to("视频", "S1")},
		{FileID: "n", Name: "n.pdf", ParentID: "F", ToPath: to("文档")},
		{FileID: "e", Name: "e.txt", ParentID: "F", NewName: "E.txt", ToPath: to()},
		{FileID: "j", Name: "junk.url", ParentID: "F", Op: opTrash},
		{FileID: "old", Folder: true, Name: "空文件夹", ParentID: "F", Op: opTrashIfEmpty},
	}})
	if f := failures(done); len(f) != 0 || len(done.Created) != 3 || !tree.file("j").trashed || !tree.file("old").trashed {
		t.Fatalf("first run: failures %v, created %v", f, done.Created)
	}
	if err := first.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}

	second, again := startOrganizeDrive(t, tree, config)
	t.Cleanup(func() { _ = second.ServiceShutdown() })
	if again != account {
		t.Fatalf("account %q after the restart, was %q", again, account)
	}
	history, err := second.OrganizeHistory(account)
	if err != nil || len(history) != 1 || history[0].ID != done.ID || !history[0].Finished || history[0].Undo != nil {
		t.Fatalf("history after the restart = %+v, %v", history, err)
	}
	info, err := os.Stat(second.organizePath(account))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("record file: %v, %v", info, err)
	}

	undone, err := second.UndoOrganize(account, done.ID)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	u := undone.Undo
	if u == nil || !u.Finished || u.Stopped || u.Restored != 2 || u.Moved != 3 || u.Removed != 3 || len(u.Skipped) != 0 {
		t.Fatalf("undo = %+v", u)
	}
	f := tree.in("F")
	if len(f) != 5 || f["a.mp4"] != "a" || f["n.pdf"] != "n" || f["e.txt"] != "e" || f["junk.url"] != "j" || f["空文件夹"] != "old" {
		t.Errorf("after Undo F holds %v", f)
	}
	for _, c := range done.Created {
		if !tree.file(c.ID).trashed {
			t.Errorf("made folder %s still live", c.Name)
		}
	}
	if _, err := second.UndoOrganize(account, done.ID); !errors.Is(err, errUndone) {
		t.Errorf("a second undo: %v, want errUndone", err)
	}
	if h, _ := second.OrganizeHistory(account); len(h) != 1 || h[0].Undo == nil || h[0].Undo.Moved != 3 {
		t.Errorf("history after undo = %+v", h)
	}
}

// Undo checks each step against the drive as it is now.
func TestUndoOrganizeSkipsWhatChangedSince(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "待整理", "", true)
	tree.add("F2", "别处", "", true)
	tree.add("a", "a.mp4", "F", false)
	tree.add("b", "b.txt", "F", false)
	tree.add("c", "c.mp4", "F", false)
	tree.add("t1", "t1.txt", "F", false)
	tree.add("t2", "t2.txt", "F", false)
	svc, account := newOrganizeDrive(t, tree)

	done := organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "a", Name: "a.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "b", Name: "b.txt", ParentID: "F", NewName: "b2.txt", ToPath: to()},
		{FileID: "c", Name: "c.mp4", ParentID: "F", ToPath: to("视频")},
		{FileID: "t1", Name: "t1.txt", ParentID: "F", Op: opTrash},
		{FileID: "t2", Name: "t2.txt", ParentID: "F", Op: opTrash},
	}})
	if f := failures(done); len(f) != 0 {
		t.Fatalf("first run: %v", f)
	}
	videos := tree.in("F")["视频"]

	tree.mu.Lock()
	tree.add("intruder", "A.MP4", "F", false) // takes a's old name
	tree.files["c"].parent = "F2"             // moved by hand since
	tree.add("t1-new", "t1.txt", "F", false)  // takes t1's old name
	delete(tree.files, "t2")                  // emptied from the bin
	tree.mu.Unlock()

	undone, err := svc.UndoOrganize(account, done.ID)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	u := undone.Undo
	skipped := map[string]string{}
	for _, s := range u.Skipped {
		skipped[s.FileID] = s.Error
	}
	want := map[string]string{
		"a":    probPlaceTaken(),
		"c":    probMovedSince(),
		"t1":   probRestoreTaken(),
		"t2":   probNotInBin(),
		videos: probStillHolds(),
	}
	for id, msg := range want {
		if skipped[id] != msg {
			t.Errorf("undo of %s: %q, want %q", id, skipped[id], msg)
		}
	}
	if len(skipped) != len(want) || u.Moved != 1 || u.Restored != 0 || u.Removed != 0 {
		t.Errorf("undo = %+v", u)
	}
	if f := tree.in("F"); f["b.txt"] != "b" || f["A.MP4"] != "intruder" || f["t1.txt"] != "t1-new" {
		t.Errorf("F holds %v", f)
	}
	if tree.in(videos)["a.mp4"] != "a" || !tree.file("t1").trashed {
		t.Errorf("Videos holds %v; t1 = %+v", tree.in(videos), tree.file("t1"))
	}
}

// Records are kept per account, newest first, 30 at most, and go with the
// account.
func TestOrganizeRecordsAreKeptAndRemovedWithTheAccount(t *testing.T) {
	tree := newFakeTree()
	tree.add("F", "待整理", "", true)
	tree.add("a", "a.mp4", "F", false)
	svc, account := newOrganizeDrive(t, tree)

	organize(t, svc, account, OrganizeRequest{FolderID: "F", Changes: []OrganizeChange{
		{FileID: "a", Name: "a.mp4", ParentID: "F", NewName: "b.mp4", ToPath: to()},
	}})
	for i := range organizeKeep + 2 {
		if err := svc.keepOrganizeRecord(account, OrganizeRecord{ID: fmt.Sprintf("r%02d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	h, err := svc.OrganizeHistory(account)
	if err != nil || len(h) != organizeKeep || h[0].ID != fmt.Sprintf("r%02d", organizeKeep+1) {
		t.Fatalf("history = %d records, first %+v, %v", len(h), h[0], err)
	}

	path := svc.organizePath(account)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("record file: %v", err)
	}
	svc.removeAccountFiles(account)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("record file after removing the account: %v", err)
	}
}
