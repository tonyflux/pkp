package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/upload"
)

// fakeCenter stands in for the system's notification center.
type fakeCenter struct {
	startErr error
	mu       sync.Mutex
	sent     []notifications.NotificationOptions
	asked    int
}

func (c *fakeCenter) start(func(map[string]any)) error { return c.startErr }
func (c *fakeCenter) allowed() (bool, error)           { return true, nil }
func (c *fakeCenter) ask() (bool, error) {
	c.mu.Lock()
	c.asked++
	c.mu.Unlock()
	return true, nil
}
func (c *fakeCenter) send(o notifications.NotificationOptions) error {
	c.mu.Lock()
	c.sent = append(c.sent, o)
	c.mu.Unlock()
	return nil
}

func (c *fakeCenter) posted() []notifications.NotificationOptions {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]notifications.NotificationOptions(nil), c.sent...)
}

func (c *fakeCenter) askedN() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asked
}

// waitPosted waits until want notices were posted, then a little longer
// to catch any extra one.
func (c *fakeCenter) waitPosted(t *testing.T, want int) []notifications.NotificationOptions {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(c.posted()) < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(4 * noticeSettle)
	got := c.posted()
	if len(got) != want {
		t.Fatalf("posted %d notices, want %d: %+v", len(got), want, got)
	}
	return got
}

// newNotifyTest attaches system notifications to svc with a fake center, started, and
// PKP away from the front unless the test says otherwise.
func newNotifyTest(t *testing.T, svc *DriveService, lock *AppLockService) (*NotifyService, *fakeCenter) {
	t.Helper()
	settle := noticeSettle
	noticeSettle = 20 * time.Millisecond
	t.Cleanup(func() { noticeSettle = settle })
	fc := &fakeCenter{}
	newCenter := newNotifyCenter
	newNotifyCenter = func() notifyCenter { return fc }
	t.Cleanup(func() { newNotifyCenter = newCenter })

	n := NewNotifyService(svc, nil, lock)
	if err := n.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	n.away.Store(true)
	// system notifications starts on: startup asks the system once, in the background.
	for deadline := time.Now().Add(2 * time.Second); fc.askedN() == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if fc.askedN() != 1 {
		t.Fatalf("startup asked %d times", fc.askedN())
	}
	return n, fc
}

func downloadMoves(svc *DriveService, key, id string, states ...string) {
	for _, st := range states {
		svc.announce(notice{Kind: noticeDownload, Account: key, ID: id, Name: id + ".mkv", State: st, Where: "~/Downloads"})
	}
}

func TestNotifyFinishedTransfersWaitForTheQueueFailuresDoNot(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) { authAndTokenRoutes(w, r) })
	n, fc := newNotifyTest(t, svc, nil)
	var busy atomic.Bool
	busy.Store(true)
	n.busy = func(string) bool { return busy.Load() }
	key := svc.Accounts()[0].ID

	// Restored from the journal as done, never seen moving: not news.
	downloadMoves(svc, key, "old", download.StateDone)
	// Two finish while a third still downloads; one fails.
	downloadMoves(svc, key, "a", download.StateQueued, download.StateDownloading, download.StateDone)
	downloadMoves(svc, key, "b", download.StateDownloading, download.StateDone)
	downloadMoves(svc, key, "bad", download.StateDownloading, download.StateFailed)
	got := fc.waitPosted(t, 1)
	if got[0].Data["key"] != "download:"+key+":bad" || got[0].Data["target"] != "transfers" {
		t.Errorf("failure notice opens %v, want the failed download", got[0].Data)
	}

	// The last one moving is cancelled: the queue is idle, the two
	// finished files are said once, together.
	busy.Store(false)
	downloadMoves(svc, key, "c", download.StateDownloading, download.StateCancelled)
	got = fc.waitPosted(t, 2)
	done := got[1]
	if !strings.Contains(done.Body, "a.mkv") || !strings.Contains(done.Body, "2") || done.Data["key"] != "" {
		t.Errorf("finished notice = %+v; want a.mkv and the count of 2, opening the list", done)
	}

	// Uploads keep their own ledger: a cancelled upload says nothing.
	svc.announce(notice{Kind: noticeUpload, Account: key, ID: "u", Name: "u.mp4", State: upload.StateUploading})
	svc.announce(notice{Kind: noticeUpload, Account: key, ID: "u", Name: "u.mp4", State: upload.StateCancelled})
	fc.waitPosted(t, 2)
}

func TestNotifyStaysQuietInFrontUnlessAsked(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) { authAndTokenRoutes(w, r) })
	n, fc := newNotifyTest(t, svc, nil)
	key := svc.Accounts()[0].ID
	run := notice{Kind: noticeOrganize, Account: key, Done: 3}

	n.away.Store(false)
	svc.announce(run)
	fc.waitPosted(t, 0)

	if _, err := n.SetForeground(true); err != nil {
		t.Fatal(err)
	}
	svc.announce(run)
	fc.waitPosted(t, 1)

	if _, err := n.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	n.away.Store(true)
	svc.announce(run)
	downloadMoves(svc, key, "x", download.StateDownloading, download.StateDone)
	fc.waitPosted(t, 1)
	if st := n.State(); st.Enabled || !st.Foreground || !st.Available {
		t.Errorf("state = %+v", st)
	}

	// Back on: the system is asked (macOS asks the user only once).
	if _, err := n.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if fc.askedN() != 2 {
		t.Errorf("switching on asked %d times in all, want 2", fc.askedN())
	}
	// A run that touched nothing is not news.
	svc.announce(notice{Kind: noticeRemove, Account: key, Verb: "还原"})
	fc.waitPosted(t, 1)
}

func TestNotifyNamesNoFileOrAccountWhileLocked(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) { authAndTokenRoutes(w, r) })
	seedSession(t, svc, "second@example.com")
	if _, err := svc.RenameAccount(svc.Accounts()[0].ID, "工作号"); err != nil {
		t.Fatal(err)
	}
	lock := &AppLockService{}
	_, fc := newNotifyTest(t, svc, lock)
	key := svc.Accounts()[0].ID

	downloadMoves(svc, key, "秘密影片", download.StateDownloading, download.StateDone)
	open := fc.waitPosted(t, 1)[0]
	if open.Subtitle != "工作号" || !strings.Contains(open.Body, "秘密影片") {
		t.Fatalf("unlocked notice = %+v; want the account and the file named", open)
	}

	lock.mu.Lock()
	lock.locked = true
	lock.mu.Unlock()
	downloadMoves(svc, key, "秘密影片2", download.StateDownloading, download.StateFailed)
	svc.announce(notice{Kind: noticeSession, Account: key, Name: "工作号", Failed: true})
	for _, o := range fc.waitPosted(t, 3)[1:] {
		text := o.Title + o.Subtitle + o.Body
		if strings.Contains(text, "秘密影片") || strings.Contains(text, "工作号") || o.Data["key"] != "" {
			t.Errorf("locked notice gives away %+v", o)
		}
	}
}

func TestNotifyCloudAddsThatFinishOrFail(t *testing.T) {
	api := &inboxAPI{}
	tasks := map[string]drive.Task{
		"fails": {ID: "fails", Name: "坏种子", Phase: drive.PhaseError, Message: "种子没有可用资源"},
	}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if id, ok := strings.CutPrefix(r.URL.Path, "/drive/v1/tasks/"); ok {
			if task, found := tasks[id]; found {
				json.NewEncoder(w).Encode(task)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": "task_not_found", "error_code": 9})
			return
		}
		api.serve(w, r)
	})
	s, err := svc.active()
	if err != nil {
		t.Fatal(err)
	}
	_, fc := newNotifyTest(t, svc, nil)
	now := time.Now()
	running := func(id string) drive.Task {
		return drive.Task{ID: id, Name: id, Phase: drive.PhaseRunning, UpdatedTime: now.Format(time.RFC3339)}
	}

	api.set(running("fails"), running("deleted"), running("lands"))
	poll(t, svc, s)
	fc.waitPosted(t, 0)

	// "fails" failed and left the list; "deleted" is gone for good; "lands"
	// completed.
	api.set(doneTask("lands", now.Add(time.Minute), "p"))
	poll(t, svc, s)
	got := fc.waitPosted(t, 2)
	byKey := map[any]notifications.NotificationOptions{}
	for _, o := range got {
		byKey[o.Data["key"]] = o
	}
	failed, ok := byKey["cloud:"+s.key+":fails"]
	if !ok || !strings.Contains(failed.Body, "坏种子") {
		t.Errorf("no failure notice for the failed task: %+v", got)
	}
	if _, ok := byKey["cloud:"+s.key+":lands"]; !ok {
		t.Errorf("no notice for the finished task: %+v", got)
	}
	if failed.Title == byKey["cloud:"+s.key+":lands"].Title {
		t.Errorf("failure and success read alike: %q", failed.Title)
	}
}

func TestNotifyUnavailableNeverStopsPKP(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) { authAndTokenRoutes(w, r) })
	fc := &fakeCenter{startErr: errors.New("notifications require a valid bundle identifier")}
	newCenter := newNotifyCenter
	newNotifyCenter = func() notifyCenter { return fc }
	t.Cleanup(func() { newNotifyCenter = newCenter })

	n := NewNotifyService(svc, nil, nil)
	if err := n.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("an unavailable center failed startup: %v", err)
	}
	n.away.Store(true)
	st := n.State()
	if st.Available || st.Unavailable == "" {
		t.Errorf("state = %+v; want unavailable with a reason", st)
	}
	if err := n.SendTest(); err == nil {
		t.Error("SendTest succeeded without a center")
	}
	svc.announce(notice{Kind: noticeOrganize, Account: "k", Done: 1})
	time.Sleep(50 * time.Millisecond)
	if len(fc.posted()) != 0 || fc.askedN() != 0 {
		t.Errorf("used a center that never started: %d sent, %d asked", len(fc.posted()), fc.askedN())
	}
}
