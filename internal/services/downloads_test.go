package services

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/download"
)

// fileBytes is what the stand-in CDN serves for a file id.
func fileBytes(id string) []byte { return bytes.Repeat([]byte(id+"·"), 30000) }

// newDownloadDrive is a signed-in service over a stand-in drive with a small
// tree under F1 — a page of S1 and a.mkv, a page of b.mkv still being saved
// and the empty S2; S1 holds c.mkv — and a CDN serving every file's bytes.
// A file whose id starts with "held" sends a little and then nothing until
// its request goes away.
func newDownloadDrive(t *testing.T) (*DriveService, *accountSession) {
	t.Helper()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		body := fileBytes(id)
		if strings.HasPrefix(id, "held") {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body)))
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[:1024])
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
			return
		}
		http.ServeContent(w, r, id, time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(cdn.Close)

	file := func(id, name, phase string) string {
		return fmt.Sprintf(`{"kind":"drive#file","id":%q,"name":%q,"size":"%d","phase":%q}`, id, name, len(fileBytes(id)), phase)
	}
	folder := func(id, name string) string {
		return fmt.Sprintf(`{"kind":"drive#folder","id":%q,"name":%q}`, id, name)
	}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/drive/v1/files":
			switch q.Get("parent_id") + "|" + q.Get("page_token") {
			case "F1|":
				fmt.Fprintf(w, `{"files":[%s,%s],"next_page_token":"p2"}`, folder("S1", "S1"), file("A", "a.mkv", "PHASE_TYPE_COMPLETE"))
			case "F1|p2":
				fmt.Fprintf(w, `{"files":[%s,%s]}`, file("B", "b.mkv", "PHASE_TYPE_RUNNING"), folder("S2", "S2"))
			case "S1|":
				fmt.Fprintf(w, `{"files":[%s]}`, file("C", "c.mkv", "PHASE_TYPE_COMPLETE"))
			case "Broken|":
				fmt.Fprintf(w, `{"files":[%s],"next_page_token":"p2"}`, file("held-read", "partial.bin", "PHASE_TYPE_COMPLETE"))
			case "Broken|p2":
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":"permission_denied","error_description":"folder read refused"}`)
			default:
				io.WriteString(w, `{"files":[]}`)
			}
		case strings.HasPrefix(r.URL.Path, "/drive/v1/files/") && q.Get("usage") == "FETCH":
			id := strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")
			fmt.Fprintf(w, `{"id":%q,"name":%q,"size":"%d","links":{"application/octet-stream":{"url":%q,"expire":%q}}}`,
				id, id, len(fileBytes(id)), cdn.URL+"/f?id="+id, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		default:
			io.WriteString(w, `{}`)
		}
	})
	s, err := svc.active()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, s
}

// useDownloadDir points the download folder at home/dl.
func useDownloadDir(t *testing.T, svc *DriveService, home string) string {
	t.Helper()
	dl := filepath.Join(home, "dl")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDownloadDir(dl); err != nil {
		t.Fatal(err)
	}
	return dl
}

func waitDownload(t *testing.T, svc *DriveService, id string, ok func(DownloadTask) bool) DownloadTask {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, task := range svc.Downloads() {
			if task.ID == id && ok(task) {
				return task
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("download %s never got there: %+v", id, svc.Downloads())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func downloaded(task DownloadTask) bool { return task.State == download.StateDone }

func TestDownloadFilesWalksFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	svc, s := newDownloadDrive(t)
	dl := useDownloadDir(t, svc, home)
	if err := os.Mkdir(filepath.Join(dl, "电影"), 0o755); err != nil {
		t.Fatal(err)
	}

	added, err := svc.DownloadFiles(s.key, []DownloadItem{
		{ID: "F1", Name: "电影", Folder: true},
		{ID: "F2", Name: "空", Folder: true},
		{ID: "X", Name: "x.bin", Size: int64(len(fileBytes("X")))},
	})
	if err == nil || !strings.Contains(err.Error(), "「b.mkv」还没有保存完成") {
		t.Fatalf("the file still being saved was not reported: %v", err)
	}
	want := map[string]struct{ path, origin string }{
		"A": {filepath.Join(dl, "电影 (1)", "a.mkv"), "全部文件 / 电影"},
		"C": {filepath.Join(dl, "电影 (1)", "S1", "c.mkv"), "全部文件 / 电影 / S1"},
		"X": {filepath.Join(dl, "x.bin"), "全部文件"},
	}
	if len(added) != len(want) {
		t.Fatalf("queued %d files: %+v", len(added), added)
	}
	for _, task := range added {
		w := want[task.FileID]
		if task.Path != w.path || task.Origin != w.origin || task.Account != s.key {
			t.Errorf("%s queued as %s from %q, want %s from %q", task.FileID, task.Path, task.Origin, w.path, w.origin)
		}
		if task.FileID == "C" && task.SavedTo != filepath.Join("~", "dl", "电影 (1)", "S1") {
			t.Errorf("c.mkv is saved to %q", task.SavedTo)
		}
	}
	for _, task := range added {
		waitDownload(t, svc, task.ID, downloaded)
		got, err := os.ReadFile(task.Path)
		if err != nil || !bytes.Equal(got, fileBytes(task.FileID)) {
			t.Errorf("%s: %d bytes on disk, %v", task.Path, len(got), err)
		}
	}
	for _, empty := range []string{filepath.Join(dl, "电影 (1)", "S2"), filepath.Join(dl, "空")} {
		if _, err := os.Stat(empty); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("made %s for nothing: %v", empty, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dl, "电影")); err != nil || len(entries) != 0 {
		t.Errorf("the folder already there was written into: %v, %v", entries, err)
	}
	if _, err := svc.DownloadFiles(s.key, []DownloadItem{{ID: "X", Name: "x.bin"}}); err != nil {
		t.Errorf("downloading a finished file again: %v", err)
	}
}

func TestQueueDownloadsPreservesPartialFolderTasks(t *testing.T) {
	for _, test := range []struct {
		name   string
		folder string
		files  []string
	}{
		{name: "refused child", folder: "F1", files: []string{"A", "C", "X"}},
		{name: "read failure", folder: "Broken", files: []string{"held-read", "X"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			svc, s := newDownloadDrive(t)
			useDownloadDir(t, svc, home)

			report, err := svc.QueueDownloads(s.key, []DownloadItem{
				{ID: test.folder, Name: "partial", Folder: true},
				{ID: "X", Name: "x.bin"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Items) != 2 || report.Items[0].ID != test.folder ||
				report.Items[0].Name != "partial" || report.Items[0].State != "unknown" ||
				report.Items[0].Error == "" || report.Items[1].State != "done" || report.Items[1].Error != "" {
				t.Fatalf("submission receipts = %+v", report.Items)
			}
			if len(report.Tasks) != len(test.files) || len(svc.Downloads()) != len(test.files) {
				t.Fatalf("queued tasks lost: report %+v, queue %+v", report.Tasks, svc.Downloads())
			}
			queued := map[string]bool{}
			for _, task := range report.Tasks {
				queued[task.FileID] = true
			}
			for _, id := range test.files {
				if !queued[id] {
					t.Errorf("successful task %s missing from report", id)
				}
			}
		})
	}
}

func TestQueueDownloadsReportsKnownRefusedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	svc, s := newDownloadDrive(t)
	useDownloadDir(t, svc, home)
	if _, err := svc.DownloadFiles(s.key, []DownloadItem{{ID: "held1", Name: "held.bin"}}); err != nil {
		t.Fatal(err)
	}
	report, err := svc.QueueDownloads(s.key, []DownloadItem{
		{ID: "held1", Name: "held.bin"},
		{ID: "X", Name: "x.bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 2 || report.Items[0].ID != "held1" ||
		report.Items[0].State != "failed" || !strings.Contains(report.Items[0].Error, "已在下载列表里") ||
		report.Items[1].State != "done" {
		t.Fatalf("submission receipts = %+v", report.Items)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].FileID != "X" || len(svc.Downloads()) != 2 {
		t.Fatalf("refused submission duplicated or successful task lost: %+v", report.Tasks)
	}
}

func TestQueueDownloadsUsesExplicitAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	svc, original := newDownloadDrive(t)
	useDownloadDir(t, svc, home)
	current := seedSession(t, svc, "other@example.com")

	report, err := svc.QueueDownloads(original.key, []DownloadItem{{ID: "X", Name: "x.bin"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].Account != original.key ||
		len(report.Items) != 1 || report.Items[0].State != "done" {
		t.Fatalf("submission used the wrong account: %+v", report)
	}
	if active, err := svc.active(); err != nil || active != current {
		t.Fatalf("submission changed the current account: %v", err)
	}
	if q := current.downloadQueue(); q != nil && len(q.List()) != 0 {
		t.Fatalf("submission reached the current account's queue: %+v", q.List())
	}
	for _, account := range []string{"", "missing-account"} {
		if report, err := svc.QueueDownloads(account, []DownloadItem{{ID: "Y", Name: "y.bin"}}); err == nil || len(report.Tasks) != 0 {
			t.Errorf("invalid account %q submitted downloads: %+v, %v", account, report, err)
		}
	}
}

// The download settings start at their defaults, keep what was chosen across
// a restart, and never leave their range — not even from a hand-edited file.
func TestDownloadSettingsPersistWithinBounds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	config := t.TempDir()
	open := func() *DriveService {
		return NewDriveService(DriveOptions{ConfigDir: config, NoIndexSync: true, Logger: quiet()}, nil)
	}
	if s := open().LocalSettings(); s.DownloadDir != filepath.Join(home, "Downloads") || s.ParallelDownloads != 2 || s.DownloadThreads != download.MaxThreads {
		t.Fatalf("defaults = %+v; want ~/Downloads, 2 files and %d threads", s, download.MaxThreads)
	}
	svc := open()
	for _, n := range []int{0, 6} {
		if err := svc.SetParallelDownloads(n); err == nil {
			t.Errorf("%d files at once was accepted", n)
		}
	}
	for _, n := range []int{0, download.MaxThreads + 1} {
		if err := svc.SetDownloadThreads(n); err == nil {
			t.Errorf("%d threads was accepted", n)
		}
	}
	plain := filepath.Join(home, "plain.txt")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"relative/dl", plain, filepath.Join(home, "missing")} {
		if err := svc.SetDownloadDir(dir); err == nil {
			t.Errorf("download folder %s was accepted", dir)
		}
	}
	dl := filepath.Join(home, "dl")
	if err := os.Mkdir(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDownloadDir(dl); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetParallelDownloads(5); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDownloadThreads(4); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dl); len(entries) != 0 {
		t.Errorf("the write check left %d files in the folder", len(entries))
	}
	if s := open().LocalSettings(); s.DownloadDir != dl || s.ParallelDownloads != 5 || s.DownloadThreads != 4 {
		t.Fatalf("after a restart = %+v", s)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"downloadThreads": 64, "parallelDownloads": -1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := open().LocalSettings(); s.DownloadThreads != download.MaxThreads || s.ParallelDownloads != 1 || s.DownloadDir != filepath.Join(home, "Downloads") {
		t.Fatalf("a hand-edited file reads as %+v", s)
	}
}

// Removing an account deletes its download records and what its unfinished
// downloads had on disk; finished files are the user's and stay.
func TestRemoveAccountDeletesDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	svc, s := newDownloadDrive(t)
	dl := useDownloadDir(t, svc, home)
	added, err := svc.DownloadFiles(s.key, []DownloadItem{{ID: "held1", Name: "big.bin"}, {ID: "Y", Name: "y.bin"}})
	if err != nil || len(added) != 2 {
		t.Fatalf("queued %+v, %v", added, err)
	}
	waitDownload(t, svc, added[1].ID, downloaded)
	waitDownload(t, svc, added[0].ID, func(task DownloadTask) bool { return task.State == download.StateDownloading })
	partial := added[0].Path + download.PartialExt
	journal := svc.accountFile(s.key, "downloads-", ".json")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		_, perr := os.Stat(partial)
		_, jerr := os.Stat(journal)
		if perr == nil && jerr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("before removal: partial %v, journal %v", perr, jerr)
		}
	}

	if _, err := svc.RemoveAccount(s.key); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{partial, journal} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived the removal: %v", filepath.Base(path), err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(dl, "y.bin")); err != nil || !bytes.Equal(got, fileBytes("Y")) {
		t.Errorf("the finished download went with the account: %v", err)
	}
}

func TestOpenAndRevealDownload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var opened, revealed []string
	open, reveal := openPath, revealPath
	openPath = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	revealPath = func(path string, selectFile bool) error {
		revealed = append(revealed, fmt.Sprintf("%s|%v", path, selectFile))
		return nil
	}
	t.Cleanup(func() { openPath, revealPath = open, reveal })

	svc, s := newDownloadDrive(t)
	dl := useDownloadDir(t, svc, home)
	added, err := svc.DownloadFiles(s.key, []DownloadItem{{ID: "Y", Name: "y.bin"}, {ID: "held1", Name: "big.bin"}})
	if err != nil || len(added) != 2 {
		t.Fatalf("queued %+v, %v", added, err)
	}
	done, held := added[0], added[1]
	waitDownload(t, svc, done.ID, downloaded)
	waitDownload(t, svc, held.ID, func(task DownloadTask) bool { return task.State == download.StateDownloading })
	if err := svc.PauseDownload(s.key, held.ID); err != nil {
		t.Fatal(err)
	}
	waitDownload(t, svc, held.ID, func(task DownloadTask) bool { return task.State == download.StatePaused })

	if err := svc.OpenDownload(s.key, done.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevealDownload(s.key, done.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.OpenDownload(s.key, held.ID); err == nil || err.Error() != "下载还没有完成" {
		t.Fatalf("opening an unfinished download: %v", err)
	}
	if err := svc.RevealDownload(s.key, held.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevealDownloadDir(); err != nil {
		t.Fatal(err)
	}
	// The finished file moved away: there is nothing to open, and its
	// folder is what is shown.
	if err := os.Remove(done.Path); err != nil {
		t.Fatal(err)
	}
	if err := svc.OpenDownload(s.key, done.ID); err == nil || err.Error() != "文件已被移动或删除" {
		t.Fatalf("opening a deleted download: %v", err)
	}
	if err := svc.RevealDownload(s.key, done.ID); err != nil {
		t.Fatal(err)
	}

	if want := []string{filepath.Join(dl, "y.bin")}; strings.Join(opened, ",") != strings.Join(want, ",") {
		t.Errorf("opened %v, want %v", opened, want)
	}
	want := []string{
		filepath.Join(dl, "y.bin") + "|true",
		filepath.Join(dl, "big.bin") + download.PartialExt + "|true",
		dl + "|false",
		dl + "|false",
	}
	if strings.Join(revealed, ",") != strings.Join(want, ",") {
		t.Errorf("revealed %v, want %v", revealed, want)
	}
}
