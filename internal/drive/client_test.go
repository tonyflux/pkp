package drive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type staticTokens struct {
	token      atomic.Pointer[Token]
	invalidate atomic.Int64
}

func newStaticTokens(access string) *staticTokens {
	s := &staticTokens{}
	s.token.Store(&Token{AccessToken: access, ExpiresAt: time.Now().Add(time.Hour)})
	return s
}

func (s *staticTokens) Token(context.Context) (Token, error) { return *s.token.Load(), nil }

func (s *staticTokens) Invalidate(_ context.Context, _ string) error {
	s.invalidate.Add(1)
	s.token.Store(&Token{AccessToken: "refreshed", ExpiresAt: time.Now().Add(time.Hour)})
	return nil
}

// fakeCaptcha records how often the client asked for a replacement token.
type fakeCaptcha struct {
	mu        sync.Mutex
	token     string
	refreshes int
	actions   []string
	err       error
}

func (c *fakeCaptcha) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *fakeCaptcha) Refresh(_ context.Context, action, rejected string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	if c.token != rejected {
		return nil
	}
	c.refreshes++
	c.actions = append(c.actions, action)
	c.token = "captcha-" + strconv.Itoa(c.refreshes)
	return nil
}

func (c *fakeCaptcha) stats() (int, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshes, append([]string(nil), c.actions...)
}

func newTestClient(t *testing.T, handler http.HandlerFunc, tokens TokenSource) (*Client, *httptest.Server) {
	t.Helper()
	client, srv, _ := newTestClientWithCaptcha(t, handler, tokens, &fakeCaptcha{token: "captcha-0"})
	return client, srv
}

func newTestClientWithCaptcha(t *testing.T, handler http.HandlerFunc, tokens TokenSource, captcha *fakeCaptcha) (*Client, *httptest.Server, *fakeCaptcha) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := New(Config{
		APIBaseURL:  srv.URL,
		AuthBaseURL: srv.URL,
		Profile:     AndroidProfile(),
		DeviceID:    "0123456789abcdef0123456789abcdef",
		Captcha:     captcha,
		Logger:      quiet(),
	}, tokens)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client, srv, captcha
}

// A client with no stable device identity looks like a new device on every
// start, which is the fastest way into the captcha wall.
func TestNewRequiresDeviceID(t *testing.T) {
	if _, err := New(Config{}, nil); err == nil {
		t.Error("expected an error when DeviceID is missing")
	}
	client, err := New(Config{DeviceID: "d"}, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if client.Config().Profile.ClientID != AndroidProfile().ClientID {
		t.Error("the profile did not default to the published Android identity")
	}
}

// Ids are PathEscape'd by the callers; the URL builder must not encode the
// '%' a second time, or "a/b" reaches the server as "a%252Fb".
func TestEscapedIDIsNotDoubleEncoded(t *testing.T) {
	var gotPath, gotQuery string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		io.WriteString(w, `{"kind":"drive#file","id":"a/b"}`)
	}, newStaticTokens("tok"))

	if _, err := client.GetFile(context.Background(), "a/b", GetFileParams{Usage: "FETCH"}); err != nil {
		t.Fatalf("get file: %v", err)
	}
	if gotPath != "/drive/v1/files/a%2Fb" {
		t.Errorf("path = %q, want /drive/v1/files/a%%2Fb", gotPath)
	}
	if gotQuery != "usage=FETCH" {
		t.Errorf("query = %q, want usage=FETCH", gotQuery)
	}
}

func TestListFilesEncodesParamsAndDecodesResponse(t *testing.T) {
	var gotQuery, gotAuth, gotDevice, gotClient string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotDevice = r.Header.Get("X-Device-Id")
		gotClient = r.Header.Get("X-Client-Id")
		// The gateway renders proto int64 as a quoted string.
		io.WriteString(w, `{"kind":"drive#fileList","next_page_token":"page2","version":"v9",
			"files":[{"kind":"drive#file","id":"f1","name":"a.mkv","size":"8589934592",
			"links":{"application/octet-stream":{"url":"https://cdn/a.mkv","expire":"2026-10-09T12:00:00.000Z"}}}]}`)
	}, newStaticTokens("tok"))

	list, err := client.ListFiles(context.Background(), ListFilesParams{
		ParentID: "root",
		Limit:    500, // over the cap on purpose
		Filters:  OnlyLive().Eq("kind", KindFile),
		Order:    OrderNameAsc,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	query, err := parseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if query["parent_id"] != "root" {
		t.Errorf("parent_id = %q", query["parent_id"])
	}
	if query["limit"] != "100" {
		t.Errorf("limit = %q, want it clamped to 100", query["limit"])
	}
	if query["order"] != OrderNameAsc {
		t.Errorf("order = %q", query["order"])
	}

	var filters map[string]map[string]any
	if err := json.Unmarshal([]byte(query["filters"]), &filters); err != nil {
		t.Fatalf("filters %q is not JSON: %v", query["filters"], err)
	}
	if filters["trashed"]["eq"] != false || filters["kind"]["eq"] != KindFile {
		t.Errorf("filters = %v", filters)
	}

	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotDevice == "" || gotClient != AndroidProfile().ClientID {
		t.Errorf("identity headers: device=%q client=%q", gotDevice, gotClient)
	}

	if list.NextPageToken != "page2" || list.Version != "v9" {
		t.Errorf("paging = %q/%q", list.NextPageToken, list.Version)
	}
	if len(list.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(list.Files))
	}
	if got := list.Files[0].Size; got != 8589934592 {
		t.Errorf("size = %d, want 8589934592 decoded from the quoted form", got)
	}
	if got := list.Files[0].ContentLink(); got != "https://cdn/a.mkv" {
		t.Errorf("ContentLink = %q", got)
	}
}

// A stored sync_time must round-trip into the exact token shape the server
// emits, or the server silently restarts the walk from the beginning.
func TestSyncCursorEncodesServerTokenShape(t *testing.T) {
	if got := SyncCursor(""); got != "" {
		t.Errorf("SyncCursor(\"\") = %q, want empty (start over)", got)
	}
	token := SyncCursor("2026-10-10T15:53:13.307+08:00")
	if !strings.HasPrefix(token, "2.") {
		t.Fatalf("token %q lacks the 2. prefix", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "2."))
	if err != nil {
		t.Fatalf("token payload is not raw base64url: %v", err)
	}
	want := `{"kind":"modified_time_asc","data":{"modified_time":"2026-10-10T15:53:13.307+08:00","id":""}}`
	if string(raw) != want {
		t.Errorf("payload = %s\nwant      %s", raw, want)
	}
}

// Page tokens count pages ("2.", "3.", … "17."); every one of them must
// give its position back, as must SyncCursor's own. Seen on the real
// server 2026-10-17: only the first page's token starts with "2.".
func TestCursorTimeReadsEveryPageToken(t *testing.T) {
	const at = "2026-10-10T15:53:13.307+08:00"
	payload := strings.TrimPrefix(SyncCursor(at), "2.")
	for _, tc := range []struct{ token, want string }{
		{SyncCursor(at), at},
		{"3." + payload, at},
		{"17." + payload, at},
		{"", ""},
		{"x." + payload, ""},
		{"." + payload, ""},
		{"3.not-base64!", ""},
	} {
		if got := CursorTime(tc.token); got != tc.want {
			t.Errorf("CursorTime(%q) = %q, want %q", tc.token, got, tc.want)
		}
	}
}

func TestDestroyTimeDecodesVersion(t *testing.T) {
	tests := []struct{ version, want string }{
		{"eyJsYXN0X2Rlc3Ryb3lfdGltZSI6IjIwMjYtMTAtMDVUMTU6MzU6MDguNzgxKzA4OjAwIn0", "2026-10-05T15:35:08.781+08:00"},
		{"", ""},
		{"not base64!", ""},
		{base64.RawURLEncoding.EncodeToString([]byte(`[1,2]`)), ""},
	}
	for _, tc := range tests {
		if got := DestroyTime(tc.version); got != tc.want {
			t.Errorf("DestroyTime(%q) = %q, want %q", tc.version, got, tc.want)
		}
	}
}

// An access token can expire between two calls. The client must refresh and
// replay once, transparently, or every long-running view breaks hourly.
func TestUnauthorizedTriggersOneRefreshAndReplay(t *testing.T) {
	var calls atomic.Int64
	var seenTokens []string
	tokens := newStaticTokens("stale")

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenTokens = append(seenTokens, r.Header.Get("Authorization"))
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"unauthenticated","error_code":16}`)
			return
		}
		io.WriteString(w, `{"files":[]}`)
	}, tokens)

	if _, err := client.ListFiles(context.Background(), ListFilesParams{}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (original + replay)", got)
	}
	if got := tokens.invalidate.Load(); got != 1 {
		t.Errorf("Invalidate calls = %d, want 1", got)
	}
	if len(seenTokens) == 2 && seenTokens[1] != "Bearer refreshed" {
		t.Errorf("replay used %q, want the refreshed token", seenTokens[1])
	}
}

// A second 401 means the credentials are genuinely dead. Looping on refresh
// would hammer the auth server and still fail.
func TestUnauthorizedTwiceGivesUp(t *testing.T) {
	var calls atomic.Int64
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"unauthenticated"}`)
	}, newStaticTokens("stale"))

	_, err := client.ListFiles(context.Background(), ListFilesParams{})
	if !IsUnauthorized(err) {
		t.Fatalf("err = %v, want an unauthorized APIError", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want exactly 2", got)
	}
}

func TestGetRetriesServerErrors(t *testing.T) {
	var calls atomic.Int64
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, `{"files":[]}`)
	}, newStaticTokens("tok"))

	if _, err := client.ListFiles(context.Background(), ListFilesParams{}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want 3", got)
	}
}

// Replaying a mutation would create a second folder, a second offline task or
// a second copy. Only GETs may be retried.
func TestMutationsAreNotRetried(t *testing.T) {
	var calls atomic.Int64
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}, newStaticTokens("tok"))

	if _, err := client.CreateFolder(context.Background(), "", "movies"); err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

// shortStall makes the stall timeout d for one test.
func shortStall(t *testing.T, d time.Duration) {
	t.Helper()
	old := stallTimeout
	stallTimeout = d
	t.Cleanup(func() { stallTimeout = old })
}

// The API now and then sits on a request for 10 s and more while the same
// request sent again answers at once: a GET that gets no answer in time is
// dropped and sent again, and the caller only ever sees the answer.
func TestGetResendsAStalledRequest(t *testing.T) {
	shortStall(t, 100*time.Millisecond)
	var calls atomic.Int64
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			<-r.Context().Done() // never answers
			return
		}
		io.WriteString(w, `{"files":[]}`)
	}, newStaticTokens("tok"))

	start := time.Now()
	if _, err := client.ListFiles(context.Background(), ListFilesParams{}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %v, want the second answer right after the stall", took)
	}
}

// The last try waits past the stall: an answer that is only slow beats none.
func TestGetLastAttemptWaitsForASlowAnswer(t *testing.T) {
	shortStall(t, 50*time.Millisecond)
	var calls atomic.Int64
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"files":[]}`)
	}, newStaticTokens("tok"))

	if _, err := client.ListFiles(context.Background(), ListFilesParams{}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want 3", got)
	}
}

// A mutation is never cut short for being slow: sending it again could do it
// twice, so it waits for its one answer.
func TestMutationWaitsForASlowAnswer(t *testing.T) {
	shortStall(t, 50*time.Millisecond)
	var calls atomic.Int64
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(300 * time.Millisecond)
		io.WriteString(w, `{"file":{"id":"f1","kind":"drive#folder","name":"movies"}}`)
	}, newStaticTokens("tok"))

	if _, err := client.CreateFolder(context.Background(), "", "movies"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestQuotaRejectionIsSurfacedWithCounters(t *testing.T) {
	var observed []QuotaSnapshot
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("RateLimit-Quota-Used-downstream", "900")
		h.Set("RateLimit-Quota-Limit-downstream", "1000")
		h.Set("RateLimit-Quota-Reset-downstream", "2026-11-01T00:00:00+08:00")
		h.Set("RateLimit-Quota-Used-daily-downstream", "42")
		h.Set("Retry-After", "30")
		h.Set("x-limited", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":"CONNECTED_APPS_QUOTA_EXCEEDED","error_description":"quota reached"}`)
	}))
	defer srv.Close()

	client, err := New(Config{
		APIBaseURL: srv.URL,
		DeviceID:   "d",
		Logger:     quiet(),
		OnQuota:    func(q QuotaSnapshot) { observed = append(observed, q) },
	}, newStaticTokens("tok"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	_, err = client.ListFiles(context.Background(), ListFilesParams{})
	if !IsQuotaExceeded(err) {
		t.Fatalf("err = %v, want a quota rejection", err)
	}

	apiErr := asAPIError(err)
	if apiErr.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", apiErr.RetryAfter)
	}
	if apiErr.Quota == nil {
		t.Fatal("quota counters were dropped")
	}
	if got := apiErr.Quota.Dimensions["downstream"]; got.Used != 900 || got.Limit != 1000 {
		t.Errorf("downstream = %+v, want used 900 of 1000", got)
	}
	// The dimension name itself contains a dash; splitting on the last one
	// would yield "daily" and lose the axis.
	if got := apiErr.Quota.Dimensions["daily-downstream"]; got.Used != 42 {
		t.Errorf("daily-downstream = %+v, want used 42", got)
	}
	if !apiErr.Quota.Throttled {
		t.Error("Throttled = false despite x-limited: 1")
	}
	if len(observed) != 1 {
		t.Errorf("OnQuota fired %d times, want 1", len(observed))
	}
}

// Risk control rejections must be distinguishable: they need new credentials,
// not a retry.
func TestCaptchaRejectionIsClassified(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"captcha_invalid","error_code":9,"error_description":"client info expired"}`)
	}, newStaticTokens("tok"))

	_, err := client.ListFiles(context.Background(), ListFilesParams{})
	if !IsCaptchaRequired(err) {
		t.Fatalf("err = %v, want a captcha error", err)
	}
	if !strings.Contains(err.Error(), "client info expired") {
		t.Errorf("error text lost the description: %v", err)
	}
}

func TestMoveSendsIDsAndDestination(t *testing.T) {
	var body map[string]any
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/drive/v1/files:batchMove" {
			t.Errorf("path = %q", r.URL.Path)
		}
		io.WriteString(w, `{}`)
	}, newStaticTokens("tok"))

	if err := client.Move(context.Background(), []string{"a", "b"}, "dest"); err != nil {
		t.Fatalf("move: %v", err)
	}
	ids, _ := body["ids"].([]any)
	if len(ids) != 2 || ids[0] != "a" {
		t.Errorf("ids = %v", body["ids"])
	}
	to, _ := body["to"].(map[string]any)
	if to["parent_id"] != "dest" {
		t.Errorf("to = %v, want parent_id dest", body["to"])
	}
}

func TestCreateOfflineTaskBody(t *testing.T) {
	var body map[string]any
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"task":{"id":"t1","phase":"PHASE_TYPE_RUNNING","progress":12,"file_size":"1024"},
			"file":{"id":"f1","name":"ubuntu.iso"}}`)
	}, newStaticTokens("tok"))

	got, err := client.CreateOfflineTask(context.Background(), OfflineTaskParams{URL: "magnet:?xt=urn:btih:abc"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if body["kind"] != KindFile || body["upload_type"] != uploadTypeURL {
		t.Errorf("body kind/upload_type = %v/%v", body["kind"], body["upload_type"])
	}
	if urlField, _ := body["url"].(map[string]any); urlField["url"] != "magnet:?xt=urn:btih:abc" {
		t.Errorf("url = %v", body["url"])
	}
	// Without an explicit parent the server must be told to use its managed
	// download folder, otherwise the task lands in the drive root.
	if body["folder_type"] != FolderTypeDownload {
		t.Errorf("folder_type = %v, want %q", body["folder_type"], FolderTypeDownload)
	}
	if got.Task.ID != "t1" || got.Task.FileSize != 1024 || got.File.Name != "ubuntu.iso" {
		t.Errorf("decoded = %+v", got)
	}
}

func TestDeleteTasksRequiresExplicitFileDeletion(t *testing.T) {
	var query string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}, newStaticTokens("tok"))

	if err := client.DeleteTasks(context.Background(), []string{"t1", "t2"}, false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(query, "task_ids=t1&task_ids=t2") {
		t.Errorf("query = %q, want repeated task_ids", query)
	}
	if !strings.Contains(query, "delete_files=false") {
		t.Errorf("query = %q, want delete_files=false", query)
	}
}

func TestInt64AcceptsBothEncodings(t *testing.T) {
	var out struct {
		A Int64 `json:"a"`
		B Int64 `json:"b"`
		C Int64 `json:"c"`
	}
	if err := json.Unmarshal([]byte(`{"a":"17179869184","b":42,"c":null}`), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.A != 17179869184 || out.B != 42 || out.C != 0 {
		t.Errorf("decoded = %d/%d/%d", out.A, out.B, out.C)
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(encoded) != `{"a":17179869184,"b":42,"c":0}` {
		t.Errorf("re-encoded as %s, want plain numbers for the frontend", encoded)
	}
}

func parseQuery(raw string) (map[string]string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	flat := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) > 0 {
			flat[k] = v[0]
		}
	}
	return flat, nil
}

// Risk control rejects a stale captcha token with HTTP 400 and an envelope,
// not with a status the retry logic would act on. The client has to mint a
// replacement and replay once, or every long session dies at the first
// rejection.
func TestCaptchaRejectionIsRecovered(t *testing.T) {
	var calls atomic.Int64
	var sentTokens []string
	client, _, captcha := newTestClientWithCaptcha(t, func(w http.ResponseWriter, r *http.Request) {
		sentTokens = append(sentTokens, r.Header.Get("X-Captcha-Token"))
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"captcha_invalid","error_code":9,"error_description":"client info expired"}`)
			return
		}
		io.WriteString(w, `{"files":[]}`)
	}, newStaticTokens("tok"), &fakeCaptcha{token: "captcha-0"})

	if _, err := client.ListFiles(context.Background(), ListFilesParams{}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (original + replay)", got)
	}

	refreshes, actions := captcha.stats()
	if refreshes != 1 {
		t.Errorf("captcha refreshes = %d, want 1", refreshes)
	}
	if len(actions) != 1 || actions[0] != "GET:/drive/v1/files" {
		t.Errorf("captcha action = %v, want the method and path of the rejected call", actions)
	}
	if len(sentTokens) == 2 && sentTokens[1] == sentTokens[0] {
		t.Errorf("the replay reused the rejected token %q", sentTokens[1])
	}
}

// The same rejection can arrive inside a 200 body. Missing it there would
// hand the caller an all-defaults struct that reads as an empty folder.
func TestCaptchaRejectionInsideSuccessBody(t *testing.T) {
	var calls atomic.Int64
	client, _, captcha := newTestClientWithCaptcha(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			io.WriteString(w, `{"error":"captcha_required","error_code":9}`)
			return
		}
		io.WriteString(w, `{"files":[{"kind":"drive#file","id":"f1","name":"a.mkv"}]}`)
	}, newStaticTokens("tok"), &fakeCaptcha{token: "captcha-0"})

	list, err := client.ListFiles(context.Background(), ListFilesParams{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Files) != 1 {
		t.Errorf("files = %d, want the replayed result", len(list.Files))
	}
	if refreshes, _ := captcha.stats(); refreshes != 1 {
		t.Errorf("captcha refreshes = %d, want 1", refreshes)
	}
}

// A second rejection over a fresh token is a real failure. Looping would
// hammer the captcha endpoint, which is itself rate limited.
func TestCaptchaRejectionTwiceGivesUp(t *testing.T) {
	var calls atomic.Int64
	client, _, captcha := newTestClientWithCaptcha(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"captcha_invalid","error_code":9}`)
	}, newStaticTokens("tok"), &fakeCaptcha{token: "captcha-0"})

	_, err := client.ListFiles(context.Background(), ListFilesParams{})
	if !IsCaptchaRequired(err) {
		t.Fatalf("err = %v, want a captcha error", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want exactly 2", got)
	}
	if refreshes, _ := captcha.stats(); refreshes != 1 {
		t.Errorf("captcha refreshes = %d, want 1", refreshes)
	}
}

// Code 9 is not captcha-exclusive. Treating an unrelated failure as one costs
// a captcha handshake and a retry before the real error reaches the caller.
func TestNonCaptchaCodeNineIsNotRetried(t *testing.T) {
	var calls atomic.Int64
	client, _, captcha := newTestClientWithCaptcha(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"file_in_recycle_bin","error_code":9}`)
	}, newStaticTokens("tok"), &fakeCaptcha{token: "captcha-0"})

	_, err := client.GetFile(context.Background(), "f1", GetFileParams{})
	if IsCaptchaRequired(err) {
		t.Fatalf("err = %v, want it classified as a plain API error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
	if refreshes, _ := captcha.stats(); refreshes != 0 {
		t.Errorf("captcha refreshes = %d, want 0", refreshes)
	}
}

type observedTokenInvalidation struct {
	*TokenManager
	entered chan struct{}
}

func (s *observedTokenInvalidation) Invalidate(ctx context.Context, accessToken string) error {
	close(s.entered)
	return s.TokenManager.Invalidate(ctx, accessToken)
}

func TestClient401CancellationDoesNotWaitForAnotherRefresh(t *testing.T) {
	apiEntered := make(chan struct{})
	send401 := make(chan struct{})
	release401 := sync.OnceFunc(func() { close(send401) })
	refreshEntered := make(chan struct{})
	release := make(chan struct{})
	releaseRefresh := sync.OnceFunc(func() { close(release) })
	var apiCalls, refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/drive/v1/files/f1":
			if apiCalls.Add(1) == 1 {
				close(apiEntered)
				select {
				case <-send401:
					w.WriteHeader(http.StatusUnauthorized)
					writeJSON(w, map[string]any{"error": "unauthorized"})
				case <-r.Context().Done():
				}
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer renewed-access" {
				t.Errorf("subsequent Authorization = %q", got)
			}
			writeJSON(w, map[string]any{"id": "f1"})
		case "/v1/auth/token":
			if refreshes.Add(1) != 1 {
				t.Error("unexpected second refresh")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			close(refreshEntered)
			select {
			case <-release:
				writeJSON(w, map[string]any{
					"access_token": "renewed-access", "refresh_token": "renewed-refresh", "expires_in": 7200,
				})
			case <-r.Context().Done():
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	defer release401()
	defer releaseRefresh()
	m := NewTokenManager(TokenManagerConfig{AuthBaseURL: srv.URL, DeviceID: "d", Store: &MemoryStore{}})
	if err := m.Seed(Token{
		AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	source := &observedTokenInvalidation{TokenManager: m, entered: make(chan struct{})}
	client, err := New(Config{APIBaseURL: srv.URL, DeviceID: "d", Captcha: m.Captcha()}, source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requestDone := make(chan error, 1)
	go func() {
		_, err := client.GetFile(ctx, "f1", GetFileParams{})
		requestDone <- err
	}()
	select {
	case <-apiEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach API")
	}
	if err := m.Invalidate(context.Background(), "old-access"); err != nil {
		t.Fatal(err)
	}
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	ownerDone := make(chan error, 1)
	go func() {
		_, err := m.Token(ownerCtx)
		ownerDone <- err
	}()
	select {
	case <-refreshEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("independent refresh did not reach HTTP")
	}
	release401()
	select {
	case <-source.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("401 did not reach token invalidation")
	}
	cancel()
	select {
	case err := <-requestDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("API request error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled API request waited for unrelated refresh")
	}
	select {
	case err := <-ownerDone:
		t.Fatalf("independent owner ended before release: %v", err)
	default:
	}
	releaseRefresh()
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("independent owner did not finish")
	}
	if _, err := client.GetFile(context.Background(), "f1", GetFileParams{}); err != nil {
		t.Fatalf("subsequent API request: %v", err)
	}
	if refreshes.Load() != 1 {
		t.Errorf("refreshes = %d, want 1", refreshes.Load())
	}
}
