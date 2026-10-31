package services

import (
	"fmt"
	"os"
	"testing"

	"github.com/tonyflux/pkp/internal/index"
)

func TestMediaLibraryPagesAllCompletedVideosWithOwner(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	owner, _ := svc.active()
	if _, err := svc.MediaLibraryPage("", "", 500); err == nil {
		t.Fatal("implicit owner accepted")
	}
	page, err := svc.MediaLibraryPage(owner.key, "", 500)
	if err != nil || page.Ready {
		t.Fatalf("unready page = %+v, %v", page, err)
	}
	rows := []index.Row{indexedFolder("folder", "", "Show")}
	for i := range 4003 {
		rows = append(rows, indexedVideo(fmt.Sprintf("v%04d", i), "folder", fmt.Sprintf("Show.S01E%04d.mkv", i), 1, ""))
	}
	trashed := indexedVideo("trashed", "folder", "gone.mkv", 1, "")
	trashed.Trashed = true
	pending := indexedVideo("pending", "folder", "pending.mkv", 1, "")
	pending.Phase = "PHASE_TYPE_RUNNING"
	other := indexedVideo("audio", "folder", "audio.mp3", 1, "")
	other.Category = "audio"
	fillIndex(t, svc, append(rows, trashed, pending, other))
	seen := map[string]bool{}
	after := ""
	for {
		page, err = svc.MediaLibraryPage(owner.key, after, 9000)
		if err != nil {
			t.Fatal(err)
		}
		if page.Account != owner.key || !page.Ready || page.Total != 4003 || len(page.Files) > 2000 {
			t.Fatalf("page = %+v", page)
		}
		for _, file := range page.Files {
			if seen[file.Entry.ID] {
				t.Fatalf("duplicate %q", file.Entry.ID)
			}
			seen[file.Entry.ID] = true
			if len(file.Ancestors) != 1 || file.Ancestors[0].ID != "folder" || file.Entry.Path != "全部文件 / Show" {
				t.Fatalf("ancestry = %+v", file)
			}
		}
		if page.Next == "" {
			break
		}
		if page.Next <= after {
			t.Fatal("cursor did not advance")
		}
		after = page.Next
	}
	if len(seen) != 4003 {
		t.Fatalf("enumerated %d videos", len(seen))
	}
	for _, limit := range []int{1, 500, 2000, 9000} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			page, err := svc.MediaLibraryPage(owner.key, "", limit)
			want := min(limit, 2000)
			if err != nil || len(page.Files) != want || page.Next != page.Files[want-1].Entry.ID {
				t.Fatalf("first page: size=%d cursor=%q error=%v", len(page.Files), page.Next, err)
			}
			next, err := svc.MediaLibraryPage(owner.key, page.Next, limit)
			if err != nil || len(next.Files) != want || next.Files[0].Entry.ID <= page.Next || next.Total != page.Total || next.Revision != page.Revision {
				t.Fatalf("next page: size=%d error=%v", len(next.Files), err)
			}
		})
	}
	page, err = svc.MediaLibraryPage(owner.key, "v2002", 2000)
	if err != nil || len(page.Files) != 2000 || page.Next != "v4002" {
		t.Fatalf("exact final page: size=%d cursor=%q error=%v", len(page.Files), page.Next, err)
	}
	page, err = svc.MediaLibraryPage(owner.key, page.Next, 2000)
	if err != nil || len(page.Files) != 0 || page.Next != "" || page.Total != 4003 {
		t.Fatalf("exhausted page: %+v error=%v", page, err)
	}
	seedSession(t, svc, "another@example.com")
	page, err = svc.MediaLibraryPage(owner.key, "", 2)
	if err != nil || len(page.Files) != 2 || page.Account != owner.key {
		t.Fatalf("switch changed explicit owner: %+v %v", page, err)
	}
}

func TestMediaLibraryOverridesAreAccountLocalAtomicAndResettable(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	owner, _ := svc.active()
	fillIndex(t, svc, []index.Row{indexedVideo("same", "", "home.mkv", 1, "")})
	value := &LibraryOverride{WorkID: "group-a", Title: "Local title", Type: "series", Year: 2020, Season: 1, Episode: 2}
	if err := svc.SetMediaLibraryOverride(owner.key, "same", value); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(svc.mediaLibraryPath(owner.key))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private record: %v %v", info, err)
	}
	other := seedSession(t, svc, "other@example.com")
	fillIndex(t, svc, []index.Row{indexedVideo("same", "", "different.mkv", 1, "")})
	got, err := svc.MediaLibraryOverrides(other.key)
	if err != nil || len(got) != 0 {
		t.Fatalf("cross-account override: %+v %v", got, err)
	}
	got, err = svc.MediaLibraryOverrides(owner.key)
	if err != nil || got["same"] != *value {
		t.Fatalf("owner override: %+v %v", got, err)
	}
	bad := *value
	bad.Episode = 0
	if err := svc.SetMediaLibraryOverride(owner.key, "same", &bad); err == nil {
		t.Fatal("series without episode accepted")
	}
	if err := svc.SetMediaLibraryOverride(owner.key, "missing", value); err == nil {
		t.Fatal("unknown file accepted")
	}
	if err := svc.SetMediaLibraryOverride("", "same", value); err == nil {
		t.Fatal("implicit owner accepted")
	}
	if err := svc.SetMediaLibraryOverride(owner.key, "same", nil); err != nil {
		t.Fatal(err)
	}
	got, err = svc.MediaLibraryOverrides(owner.key)
	if err != nil || len(got) != 0 {
		t.Fatalf("reset: %+v %v", got, err)
	}
	if err := os.WriteFile(svc.mediaLibraryPath(owner.key), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetMediaLibraryOverride(owner.key, "same", value); err == nil {
		t.Fatal("corrupt record was overwritten")
	}
	svc.removeMediaLibraryFile(owner.key)
	if _, err := os.Stat(svc.mediaLibraryPath(owner.key)); !os.IsNotExist(err) {
		t.Fatalf("remove account record: %v", err)
	}
}

func TestMediaLibraryHistoryUsesExplicitAccountAndKeepsFinished(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	owner, _ := svc.active()
	if err := owner.playLog().opened(playRecord{FileID: "one", Position: 99, Duration: 100, PlayedAt: "2026-10-22T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	seedSession(t, svc, "other@example.com")
	plays, err := svc.MediaLibraryHistory(owner.key)
	if err != nil || len(plays) != 1 || plays[0].FileID != "one" || !finished(plays[0].Position, plays[0].Duration) {
		t.Fatalf("history = %+v, %v", plays, err)
	}
	if _, err := svc.MediaLibraryHistory(""); err == nil {
		t.Fatal("implicit owner accepted")
	}
}
