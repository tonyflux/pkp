package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/ai"
)

const modelKey = "sk-model-TEST-secret-8E4C"

func TestMain(m *testing.M) {
	// A model service that refuses is asked again; tests need not wait.
	ai.RetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	os.Exit(m.Run())
}

// lockedBuffer collects a service's log across goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newModelService is a service with a stand-in keychain and a log the test
// can read; nothing is signed in.
func newModelService(t *testing.T, vault PasswordVault) (*DriveService, *lockedBuffer) {
	t.Helper()
	logs := &lockedBuffer{}
	svc := NewDriveService(DriveOptions{
		ConfigDir:   t.TempDir(),
		NoIndexSync: true,
		Passwords:   vault,
		Logger:      slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, nil)
	return svc, logs
}

// modelServer is an OpenAI-compatible stand-in that accepts modelKey only
// and echoes any other key back in its refusal, the way some services do.
func modelServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key != modelKey {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"Incorrect API key provided: `+key+`"}}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["response_format"] == nil {
			io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"route\": \"search\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":321,"completion_tokens":12}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func configureModel(t *testing.T, svc *DriveService, baseURL string) {
	t.Helper()
	if _, err := svc.SetAIConnection("openai", baseURL, "gpt-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetAIKey(modelKey); err != nil {
		t.Fatal(err)
	}
}

var modelRequest = AIRequest{
	System:       "你是 PKP 的助手。",
	Messages:     []AIMessage{{Role: "user", Content: "找上周的视频"}},
	OutputName:   "understand",
	OutputSchema: `{"type":"object","properties":{"route":{"type":"string"}}}`,
}

// The key lives in the keychain only: settings.json names the endpoint it
// belongs to, the page sees its last four characters, and neither the log
// nor any error ever carries it — not even when a service echoes it back.
func TestModelKeyStaysInTheKeychain(t *testing.T) {
	vault := &memoryVault{}
	svc, logs := newModelService(t, vault)
	srv := modelServer(t)

	got := svc.AISettings()
	if got.Provider != "anthropic" || got.BaseURL != "https://api.openai.com/v1" || got.TokenLimit != 50000 || got.Naming != "plex" || !got.Keychain || got.Configured {
		t.Fatalf("defaults = %+v", got)
	}
	if _, err := svc.SetAIConnection(" openai ", srv.URL+"/", " gpt-test "); err != nil {
		t.Fatal(err)
	}
	got, err := svc.SetAIKey("  " + modelKey + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyHint != "••••8E4C" || !got.Configured || got.BaseURL != srv.URL || got.Model != "gpt-test" {
		t.Errorf("after saving the key: %+v", got)
	}
	if stored, _ := vault.Password("pkp-ai-api-key"); stored != modelKey {
		t.Errorf("keychain holds %q", stored)
	}
	if again := svc.AISettings(); again.KeyHint != "••••8E4C" {
		t.Errorf("hint read again: %q", again.KeyHint)
	}

	reply, err := svc.AIComplete("q1", modelRequest)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Output != `{"route":"search"}` || reply.Input != 321 || reply.OutputTokens != 12 {
		t.Errorf("reply = %+v", reply)
	}
	if res := svc.TestAIConnection(); !res.OK || res.Kind != "ok" {
		t.Errorf("test = %+v", res)
	}

	// A wrong key the service echoes back is scrubbed from the result.
	wrong := "sk-wrong-key-123456"
	if _, err := svc.SetAIKey(wrong); err != nil {
		t.Fatal(err)
	}
	res := svc.TestAIConnection()
	_, completeErr := svc.AIComplete("q2", modelRequest)
	if res.Kind != "auth" || res.Status != 401 || strings.Contains(res.Message, wrong) || !strings.Contains(res.Message, "Incorrect API key provided: ••••") {
		t.Errorf("test with a wrong key = %+v", res)
	}
	if completeErr == nil || strings.Contains(completeErr.Error(), wrong) || !strings.HasPrefix(completeErr.Error(), "API Key 无效或没有权限：") {
		t.Errorf("complete with a wrong key: %v", completeErr)
	}

	data, err := os.ReadFile(filepath.Join(svc.configDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{modelKey, wrong, "8E4C"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("settings.json holds %q: %s", secret, data)
		}
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log holds %q", secret)
		}
	}
	if !bytes.Contains(data, []byte(`"aiKeyFor":"openai `+srv.URL+`"`)) {
		t.Errorf("settings.json does not bind the key to its endpoint: %s", data)
	}

	// The stand-in listens on 127.0.0.1: without a key it still counts as
	// configured, the way a local Ollama does.
	if got, err := svc.SetAIKey(""); err != nil || got.KeyHint != "" || !got.Configured {
		t.Errorf("clearing the key: %+v %v", got, err)
	}
	if _, err := vault.Password("pkp-ai-api-key"); !errors.Is(err, ErrNoSavedPassword) {
		t.Errorf("cleared key still in the keychain: %v", err)
	}
	if got, _ := svc.SetAIKey("abc"); got.KeyHint != "••••" {
		t.Errorf("a short key's hint = %q, want nothing of it shown", got.KeyHint)
	}
	if _, err := svc.SetAIKey("sk two words"); err == nil {
		t.Error("a key with a space was accepted")
	}
}

// A key is bound to the provider and base URL it was entered for: changing
// either deletes it; changing the model, or the OpenAI address while
// Anthropic is chosen, keeps it.
func TestModelKeyFollowsItsEndpoint(t *testing.T) {
	vault := &memoryVault{}
	svc, _ := newModelService(t, vault)
	configureModel(t, svc, "https://api.deepseek.com/v1")

	if got, _ := svc.SetAIConnection("openai", "https://api.deepseek.com/v1", "deepseek-reasoner"); got.KeyHint == "" {
		t.Error("changing the model dropped the key")
	}
	got, err := svc.SetAIConnection("openai", "https://evil.example.com/v1", "deepseek-reasoner")
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyHint != "" || got.Configured {
		t.Errorf("after changing the base URL: %+v, want no key", got)
	}
	if _, err := vault.Password("pkp-ai-api-key"); !errors.Is(err, ErrNoSavedPassword) {
		t.Errorf("the key survived a base URL change: %v", err)
	}
	if _, err := svc.AIComplete("", modelRequest); err == nil || err.Error() != "还没有配置模型" {
		t.Errorf("complete without a key: %v", err)
	}
	// Going back does not bring it back.
	if got, _ := svc.SetAIConnection("openai", "https://api.deepseek.com/v1", "m"); got.KeyHint != "" {
		t.Errorf("the key came back: %+v", got)
	}

	if _, err := svc.SetAIKey(modelKey); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.SetAIConnection("anthropic", "https://api.deepseek.com/v1", "claude"); got.KeyHint != "" {
		t.Errorf("switching provider kept the key: %+v", got)
	}
	if _, err := svc.SetAIKey(modelKey); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.SetAIConnection("anthropic", "https://other.example.com/v1", "claude"); got.KeyHint == "" || !got.Configured {
		t.Errorf("an OpenAI address changed while Anthropic is chosen dropped the key: %+v", got)
	}

	for _, bad := range [][3]string{{"gemini", "", "m"}, {"openai", "ftp://x", "m"}, {"openai", "api.openai.com", "m"}, {"openai", "", "a\nb"}} {
		if _, err := svc.SetAIConnection(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("SetAIConnection%q accepted", bad)
		}
	}
	if got := svc.AISettings(); got.Provider != "anthropic" || got.KeyHint == "" {
		t.Errorf("a refused change changed the settings: %+v", got)
	}
}

// Without a keychain no key can be saved, and the page is told so.
func TestModelWithoutKeychain(t *testing.T) {
	svc, _ := newModelService(t, &memoryVault{})
	svc.vault = nil
	if got := svc.AISettings(); got.Keychain {
		t.Errorf("settings = %+v, want no keychain", got)
	}
	if _, err := svc.SetAIKey(modelKey); err == nil || err.Error() != "这台电脑没有可用的钥匙串，无法保存 API Key" {
		t.Errorf("saving a key without a keychain: %v", err)
	}
	if res := svc.TestAIConnection(); res.OK || res.Kind != "config" {
		t.Errorf("test without a key = %+v", res)
	}
}

// Per-request usage limit is held to [1000, 2000000]; a hand-edited file is settled
// into it. Naming takes the three styles only and keeps custom templates.
func TestModelTokenLimitAndNaming(t *testing.T) {
	svc, _ := newModelService(t, &memoryVault{})
	for _, bad := range []int{-1, 999, 2000001} {
		if _, err := svc.SetAITokenLimit(bad); err == nil {
			t.Errorf("token limit %d accepted", bad)
		}
	}
	// 0 is Unlimited, and survives a read back from settings.json.
	for _, ok := range []int{1000, 2000000, 0, 80000} {
		if got, err := svc.SetAITokenLimit(ok); err != nil || got.TokenLimit != ok {
			t.Errorf("token limit %d: %+v %v", ok, got.TokenLimit, err)
		}
	}
	if _, err := svc.SetAITokenLimit(0); err != nil {
		t.Fatal(err)
	}
	if got := svc.AISettings().TokenLimit; got != 0 {
		t.Errorf("Unlimited read back as %d", got)
	}
	if _, err := svc.SetAITokenLimit(80000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.settingsPath(), []byte(`{"aiTokenLimit":5,"aiProvider":"x","aiNaming":"y","aiBaseUrl":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := svc.AISettings(); got.TokenLimit != 1000 || got.Provider != "anthropic" || got.Naming != "plex" || got.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("hand-edited settings = %+v", got)
	}

	if _, err := svc.SetAINaming("emby", "", ""); err == nil {
		t.Error("an unknown naming style was accepted")
	}
	got, err := svc.SetAINaming("custom", "{剧名}/S{季:2}", "{片名} ({年份})")
	if err != nil || got.Naming != "custom" || got.SeriesTemplate != "{剧名}/S{季:2}" || got.MovieTemplate != "{片名} ({年份})" {
		t.Errorf("custom naming: %+v %v", got, err)
	}
	if got, _ := svc.SetAINaming("chinese", "{剧名}/S{季:2}", "{片名} ({年份})"); got.Naming != "chinese" || got.SeriesTemplate == "" {
		t.Errorf("a preset lost the custom templates: %+v", got)
	}
}

// Test connection tells a working connection, a refused key and an address that
// does not answer apart, with the service's own words.
func TestModelConnectionTest(t *testing.T) {
	svc, _ := newModelService(t, &memoryVault{})
	if res := svc.TestAIConnection(); res.Kind != "config" || res.Message == "" {
		t.Errorf("unconfigured: %+v", res)
	}
	srv := modelServer(t)
	configureModel(t, svc, srv.URL)
	if res := svc.TestAIConnection(); !res.OK {
		t.Errorf("good key: %+v", res)
	}

	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"model is overloaded"}}`)
	}))
	defer refusing.Close()
	configureModel(t, svc, refusing.URL)
	if res := svc.TestAIConnection(); res.Kind != "server" || res.Status != 503 || res.Message != "model is overloaded" {
		t.Errorf("server error: %+v", res)
	}
	if _, err := svc.AIComplete("", modelRequest); err == nil || err.Error() != "模型服务返回错误（HTTP 503）：model is overloaded" {
		t.Errorf("complete against a server error: %v", err)
	}

	// The webview tells a content check's refusal by how it starts.
	sensitive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"error":{"message":"input new_sensitive (1026)"}}`)
	}))
	defer sensitive.Close()
	configureModel(t, svc, sensitive.URL)
	if _, err := svc.AIComplete("", modelRequest); err == nil || err.Error() != "模型服务的内容审核拒绝了这次请求：input new_sensitive (1026)" {
		t.Errorf("complete against a content check: %v", err)
	}
	if res := svc.TestAIConnection(); res.Kind != "server" || res.Status != 422 {
		t.Errorf("Test connection against a content check: %+v", res)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	ln.Close()
	configureModel(t, svc, closed)
	if res := svc.TestAIConnection(); res.OK || res.Kind != "unreachable" || res.Message == "" {
		t.Errorf("closed port: %+v", res)
	}
	if _, err := svc.AIComplete("", modelRequest); err == nil || !strings.HasPrefix(err.Error(), "连不上模型服务：") {
		t.Errorf("complete against a closed port: %v", err)
	}
}

// AICancel calls off a request the service is still working on; the call
// returns Canceled at once and its id is free again.
func TestModelCancel(t *testing.T) {
	arrived := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server notices the client leaving only once the body is read.
		io.Copy(io.Discard, r.Body)
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()
	svc, _ := newModelService(t, &memoryVault{})
	configureModel(t, svc, srv.URL)

	done := make(chan error, 1)
	go func() {
		_, err := svc.AIComplete("plan-1", modelRequest)
		done <- err
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the service")
	}
	if _, err := svc.AIComplete("plan-1", modelRequest); err == nil {
		t.Error("a second call under an id in flight was accepted")
	}
	svc.AICancel("other")
	svc.AICancel("plan-1")
	select {
	case err := <-done:
		if err == nil || err.Error() != "已取消" {
			t.Errorf("cancelled call: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AICancel did not end the call")
	}
	svc.aiCallsMu.Lock()
	left := len(svc.aiCalls)
	svc.aiCallsMu.Unlock()
	if left != 0 {
		t.Errorf("%d calls still registered", left)
	}
}

// Before sending to AI is each account's own and each level's own: Always allow for one
// account's metadata leaves the other account, and its own content level,
// asking; the choices survive a relaunch in accounts.json.
func TestModelConsentIsPerAccount(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("a@example.com", "pw-a", "user-a")
	api.add("b@example.com", "pw-b", "user-b")
	vault := &memoryVault{}
	dir := t.TempDir()
	svc := startAccounts(t, dir, api, vault)
	a := signIn(t, svc, SignInRequest{Login: "a@example.com", Password: "pw-a"}).Account
	b := signIn(t, svc, SignInRequest{Login: "b@example.com", Password: "pw-b"}).Account
	if a.AIConsent || b.AIConsent || a.AIContentConsent || b.AIContentConsent {
		t.Fatalf("new accounts consent by default: %+v %+v", a, b)
	}

	if err := svc.SetAIConsent(a.ID, "metadata", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAIConsent(b.ID, "content", true); err != nil {
		t.Fatal(err)
	}
	// consent is "<metadata> <content>" per account.
	consent := func(svc *DriveService) map[string]string {
		out := map[string]string{}
		for _, acc := range svc.Accounts() {
			out[acc.ID] = fmt.Sprint(acc.AIConsent, " ", acc.AIContentConsent)
		}
		return out
	}
	if got := consent(svc); got[a.ID] != "true false" || got[b.ID] != "false true" {
		t.Errorf("after consenting: %v", got)
	}
	var file accountsFile
	data, err := os.ReadFile(filepath.Join(dir, accountsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for _, rec := range file.Accounts {
		if rec.AIConsent != (rec.Key == a.ID) || rec.AIContentConsent != (rec.Key == b.ID) {
			t.Errorf("accounts.json: %s aiConsent %v aiContentConsent %v", rec.Login, rec.AIConsent, rec.AIContentConsent)
		}
	}
	if !bytes.Contains(data, []byte(`"aiContentConsent": true`)) {
		t.Errorf("accounts.json = %s", data)
	}

	relaunched := startAccounts(t, dir, api, vault)
	if got := consent(relaunched); got[a.ID] != "true false" || got[b.ID] != "false true" {
		t.Errorf("after relaunch: %v", got)
	}
	if err := relaunched.SetAIConsent(a.ID, "metadata", false); err != nil {
		t.Fatal(err)
	}
	if got := consent(relaunched); got[a.ID] != "false false" || got[b.ID] != "false true" {
		t.Errorf("after withdrawing: %v", got)
	}
	if err := relaunched.SetAIConsent("nobody", "metadata", true); !errors.Is(err, errNoSuchAccount) {
		t.Errorf("unknown account: %v", err)
	}
	for _, level := range []string{"", "all", "Metadata"} {
		if err := relaunched.SetAIConsent(a.ID, level, true); !errors.Is(err, errAIConsentLevel) {
			t.Errorf("level %q: %v", level, err)
		}
	}
	if got := consent(relaunched); got[a.ID] != "false false" {
		t.Errorf("a refused level changed the consent: %v", got)
	}
}
