package services

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// Organize on Import: every signed-in account's cloud-add
// tasks are watched here, in Go, so a task that finishes while the window
// is closed is still noticed. A finished task becomes an arrival; the
// webview checks it for suggestions before any surface offers to organize
// it. Nothing is organized from here; the user confirms the plan.

// EventInboxChanged tells the webview and the menu bar that an account's
// arrivals changed: new ones came in, or one was dismissed or handled.
const EventInboxChanged = "inbox:changed"

// EventArrivalSuggestionsChanged refreshes the menu bar without re-running
// the webview's analysis of unchanged arrivals.
const EventArrivalSuggestionsChanged = "inbox:suggestions"

// InboxChanged is the payload of EventInboxChanged.
type InboxChanged struct {
	Account string `json:"account"`
}

// Arrival states.
const (
	ArrivalNew       = "new"
	ArrivalDismissed = "dismissed"
	ArrivalOrganized = "organized"
	// ArrivalReviewed has explicitly kept suggestions, not a blanket dismissal.
	ArrivalReviewed = "reviewed"
)

const (
	inboxVersion = 1
	// inboxKeep bounds the record; older arrivals were seen or ignored long
	// ago and the task list still has them.
	inboxKeep = 100
	// inboxPage is the one page read each time: tasks come newest first,
	// and a hundred finishing between two reads is not a real account.
	inboxPage = 100
	// Reviews are oldest first; re-keeping a key makes it newest.
	suggestionReviewKeep = 10000
	maxReviewKeyBytes    = 16 << 10
	maxReviewFileIDBytes = 1024
	// The list is read every inboxFast while a task is pending or running
	// (or for inboxKickHold after an add, before the new task may show as
	// moving), else every inboxSlow: an idle account costs one short
	// request every five minutes.
	inboxFast     = time.Minute
	inboxSlow     = 5 * time.Minute
	inboxKickHold = 5 * time.Minute
)

var (
	errArrivalState     = i18n.New("入库记录的状态不对")
	errArrivalMissing   = i18n.New("这条入库记录已经不在了")
	errSuggestionReview = i18n.New("整理建议的保留记录无效")
)

// Arrival is one finished cloud-add task waiting to be organized, or that
// was.
type Arrival struct {
	ID          string `json:"id"`
	Account     string `json:"account"`
	Name        string `json:"name"`
	FileID      string `json:"fileId"`
	ParentID    string `json:"parentId"`
	SavedTo     string `json:"savedTo"`
	Source      string `json:"source"`
	CompletedAt string `json:"completedAt"`
	State       string `json:"state"`
	RecordID    string `json:"recordId"`
	// HasSuggestions is derived by the webview, separate from the user's decision.
	HasSuggestions bool `json:"hasSuggestions"`
}

// SuggestionReview records an explicit keep-as-is decision for one file proposal.
// Key is opaque: the frontend owns its canonical input/proposal snapshot.
type SuggestionReview struct {
	Key    string `json:"key"`
	FileID string `json:"fileId"`
}

// inboxFile is inbox-<key>.json. Watermark is the newest completion time
// already considered; "" means the account was never watched, and the
// first read only sets it, so years of finished tasks never flood in.
type inboxFile struct {
	Version   int                `json:"version"`
	Watermark string             `json:"watermark"`
	Arrivals  []Arrival          `json:"arrivals"`
	Reviews   []SuggestionReview `json:"reviews"`
}

// Arrivals is an account's arrivals ("" is the account on screen), newest
// first, whatever their state.
func (d *DriveService) Arrivals(account string) ([]Arrival, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	d.inboxMu.Lock()
	f, err := d.readInbox(s.key)
	d.inboxMu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.Arrivals, nil
}

// SuggestionReviews is an account's explicit keep-as-is decisions, oldest first.
func (d *DriveService) SuggestionReviews(account string) ([]SuggestionReview, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	d.inboxMu.Lock()
	f, err := d.readInbox(s.key)
	d.inboxMu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.Reviews, nil
}

// SetSuggestionReviews keeps the supplied proposals, or undoes keeps by key.
// Arrivals and the watcher watermark are preserved under the same inbox lock.
func (d *DriveService) SetSuggestionReviews(account string, reviews []SuggestionReview, keep bool) error {
	if len(reviews) > maxPlan {
		return errPlanTooLarge
	}
	last := make(map[string]int, len(reviews))
	for i, review := range reviews {
		if len(review.Key) > maxReviewKeyBytes || strings.TrimSpace(review.Key) == "" || !utf8.ValidString(review.Key) ||
			len(review.FileID) > maxReviewFileIDBytes || strings.TrimSpace(review.FileID) == "" || !utf8.ValidString(review.FileID) {
			return errSuggestionReview
		}
		last[review.Key] = i
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return err
	}
	if len(reviews) == 0 {
		return nil
	}
	d.inboxMu.Lock()
	f, err := d.readInbox(s.key)
	if err != nil {
		d.inboxMu.Unlock()
		return err
	}
	f.Reviews = slices.DeleteFunc(f.Reviews, func(review SuggestionReview) bool {
		_, found := last[review.Key]
		return found
	})
	if keep {
		for i, review := range reviews {
			if last[review.Key] == i {
				f.Reviews = append(f.Reviews, review)
			}
		}
	}
	if len(f.Reviews) > suggestionReviewKeep {
		f.Reviews = f.Reviews[len(f.Reviews)-suggestionReviewKeep:]
	}
	err = d.writeInbox(s.key, f)
	d.inboxMu.Unlock()
	if err != nil {
		return err
	}
	emitInbox(s.key)
	return nil
}

// SetArrivalSuggestions publishes the arrivals with actionable plans. It never
// changes their processing state or the watcher's watermark.
func (d *DriveService) SetArrivalSuggestions(account string, ids []string) error {
	if len(ids) > inboxKeep {
		return errPlanTooLarge
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return err
	}
	suggested := make(map[string]bool, len(ids))
	for _, id := range ids {
		suggested[id] = true
	}
	d.inboxMu.Lock()
	f, err := d.readInbox(s.key)
	if err != nil {
		d.inboxMu.Unlock()
		return err
	}
	changed := false
	for i := range f.Arrivals {
		a := &f.Arrivals[i]
		want := a.State == ArrivalNew && suggested[a.ID]
		if a.HasSuggestions != want {
			a.HasSuggestions = want
			changed = true
		}
	}
	if changed {
		err = d.writeInbox(s.key, f)
	}
	d.inboxMu.Unlock()
	if err != nil {
		return err
	}
	if changed {
		if app := application.Get(); app != nil {
			app.Event.Emit(EventArrivalSuggestionsChanged, InboxChanged{Account: s.key})
		}
	}
	return nil
}

// SetArrival records what became of an arrival: dismissed (Do Not Organize),
// organized (by recordID), explicitly reviewed, or new again.
func (d *DriveService) SetArrival(account, id, state, recordID string) error {
	switch state {
	case ArrivalNew, ArrivalDismissed, ArrivalOrganized, ArrivalReviewed:
	default:
		return errArrivalState
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return err
	}
	d.inboxMu.Lock()
	f, err := d.readInbox(s.key)
	if err != nil {
		d.inboxMu.Unlock()
		return err
	}
	i := slices.IndexFunc(f.Arrivals, func(a Arrival) bool { return a.ID == id })
	if i < 0 {
		d.inboxMu.Unlock()
		return errArrivalMissing
	}
	f.Arrivals[i].State = state
	f.Arrivals[i].RecordID = strings.TrimSpace(recordID)
	f.Arrivals[i].HasSuggestions = false
	if state == ArrivalNew {
		f.Arrivals[i].RecordID = ""
	}
	err = d.writeInbox(s.key, f)
	d.inboxMu.Unlock()
	if err != nil {
		return err
	}
	emitInbox(s.key)
	return nil
}

/* ---------------- the watcher ---------------- */

// kickInbox reads the account's tasks now and keeps reading them often for
// a while: a task was just added, and it may finish within the minute.
func kickInbox(s *accountSession) {
	if idx := s.index(); idx != nil {
		select {
		case idx.inboxNow <- struct{}{}:
		default:
		}
	}
}

// inboxPace decides how long the watcher waits before the next read.
type inboxPace struct {
	// moving: the last read saw a pending or running task.
	moving bool
	// fastUntil is when the last kick stops counting.
	fastUntil time.Time
}

func (p *inboxPace) kick(now time.Time) { p.fastUntil = now.Add(inboxKickHold) }

func (p inboxPace) wait(now time.Time) time.Duration {
	if p.moving || now.Before(p.fastUntil) {
		return inboxFast
	}
	return inboxSlow
}

// inboxLoop watches an account's tasks for as long as its index session
// lives, which is as long as the account has a client. Like syncLoop it
// calls the client directly: classify would expire the session and wait
// for this very loop to end.
func (d *DriveService) inboxLoop(s *accountSession, idx *indexSession, client *drive.Client) {
	defer idx.wg.Done()
	var pace inboxPace
	for {
		moving, err := d.pollInbox(idx.ctx, s, client)
		switch {
		case idx.ctx.Err() != nil:
			return
		case sessionEnded(err):
			// expire closes this index session, which waits for this loop.
			go d.expire(s, client)
			return
		case err != nil:
			// The next read says more; the pace stays as it was.
			d.log.Debug("inbox: tasks unread", "account", s.key, "error", err)
		default:
			pace.moving = moving
		}
		timer := time.NewTimer(pace.wait(time.Now()))
		select {
		case <-idx.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-idx.inboxNow:
			timer.Stop()
			pace.kick(time.Now())
		}
	}
}

// pollInbox reads one page of the account's offline tasks and turns those
// that finished after the watermark into arrivals. moving reports a task
// still pending or running. New arrivals ask for an index pass, so their
// files can be planned, and are announced on EventInboxChanged.
func (d *DriveService) pollInbox(ctx context.Context, s *accountSession, client *drive.Client) (moving bool, err error) {
	rctx, cancel := context.WithTimeout(ctx, driveTimeout)
	list, err := client.ListTasks(rctx, drive.ListTasksParams{
		Type:    drive.TaskTypeOffline,
		Limit:   inboxPage,
		Filters: drive.NewFilters().Set("phase", "in", drive.PhasePending+","+drive.PhaseRunning+","+drive.PhaseComplete),
	})
	cancel()
	if err != nil {
		return false, err
	}
	var done []drive.Task
	for _, t := range list.Tasks {
		switch t.Phase {
		case drive.PhasePending, drive.PhaseRunning:
			moving = true
		case drive.PhaseComplete:
			done = append(done, t)
		}
	}
	d.noticeCloudFailures(ctx, s.key, client, list.Tasks)

	var store *index.Store
	if idx := s.index(); idx != nil && idx.syncer.Ready() {
		store = idx.store
	}

	d.inboxMu.Lock()
	f, err := d.readInbox(s.key)
	if err != nil {
		d.inboxMu.Unlock()
		return moving, err
	}
	mark, markErr := time.Parse(time.RFC3339Nano, f.Watermark)
	first := f.Watermark == "" || markErr != nil
	high := mark
	if first {
		// The server's clock may run ahead of ours: a task it stamped
		// later than our now finished before this first read all the same.
		high = time.Now()
	}
	seen := make(map[string]bool, len(f.Arrivals))
	for _, a := range f.Arrivals {
		seen[a.ID] = true
	}
	var fresh []Arrival
	for _, t := range done {
		at, err := time.Parse(time.RFC3339Nano, t.UpdatedTime)
		if err != nil {
			continue
		}
		if at.After(high) {
			high = at
		}
		if first || !at.After(mark) || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		fresh = append(fresh, arrivalOf(s.key, t, store))
	}
	if !first && len(fresh) == 0 && high.Equal(mark) {
		d.inboxMu.Unlock()
		return moving, nil
	}
	f.Watermark = high.UTC().Format(time.RFC3339Nano)
	f.Arrivals = append(fresh, f.Arrivals...)
	slices.SortStableFunc(f.Arrivals, func(a, b Arrival) int {
		switch {
		case newer(a.CompletedAt, b.CompletedAt):
			return -1
		case newer(b.CompletedAt, a.CompletedAt):
			return 1
		}
		return 0
	})
	if len(f.Arrivals) > inboxKeep {
		f.Arrivals = f.Arrivals[:inboxKeep]
	}
	err = d.writeInbox(s.key, f)
	d.inboxMu.Unlock()
	if err != nil {
		return moving, err
	}
	if len(fresh) > 0 {
		requestSync(s)
		emitInbox(s.key)
		for _, a := range fresh {
			d.announce(notice{Kind: noticeCloud, Account: s.key, ID: a.ID, Name: a.Name, Where: a.SavedTo})
		}
	}
	return moving, nil
}

// noticeCloudFailures tells System Notifications about the account's cloud-add tasks
// that were pending or running at the last read and have failed since.
// The list holds only pending, running and complete tasks, so one that
// left it without completing is read on its own: it failed, or was
// deleted, or merely fell off the page.
func (d *DriveService) noticeCloudFailures(ctx context.Context, key string, client *drive.Client, tasks []drive.Task) {
	now := map[string]string{}
	listed := map[string]bool{}
	for _, t := range tasks {
		listed[t.ID] = true
		if t.Phase == drive.PhasePending || t.Phase == drive.PhaseRunning {
			now[t.ID] = t.Name
		}
	}
	d.inboxMu.Lock()
	before := d.cloudMoving[key]
	d.inboxMu.Unlock()
	for id, name := range before {
		if listed[id] {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, driveTimeout)
		t, err := client.GetTask(rctx, id)
		cancel()
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			// Deleted, or unreadable now: nothing to say.
		case t.Phase == drive.PhaseError:
			d.announce(notice{Kind: noticeCloud, Account: key, ID: id, Name: cmp.Or(t.Name, name), Failed: true, Error: t.Message})
		case !t.Done():
			now[id] = cmp.Or(t.Name, name)
		}
	}
	d.inboxMu.Lock()
	if d.cloudMoving == nil {
		d.cloudMoving = map[string]map[string]string{}
	}
	d.cloudMoving[key] = now
	d.inboxMu.Unlock()
}

// arrivalOf is a finished task as an arrival.
func arrivalOf(key string, t drive.Task, store *index.Store) Arrival {
	a := Arrival{
		ID:          t.ID,
		Account:     key,
		Name:        t.Name,
		FileID:      t.FileID,
		Source:      "cloud",
		CompletedAt: t.UpdatedTime,
		State:       ArrivalNew,
	}
	if a.Name == "" {
		a.Name = t.FileName
	}
	if t.ReferenceResource != nil {
		a.ParentID = t.ReferenceResource.ParentID
	}
	if store != nil {
		a.SavedTo = savedTo(store, a.ParentID)
	}
	return a
}

func emitInbox(key string) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventInboxChanged, InboxChanged{Account: key})
	}
}

/* ---------------- the record file ---------------- */

func (d *DriveService) inboxPath(key string) string {
	return d.accountFile(key, "inbox-", ".json")
}

// readInbox reads an account's record; inboxMu must be held. A missing file
// is an account never watched, an unreadable one is moved aside and the
// watching starts over from now.
func (d *DriveService) readInbox(key string) (inboxFile, error) {
	empty := inboxFile{Version: inboxVersion, Arrivals: []Arrival{}, Reviews: []SuggestionReview{}}
	if d.configErr != nil {
		return empty, d.configErr
	}
	path := d.inboxPath(key)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	f := empty
	if err := json.Unmarshal(data, &f); err != nil {
		d.log.Warn("inbox: record unreadable, starting over", "path", path, "error", err)
		if err := os.Rename(path, path+".bad"); err != nil {
			d.log.Warn("inbox: could not move the unreadable record aside", "error", err)
		}
		return empty, nil
	}
	if f.Arrivals == nil {
		f.Arrivals = []Arrival{}
	}
	if f.Reviews == nil {
		f.Reviews = []SuggestionReview{}
	}
	for i := range f.Arrivals {
		f.Arrivals[i].Account = key
	}
	return f, nil
}

// writeInbox replaces an account's record atomically; inboxMu must be held.
func (d *DriveService) writeInbox(key string, f inboxFile) error {
	f.Version = inboxVersion
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.configDir, 0o700); err != nil {
		return err
	}
	path := d.inboxPath(key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
