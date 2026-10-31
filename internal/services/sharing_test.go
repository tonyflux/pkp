package services

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The share list flattens the server's quoted numbers and "-1 means never"
// conventions; the UI must not have to know them.
func TestListSharesFlattensServerConventions(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/drive/v1/share/list" {
			t.Errorf("path = %s", r.URL.Path)
		}
		io.WriteString(w, `{"data":[
		  {"share_id":"a","share_status":"OK","title":"","params":{"name":"旅行影像"},"file_kind":"drive#folder","view_count":"126","restore_count":"18","expiration_at":"-1","expiration_left":"-1","share_url":"https://x/a","create_time":"2026-10-08T00:00:00+08:00"},
		  {"share_id":"b","share_status":"OK","title":"项目笔记.pdf","file_kind":"drive#file","expiration_at":"2026-10-17T00:00:00+08:00","expiration_left":"7","pass_code":"abcd"},
		  {"share_id":"c","share_status":"EXPIRED","title":"灵感参考.png","file_kind":"drive#file"},
		  {"share_id":"d","share_status":"PROHIBITED","share_status_text":"违规","title":"x","file_kind":"drive#file"}
		],"next_page_token":"n2"}`)
	})
	page, err := svc.ListShares("")
	if err != nil {
		t.Fatal(err)
	}
	if page.NextPageToken != "n2" || len(page.Shares) != 4 {
		t.Fatalf("page = %+v", page)
	}
	a, b, c, d := page.Shares[0], page.Shares[1], page.Shares[2], page.Shares[3]
	if a.Title != "旅行影像" || !a.Folder || a.Views != 126 || a.Saves != 18 || a.Status != "active" || a.ExpiresAt != "" || a.DaysLeft != -1 {
		t.Errorf("a = %+v", a)
	}
	if b.Status != "active" || b.ExpiresAt == "" || b.DaysLeft != 7 || b.PassCode != "abcd" {
		t.Errorf("b = %+v", b)
	}
	if c.Status != "expired" || d.Status != "blocked" || d.StatusText != "违规" {
		t.Errorf("c = %+v d = %+v", c, d)
	}
}

func TestCreateShareMapsRequest(t *testing.T) {
	var body map[string]any
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"share_id":"s","share_url":"https://x/s","pass_code":"zz12"}`)
	})
	created, err := svc.CreateShare(ShareRequest{FileIDs: []string{"f"}, ExpiresAt: "2026-11-01T00:00:00+08:00", PassCode: true})
	if err != nil {
		t.Fatal(err)
	}
	if body["expiration_at"] != "2026-11-01T00:00:00+08:00" || body["expiration_days"] != nil || body["pass_code_option"] != "REQUIRED" {
		t.Errorf("body = %v", body)
	}
	if created.URL != "https://x/s" || created.PassCode != "zz12" {
		t.Errorf("created = %+v", created)
	}
	if _, err := svc.CreateShare(ShareRequest{}); err == nil {
		t.Error("an empty selection must be rejected")
	}
}

func TestCreateShareCustomPassCode(t *testing.T) {
	var body map[string]any
	var calls int
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		calls++
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"share_id":"s","share_url":"https://x/s","pass_code":"ab12"}`)
	})
	created, err := svc.CreateShare(ShareRequest{FileIDs: []string{"f"}, CustomPassCode: " ab12 "})
	if err != nil {
		t.Fatal(err)
	}
	if body["custom_pass_code"] != "ab12" || body["pass_code_option"] != "REQUIRED" || body["share_to"] != "encryptedlink" {
		t.Errorf("body = %v", body)
	}
	if created.PassCode != "ab12" {
		t.Errorf("created = %+v", created)
	}
	before := calls
	for _, bad := range []string{"ab", "abcd1", "ab-1", "你好12", "ab 1"} {
		if _, err := svc.CreateShare(ShareRequest{FileIDs: []string{"f"}, CustomPassCode: bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if calls != before {
		t.Errorf("invalid codes reached the server: %d extra calls", calls-before)
	}
}

// Revocation can partly fail; the sessions that stayed must not read as gone.
func TestRevokeSessionsReportsWhatStayed(t *testing.T) {
	var asked []string
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/session/v1/clients:batchDelete" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body struct {
			SessionIDs []string `json:"session_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		asked = body.SessionIDs
		if len(body.SessionIDs) == 1 {
			io.WriteString(w, `{"deleted":["a"]}`)
			return
		}
		io.WriteString(w, `{"deleted":["a"],"failures":[{"session_id":"b","error":"session expired"}]}`)
	})

	if err := svc.RevokeSessions([]string{"a"}); err != nil {
		t.Fatalf("single: %v", err)
	}
	err := svc.RevokeSessions([]string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "有 1 个会话（共 2 个）") {
		t.Errorf("err = %v, want the session that stayed reported", err)
	}
	if len(asked) != 2 {
		t.Errorf("asked = %v", asked)
	}
}

// The account service takes a token's lifetime as text in whole seconds, and
// its answer names no scopes; the deadline and the scopes shown with the new
// token have to come from what was asked.
func TestCreateAccessTokenSendsLifetimeAsText(t *testing.T) {
	var body map[string]any
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/v1/user/pat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"token_type":"Bearer","access_token":"pat-1","expires_in":2592000,"scope":""}`)
	})

	tok, err := svc.CreateAccessToken(AccessTokenOptions{Label: " 备份脚本 ", Scopes: []string{"files:read", "share"}, Lifetime: 2592000})
	if err != nil {
		t.Fatal(err)
	}
	if body["expires_in"] != "2592000" || body["label"] != "备份脚本" {
		t.Errorf("body = %v", body)
	}
	if tok.Token != "pat-1" || tok.Scopes != "files:read share" {
		t.Errorf("token = %+v", tok)
	}
	deadline, err := time.Parse(time.RFC3339, tok.ExpiresAt)
	if err != nil || deadline.Sub(time.Now()) < 29*24*time.Hour {
		t.Errorf("ExpiresAt = %q, want about 30 days out", tok.ExpiresAt)
	}
	if _, err := svc.CreateAccessToken(AccessTokenOptions{Label: "  ", Scopes: []string{"files:read"}}); err == nil {
		t.Error("a blank label must be rejected")
	}
}
