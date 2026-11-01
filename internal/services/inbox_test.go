package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/drive"
)

// inboxAPI is a stand-in PikPak whose offline task list the test sets.
type inboxAPI struct {
	mu    sync.Mutex
	tasks []drive.Task
	reads int
}

func (a *inboxAPI) set(tasks ...drive.Task) {
	a.mu.Lock()
	a.tasks = tasks
	a.mu.Unlock()
}

func (a *inboxAPI) serve(w http.ResponseWriter, r *http.Request) {
	if authAndTokenRoutes(w, r) {
		return
	}
	if r.URL.Path != "/drive/v1/tasks" {
		http.NotFound(w, r)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reads++
	json.NewEncoder(w).Encode(map[string]any{"tasks": a.tasks, "next_page_token": ""})
}

func doneTask(id string, at time.Time, parent string) drive.Task {
	return drive.Task{
		ID: id, Name: id + ".mkv", Phase: drive.PhaseComplete, FileID: "file-" + id,
		UpdatedTime:       at.Format(time.RFC3339),
		ReferenceResource: &drive.TaskReference{ParentID: parent},
	}
}

func newInboxService(t *testing.T) (*DriveService, *accountSession, *inboxAPI) {
	t.Helper()
	api := &inboxAPI{}
	svc := newDriveService(t, api.serve)
	s, err := svc.active()
	if err != nil {
		t.Fatal(err)
	}
	return svc, s, api
}

func poll(t *testing.T, svc *DriveService, s *accountSession) bool {
	t.Helper()
	client, err := s.api()
	if err != nil {
		t.Fatal(err)
	}
	moving, err := svc.pollInbox(context.Background(), s, client)
	if err != nil {
		t.Fatalf("pollInbox: %v", err)
	}
	return moving
}

func arrivals(t *testing.T, svc *DriveService) []Arrival {
	t.Helper()
	list, err := svc.Arrivals("")
	if err != nil {
		t.Fatalf("Arrivals: %v", err)
	}
	return list
}

func TestInboxTurnsNewlyCompletedTasksIntoArrivals(t *testing.T) {
	svc, s, api := newInboxService(t)
	now := time.Now()

	// First run ever: what finished before is history, not arrivals.
	api.set(doneTask("before", now.Add(-time.Hour), "p"))
	poll(t, svc, s)
	if got := arrivals(t, svc); len(got) != 0 {
		t.Fatalf("first run made arrivals: %+v", got)
	}

	byHash := doneTask("by-hash", now.Add(2*time.Minute), "")
	running := drive.Task{ID: "moving", Phase: drive.PhaseRunning, UpdatedTime: now.Add(time.Hour).Format(time.RFC3339)}
	api.set(
		doneTask("plain", now.Add(time.Minute), "fold"),
		doneTask("by-id", now.Add(3*time.Minute), "fold"),
		byHash,
		doneTask("before", now.Add(-time.Hour), "p"),
		doneTask("old", now.Add(-2*time.Hour), "p"),
		running,
	)
	if !poll(t, svc, s) {
		t.Error("a running task was not reported as moving")
	}
	got := arrivals(t, svc)
	want := []struct{ id, source string }{{"by-id", "cloud"}, {"by-hash", "cloud"}, {"plain", "cloud"}}
	if len(got) != len(want) {
		t.Fatalf("arrivals = %+v, want %v", got, want)
	}
	for i, w := range want {
		a := got[i]
		if a.ID != w.id || a.Source != w.source || a.State != ArrivalNew || a.Account != s.key {
			t.Errorf("arrival %d = %+v, want %s from %s, new, of %s", i, a, w.id, w.source, s.key)
		}
	}
	if a := got[2]; a.FileID != "file-plain" || a.ParentID != "fold" || a.Name != "plain.mkv" {
		t.Errorf("cloud arrival = %+v", a)
	}

	// Read again, and after the user dismissed one: nothing doubles, the
	// state stands even when the task's time moves.
	if err := svc.SetArrival("", "plain", ArrivalDismissed, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetArrival("", "by-id", ArrivalOrganized, "rec-1"); err != nil {
		t.Fatal(err)
	}
	api.set(doneTask("plain", now.Add(10*time.Minute), "fold"), doneTask("by-id", now.Add(3*time.Minute), "fold"), byHash)
	if poll(t, svc, s) {
		t.Error("moving with nothing pending or running")
	}
	got = arrivals(t, svc)
	if len(got) != 3 {
		t.Fatalf("arrivals after another read = %+v", got)
	}
	states := map[string]Arrival{}
	for _, a := range got {
		states[a.ID] = a
	}
	if states["plain"].State != ArrivalDismissed || states["by-id"].State != ArrivalOrganized || states["by-id"].RecordID != "rec-1" {
		t.Errorf("states = %+v", states)
	}

	if err := svc.SetArrival("", "plain", "deleted", ""); err == nil {
		t.Error("an unknown state was accepted")
	}
	if err := svc.SetArrival("", "nope", ArrivalDismissed, ""); !errors.Is(err, errArrivalMissing) {
		t.Errorf("unknown arrival: %v", err)
	}
	if err := svc.SetArrival("", "by-id", ArrivalNew, "rec-1"); err != nil {
		t.Fatal(err)
	}
	for _, a := range arrivals(t, svc) {
		if a.ID == "by-id" && (a.State != ArrivalNew || a.RecordID != "") {
			t.Errorf("back to new = %+v", a)
		}
	}
}

func TestArrivalSuggestionsDoNotResolveOrDiscardUnidentifiedFiles(t *testing.T) {
	svc, s, api := newInboxService(t)
	api.set()
	poll(t, svc, s)
	now := time.Now().Add(time.Minute)
	api.set(doneTask("known", now, ""), doneTask("unknown", now, ""))
	poll(t, svc, s)
	if err := svc.SetArrivalSuggestions(s.key, []string{"known"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range arrivals(t, svc) {
		if a.HasSuggestions != (a.ID == "known") || a.State != ArrivalNew || a.RecordID != "" {
			t.Fatalf("suggestion publication changed a decision: %+v", a)
		}
	}
	if err := svc.SetArrivalSuggestions(s.key, nil); err != nil {
		t.Fatal(err)
	}
	for _, a := range arrivals(t, svc) {
		if a.HasSuggestions || a.State != ArrivalNew || a.RecordID != "" {
			t.Fatalf("no suggestions must stay unprocessed: %+v", a)
		}
	}
	if err := svc.SetArrival(s.key, "known", ArrivalDismissed, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetArrivalSuggestions(s.key, []string{"known", "unknown"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range arrivals(t, svc) {
		if a.ID == "known" && (a.HasSuggestions || a.State != ArrivalDismissed) {
			t.Fatalf("a stale analysis revived a dismissed arrival: %+v", a)
		}
		if a.ID == "unknown" && (!a.HasSuggestions || a.State != ArrivalNew) {
			t.Fatalf("a previously unidentified arrival lost its new suggestions: %+v", a)
		}
	}
	poll(t, svc, s)
	if got := arrivals(t, svc); len(got) != 2 {
		t.Fatalf("suggestion updates changed the watcher watermark: %+v", got)
	}
}

func TestInboxKeepsTheNewestHundred(t *testing.T) {
	svc, s, api := newInboxService(t)
	api.set()
	poll(t, svc, s)

	start := time.Now().Add(time.Minute)
	batch := func(from, n int) {
		tasks := make([]drive.Task, n)
		for i := range tasks {
			tasks[i] = doneTask(fmt.Sprintf("t%03d", from+i), start.Add(time.Duration(from+i)*time.Second), "")
		}
		api.set(tasks...)
	}
	batch(0, 80)
	poll(t, svc, s)
	batch(80, 80)
	poll(t, svc, s)

	got := arrivals(t, svc)
	if len(got) != inboxKeep {
		t.Fatalf("kept %d arrivals, want %d", len(got), inboxKeep)
	}
	if got[0].ID != "t159" || got[len(got)-1].ID != "t060" {
		t.Errorf("kept %s … %s, want the newest first, t159 … t060", got[0].ID, got[len(got)-1].ID)
	}
}

func TestInboxWatermarkIgnoresWhatFinishedBefore(t *testing.T) {
	svc, s, api := newInboxService(t)
	now := time.Now()
	// The server's clock runs ahead: its newest stamp becomes the mark.
	api.set(doneTask("ahead", now.Add(10*time.Minute), ""))
	poll(t, svc, s)
	api.set(doneTask("ahead", now.Add(10*time.Minute), ""), doneTask("between", now.Add(5*time.Minute), ""))
	poll(t, svc, s)
	if got := arrivals(t, svc); len(got) != 0 {
		t.Fatalf("arrivals from before the mark: %+v", got)
	}
	api.set(doneTask("after", now.Add(11*time.Minute), ""))
	poll(t, svc, s)
	if got := arrivals(t, svc); len(got) != 1 || got[0].ID != "after" {
		t.Fatalf("arrivals = %+v, want only the one after the mark", got)
	}
}

func TestInboxPacing(t *testing.T) {
	now := time.Now()
	var p inboxPace
	if p.wait(now) != inboxSlow {
		t.Errorf("idle wait = %v, want %v", p.wait(now), inboxSlow)
	}
	p.moving = true
	if p.wait(now) != inboxFast {
		t.Errorf("moving wait = %v, want %v", p.wait(now), inboxFast)
	}
	p.moving = false
	p.kick(now)
	if p.wait(now.Add(time.Minute)) != inboxFast {
		t.Error("a kick did not make the reads fast")
	}
	if p.wait(now.Add(inboxKickHold+time.Second)) != inboxSlow {
		t.Error("a kick kept the reads fast for good")
	}

	// kickInbox reaches the account's watcher without blocking.
	_, s, _ := newInboxService(t)
	kickInbox(s)
	kickInbox(s)
	select {
	case <-s.index().inboxNow:
	default:
		t.Error("the kick did not reach the watcher")
	}
}

func TestInboxFileGoesWithTheAccount(t *testing.T) {
	svc, s, api := newInboxService(t)
	api.set()
	poll(t, svc, s)
	path := svc.inboxPath(s.key)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("inbox file: %v %v", info, err)
	}
	svc.removeAccountFiles(s.key)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("inbox file after removing the account: %v", err)
	}
}

func suggestionReviews(t *testing.T, svc *DriveService, account string) []SuggestionReview {
	t.Helper()
	list, err := svc.SuggestionReviews(account)
	if err != nil {
		t.Fatalf("SuggestionReviews: %v", err)
	}
	return list
}

func inboxRecord(t *testing.T, svc *DriveService, key string) inboxFile {
	t.Helper()
	svc.inboxMu.Lock()
	defer svc.inboxMu.Unlock()
	f, err := svc.readInbox(key)
	if err != nil {
		t.Fatalf("readInbox: %v", err)
	}
	return f
}

func TestSuggestionReviewsPersistAndUndoOnlyTheirKey(t *testing.T) {
	svc, s, api := newInboxService(t)
	api.set()
	poll(t, svc, s)
	api.set(doneTask("arrival", time.Now().Add(time.Minute), "folder"))
	poll(t, svc, s)
	before := inboxRecord(t, svc, s.key)
	reviews := []SuggestionReview{
		{Key: `{"file":"a","op":"rename","name":"A.mkv"}`, FileID: "a"},
		{Key: `{"file":"a","op":"move","path":["Films"]}`, FileID: "a"},
	}
	if err := svc.SetSuggestionReviews("", reviews, true); err != nil {
		t.Fatal(err)
	}
	after := inboxRecord(t, svc, s.key)
	if after.Watermark != before.Watermark || !slices.Equal(after.Arrivals, before.Arrivals) {
		t.Fatalf("keeping suggestions changed watcher data: before %+v, after %+v", before, after)
	}
	if err := svc.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	restarted := NewDriveService(svc.opts, nil)
	if err := restarted.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.ServiceShutdown() })
	if got := suggestionReviews(t, restarted, s.key); !slices.Equal(got, reviews) {
		t.Fatalf("reviews after restart = %+v, want %+v", got, reviews)
	}
	if err := restarted.SetSuggestionReviews(s.key, reviews[:1], false); err != nil {
		t.Fatal(err)
	}
	if got := suggestionReviews(t, restarted, s.key); !slices.Equal(got, reviews[1:]) {
		t.Fatalf("undo removed another proposal for the same file: %+v", got)
	}
	if err := restarted.SetSuggestionReviews(s.key, reviews, false); err != nil {
		t.Fatal(err)
	}
	if got := suggestionReviews(t, restarted, s.key); got == nil || len(got) != 0 {
		t.Fatalf("undo all = %+v, want an empty list", got)
	}
	after = inboxRecord(t, restarted, s.key)
	if after.Watermark != before.Watermark || !slices.Equal(after.Arrivals, before.Arrivals) {
		t.Fatalf("undo changed watcher data: before %+v, after %+v", before, after)
	}
}

func TestSuggestionReviewsSurviveWatcherAndArrivalUpdates(t *testing.T) {
	svc, s, api := newInboxService(t)
	api.set()
	poll(t, svc, s)
	now := time.Now().Add(time.Minute)
	a, b := doneTask("a", now, ""), doneTask("b", now.Add(time.Minute), "")
	api.set(a, b)
	poll(t, svc, s)
	review := SuggestionReview{Key: "proposal-a", FileID: "file-a"}
	if err := svc.SetSuggestionReviews("", []SuggestionReview{review}, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetArrival("", "a", ArrivalReviewed, ""); err != nil {
		t.Fatal(err)
	}
	poll(t, svc, s)
	api.set(doneTask("c", now.Add(2*time.Minute), ""), a, b)
	poll(t, svc, s)
	if err := svc.SetArrival("", "b", ArrivalDismissed, ""); err != nil {
		t.Fatal(err)
	}
	if got := suggestionReviews(t, svc, ""); !slices.Equal(got, []SuggestionReview{review}) {
		t.Fatalf("watcher/arrival update lost partial keep: %+v", got)
	}
	got := arrivals(t, svc)
	if len(got) != 3 || got[0].ID != "c" || got[0].State != ArrivalNew ||
		got[1].ID != "b" || got[1].State != ArrivalDismissed || got[2].ID != "a" || got[2].State != ArrivalReviewed {
		t.Fatalf("arrival states after polling = %+v", got)
	}
	if err := svc.SetArrival("", "a", ArrivalNew, ""); err != nil {
		t.Fatal(err)
	}
	if got := suggestionReviews(t, svc, ""); !slices.Equal(got, []SuggestionReview{review}) {
		t.Fatalf("reopening an arrival lost review history: %+v", got)
	}
}

func TestSuggestionReviewsStayWithTheirAccount(t *testing.T) {
	svc, first, _ := newInboxService(t)
	review := SuggestionReview{Key: "same-proposal", FileID: "same-file"}
	if err := svc.SetSuggestionReviews("", []SuggestionReview{review}, true); err != nil {
		t.Fatal(err)
	}
	second := seedSession(t, svc, "second@example.com")
	if got := suggestionReviews(t, svc, ""); len(got) != 0 {
		t.Fatalf("another account inherited reviews: %+v", got)
	}
	other := SuggestionReview{Key: review.Key, FileID: "other-file"}
	if err := svc.SetSuggestionReviews(second.key, []SuggestionReview{other}, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSuggestionReviews(first.key, []SuggestionReview{review}, false); err != nil {
		t.Fatal(err)
	}
	if got := suggestionReviews(t, svc, first.key); len(got) != 0 {
		t.Fatalf("first account undo = %+v", got)
	}
	if got := suggestionReviews(t, svc, second.key); !slices.Equal(got, []SuggestionReview{other}) {
		t.Fatalf("first account undo changed second account: %+v", got)
	}
}

func TestSuggestionReviewsKeepNewestAndReplaceByKey(t *testing.T) {
	svc, _, _ := newInboxService(t)
	for from := 0; from < suggestionReviewKeep; from += maxPlan {
		batch := make([]SuggestionReview, maxPlan)
		for i := range batch {
			batch[i] = SuggestionReview{Key: fmt.Sprintf("proposal-%05d", from+i), FileID: "file"}
		}
		if err := svc.SetSuggestionReviews("", batch, true); err != nil {
			t.Fatal(err)
		}
	}
	updated := SuggestionReview{Key: "proposal-00000", FileID: "replacement"}
	latest := SuggestionReview{Key: "newest", FileID: "last-duplicate"}
	if err := svc.SetSuggestionReviews("", []SuggestionReview{
		updated, {Key: latest.Key, FileID: "first-duplicate"}, latest,
	}, true); err != nil {
		t.Fatal(err)
	}
	got := suggestionReviews(t, svc, "")
	if len(got) != suggestionReviewKeep || got[0].Key != "proposal-00002" ||
		got[len(got)-2] != updated || got[len(got)-1] != latest {
		t.Fatalf("retention/replacement: count %d, first %+v, last %+v", len(got), got[0], got[len(got)-2:])
	}
}

func TestSuggestionReviewsValidateBeforeChangingRecords(t *testing.T) {
	svc, _, _ := newInboxService(t)
	kept := SuggestionReview{Key: "kept", FileID: "file"}
	if err := svc.SetSuggestionReviews("", []SuggestionReview{kept}, true); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []SuggestionReview{
		{FileID: "file"}, {Key: " \n", FileID: "file"}, {Key: "key"}, {Key: "key", FileID: " \t"},
		{Key: strings.Repeat("k", maxReviewKeyBytes+1), FileID: "file"},
		{Key: "key", FileID: strings.Repeat("f", maxReviewFileIDBytes+1)},
		{Key: string([]byte{0xff}), FileID: "file"}, {Key: "key", FileID: string([]byte{0xff})},
	} {
		for _, keep := range []bool{true, false} {
			if err := svc.SetSuggestionReviews("", []SuggestionReview{kept, bad}, keep); !errors.Is(err, errSuggestionReview) {
				t.Errorf("invalid review (key %d bytes, file %d bytes), keep %v: %v", len(bad.Key), len(bad.FileID), keep, err)
			}
		}
	}
	if err := svc.SetSuggestionReviews("", make([]SuggestionReview, maxPlan+1), true); !errors.Is(err, errPlanTooLarge) {
		t.Errorf("oversized request: %v", err)
	}
	if got := suggestionReviews(t, svc, ""); !slices.Equal(got, []SuggestionReview{kept}) {
		t.Fatalf("invalid request changed existing keeps: %+v", got)
	}
	boundary := SuggestionReview{Key: strings.Repeat("k", maxReviewKeyBytes), FileID: strings.Repeat("f", maxReviewFileIDBytes)}
	if err := svc.SetSuggestionReviews("", []SuggestionReview{boundary}, true); err != nil {
		t.Errorf("exact size bounds rejected: %v", err)
	}
	if err := svc.SetSuggestionReviews("missing-account", []SuggestionReview{kept}, true); !errors.Is(err, errNoSuchAccount) {
		t.Errorf("unknown account write: %v", err)
	}
	if _, err := svc.SuggestionReviews("missing-account"); !errors.Is(err, errNoSuchAccount) {
		t.Errorf("unknown account read: %v", err)
	}
	if err := svc.SetSuggestionReviews("", nil, true); err != nil {
		t.Errorf("empty request: %v", err)
	}
}
