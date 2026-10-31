package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

func TestPlayLogRoundTripsAndCaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plays-dev.json")
	l := openPlayLog(path, quiet())
	for i := range maxPlays + 5 {
		l.opened(playRecord{FileID: fmt.Sprint("f", i), Name: fmt.Sprint("v", i, ".mkv"), Duration: 100, PlayedAt: "2026-10-13T00:00:00Z"})
	}
	// Reopening moves a file to the top instead of duplicating it.
	l.opened(playRecord{FileID: "f10", Name: "v10.mkv", Position: 5, Duration: 100})
	l.progress("f10", 42, 0, time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC))
	l.progress("unknown", 1, 1, time.Now())
	l.forget("f53")

	got := openPlayLog(path, quiet()).list()
	if len(got) != maxPlays-1 {
		t.Fatalf("records = %d, want %d (capped, then one forgotten)", len(got), maxPlays-1)
	}
	if got[0].FileID != "f10" || got[0].Position != 42 || got[0].Duration != 100 || got[0].PlayedAt != "2026-10-13T01:00:00Z" {
		t.Errorf("top = %+v, want f10 at 42s of 100s", got[0])
	}
	if got[1].FileID != "f54" {
		t.Errorf("second = %s, want the newest other play f54", got[1].FileID)
	}
	for _, r := range got {
		switch r.FileID {
		case "unknown", "f53", "f0", "f4":
			t.Errorf("%s should not be recorded", r.FileID)
		}
	}
}

func TestPlayLogMovesAnUnreadableFileAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plays-dev.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := openPlayLog(path, quiet()).list(); len(got) != 0 {
		t.Errorf("records = %v, want none", got)
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Errorf("the unreadable file was not kept aside: %v", err)
	}
}

func TestPlayLogWithoutPathStaysInMemory(t *testing.T) {
	l := openPlayLog("", quiet())
	l.opened(playRecord{FileID: "f1"})
	if got := l.list(); len(got) != 1 {
		t.Errorf("records = %v", got)
	}
}

func TestPlayTrackReportsEveryStepAndTheEnd(t *testing.T) {
	var tr playTrack
	if tr.observe("f1", PlayerState{Position: 30, Duration: 100}) {
		t.Error("a file other than the tracked one must not report")
	}
	tr = playTrack{fileID: "f1"}
	steps := []struct {
		state PlayerState
		want  bool
	}{
		{PlayerState{Loading: true, Position: 500, Duration: 900}, false}, // still the old file's figures
		{PlayerState{Position: 3, Duration: 0}, false},                    // mpv has not said the runtime yet
		{PlayerState{Position: 10, Duration: 100}, false},
		{PlayerState{Position: 15, Duration: 100}, true},
		{PlayerState{Position: 29, Duration: 100}, false},
		{PlayerState{Position: 31, Duration: 100}, true},
		{PlayerState{Position: 35, Duration: 100, EOF: true}, true},
		{PlayerState{Position: 35, Duration: 100, EOF: true}, false},
	}
	for i, s := range steps {
		if got := tr.observe("f1", s.state); got != s.want {
			t.Errorf("step %d (%+v) reported %v, want %v", i, s.state, got, s.want)
		}
	}
	account, id, pos, dur, ok := tr.end()
	if !ok || id != "f1" || pos != 35 || dur != 100 || account != "" {
		t.Errorf("end = %q %s %v/%v %v", account, id, pos, dur, ok)
	}
	if _, _, _, _, ok := tr.end(); ok {
		t.Error("end must forget the file")
	}
}

// activePlays is the play record of the account on screen.
func activePlays(t *testing.T, svc *DriveService) *playLog {
	t.Helper()
	s, err := svc.active()
	if err != nil {
		t.Fatal(err)
	}
	return s.playLog()
}

// playsService serves the cloud play history from events, or fails with
// status when it is non-zero.
func playsService(t *testing.T, events string, status int) *DriveService {
	return newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/drive/v1/events" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var filters map[string]map[string]any
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil || filters["type"]["in"] != drive.EventTypePlay {
			t.Errorf("filters = %q", r.URL.Query().Get("filters"))
		}
		if status != 0 {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":"permission_denied","error_code":10}`)
			return
		}
		io.WriteString(w, events)
	})
}

func playEvent(id, at string, seconds, duration int, ref string) string {
	if ref != "" {
		ref = `,"reference_resource":` + ref
	}
	return fmt.Sprintf(`{"id":"e-%s","type":"TYPE_PLAY","file_id":%q,"file_name":"%s.mkv","updated_time":%q,
		"params":{"play_seconds":"%d","play_duration":"%d"}%s}`, id, id, id, at, seconds, duration, ref)
}

func liveRef(id string) string {
	return fmt.Sprintf(`{"kind":"drive#file","id":%q,"name":"%s-now.mkv","thumbnail_link":"https://thumb/%s"}`, id, id, id)
}

func TestRecentPlaysMergesCloudAndLocal(t *testing.T) {
	events := `{"events":[` +
		playEvent("both", "2026-10-12T20:00:00+08:00", 600, 7200, liveRef("both")) + "," + // local is newer
		playEvent("cloud", "2026-10-12T19:00:00+08:00", 100, 3600, liveRef("cloud")) + "," +
		playEvent("done", "2026-10-12T18:00:00+08:00", 3595, 3600, liveRef("done")) + "," + // within 10 s of the end
		playEvent("binned", "2026-10-12T17:00:00+08:00", 50, 3600, `{"kind":"drive#file","id":"binned","trashed":true}`) + "," +
		playEvent("deleted", "2026-10-12T16:00:00+08:00", 50, 3600, "") + "," +
		playEvent("opened", "2026-10-12T15:00:00+08:00", 2, 0, liveRef("opened")) + // no progress yet: still listed
		`]}`
	svc := playsService(t, events, 0)

	s := activeIndex(svc)
	if err := s.store.Upsert([]index.Row{
		{ID: "indexed", Kind: drive.KindFile, Name: "renamed.mkv", Category: "video", ThumbnailLink: "https://thumb/fresh"},
		{ID: "trashed", Kind: drive.KindFile, Name: "t.mkv", Category: "video", Trashed: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetMeta(index.MetaFullAt, "2026-10-13T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	plays := activePlays(t, svc)
	for _, r := range []playRecord{
		{FileID: "both", Name: "both.mkv", Position: 900, Duration: 0, PlayedAt: "2026-10-12T13:00:00Z"},
		{FileID: "indexed", Name: "old.mkv", Thumbnail: "https://thumb/stale", Position: 300, Duration: 1000, PlayedAt: "2026-10-12T10:00:00Z"},
		{FileID: "trashed", Name: "t.mkv", Position: 300, Duration: 1000, PlayedAt: "2026-10-12T09:00:00Z"},
		{FileID: "absent", Name: "a.mkv", Position: 300, Duration: 1000, PlayedAt: "2026-10-12T08:00:00Z"},
		{FileID: "binned", Name: "binned.mkv", Position: 60, Duration: 3600, PlayedAt: "2026-10-12T06:30:00Z"},
		{FileID: "finished", Name: "f.mkv", Position: 990, Duration: 1000, PlayedAt: "2026-10-12T06:00:00Z"},
	} {
		plays.opened(r)
	}

	got, err := svc.RecentPlays(20)
	if err != nil {
		t.Fatalf("recent plays: %v", err)
	}
	want := []RecentPlay{
		// Local was played after the cloud event: its position wins, the
		// cloud keeps the runtime it knows, the name and the thumbnail.
		{FileID: "both", Name: "both-now.mkv", Thumbnail: "https://thumb/both", Position: 900, Duration: 7200, PlayedAt: "2026-10-12T13:00:00Z", Available: true, Local: true},
		{FileID: "cloud", Name: "cloud-now.mkv", Thumbnail: "https://thumb/cloud", Position: 100, Duration: 3600, PlayedAt: "2026-10-12T11:00:00Z", Available: true},
		{FileID: "indexed", Name: "renamed.mkv", Thumbnail: "https://thumb/fresh", Position: 300, Duration: 1000, PlayedAt: "2026-10-12T10:00:00Z", Available: true, Local: true},
		{FileID: "trashed", Name: "t.mkv", Position: 300, Duration: 1000, PlayedAt: "2026-10-12T09:00:00Z", Local: true},
		{FileID: "absent", Name: "a.mkv", Position: 300, Duration: 1000, PlayedAt: "2026-10-12T08:00:00Z", Local: true},
		{FileID: "opened", Name: "opened-now.mkv", Thumbnail: "https://thumb/opened", Position: 2, PlayedAt: "2026-10-12T07:00:00Z", Available: true},
		// The cloud says it is in the bin: kept for remove, not offered to play.
		{FileID: "binned", Name: "binned.mkv", Position: 60, Duration: 3600, PlayedAt: "2026-10-12T06:30:00Z", Local: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries:\n%+v\nwant %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d:\n got  %+v\n want %+v", i, got[i], want[i])
		}
	}

	if limited, _ := svc.RecentPlays(2); len(limited) != 2 || limited[0].FileID != "both" {
		t.Errorf("limit 2 = %+v", limited)
	}

	if err := svc.ForgetPlay("absent"); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.RecentPlays(20)
	for _, p := range after {
		if p.FileID == "absent" {
			t.Error("a forgotten play is still listed")
		}
	}
}

// Before the index has walked the drive it cannot say a file is gone, so a
// local play stays available.
func TestRecentPlaysTrustsLocalRecordsBeforeTheIndexIsReady(t *testing.T) {
	svc := playsService(t, `{"events":[]}`, 0)
	activePlays(t, svc).opened(playRecord{FileID: "absent", Name: "a.mkv", Position: 30, Duration: 100, PlayedAt: "2026-10-12T08:00:00Z"})
	got, err := svc.RecentPlays(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Available || !got[0].Local {
		t.Errorf("got %+v, want one available local entry", got)
	}
}

func TestRecentPlaysFallsBackToLocalWhenTheCloudFails(t *testing.T) {
	svc := playsService(t, "", http.StatusForbidden)
	activePlays(t, svc).opened(playRecord{FileID: "f1", Name: "a.mkv", Position: 30, Duration: 100, PlayedAt: "2026-10-12T08:00:00Z"})
	got, err := svc.RecentPlays(5)
	if err != nil {
		t.Fatalf("a cloud failure with a local record must not fail: %v", err)
	}
	if len(got) != 1 || got[0].FileID != "f1" {
		t.Errorf("got %+v", got)
	}

	if _, err := svc.RemoveAccount(svc.Status().AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecentPlays(5); err == nil {
		t.Error("with no account there is neither source; want an error")
	}
	if err := svc.ForgetPlay("f1"); err == nil {
		t.Error("ForgetPlay with no account: want an error")
	}
}

// The local record survives a restart: it is per account, in the config dir.
func TestPlayRecordIsPerAccountOnDisk(t *testing.T) {
	svc := playsService(t, `{"events":[]}`, 0)
	activePlays(t, svc).opened(playRecord{FileID: "f1", Name: "a.mkv", Position: 30, Duration: 100, PlayedAt: "2026-10-12T08:00:00Z"})
	path := filepath.Join(svc.configDir, "plays-"+drive.DeviceIDFor("user@example.com")+".json")
	if got := openPlayLog(path, quiet()).list(); len(got) != 1 || got[0].FileID != "f1" {
		t.Errorf("read back from %s: %+v", path, got)
	}
	seedSession(t, svc, "other@example.com")
	if got := activePlays(t, svc).list(); len(got) != 0 {
		t.Errorf("another account sees %+v", got)
	}
}

// Clear hides this computer's record and cloud events at or before that
// moment. A later cloud event and a later local play still show, including
// one watched to the end. Files are not involved.
func TestClearPlaysHidesOlderHistoryOnThisComputer(t *testing.T) {
	events := `{"events":[` +
		playEvent("new", "2026-10-20T08:00:00Z", 40, 100, liveRef("new")) + "," +
		playEvent("done", "2026-10-21T08:00:00Z", 100, 100, liveRef("done")) + "," +
		playEvent("edge", "2026-10-12T00:00:00Z", 20, 100, liveRef("edge")) + "," +
		playEvent("old", "2026-10-01T08:00:00Z", 30, 100, liveRef("old")) +
		`]}`
	svc := playsService(t, events, 0)
	log := activePlays(t, svc)
	if err := log.opened(playRecord{FileID: "local", Name: "local.mkv", Position: 10, Duration: 100, PlayedAt: "2026-10-02T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := log.clear(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	got, err := svc.PlayHistoryPage(svc.Status().AccountID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ids := playIDs(got.Plays); len(ids) != 2 || ids[0] != "done" || ids[1] != "new" {
		t.Fatalf("history after clear = %v, want done, new (finished included, older hidden)", ids)
	}
	recent, err := svc.RecentPlays(10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := playIDs(recent); len(ids) != 1 || ids[0] != "new" {
		t.Fatalf("continue watching = %v, want only the unfinished later play", ids)
	}

	if err := log.opened(playRecord{FileID: "after", Name: "after.mkv", Position: 5, Duration: 100, PlayedAt: "2026-10-22T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	got, err = svc.PlayHistoryPage(svc.Status().AccountID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ids := playIDs(got.Plays); len(ids) != 3 || ids[0] != "after" {
		t.Fatalf("history after a new play = %v, want after first", ids)
	}

	path := filepath.Join(svc.configDir, "plays-"+drive.DeviceIDFor("user@example.com")+".json")
	restored := openPlayLog(path, quiet())
	_, cutoff := restored.snapshot()
	if cutoff.IsZero() || len(restored.list()) != 1 || restored.list()[0].FileID != "after" {
		t.Fatalf("cleared record on disk = %+v at %s", restored.list(), restored.cleared)
	}
}

func TestClearPlaysPersistsAndIsAccountScoped(t *testing.T) {
	svc := playsService(t, `{"events":[]}`, 0)
	first := activePlays(t, svc)
	if err := first.opened(playRecord{FileID: "same", Name: "first.mp4"}); err != nil {
		t.Fatal(err)
	}
	seedSession(t, svc, "second@example.com")
	second := activePlays(t, svc)
	if err := second.opened(playRecord{FileID: "same", Name: "second.mp4"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearPlays(); err != nil {
		t.Fatal(err)
	}
	if rows := first.list(); len(rows) != 1 || rows[0].Name != "first.mp4" {
		t.Fatalf("clear affected the other account: %+v", rows)
	}
	rows, cutoff := openPlayLog(second.path, quiet()).snapshot()
	if len(rows) != 0 || cutoff.IsZero() {
		t.Fatalf("clear did not survive restart: %+v, %s", rows, cutoff)
	}
	if saved, err := second.progress("same", 30, 100, time.Now()); saved || err != nil {
		t.Fatalf("the running video resurrected a cleared record: %t, %v", saved, err)
	}
}

func TestClearPlaysKeepsHistoryWhenSavingFails(t *testing.T) {
	l := openPlayLog("", quiet())
	if err := l.opened(playRecord{FileID: "keep"}); err != nil {
		t.Fatal(err)
	}
	l.path = filepath.Join(t.TempDir(), "missing", "plays.json")
	if err := l.clear(time.Now()); err == nil {
		t.Fatal("want a save error")
	}
	rows, cutoff := l.snapshot()
	if len(rows) != 1 || rows[0].FileID != "keep" || !cutoff.IsZero() {
		t.Fatalf("failed clear changed in-memory history: %+v, %s", rows, cutoff)
	}
}

func TestClearPlaysNeedsAnAccount(t *testing.T) {
	svc := playsService(t, `{"events":[]}`, 0)
	if _, err := svc.RemoveAccount(svc.Status().AccountID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearPlays(); err == nil {
		t.Error("ClearPlays with no account: want an error")
	}
}

func TestLocalPlayHistoryNeverFetchesCloud(t *testing.T) {
	var requests atomic.Int32
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !authAndTokenRoutes(w, r) {
			t.Errorf("local history made a remote request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	owner, _ := svc.active()
	for _, record := range []playRecord{
		{FileID: "one", Position: 99, Duration: 100, PlayedAt: "2026-10-22T08:00:00Z"},
		{FileID: "gone", Position: 20, Duration: 100, PlayedAt: "2026-10-21T08:00:00Z"},
	} {
		if err := owner.playLog().opened(record); err != nil {
			t.Fatal(err)
		}
	}
	fillIndex(t, svc, []index.Row{indexedVideo("one", "", "current.mkv", 1, "")})
	other := seedSession(t, svc, "other@example.com")
	before := requests.Load()
	plays, err := svc.LocalPlayHistory(owner.key)
	if err != nil || len(plays) != 2 {
		t.Fatalf("local history = %+v, %v", plays, err)
	}
	if plays[0].FileID != "one" || plays[0].Name != "current.mkv" || !plays[0].Available || !plays[0].Local || !finished(plays[0].Position, plays[0].Duration) || plays[1].Available {
		t.Fatalf("local history visibility = %+v", plays)
	}
	if plays, err := svc.LocalPlayHistory(other.key); err != nil || len(plays) != 0 {
		t.Fatalf("other account history = %+v, %v", plays, err)
	}
	for _, account := range []string{"", "missing"} {
		if _, err := svc.LocalPlayHistory(account); err == nil {
			t.Fatalf("invalid owner %q accepted", account)
		}
	}
	if requests.Load() != before {
		t.Fatal("local history contacted the network")
	}
}

type playHistoryPageResult struct {
	page PlayHistoryPage
	err  error
}

func requestPlayHistoryPage(svc *DriveService, account, token string) <-chan playHistoryPageResult {
	result := make(chan playHistoryPageResult, 1)
	go func() {
		page, err := svc.PlayHistoryPage(account, token)
		result <- playHistoryPageResult{page, err}
	}()
	return result
}

func awaitPlayHistoryPage(t *testing.T, result <-chan playHistoryPageResult) PlayHistoryPage {
	t.Helper()
	select {
	case result := <-result:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.page
	case <-time.After(5 * time.Second):
		t.Fatal("history page did not return")
		return PlayHistoryPage{}
	}
}

func TestPlayHistoryPageReturnsBeforeDelayedNextPage(t *testing.T) {
	var requests atomic.Int32
	entered, release := make(chan struct{}, 1), make(chan struct{}, 1)
	defer close(release)
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		requests.Add(1)
		if r.URL.Path != "/drive/v1/events" || r.URL.Query().Get("limit") != "50" {
			t.Errorf("unexpected history request %s", r.URL)
		}
		switch r.URL.Query().Get("page_token") {
		case "":
			io.WriteString(w, `{"events":[`+playEvent("a", "2026-10-20T08:00:00Z", 10, 100, liveRef("a"))+`],"next_page_token":"p2"}`)
		case "p2":
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			io.WriteString(w, `{"events":[`+playEvent("b", "2026-10-19T08:00:00Z", 99, 100, liveRef("b"))+`]}`)
		default:
			t.Errorf("unexpected page token %q", r.URL.Query().Get("page_token"))
		}
	})
	account := svc.Status().AccountID
	first := awaitPlayHistoryPage(t, requestPlayHistoryPage(svc, account, ""))
	if first.Account != account || first.NextPageToken != "p2" || len(first.Plays) != 1 || first.Plays[0].FileID != "a" || requests.Load() != 1 {
		t.Fatalf("first page = %+v, requests = %d", first, requests.Load())
	}
	next := requestPlayHistoryPage(svc, account, first.NextPageToken)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("next page was not requested")
	}
	release <- struct{}{}
	second := awaitPlayHistoryPage(t, next)
	if second.Account != account || second.NextPageToken != "" || len(second.Plays) != 1 || second.Plays[0].FileID != "b" || !finished(second.Plays[0].Position, second.Plays[0].Duration) {
		t.Fatalf("next page = %+v", second)
	}
}

func TestPlayHistoryPageKeepsExplicitOwnerAndLatestLocalProgress(t *testing.T) {
	entered, release := make(chan string, 1), make(chan struct{}, 1)
	defer close(release)
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		entered <- r.Header.Get("X-Device-Id")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"events":[`+playEvent("same", "2026-10-21T08:00:00Z", 20, 100, liveRef("same"))+`]}`)
	})
	owner, _ := svc.active()
	if err := owner.playLog().opened(playRecord{FileID: "same", Position: 30, Duration: 100, PlayedAt: "2026-10-22T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	other := seedSession(t, svc, "other@example.com")
	if err := other.playLog().opened(playRecord{FileID: "foreign", PlayedAt: "2026-10-24T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	result := requestPlayHistoryPage(svc, owner.key, "")
	select {
	case account := <-entered:
		if account != owner.key {
			t.Fatalf("cloud request owner = %q, want %q", account, owner.key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("history request did not start")
	}
	if _, err := svc.SwitchAccount(owner.key); err != nil {
		t.Fatal(err)
	}
	if saved, err := owner.playLog().progress("same", 65, 100, time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)); err != nil || !saved {
		t.Fatalf("progress while fetching = %t, %v", saved, err)
	}
	if err := owner.playLog().opened(playRecord{FileID: "during", Position: 10, Duration: 100, PlayedAt: "2026-10-24T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SwitchAccount(other.key); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	page := awaitPlayHistoryPage(t, result)
	if page.Account != owner.key || len(page.Plays) != 2 || page.Plays[0].FileID != "during" || page.Plays[1].FileID != "same" || page.Plays[1].Position != 65 || !page.Plays[1].Local {
		t.Fatalf("history after switches and local progress = %+v", page)
	}
}

func TestPlayHistoryPageHonorsClearDuringCloudRequest(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan time.Time, 1)
	defer close(release)
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		entered <- struct{}{}
		var cutoff time.Time
		select {
		case cutoff = <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"events":[`+
			playEvent("new", cutoff.Add(time.Second).Format(time.RFC3339Nano), 40, 100, liveRef("new"))+","+
			playEvent("edge", cutoff.Format(time.RFC3339Nano), 20, 100, liveRef("edge"))+","+
			playEvent("old", cutoff.Add(-time.Second).Format(time.RFC3339Nano), 30, 100, liveRef("old"))+","+
			playEvent("gone", cutoff.Format(time.RFC3339Nano), 20, 100, "")+`]}`)
	})
	account := svc.Status().AccountID
	log := activePlays(t, svc)
	if err := log.opened(playRecord{FileID: "before", PlayedAt: "2026-10-01T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	result := requestPlayHistoryPage(svc, account, "")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("history request did not start")
	}
	if err := svc.ClearPlays(); err != nil {
		t.Fatal(err)
	}
	_, cutoff := log.snapshot()
	if err := log.opened(playRecord{FileID: "after", Position: 5, Duration: 100, PlayedAt: cutoff.Add(2 * time.Second).Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	release <- cutoff
	page := awaitPlayHistoryPage(t, result)
	if ids := playIDs(page.Plays); len(ids) != 2 || ids[0] != "after" || ids[1] != "new" {
		t.Fatalf("in-flight clear restored old history: %v", ids)
	}
	if len(page.UnavailableIDs) != 0 {
		t.Fatalf("clear did not hide unavailable events: %v", page.UnavailableIDs)
	}
}

func TestPlayHistoryPageReturnsCloudErrorsAndRequiresOwner(t *testing.T) {
	svc := playsService(t, "", http.StatusForbidden)
	account := svc.Status().AccountID
	if err := activePlays(t, svc).opened(playRecord{FileID: "keep", Position: 30, Duration: 100, PlayedAt: "2026-10-22T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlayHistoryPage(account, ""); err == nil {
		t.Fatal("cloud failure was suppressed despite the caller owning local fallback")
	}
	local, err := svc.LocalPlayHistory(account)
	if err != nil || len(local) != 1 || local[0].FileID != "keep" {
		t.Fatalf("local history after cloud failure = %+v, %v", local, err)
	}
	for _, account := range []string{"", "missing"} {
		if _, err := svc.PlayHistoryPage(account, ""); !errors.Is(err, errNoSuchAccount) {
			t.Errorf("invalid owner %q: want no-such-account, got %v", account, err)
		}
	}
}

func TestPlayHistoryPageCarriesUnavailableFilesAcrossPages(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Query().Get("page_token") == "" {
			io.WriteString(w, `{"events":[`+
				playEvent("gone", "2026-10-22T08:00:00Z", 20, 100, "")+","+
				playEvent("local", "2026-10-22T08:00:00Z", 20, 100, `{"id":"local","trashed":true}`)+","+
				playEvent("gone", "2026-10-21T08:00:00Z", 20, 100, liveRef("gone"))+`],"next_page_token":"p2"}`)
			return
		}
		io.WriteString(w, `{"events":[`+playEvent("gone", "2026-10-20T08:00:00Z", 20, 100, liveRef("gone"))+`]}`)
	})
	account := svc.Status().AccountID
	if err := activePlays(t, svc).opened(playRecord{FileID: "local", PlayedAt: "2026-10-23T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	first, err := svc.PlayHistoryPage(account, "")
	if err != nil || len(first.UnavailableIDs) != 2 || first.UnavailableIDs[0] != "gone" || first.UnavailableIDs[1] != "local" || len(first.Plays) != 1 || first.Plays[0].FileID != "local" || first.Plays[0].Available || !first.Plays[0].Local {
		t.Fatalf("unavailable first page = %+v, %v", first, err)
	}
	second, err := svc.PlayHistoryPage(account, first.NextPageToken)
	if err != nil || len(second.UnavailableIDs) != 0 || len(second.Plays) != 2 {
		t.Fatalf("older page = %+v, %v", second, err)
	}
}

func playIDs(plays []RecentPlay) []string {
	ids := make([]string, len(plays))
	for i, p := range plays {
		ids[i] = p.FileID
	}
	return ids
}

// ResumeFile opens the video where it was left, and what PKP plays lands in
// its own record: on open, then where playback stood when it stopped.
func TestResumeFileStartsThereAndIsRecorded(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	media, _ := serveClip(t, clip)
	d, p := playingDrive(t, func() string { return videoJSON(media.URL+"/origin", media.URL+"/t480") })

	if _, err := d.ResumeFile("", "f1", 1.0); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := activePlays(t, d).list(); len(got) != 1 || got[0].FileID != "f1" || got[0].Name != "clip.mp4" || got[0].Position != 1.0 {
		t.Fatalf("record on open = %+v", got)
	}
	waitFor(t, 20*time.Second, "the resumed video to load", func() bool {
		s := p.State()
		return !s.Loading && s.Duration > 1.5
	})
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if pos := p.State().Position; pos < 0.9 {
		t.Errorf("position = %.2f, want playback to have started at 1.0", pos)
	}
	waitFor(t, 5*time.Second, "the player to sample the video", func() bool {
		_, _, _, _, ok := p.lastPlayed()
		return ok
	})
	if err := p.StopPlayback(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := activePlays(t, d).list()
	if len(got) != 1 || got[0].Position < 0.9 || got[0].Duration <= 1.5 {
		t.Errorf("record after stop = %+v, want the position and runtime mpv reported", got)
	}
}
