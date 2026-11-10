package services

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func conversationService(t *testing.T) *DriveService {
	t.Helper()
	return newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if !authAndTokenRoutes(w, r) {
			http.NotFound(w, r)
		}
	})
}

func conversationIDs(list []ConversationInfo) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.ID
	}
	return out
}

// The newest 20 stay, newest first; a body comes back byte for byte; a
// saved one is replaced, not repeated.
func TestConversationsKeepTheNewest(t *testing.T) {
	svc := conversationService(t)
	base := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	for i := range conversationKeep + 2 {
		info := ConversationInfo{ID: fmt.Sprint("c", i), Title: fmt.Sprint("对话 ", i), Turns: i,
			UpdatedAt: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)}
		if err := svc.SaveConversation("", info, fmt.Sprintf(`{ "turn": %d,  "text": "第 %d 次" }`, i, i)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := svc.Conversations("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != conversationKeep || list[0].ID != "c21" || list[conversationKeep-1].ID != "c2" {
		t.Fatalf("list = %v", conversationIDs(list))
	}
	if list[0].Title != "对话 21" || list[0].Turns != 21 || list[0].UpdatedAt != "2026-10-16T08:21:00Z" {
		t.Errorf("newest = %+v", list[0])
	}
	if _, err := svc.Conversation("", "c0"); !errors.Is(err, errNoConversation) {
		t.Errorf("oldest beyond 20: %v, want errNoConversation", err)
	}
	if body, err := svc.Conversation("", "c7"); err != nil || body != `{ "turn": 7,  "text": "第 7 次" }` {
		t.Errorf("body = %q %v", body, err)
	}

	// Saving c5 again, now with a plan waiting, moves it to the top. An
	// unreadable time becomes now, which is newer than every other.
	if err := svc.SaveConversation("", ConversationInfo{ID: "c5", Title: "改过\n的标题", UpdatedAt: "yesterday", PlanPending: true}, `{"turn":5}`); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.Conversations("")
	if len(list) != conversationKeep || list[0].ID != "c5" || !list[0].PlanPending || list[0].Title != "改过 的标题" {
		t.Errorf("after resave = %v %+v", conversationIDs(list), list[0])
	}
	if n := slices.Index(conversationIDs(list), "c5"); slices.Index(conversationIDs(list)[n+1:], "c5") >= 0 {
		t.Error("c5 listed twice")
	}
	if at, err := time.Parse(time.RFC3339, list[0].UpdatedAt); err != nil || time.Since(at) > time.Minute {
		t.Errorf("stamped %q", list[0].UpdatedAt)
	}

	s, _ := svc.active()
	info, err := os.Stat(svc.conversationsPath(s.key))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", info, err)
	}
}

func TestSaveConversationChecksWhatItKeeps(t *testing.T) {
	svc := conversationService(t)
	for _, c := range []struct {
		info ConversationInfo
		body string
		want error
	}{
		{ConversationInfo{ID: ""}, `{}`, errConversationID},
		{ConversationInfo{ID: strings.Repeat("x", maxConversationID+1)}, `{}`, errConversationID},
		{ConversationInfo{ID: "a"}, `{"x":`, errConversationBody},
		{ConversationInfo{ID: "a"}, `"` + strings.Repeat("x", maxConversationBody) + `"`, errConversationTooLarge},
	} {
		if err := svc.SaveConversation("", c.info, c.body); !errors.Is(err, c.want) {
			t.Errorf("save %q (%d bytes): %v, want %v", c.info.ID, len(c.body), err, c.want)
		}
	}
	// Exactly at the bound is kept.
	full := `"` + strings.Repeat("x", maxConversationBody-2) + `"`
	if err := svc.SaveConversation("", ConversationInfo{ID: "full"}, full); err != nil {
		t.Errorf("2 MiB body: %v", err)
	}
	if list, _ := svc.Conversations(""); len(list) != 1 {
		t.Errorf("refused saves left %v", conversationIDs(list))
	}
	if err := svc.SaveConversation("no-such-account", ConversationInfo{ID: "a"}, `{}`); !errors.Is(err, errNoSuchAccount) {
		t.Errorf("unknown account: %v", err)
	}
}

func TestConversationsKeepLargeUploadReceipts(t *testing.T) {
	svc := conversationService(t)
	var results strings.Builder
	for i := range 10000 {
		if i > 0 {
			results.WriteByte(',')
		}
		failure := ""
		if i%10 == 0 {
			failure = "Permission denied"
		}
		fmt.Fprintf(&results, `{"id":"u%d","name":%q,"error":%q}`, i, fmt.Sprintf("%s-%d.mkv", strings.Repeat("n", 190), i), failure)
	}
	body := func(action, text string) string {
		return fmt.Sprintf(`{"version":1,"turns":[{"id":"turn","cards":[{"kind":"operation","id":"upload","action":%q,"state":"failed","results":[%s]}],"traces":[],"user":%q}]}`, action, results.String(), text)
	}
	for _, action := range []string{"upload_files", "upload_folder"} {
		t.Run(action, func(t *testing.T) {
			original := body(action, "Upload my selection")
			if len(original) <= maxConversationBody {
				t.Fatal("fixture must exceed the ordinary conversation limit")
			}
			if err := svc.SaveConversation("", ConversationInfo{ID: action}, original); err != nil {
				t.Fatalf("save upload outcomes: %v", err)
			}
			if restored, err := svc.Conversation("", action); err != nil || restored != original {
				t.Fatalf("upload outcomes changed on reopen: %v", err)
			}
		})
	}
	for name, original := range map[string]string{
		"other operation":        body("star", ""),
		"oversized conversation": body("upload_folder", strings.Repeat("x", maxConversationBody)),
		"unknown receipt fields": fmt.Sprintf(`{"version":1,"turns":[{"cards":[{"kind":"operation","action":"upload_folder","results":[{"id":"u1","name":"File","error":"","extra":%q}]}]}]}`, strings.Repeat("x", maxConversationBody)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := svc.SaveConversation("", ConversationInfo{ID: "rejected"}, original); !errors.Is(err, errConversationTooLarge) {
				t.Fatalf("save unrelated oversized content: %v", err)
			}
		})
	}
}

// Deleting forgets one; conversations are their account's, and go with it.
func TestConversationsDeleteAndBelongToTheirAccount(t *testing.T) {
	svc := conversationService(t)
	a := seedSession(t, svc, "a@example.com")
	b := seedSession(t, svc, "b@example.com")
	for _, id := range []string{"x", "y"} {
		if err := svc.SaveConversation(a.key, ConversationInfo{ID: id, UpdatedAt: "2026-10-16T08:00:00Z"}, `{}`); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := svc.Conversations(b.key); err != nil || len(list) != 0 || list == nil {
		t.Errorf("other account's = %v %v", list, err)
	}
	if list, _ := svc.Conversations(""); len(list) != 0 {
		t.Errorf("account on screen (b) sees %v", conversationIDs(list))
	}

	if err := svc.DeleteConversation(a.key, "x"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteConversation(a.key, "gone"); err != nil {
		t.Errorf("deleting a missing one: %v", err)
	}
	if list, _ := svc.Conversations(a.key); !slices.Equal(conversationIDs(list), []string{"y"}) {
		t.Errorf("after delete = %v", conversationIDs(list))
	}
	if _, err := svc.Conversation(a.key, "x"); !errors.Is(err, errNoConversation) {
		t.Errorf("deleted: %v", err)
	}

	path := svc.conversationsPath(a.key)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveAccount(a.key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("conversations of a removed account: %v, want them gone", err)
	}
}

// An unreadable file is moved aside, not overwritten.
func TestConversationsUnreadableFile(t *testing.T) {
	svc := conversationService(t)
	s, _ := svc.active()
	path := svc.conversationsPath(s.key)
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if list, err := svc.Conversations(""); err != nil || len(list) != 0 {
		t.Errorf("unreadable = %v %v", list, err)
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Errorf("moved aside: %v", err)
	}
}
