package services

import (
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/upload"
)

func up(account, id, state, updated string) UploadTask {
	return UploadTask{Task: upload.Task{ID: id, Name: id, State: state, UpdatedAt: updated}, Account: account}
}

func down(account, id, state, updated string) DownloadTask {
	return DownloadTask{Task: download.Task{ID: id, Name: id, State: state, UpdatedAt: updated}, Account: account}
}

func itemID(it MenuBarItem) string {
	switch it.Kind {
	case "upload":
		return it.Upload.ID
	case "download":
		return it.Download.ID
	case "cloud":
		return it.Task.ID
	}
	return ""
}

func TestMenuBarSummaryPicksWhatNeedsTheUser(t *testing.T) {
	accounts := []Account{{ID: "a"}, {ID: "b", Current: true}, {ID: "c", NeedsPassword: true}}
	const t1, t2, t3 = "2026-10-16T10:00:00+08:00", "2026-10-16T11:00:00+08:00", "2026-10-16T03:30:00Z"

	cases := []struct {
		name      string
		uploads   []UploadTask
		downloads []DownloadTask
		cloud     cloudRead
		item      string
		blocked   bool
		running   int
		attention int
		state     string
	}{
		{
			name:      "the account on screen's moving transfer before another account's",
			uploads:   []UploadTask{up("a", "u-a", upload.StateUploading, t1), up("b", "u-b", upload.StateUploading, t1)},
			downloads: []DownloadTask{down("a", "d-a", download.StateDownloading, t1)},
			item:      "u-b", running: 3, state: "",
		},
		{
			name:      "moving beats a failure, the failure is still counted",
			uploads:   []UploadTask{up("b", "failed", upload.StateFailed, t2)},
			downloads: []DownloadTask{down("a", "moving", download.StateDownloading, t1)},
			item:      "moving", running: 1, attention: 1,
		},
		{
			name: "the newest failure or interruption, across accounts and kinds",
			uploads: []UploadTask{
				up("b", "old", upload.StateFailed, t1),
				up("a", "newest", upload.StateInterrupted, t2),
			},
			// 03:30Z is 11:30+08:00: later than t2 although the text sorts first.
			downloads: []DownloadTask{down("a", "utc", download.StateFailed, t3)},
			item:      "utc", attention: 3,
		},
		{
			name:    "an account that needs signing in marks its transfer blocked",
			uploads: []UploadTask{up("c", "parked", upload.StateInterrupted, t1)},
			item:    "parked", blocked: true, attention: 1,
		},
		{
			name:      "cancelled and done need nothing",
			uploads:   []UploadTask{up("b", "x", upload.StateCancelled, t1), up("b", "y", upload.StateDone, t2)},
			downloads: []DownloadTask{down("b", "z", download.StateCancelled, t1)},
			item:      "",
		},
		{
			name:    "a moving cloud task before local ones that only wait",
			uploads: []UploadTask{up("b", "queued", upload.StateQueued, t1)},
			cloud: cloudRead{read: true, tasks: []TaskSummary{
				{ID: "pending", Phase: drive.PhasePending, Account: "a"},
				{ID: "cloud", Phase: drive.PhaseRunning, Account: "a"},
			}},
			item: "cloud", running: 3, state: "ok",
		},
		{
			name:      "waiting before paused; paused still counts as under way",
			downloads: []DownloadTask{down("b", "paused", download.StatePaused, t1), down("a", "queued", download.StateQueued, t1)},
			item:      "queued", running: 2,
		},
		{
			name:  "a cloud read that failed for an account is unknown, not zero",
			cloud: cloudRead{read: true, failed: true},
			item:  "", state: "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := summarize(tc.uploads, tc.downloads, accounts, tc.cloud, nil)
			if got := itemID(s.Item); got != tc.item {
				t.Errorf("item = %q, want %q", got, tc.item)
			}
			if s.Item.Blocked != tc.blocked {
				t.Errorf("blocked = %v, want %v", s.Item.Blocked, tc.blocked)
			}
			if s.Running != tc.running || s.Attention != tc.attention {
				t.Errorf("running, attention = %d, %d; want %d, %d", s.Running, s.Attention, tc.running, tc.attention)
			}
			if s.Inbox != 0 || s.InboxItem != nil {
				t.Errorf("inbox = %d, %+v without arrivals", s.Inbox, s.InboxItem)
			}
			if s.Cloud != tc.state {
				t.Errorf("cloud = %q, want %q", s.Cloud, tc.state)
			}
			if s.Account != "b" {
				t.Errorf("account = %q, want the current one", s.Account)
			}
		})
	}
}

func TestMenuBarSummaryCountsArrivalsBelowLocalAttention(t *testing.T) {
	accounts := []Account{{ID: "a"}, {ID: "b", Current: true}}
	arrival := func(account, id, state, at string) Arrival {
		return Arrival{ID: id, Account: account, State: state, CompletedAt: at, Source: "cloud", HasSuggestions: true}
	}
	arrivals := []Arrival{
		arrival("a", "a-newest", ArrivalNew, "2026-10-16T12:00:00+08:00"),
		arrival("b", "b-old", ArrivalNew, "2026-10-16T09:00:00+08:00"),
		// 02:30Z is 10:30+08:00: the on-screen account's newest.
		arrival("b", "b-new", ArrivalNew, "2026-10-16T02:30:00Z"),
		arrival("b", "b-done", ArrivalOrganized, "2026-10-16T13:00:00+08:00"),
		arrival("b", "b-dismissed", ArrivalDismissed, "2026-10-16T13:00:00+08:00"),
		arrival("gone", "gone", ArrivalNew, "2026-10-16T13:00:00+08:00"),
		{ID: "no-suggestions", Account: "b", State: ArrivalNew, CompletedAt: "2026-10-16T14:00:00+08:00"},
	}

	s := summarize(nil, nil, accounts, cloudRead{}, arrivals)
	if s.Inbox != 3 {
		t.Errorf("inbox = %d, want the 3 new arrivals of accounts still here", s.Inbox)
	}
	if s.InboxItem == nil || s.InboxItem.ID != "b-new" {
		t.Errorf("inbox item = %+v, want the on-screen account's newest", s.InboxItem)
	}
	if s.Item.Kind != "" || s.Attention != 0 {
		t.Errorf("arrivals changed the transfer item: %+v", s)
	}
	if !attentionOn(s) || badgeLabel(s) != "PKP，3 项新入库待整理" {
		t.Errorf("dot, label = %v, %q; want the arrivals named", attentionOn(s), badgeLabel(s))
	}

	// Only another account has some: its newest.
	s = summarize(nil, nil, accounts, cloudRead{}, arrivals[:1])
	if s.InboxItem == nil || s.InboxItem.ID != "a-newest" {
		t.Errorf("inbox item = %+v, want a-newest", s.InboxItem)
	}

	// A stopped transfer outranks them: the item and the label are its.
	s = summarize([]UploadTask{up("a", "failed", upload.StateFailed, "2026-10-16T08:00:00+08:00")}, nil, accounts, cloudRead{}, arrivals)
	if itemID(s.Item) != "failed" || s.Attention != 1 || s.Inbox != 3 {
		t.Errorf("item, attention, inbox = %q, %d, %d", itemID(s.Item), s.Attention, s.Inbox)
	}
	if !attentionOn(s) || badgeLabel(s) != "PKP，1 项传输需要处理" {
		t.Errorf("dot, label = %v, %q; want the transfer first", attentionOn(s), badgeLabel(s))
	}

	// A moving transfer only: no dot; with arrivals the dot is theirs.
	moving := []UploadTask{up("b", "moving", upload.StateUploading, "2026-10-16T08:00:00+08:00")}
	if s = summarize(moving, nil, accounts, cloudRead{}, nil); attentionOn(s) {
		t.Error("a moving transfer lit the dot")
	}
	if s = summarize(moving, nil, accounts, cloudRead{}, arrivals); !attentionOn(s) || badgeLabel(s) != "PKP，3 项新入库待整理" {
		t.Errorf("dot, label = %v, %q", attentionOn(s), badgeLabel(s))
	}
}

func TestMenuBarIgnoresArrivalsWithoutSuggestions(t *testing.T) {
	s := summarize(nil, nil, []Account{{ID: "a", Current: true}}, cloudRead{}, []Arrival{{ID: "unknown", Account: "a", State: ArrivalNew}})
	if s.Inbox != 0 || s.InboxItem != nil || attentionOn(s) {
		t.Fatalf("an arrival without suggestions prompted the user: %+v", s)
	}
}

func TestMenuBarQuitAsksOnlyWhenSomethingStops(t *testing.T) {
	cases := []struct {
		name      string
		uploads   []UploadTask
		downloads []DownloadTask
		unsaved   string
		want      []string
	}{
		{
			name:      "finished, failed and paused transfers do not stop",
			uploads:   []UploadTask{up("a", "1", upload.StateDone, ""), up("a", "2", upload.StateFailed, "")},
			downloads: []DownloadTask{down("a", "3", download.StatePaused, ""), down("a", "4", download.StateInterrupted, "")},
			want:      nil,
		},
		{
			name:      "one download, in the design's words",
			downloads: []DownloadTask{down("a", "1", download.StateDownloading, "")},
			want:      []string{"目前有 1 项下载到电脑。退出将停止这项本机传输；云端任务是否继续由服务端决定。只想收起窗口，请选择「继续运行」。"},
		},
		{
			name:      "queued ones stop too, every account's",
			uploads:   []UploadTask{up("a", "1", upload.StateQueued, ""), up("b", "2", upload.StateUploading, "")},
			downloads: []DownloadTask{down("b", "3", download.StateQueued, "")},
			want:      []string{"1 项下载到电脑和 2 项本地上传", "这些本机传输"},
		},
		{
			name:    "unsubmitted input alone",
			unsaved: "云添加的链接",
			want:    []string{"云添加的链接还没有提交，退出后不会保留。"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := quitQuestion(tc.uploads, tc.downloads, tc.unsaved)
			if tc.want == nil {
				if got != "" {
					t.Fatalf("asked %q, want no question", got)
				}
				return
			}
			for _, part := range tc.want {
				if !strings.Contains(got, part) {
					t.Errorf("question %q lacks %q", got, part)
				}
			}
		})
	}
}
