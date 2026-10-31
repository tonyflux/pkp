package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "sk-test-SECRET-8E4C"

var testSchema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)

func testRequest() Request {
	return Request{
		System:       "你是测试助手。",
		Messages:     []Message{{Role: "user", Content: "说你好"}},
		OutputName:   "reply",
		OutputSchema: testSchema,
	}
}

func TestMain(m *testing.M) {
	RetryDelays = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond}
	os.Exit(m.Run())
}

// fakeService answers every request with handle and records the bodies.
type fakeService struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
	paths  []string
	header []http.Header
}

func newFake(t *testing.T, handle func(w http.ResponseWriter, body map[string]any)) *fakeService {
	t.Helper()
	f := &fakeService{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.paths = append(f.paths, r.URL.Path)
		f.header = append(f.header, r.Header.Clone())
		f.mu.Unlock()
		handle(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeService) requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.bodies...)
}

func asError(t *testing.T, err error, kind string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v (%T), want an *ai.Error of kind %s", err, err, kind)
	}
	if e.Kind != kind {
		t.Fatalf("error kind = %q (%v), want %q", e.Kind, e, kind)
	}
	return e
}

// Anthropic answers through a forced tool: the request names the tool and
// forces it, carries the key and version headers and a max_tokens; the
// tool call's input is the output, compacted, with the service's usage.
func TestAnthropicForcedToolCall(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"好的"},{"type":"tool_use","id":"t1","name":"reply","input":{ "answer" : "你好" }}],"stop_reason":"tool_use","usage":{"input_tokens":120,"output_tokens":15}}`)
	})
	cfg := Config{Provider: Anthropic, BaseURL: f.URL + "/", Model: "claude-x", APIKey: testKey}
	resp, err := Complete(context.Background(), cfg, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Output) != `{"answer":"你好"}` || resp.Usage != (Usage{Input: 120, Output: 15}) {
		t.Errorf("response = %s %+v", resp.Output, resp.Usage)
	}
	if f.paths[0] != "/v1/messages" || f.header[0].Get("x-api-key") != testKey || f.header[0].Get("anthropic-version") != "2023-06-01" {
		t.Errorf("request to %s with headers %v", f.paths[0], f.header[0])
	}
	body := f.requests()[0]
	choice, _ := body["tool_choice"].(map[string]any)
	tools, _ := body["tools"].([]any)
	if choice["type"] != "tool" || choice["name"] != "reply" || len(tools) != 1 || body["max_tokens"] != float64(8192) || body["system"] != "你是测试助手。" {
		t.Errorf("request body = %v", body)
	}
	if tool, _ := tools[0].(map[string]any); tool["name"] != "reply" || tool["input_schema"] == nil {
		t.Errorf("tool = %v", tools[0])
	}
}

// stop_reason max_tokens is a truncated answer even when a tool call came
// back: its input is cut short.
func TestAnthropicMaxTokensIsTruncated(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		io.WriteString(w, `{"type":"message","content":[{"type":"tool_use","name":"reply","input":{}}],"stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":100}}`)
	})
	req := testRequest()
	req.MaxTokens = 100
	_, err := Complete(context.Background(), Config{Provider: Anthropic, BaseURL: f.URL, Model: "m", APIKey: testKey}, req)
	asError(t, err, KindTruncated)
	if f.requests()[0]["max_tokens"] != float64(100) {
		t.Errorf("max_tokens = %v, want the request's", f.requests()[0]["max_tokens"])
	}
}

func openAIFormat(body map[string]any) string {
	rf, _ := body["response_format"].(map[string]any)
	if rf == nil {
		return "none"
	}
	t, _ := rf["type"].(string)
	return t
}

// A service that refuses json_schema with a 400 naming it is asked again
// with json_object; the rung that worked is remembered for that base URL
// and model, so the next request goes straight to it. Every request
// carries the schema in its system prompt and no max_tokens.
func TestOpenAIFormatLadderIsRemembered(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		if openAIFormat(body) == "json_schema" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"response_format type json_schema is not supported","type":"invalid_request_error"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"answer\":\"hi\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":50,"completion_tokens":7}}`)
	})
	cfg := Config{Provider: OpenAI, BaseURL: f.URL, Model: "deepseek-chat", APIKey: testKey}
	for range 2 {
		resp, err := Complete(context.Background(), cfg, testRequest())
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Output) != `{"answer":"hi"}` || resp.Usage != (Usage{Input: 50, Output: 7}) {
			t.Errorf("response = %s %+v", resp.Output, resp.Usage)
		}
	}
	reqs := f.requests()
	var formats []string
	for _, b := range reqs {
		formats = append(formats, openAIFormat(b))
	}
	if strings.Join(formats, ",") != "json_schema,json_object,json_object" {
		t.Errorf("response_format per request = %v, want json_schema then json_object, then json_object straight away", formats)
	}
	if f.paths[0] != "/chat/completions" || f.header[0].Get("Authorization") != "Bearer "+testKey {
		t.Errorf("request to %s, Authorization %q", f.paths[0], f.header[0].Get("Authorization"))
	}
	for _, b := range reqs {
		if _, ok := b["max_tokens"]; ok {
			t.Errorf("OpenAI-compatible request sent max_tokens: %v", b)
		}
		msgs, _ := b["messages"].([]any)
		first, _ := msgs[0].(map[string]any)
		system, _ := first["content"].(string)
		if first["role"] != "system" || !strings.HasPrefix(system, "你是测试助手。\n\n") || !strings.Contains(system, schemaPrompt+`{"type":"object"`) {
			t.Errorf("system message = %v", first)
		}
	}
}

// A service that refuses response_format altogether ends on the rung
// without it; a 400 about something else does not step down.
func TestOpenAIFormatLadderEndsWithoutFormat(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		if openAIFormat(body) != "none" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"detail":"Unknown field: response_format"}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"answer\":\"x\"}"},"finish_reason":"stop"}]}`)
	})
	cfg := Config{Provider: OpenAI, BaseURL: f.URL, Model: "local", APIKey: testKey}
	if _, err := Complete(context.Background(), cfg, testRequest()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.requests()); n != 3 {
		t.Errorf("%d requests, want json_schema, json_object, none", n)
	}

	other := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"model not found"}}`)
	})
	_, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: other.URL, Model: "nope", APIKey: testKey}, testRequest())
	e := asError(t, err, KindServer)
	if e.Status != 400 || e.Message != "model not found" || len(other.requests()) != 1 {
		t.Errorf("error = %+v after %d requests, want the server's message after one", e, len(other.requests()))
	}
}

// Text answers are unwrapped: a <think> block, ```json fences, prose
// around the object; braces inside strings do not confuse the scan.
func TestModelOutputExtraction(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		content := "<think>先想想 {不是对象}</think>\n```json\n{\"answer\": \"a } b\"}\n```"
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}}})
		w.Write(data)
	})
	resp, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: f.URL, Model: "r1", APIKey: testKey}, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Output) != `{"answer":"a } b"}` {
		t.Errorf("output = %s", resp.Output)
	}

	for text, want := range map[string]string{
		`{"a":1}`:                   `{"a":1}`,
		"```\n{\"a\": [1, 2]}\n```": `{"a":[1,2]}`,
		"结果如下：{\"a\":\"{\\\"\"} 以上。":            `{"a":"{\""}`,
		"{broken {\"a\":2} tail":                `{"a":2}`,
		"<think>unclosed {\"a\":3}":             `{"a":3}`,
		"  <think>x</think>  {\"a\":{\"b\":4}}": `{"a":{"b":4}}`,
	} {
		got, ok := extractObject(text)
		if !ok || string(got) != want {
			t.Errorf("extractObject(%q) = %s %v, want %s", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "没有对象", "[1,2]", "{\"a\":"} {
		if got, ok := extractObject(text); ok {
			t.Errorf("extractObject(%q) = %s, want nothing", text, got)
		}
	}
}

// finish_reason length means the object is cut short; no object at all is
// an invalid answer that quotes what came back.
func TestModelTruncatedAndInvalid(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"answer\":\"cut"},"finish_reason":"length"}],"usage":{"prompt_tokens":5,"completion_tokens":9}}`)
	})
	resp, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: f.URL, Model: "m", APIKey: testKey}, testRequest())
	asError(t, err, KindTruncated)
	if resp.Usage.Output != 9 {
		t.Errorf("usage of a truncated answer = %+v, want it reported", resp.Usage)
	}

	g := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		io.WriteString(w, `{"choices":[{"message":{"content":"抱歉，我不能回答"},"finish_reason":"stop"}]}`)
	})
	_, err = Complete(context.Background(), Config{Provider: OpenAI, BaseURL: g.URL, Model: "m", APIKey: testKey}, testRequest())
	if e := asError(t, err, KindInvalid); !strings.Contains(e.Message, "抱歉，我不能回答") {
		t.Errorf("invalid message = %q, want the model's words", e.Message)
	}

	h := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"我直接回答"}],"stop_reason":"end_turn"}`)
	})
	_, err = Complete(context.Background(), Config{Provider: Anthropic, BaseURL: h.URL, Model: "m", APIKey: testKey}, testRequest())
	if e := asError(t, err, KindInvalid); !strings.Contains(e.Message, "我直接回答") {
		t.Errorf("no tool call: %q", e.Message)
	}
}

// 401 and 403 are a refused key, with the server's own message, on both
// providers and on Test as well as Complete.
func TestModelAuthRefusal(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})
	for _, p := range []Provider{Anthropic, OpenAI} {
		cfg := Config{Provider: p, BaseURL: f.URL, Model: "m", APIKey: testKey}
		_, err := Complete(context.Background(), cfg, testRequest())
		if e := asError(t, err, KindAuth); e.Status != 401 || e.Message != "invalid x-api-key" {
			t.Errorf("%s complete: %+v", p, e)
		}
		if e := asError(t, Test(context.Background(), cfg), KindAuth); e.Message != "invalid x-api-key" {
			t.Errorf("%s test: %+v", p, e)
		}
	}
}

// A port nobody listens on is unreachable, not a server error.
func TestModelUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	for _, p := range []Provider{Anthropic, OpenAI} {
		cfg := Config{Provider: p, BaseURL: "http://" + addr + "/v1", Model: "m", APIKey: testKey}
		_, err := Complete(context.Background(), cfg, testRequest())
		if e := asError(t, err, KindUnreachable); e.Message == "" {
			t.Errorf("%s: unreachable without a message", p)
		}
		asError(t, Test(context.Background(), cfg), KindUnreachable)
	}
}

// A server that echoes the key in its error never gets it into the
// message or the error text.
func TestModelErrorsScrubTheKey(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited for key `+testKey+` (Bearer `+testKey+`)"}}`)
	})
	for _, p := range []Provider{Anthropic, OpenAI} {
		cfg := Config{Provider: p, BaseURL: f.URL, Model: "m", APIKey: testKey}
		_, err := Complete(context.Background(), cfg, testRequest())
		e := asError(t, err, KindServer)
		if e.Status != 429 || strings.Contains(e.Message, testKey) || strings.Contains(err.Error(), testKey) || !strings.Contains(e.Message, "rate limited for key ••••") {
			t.Errorf("%s: %q", p, e.Message)
		}
	}

	// A long body is clipped after scrubbing, so no fragment of the key
	// survives at the cut either.
	long := strings.Repeat("x", messageLimit-10) + testKey + strings.Repeat("y", 100)
	g := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, long)
	})
	_, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: g.URL, Model: "m", APIKey: testKey}, testRequest())
	e := asError(t, err, KindServer)
	if len(e.Message) > messageLimit || strings.Contains(e.Message, testKey[:8]) {
		t.Errorf("clipped message: %d bytes, %q", len(e.Message), e.Message[len(e.Message)-40:])
	}
}

// A transient refusal (429, 5xx, Anthropic's 529) is asked again, up to
// len(RetryDelays) times, and the answer that does come is the one
// returned — streamed text included, once. A refusal that will not change
// (400, 401) is not repeated, and neither is Test Connection.
func TestCompleteRetriesTransientRefusals(t *testing.T) {
	var mu sync.Mutex
	refusals := map[string]int{}
	refuseFirst := func(status, times int) func(w http.ResponseWriter, body map[string]any) {
		key := fmt.Sprint(status, times)
		return func(w http.ResponseWriter, body map[string]any) {
			mu.Lock()
			n := refusals[key]
			refusals[key]++
			mu.Unlock()
			if n < times {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				io.WriteString(w, `{"error":{"message":"overloaded"}}`)
				return
			}
			if body["stream"] == true {
				sse(w, `{"choices":[{"delta":{"content":"好"},"finish_reason":"stop"}]}`, `[DONE]`)
				return
			}
			io.WriteString(w, `{"choices":[{"message":{"content":"{\"answer\":\"ok\"}"},"finish_reason":"stop"}]}`)
		}
	}

	for _, status := range []int{429, 500, 503, 529} {
		f := newFake(t, refuseFirst(status, 2))
		resp, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: f.URL, Model: "m", APIKey: testKey}, testRequest())
		if err != nil || string(resp.Output) != `{"answer":"ok"}` || len(f.requests()) != 3 {
			t.Errorf("HTTP %d twice: %s %v after %d requests, want the third answer", status, resp.Output, err, len(f.requests()))
		}
	}

	stream := newFake(t, refuseFirst(502, 1))
	var deltas []string
	req := chatRequest(Message{Role: RoleUser, Content: "x"})
	req.OnText = func(d string) { deltas = append(deltas, d) }
	resp, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: stream.URL, Model: "m", APIKey: testKey}, req)
	if err != nil || resp.Text != "好" || strings.Join(deltas, "") != "好" || len(stream.requests()) != 2 {
		t.Errorf("streamed after a 502: %+v %v %q after %d requests", resp, err, deltas, len(stream.requests()))
	}

	down := newFake(t, refuseFirst(503, 99))
	_, err = Complete(context.Background(), Config{Provider: Anthropic, BaseURL: down.URL, Model: "m", APIKey: testKey}, testRequest())
	if e := asError(t, err, KindServer); e.Status != 503 || e.Message != "overloaded" || len(down.requests()) != 1+len(RetryDelays) {
		t.Errorf("always 503: %+v after %d requests, want the refusal after %d", e, len(down.requests()), 1+len(RetryDelays))
	}
	before := len(down.requests())
	asError(t, Test(context.Background(), Config{Provider: Anthropic, BaseURL: down.URL, Model: "m", APIKey: testKey}), KindServer)
	if n := len(down.requests()) - before; n != 1 {
		t.Errorf("Test connection sent %d requests, want 1", n)
	}

	for _, status := range []int{400, 401} {
		f := newFake(t, refuseFirst(status, 99))
		_, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: f.URL, Model: "m", APIKey: testKey}, testRequest())
		if err == nil || len(f.requests()) != 1 {
			t.Errorf("HTTP %d: %v after %d requests, want the refusal after one", status, err, len(f.requests()))
		}
	}
}

// Test sends a plain request — no tools, no response_format — and checks
// the answer looks like the provider's.
func TestModelConnectionTest(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		if body["max_tokens"] != nil {
			io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	})
	for _, p := range []Provider{Anthropic, OpenAI} {
		if err := Test(context.Background(), Config{Provider: p, BaseURL: f.URL, Model: "m", APIKey: testKey}); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, b := range f.requests() {
		if b["tools"] != nil || b["response_format"] != nil || b["tool_choice"] != nil {
			t.Errorf("test request = %v, want a plain one", b)
		}
	}
	if _, ok := f.requests()[1]["max_tokens"]; ok {
		t.Error("OpenAI-compatible test sent max_tokens")
	}

	page := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		io.WriteString(w, `<html>hello</html>`)
	})
	if e := asError(t, Test(context.Background(), Config{Provider: OpenAI, BaseURL: page.URL, Model: "m", APIKey: testKey}), KindServer); e.Status != 200 {
		t.Errorf("a web page answering: %+v", e)
	}
}

// An incomplete connection or request is refused before anything is sent;
// a cancelled call returns the context's error, not an *Error.
func TestModelConfigAndCancel(t *testing.T) {
	for _, cfg := range []Config{
		{Provider: OpenAI, Model: "m", APIKey: testKey},
		{Provider: Anthropic, APIKey: testKey},
		{Provider: Anthropic, Model: "m"},
		{Provider: "gemini", Model: "m", APIKey: testKey},
	} {
		asError(t, Test(context.Background(), cfg), KindConfig)
	}
	bad := testRequest()
	bad.OutputName = "has space"
	_, err := Complete(context.Background(), Config{Provider: Anthropic, Model: "m", APIKey: testKey}, bad)
	asError(t, err, KindConfig)

	arrived := make(chan struct{})
	f := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server notices the client leaving only once the body is read.
		io.Copy(io.Discard, r.Body)
		close(arrived)
		<-r.Context().Done()
	}))
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-arrived
		cancel()
	}()
	start := time.Now()
	_, err = Complete(ctx, Config{Provider: OpenAI, BaseURL: f.URL, Model: "m", APIKey: testKey}, testRequest())
	if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Errorf("cancelled call: %v after %v", err, time.Since(start))
	}
}

// A content check's refusal is KindRefused whatever its status, asked once:
// not again as transient (MiniMax answers some with a 500), not again
// without response_format or stream, never mistaken for refused tools.
func TestContentRefusals(t *testing.T) {
	refuse := func(status int, body string) func(w http.ResponseWriter, _ map[string]any) {
		return func(w http.ResponseWriter, _ map[string]any) {
			w.WriteHeader(status)
			io.WriteString(w, body)
		}
	}
	stream := func(events ...string) func(w http.ResponseWriter, _ map[string]any) {
		return func(w http.ResponseWriter, _ map[string]any) { sse(w, events...) }
	}
	chat := chatRequest(Message{Role: RoleUser, Content: "x"})
	chat.OnText = func(string) {}
	for _, c := range []struct {
		name     string
		provider Provider
		req      Request
		handle   func(w http.ResponseWriter, body map[string]any)
		status   int
		message  string
	}{
		{"MiniMax 422", OpenAI, testRequest(), refuse(422, `{"error":{"type":"invalid_request_error","message":"input new_sensitive (1026)","http_code":"422"}}`), 422, "input new_sensitive (1026)"},
		{"MiniMax 500 over Anthropic", Anthropic, testRequest(), refuse(500, `{"type":"error","error":{"type":"api_error","message":"input new_sensitive, messages[1]'s content is sensitive, please check your input (1026)"}}`), 500, "input new_sensitive, messages[1]'s content is sensitive, please check your input (1026)"},
		{"DeepSeek naming response_format", OpenAI, testRequest(), refuse(400, `{"error":{"message":"Content Exists Risk (response_format json_schema)","type":"invalid_request_error"}}`), 400, "Content Exists Risk (response_format json_schema)"},
		{"DashScope", OpenAI, testRequest(), refuse(400, `{"error":{"code":"data_inspection_failed","message":"Input data may contain inappropriate content."}}`), 400, "Input data may contain inappropriate content."},
		{"Azure chat naming tools and stream", OpenAI, chat, refuse(400, `{"error":{"code":"content_filter","message":"The prompt triggered the content management policy; tools and stream are fine."}}`), 400, "The prompt triggered the content management policy; tools and stream are fine."},
		{"Zhipu chat 400", OpenAI, chat, refuse(400, `{"error":{"code":"1301","message":"系统检测到输入或生成内容可能包含不安全或敏感内容，请您避免输入易产生敏感内容的提示语"}}`), 400, "系统检测到输入或生成内容可能包含不安全或敏感内容，请您避免输入易产生敏感内容的提示语"},
		{"OpenAI stream error event", OpenAI, chat, stream(`{"choices":[{"delta":{"content":"a"}}]}`, `{"error":{"message":"output new_sensitive (1027)"}}`), 0, "output new_sensitive (1027)"},
		{"Anthropic stream error event", Anthropic, chat, stream(`{"type":"message_start","message":{"usage":{"input_tokens":5}}}`, `{"type":"error","error":{"type":"invalid_request_error","message":"input new_sensitive (1026)"}}`), 0, "input new_sensitive (1026)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t, c.handle)
			_, err := Complete(context.Background(), Config{Provider: c.provider, BaseURL: f.URL, Model: "m", APIKey: testKey}, c.req)
			e := asError(t, err, KindRefused)
			if e.Status != c.status || e.Message != c.message || len(f.requests()) != 1 {
				t.Errorf("refusal = %+v after %d requests, want HTTP %d %q after one", e, len(f.requests()), c.status, c.message)
			}
		})
	}

	// The words that mark a refusal do not turn other errors into one.
	for _, body := range []string{
		`{"error":{"message":"model not found"}}`,
		`{"error":{"message":"bad content_filter_results field"}}`,
	} {
		f := newFake(t, refuse(400, body))
		_, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: f.URL, Model: "m", APIKey: testKey}, testRequest())
		asError(t, err, KindServer)
	}
}
