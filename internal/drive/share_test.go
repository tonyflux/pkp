package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The share body must match what the server accepts (verified against the
// live API): share_to names the link kind and agrees with the pass-code
// option, expiration_days is a number with -1 for never, no restore_limit.
func TestCreateShareEncodesContract(t *testing.T) {
	var got map[string]any
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/drive/v1/share" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"share_id":"s1","share_url":"https://mypikpak.com/s/s1","pass_code":"abcd"}`)
	}, newStaticTokens("tok"))

	created, err := client.CreateShare(context.Background(), CreateShareParams{FileIDs: []string{"f1"}, ExpirationDays: 7, PassCode: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got["expiration_days"] != float64(7) || got["pass_code_option"] != PassCodeRequired || got["share_to"] != "encryptedlink" || got["restore_limit"] != nil {
		t.Errorf("body = %v", got)
	}
	if ids, _ := got["file_ids"].([]any); len(ids) != 1 || ids[0] != "f1" {
		t.Errorf("file_ids = %v", got["file_ids"])
	}
	if created.ShareID != "s1" || created.ShareURL == "" || created.PassCode != "abcd" {
		t.Errorf("created = %+v", created)
	}

	if _, err := client.CreateShare(context.Background(), CreateShareParams{FileIDs: []string{"f1"}}); err != nil {
		t.Fatal(err)
	}
	if got["expiration_days"] != float64(-1) || got["pass_code_option"] != PassCodeNotRequired || got["share_to"] != "publiclink" {
		t.Errorf("never-expiring, open share body = %v", got)
	}
	if _, err := client.CreateShare(context.Background(), CreateShareParams{}); err == nil {
		t.Error("no file ids should be rejected before any request")
	}
}

func TestListSharesDecodesQuotedNumbers(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/drive/v1/share/list" || r.URL.Query().Get("limit") != "20" {
			t.Errorf("request = %s %s", r.URL.Path, r.URL.RawQuery)
		}
		io.WriteString(w, `{"data":[{"share_id":"s1","share_status":"OK","title":"t","view_count":"126","restore_count":"18","expiration_days":"-1","expiration_at":"-1","file_size":"1234","params":{"name":"t"}}],"next_page_token":""}`)
	}, newStaticTokens("tok"))
	list, err := client.ListShares(context.Background(), ListSharesParams{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].ViewCount != 126 || list.Data[0].RestoreCount != 18 || list.Data[0].ExpirationDays != -1 {
		t.Errorf("list = %+v", list.Data)
	}
}

// WebDAV management lives on its own host; the drive token and identity
// headers still apply.
func TestWebDAVUsesDAVHostWithDriveAuth(t *testing.T) {
	var path, auth, method string
	var body map[string]any
	dav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth, method = r.URL.Path, r.Header.Get("Authorization"), r.Method
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"webdav_enable":true,"is_premium":true,"applications":[{"id":1,"username":"u","password":"p","application_name":"n","read_only":0}]}`)
	}))
	defer dav.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("drive host must not receive WebDAV calls: %s", r.URL.Path)
	}))
	defer api.Close()

	client, err := New(Config{APIBaseURL: api.URL, DAVBaseURL: dav.URL, DeviceID: "d", Logger: quiet()}, newStaticTokens("tok"))
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.ListWebDAV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if path != "/webdav/v1/applications" || auth != "Bearer tok" || !status.Enabled || len(status.Applications) != 1 {
		t.Errorf("list: path=%s auth=%s status=%+v", path, auth, status)
	}

	if err := client.UpdateWebDAVApplication(context.Background(), "u", "p", WebDAVOptions{ApplicationName: "n2", MaxEntriesInResponse: 1000, ReadOnly: true, OptimizedForMediaPlay: true}); err != nil {
		t.Fatal(err)
	}
	props, _ := body["modify_props"].(map[string]any)
	if method != http.MethodPatch || body["username"] != "u" || props["read_only"] != true || props["optimized_for_media_play"] != true || props["application_name"] != "n2" {
		t.Errorf("update body = %v (%s)", body, method)
	}
	if err := client.DeleteWebDAVApplication(context.Background(), "u", "p"); err != nil || method != http.MethodDelete || body["password"] != "p" {
		t.Errorf("delete: err=%v method=%s body=%v", err, method, body)
	}
}
