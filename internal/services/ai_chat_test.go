package services

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// chatService is an OpenAI-compatible stand-in that records each request's
// body and Authorization header and answers with handle.
type chatService struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
	auth   []string
}

func newChatService(t *testing.T, handle func(w http.ResponseWriter, body map[string]any)) *chatService {
	t.Helper()
	c := &chatService{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		c.mu.Lock()
		c.bodies = append(c.bodies, body)
		c.auth = append(c.auth, strings.Join(r.Header.Values("Authorization"), ","))
		c.mu.Unlock()
		handle(w, body)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *chatService) requests() ([]map[string]any, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.bodies), slices.Clone(c.auth)
}

func writeEvents(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		io.WriteString(w, "data: "+e+"\n\n")
		w.(http.Flusher).Flush()
	}
}

// captureDeltas collects what AIChat streams to the webview.
func captureDeltas(t *testing.T) func() []AIDelta {
	t.Helper()
	var mu sync.Mutex
	var got []AIDelta
	saved := emitAIDelta
	emitAIDelta = func(d AIDelta) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	}
	t.Cleanup(func() { emitAIDelta = saved })
	return func() []AIDelta {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

var chatTools = []AITool{{
	Name:        "search_files",
	Description: "按条件找文件",
	Schema:      `{"type":"object","properties":{"keyword":{"type":"string"}}}`,
}}

func hasToolTurn(body map[string]any) bool {
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		if mm, _ := m.(map[string]any); mm["role"] == "tool" {
			return true
		}
	}
	return false
}

// A conversation step streams its text to the webview under the call's
// id, in order, and hands back the model's tool call; the next step sends
// the call and its result and gets the final answer.
func TestAIChatStreamsAndCallsTools(t *testing.T) {
	deltas := captureDeltas(t)
	srv := newChatService(t, func(w http.ResponseWriter, body map[string]any) {
		if !hasToolTurn(body) {
			writeEvents(w,
				`{"choices":[{"delta":{"content":"我查"}}]}`,
				`{"choices":[{"delta":{"content":"一下"}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search_files","arguments":"{\"keyword\":"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":" \"猫\"}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":18}}`,
				`[DONE]`)
			return
		}
		writeEvents(w,
			`{"choices":[{"delta":{"content":"找到了"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":150,"completion_tokens":4}}`,
			`[DONE]`)
	})
	svc, _ := newModelService(t, &memoryVault{})
	configureModel(t, svc, srv.URL)

	user := AIChatMessage{Role: "user", Content: "找猫的视频"}
	reply, err := svc.AIChat("turn-1", AIChatRequest{System: "你是网盘助手。", Messages: []AIChatMessage{user}, Tools: chatTools, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "我查一下" || reply.Stop != "tool" || reply.Input != 120 || reply.OutputTokens != 18 || len(reply.ToolCalls) != 1 {
		t.Fatalf("reply = %+v", reply)
	}
	if c := reply.ToolCalls[0]; c != (AIToolCall{ID: "call_1", Name: "search_files", Arguments: `{"keyword":"猫"}`}) {
		t.Errorf("call = %+v", c)
	}
	if got := deltas(); !slices.Equal(got, []AIDelta{{ID: "turn-1", Text: "我查"}, {ID: "turn-1", Text: "一下"}}) {
		t.Errorf("deltas = %+v", got)
	}

	reply, err = svc.AIChat("turn-1", AIChatRequest{
		System: "你是网盘助手。",
		Messages: []AIChatMessage{
			user,
			{Role: "assistant", Content: reply.Text, ToolCalls: reply.ToolCalls},
			{Role: "tool", ToolCallID: "call_1", Content: "f1 猫.mp4 · 1.2 GB"},
		},
		Tools:  chatTools,
		Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "找到了" || reply.Stop != "end" || reply.Input != 150 || len(reply.ToolCalls) != 0 {
		t.Errorf("final reply = %+v", reply)
	}
	bodies, auth := srv.requests()
	data, _ := json.Marshal(bodies[1]["messages"])
	for _, want := range []string{
		`"tool_calls":[{"function":{"arguments":"{\"keyword\":\"猫\"}","name":"search_files"},"id":"call_1","type":"function"}]`,
		`{"content":"f1 猫.mp4 · 1.2 GB","role":"tool","tool_call_id":"call_1"}`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("second request messages %s lack %s", data, want)
		}
	}
	if auth[0] != "Bearer "+modelKey {
		t.Errorf("Authorization = %q", auth[0])
	}
	svc.aiCallsMu.Lock()
	left := len(svc.aiCalls)
	svc.aiCallsMu.Unlock()
	if left != 0 {
		t.Errorf("%d calls still registered", left)
	}
}

// A service that refuses tools fails with the words the webview matches
// on, and is remembered in settings.json as taking none — for that model
// only. The key it echoes reaches neither the error nor the log.
func TestAINoToolsIsRemembered(t *testing.T) {
	srv := newChatService(t, func(w http.ResponseWriter, body map[string]any) {
		if body["tools"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"model does not support tools (sent with `+modelKey+`)"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	})
	svc, logs := newModelService(t, &memoryVault{})
	configureModel(t, svc, srv.URL)

	_, err := svc.AIChat("x", AIChatRequest{Messages: []AIChatMessage{{Role: "user", Content: "找猫"}}, Tools: chatTools})
	if err == nil || err.Error() != "这个模型不支持工具调用" {
		t.Fatalf("error = %v", err)
	}
	if got := svc.AISettings(); got.Tools != "no" || got.Stream != "" {
		t.Errorf("settings after the refusal: %+v", got)
	}
	data, err := os.ReadFile(svc.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"aiCapabilities":{"openai `+srv.URL+` gpt-test":{"tools":"no"}}`) {
		t.Errorf("settings.json = %s", data)
	}
	if strings.Contains(logs.String(), modelKey) || strings.Contains(string(data), modelKey) {
		t.Error("the key leaked into the log or settings.json")
	}

	// Without tools the same service answers.
	reply, err := svc.AIChat("", AIChatRequest{Messages: []AIChatMessage{{Role: "user", Content: "你好"}}})
	if err != nil || reply.Text != "OK" || reply.Stop != "end" {
		t.Errorf("plain chat: %+v %v", reply, err)
	}

	if got, _ := svc.SetAIConnection("openai", srv.URL, "other-model"); got.Tools != "" {
		t.Errorf("another model inherited tools=no: %+v", got)
	}
	if got, _ := svc.SetAIConnection("openai", srv.URL, "gpt-test"); got.Tools != "no" {
		t.Errorf("back to the model: %+v", got)
	}
}

// Test connection also asks whether the service takes tools and streams, and keeps
// the answers for that provider, address and model.
func TestAIConnectionProbesCapabilities(t *testing.T) {
	capable := newChatService(t, func(w http.ResponseWriter, body map[string]any) {
		if body["stream"] == true {
			writeEvents(w, `{"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}`, `[DONE]`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	})
	svc, _ := newModelService(t, &memoryVault{})
	configureModel(t, svc, capable.URL)
	if got := svc.AISettings(); got.Tools != "" || got.Stream != "" {
		t.Errorf("untested: %+v", got)
	}
	if res := svc.TestAIConnection(); !res.OK || res.Tools != "yes" || res.Stream != "yes" {
		t.Errorf("capable service: %+v", res)
	}
	if got := svc.AISettings(); got.Tools != "yes" || got.Stream != "yes" {
		t.Errorf("settings after the test: %+v", got)
	}
	bodies, _ := capable.requests()
	var offered bool
	for _, b := range bodies {
		offered = offered || b["tools"] != nil
	}
	if len(bodies) != 3 || !offered {
		t.Errorf("%d requests, tools offered %v: want the test and two probes", len(bodies), offered)
	}

	plain := newChatService(t, func(w http.ResponseWriter, body map[string]any) {
		switch {
		case body["stream"] == true:
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"stream is not supported"}}`)
		case body["tools"] != nil:
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"detail":"Extra inputs are not permitted: tools"}`)
		default:
			io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
		}
	})
	configureModel(t, svc, plain.URL)
	if res := svc.TestAIConnection(); !res.OK || res.Tools != "no" || res.Stream != "no" {
		t.Errorf("plain service: %+v", res)
	}
	data, _ := os.ReadFile(svc.settingsPath())
	for _, want := range []string{
		`"openai ` + capable.URL + ` gpt-test":{"tools":"yes","stream":"yes"}`,
		`"openai ` + plain.URL + ` gpt-test":{"tools":"no","stream":"no"}`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("settings.json lacks %s: %s", want, data)
		}
	}
}

// An OpenAI-compatible service on this computer is configured without a
// key, and asked without an Authorization header; a remote one, or
// Anthropic, still needs a key.
func TestAIKeylessLocalService(t *testing.T) {
	srv := newChatService(t, func(w http.ResponseWriter, body map[string]any) {
		if body["response_format"] != nil {
			io.WriteString(w, `{"choices":[{"message":{"content":"{\"route\":\"search\"}"},"finish_reason":"stop"}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"<think>嗯</think>你好"},"finish_reason":"stop"}]}`)
	})
	svc, _ := newModelService(t, &memoryVault{})
	got, err := svc.SetAIConnection("openai", srv.URL+"/v1", "qwen2.5")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Configured || got.KeyHint != "" {
		t.Errorf("local service: %+v", got)
	}
	if res := svc.TestAIConnection(); !res.OK {
		t.Errorf("test: %+v", res)
	}
	reply, err := svc.AIChat("", AIChatRequest{Messages: []AIChatMessage{{Role: "user", Content: "你好"}}})
	if err != nil || reply.Text != "你好" {
		t.Errorf("chat: %+v %v", reply, err)
	}
	if out, err := svc.AIComplete("", modelRequest); err != nil || out.Output != `{"route":"search"}` {
		t.Errorf("complete: %+v %v", out, err)
	}
	_, auth := srv.requests()
	for i, a := range auth {
		if a != "" {
			t.Errorf("request %d sent Authorization %q", i, a)
		}
	}

	if got, _ := svc.SetAIConnection("openai", "https://api.deepseek.com/v1", "deepseek-chat"); got.Configured {
		t.Errorf("remote service without a key: %+v", got)
	}
	if _, err := svc.AIChat("", AIChatRequest{Messages: []AIChatMessage{{Role: "user", Content: "x"}}}); err == nil || err.Error() != "还没有配置模型" {
		t.Errorf("chat without a key: %v", err)
	}
	if got, _ := svc.SetAIConnection("anthropic", srv.URL, "claude"); got.Configured {
		t.Errorf("Anthropic without a key: %+v", got)
	}
}

// AIChat shares AIComplete's ids: one in flight refuses the other, and
// AICancel ends it.
func TestAIChatCancel(t *testing.T) {
	arrived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()
	svc, _ := newModelService(t, &memoryVault{})
	configureModel(t, svc, srv.URL)

	done := make(chan error, 1)
	go func() {
		_, err := svc.AIChat("turn-9", AIChatRequest{Messages: []AIChatMessage{{Role: "user", Content: "x"}}, Stream: true})
		done <- err
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the service")
	}
	if _, err := svc.AIComplete("turn-9", modelRequest); err == nil {
		t.Error("AIComplete under an AIChat's id was accepted")
	}
	svc.AICancel("turn-9")
	select {
	case err := <-done:
		if err == nil || err.Error() != "已取消" {
			t.Errorf("cancelled chat: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AICancel did not end the chat")
	}
}
