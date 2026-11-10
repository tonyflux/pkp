package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseShareURLTrustBoundary(t *testing.T) {
	for _, raw := range []string{
		"https://mypikpak.com/s/share_1",
		"https://www.mypikpak.com/s/share_1/folder-2/",
		"https://MYPIKPAK.COM/s/share_1?password=A%2BB&tracking=discarded",
		"https://mypikpak.com/s/share_1?pass_code=A%2BB&password=A%2BB",
	} {
		id, canonical, code, err := ParseShareURL(raw)
		if err != nil || id != "share_1" {
			t.Fatalf("valid link %q: %q %v", raw, id, err)
		}
		want := "https://mypikpak.com/s/share_1"
		if strings.Contains(raw, "password=") {
			want += "?pass_code=A%2BB"
			if code != "A+B" {
				t.Fatalf("code lost: %q", code)
			}
		}
		if canonical != want {
			t.Errorf("canonical = %q, want %q", canonical, want)
		}
	}
	for _, raw := range []string{
		"https://mypikpak.com.evil.example/s/share_1", "https://evil.example/mypikpak.com/s/share_1",
		"http://mypikpak.com/s/share_1", "//mypikpak.com/s/share_1", "https://user@mypikpak.com/s/share_1",
		"https://mypikpak.com:443/s/share_1", "https://mypikpak.com./s/share_1", "https://mypikpak.com/s/",
		"https://mypikpak.com/S/share_1", "https://mypikpak.com/s/share_1//child", "https://mypikpak.com/s/../child",
		"https://mypikpak.com/s/share%2fchild", "https://mypikpak.com/s/%73hare", "https://mypikpak.com/s/share%5cchild",
		"https://mypikpak.com/s/share_1?pass_code=a&pass_code=b", "https://mypikpak.com/s/share_1?password=a&pass_code=b",
		"https://mypikpak.com/s/share_1?pass_code=%0d%0aInjected", "https://mypikpak.com/s/share_1?other=%00",
		"https://mypikpak.com/s/share_1?password=%zz", "https://mypikpak.com/s/share_1#https://evil.example",
		"https://mypikpak.com/s/share_1?pass_code=%E2%80%A8", "https://mypikpak.com/s/share_1?password=%E2%80%A9",
		"https://mypikpak.com/s/share_1?pass_code=%FF", "https://mypikpak.com/s/share_1?tracking=%FE",
		" https://mypikpak.com/s/share_1", "https://mypikpak.com/s/share_1\n", "https://mypikpak.com\\@evil.example/s/share_1",
		"https://mypikpak.com/s/" + strings.Repeat("x", 257), "https://mypikpak.com/s/share_1?pass_code=" + strings.Repeat("a", 129),
	} {
		if id, canonical, code, err := ParseShareURL(raw); err == nil || id != "" || canonical != "" || code != "" {
			t.Errorf("untrusted link accepted: %q -> %q %q %q (%v)", raw, id, canonical, code, err)
		}
	}
}

func TestShareReadsNeverSendAccountAuthorization(t *testing.T) {
	reads := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Errorf("anonymous read leaked authorization: %s %v", r.Method, r.Header)
		}
		switch r.URL.Path {
		case "/drive/v1/share":
			if r.URL.Query().Get("pass_code") != "code" || r.URL.Query().Get("page_token") != "root-next" {
				t.Errorf("root access/pagination lost: %v", r.URL.Query())
			}
			io.WriteString(w, `{"share_status":"PASS_CODE_ERROR","files":[],"file_num":"12"}`)
		case "/drive/v1/share/detail":
			if r.URL.Query().Get("parent_id") != "folder" || r.URL.Query().Get("pass_code_token") != "capability" {
				t.Errorf("folder capability lost: %v", r.URL.Query())
			}
			io.WriteString(w, `{"share_status":"EXPIRED","files":[]}`)
		case "/drive/v1/share/file_info":
			io.WriteString(w, `{"share_status":"OK","file_info":{"kind":"drive#file","id":"file","name":"a.mp4","medias":[{"link":{"url":"https://cdn.example/a.mp4"}}]}}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}, newStaticTokens("private-account-token"))
	ctx := context.Background()
	page, err := client.InspectShare(ctx, InspectShareParams{ShareID: "share", PassCode: "code", PageToken: "root-next"})
	if err != nil || page.Status != SharePassCodeError || page.FileNum != 12 {
		t.Fatalf("code gate was not data: %+v %v", page, err)
	}
	folder, err := client.ListSharedFiles(ctx, ListSharedFilesParams{ShareID: "share", PassCodeToken: "capability", ParentID: "folder"})
	if err != nil || folder.Status != ShareStatusExpired {
		t.Fatalf("expiry gate lost: %+v %v", folder, err)
	}
	file, err := client.GetSharedFile(ctx, "share", "capability", "file")
	if err != nil || file.FileInfo.ID != "file" || len(file.FileInfo.Medias) != 1 || reads != 3 {
		t.Fatalf("preview file unavailable: %+v %v (%d reads)", file, err, reads)
	}
}

func TestShareRestoreHasExplicitDestinationAndOneAttempt(t *testing.T) {
	writes := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writes++
		if r.Method != http.MethodPost || r.URL.Path != "/drive/v1/share/restore" || r.Header.Get("Authorization") != "Bearer account-token" {
			t.Errorf("restore did not use selected account auth: %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		var body struct {
			ParentID      string            `json:"parent_id"`
			FileIDs       []string          `json:"file_ids"`
			AncestorIDs   []string          `json:"ancestor_ids"`
			SpecifyParent bool              `json:"specify_parent_id"`
			To            any               `json:"to"`
			Params        map[string]string `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ParentID != "chosen-folder" || !body.SpecifyParent || body.To != nil || body.AncestorIDs == nil || len(body.AncestorIDs) != 0 || len(body.FileIDs) != 2 || body.Params["trace_file_ids"] != "source-a,source-b" {
			t.Errorf("unsafe restore target/selection: %+v", body)
		}
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, `{"error":"upstream_unavailable"}`)
	}, newStaticTokens("account-token"))
	params := RestoreShareParams{ShareID: "share", ParentID: "chosen-folder", FileIDs: []string{"source-a", "source-b"}}
	if _, err := client.RestoreShare(context.Background(), params); err == nil || writes != 1 {
		t.Fatalf("mutation retried or falsely succeeded: writes=%d err=%v", writes, err)
	}
	for _, ids := range [][]string{nil, {"source", "source"}, {""}, {"a,b"}, {"../file"}} {
		params.FileIDs = ids
		if _, err := client.RestoreShare(context.Background(), params); err == nil || writes != 1 {
			t.Fatalf("invalid selection reached mutation: ids=%v writes=%d err=%v", ids, writes, err)
		}
	}
}
