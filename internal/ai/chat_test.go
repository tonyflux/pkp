package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

var lookupTool = Tool{
	Name:        "search_files",
	Description: "按条件找文件",
	Schema:      json.RawMessage(`{"type":"object","properties":{"keyword":{"type":"string"}}}`),
}

func chatRequest(msgs ...Message) Request {
	return Request{System: "你是网盘助手。", Messages: msgs, Tools: []Tool{lookupTool}}
}

// sse writes events as a server-sent event stream.
func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	for _, e := range events {
		io.WriteString(w, "data: "+e+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func messagesOf(body map[string]any) []map[string]any {
	raw, _ := body["messages"].([]any)
	out := make([]map[string]any, len(raw))
	for i, m := range raw {
		out[i], _ = m.(map[string]any)
	}
	return out
}

func jsonOf(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// A chat request offers its tools with tool_choice auto; the model's
// tool_use comes back as a call, and the next request carries it as the
// assistant's tool_use with every result merged into one user turn.
func TestAnthropicChatToolRoundTrip(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		if len(messagesOf(body)) == 1 {
			io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"我查一下。"},{"type":"tool_use","id":"toolu_1","name":"search_files","input":{ "keyword" : "猫" }},{"type":"tool_use","id":"toolu_2","name":"search_files","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":200,"output_tokens":30}}`)
			return
		}
		io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"找到 3 个文件。"}],"stop_reason":"end_turn","usage":{"input_tokens":260,"output_tokens":8}}`)
	})
	cfg := Config{Provider: Anthropic, BaseURL: f.URL, Model: "claude-x", APIKey: testKey}
	user := Message{Role: RoleUser, Content: "找猫的视频"}
	first, err := Complete(context.Background(), cfg, chatRequest(user))
	if err != nil {
		t.Fatal(err)
	}
	if first.Stop != StopTool || first.Text != "我查一下。" || len(first.ToolCalls) != 2 || first.Usage != (Usage{200, 30}) {
		t.Fatalf("first answer = %+v", first)
	}
	if c := first.ToolCalls[0]; c.ID != "toolu_1" || c.Name != "search_files" || string(c.Arguments) != `{"keyword":"猫"}` {
		t.Errorf("call = %+v (%s)", c, c.Arguments)
	}
	body := f.requests()[0]
	if jsonOf(body["tool_choice"]) != `{"type":"auto"}` || body["stream"] != nil || body["system"] != "你是网盘助手。" {
		t.Errorf("first request = %v", body)
	}
	if tools := jsonOf(body["tools"]); tools != `[{"description":"按条件找文件","input_schema":{"properties":{"keyword":{"type":"string"}},"type":"object"},"name":"search_files"}]` {
		t.Errorf("tools = %s", tools)
	}

	second, err := Complete(context.Background(), cfg, chatRequest(user,
		Message{Role: RoleAssistant, Content: first.Text, ToolCalls: first.ToolCalls},
		Message{Role: RoleTool, ToolCallID: "toolu_1", Content: "f1 猫.mp4"},
		Message{Role: RoleTool, ToolCallID: "toolu_2", Content: "没有结果"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if second.Stop != StopEnd || second.Text != "找到 3 个文件。" || len(second.ToolCalls) != 0 {
		t.Errorf("second answer = %+v", second)
	}
	msgs := messagesOf(f.requests()[1])
	if len(msgs) != 3 {
		t.Fatalf("second request messages = %v", msgs)
	}
	want := []string{
		`{"content":[{"text":"找猫的视频","type":"text"}],"role":"user"}`,
		`{"content":[{"text":"我查一下。","type":"text"},{"id":"toolu_1","input":{"keyword":"猫"},"name":"search_files","type":"tool_use"},{"id":"toolu_2","input":{},"name":"search_files","type":"tool_use"}],"role":"assistant"}`,
		`{"content":[{"content":"f1 猫.mp4","tool_use_id":"toolu_1","type":"tool_result"},{"content":"没有结果","tool_use_id":"toolu_2","type":"tool_result"}],"role":"user"}`,
	}
	for i, m := range msgs {
		if got := jsonOf(m); got != want[i] {
			t.Errorf("message %d = %s\nwant %s", i, got, want[i])
		}
	}
}

// A streamed answer hands its text over piece by piece, in order, puts a
// tool_use input together from its JSON pieces and counts the usage of
// message_start and message_delta.
func TestAnthropicChatStream(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		sse(w,
			`{"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"ping"}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好，"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"我查查"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_9","name":"search_files","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":""}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"keyword\": "}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"猫\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":17}}`,
			`{"type":"message_stop"}`,
		)
	})
	var deltas []string
	req := chatRequest(Message{Role: RoleUser, Content: "找猫"})
	req.OnText = func(d string) { deltas = append(deltas, d) }
	resp, err := Complete(context.Background(), Config{Provider: Anthropic, BaseURL: f.URL, Model: "m", APIKey: testKey}, req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "|") != "你|好，|我查查" || resp.Text != "你好，我查查" {
		t.Errorf("deltas = %q, text = %q", deltas, resp.Text)
	}
	if resp.Usage != (Usage{Input: 25, Output: 17}) || resp.Stop != StopTool || len(resp.ToolCalls) != 1 {
		t.Fatalf("response = %+v", resp)
	}
	if c := resp.ToolCalls[0]; c.ID != "toolu_9" || string(c.Arguments) != `{"keyword":"猫"}` {
		t.Errorf("call = %+v %s", c, c.Arguments)
	}
	if body := f.requests()[0]; body["stream"] != true || f.header[0].Get("Accept") != "text/event-stream" {
		t.Errorf("request = %v", body)
	}
}

// A stream that stops before the service says it is done is no answer;
// an error event is the service's error.
func TestChatStreamCutShortAndErrorEvent(t *testing.T) {
	cut := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		sse(w, `{"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"半"}}`)
	})
	req := chatRequest(Message{Role: RoleUser, Content: "x"})
	req.OnText = func(string) {}
	_, err := Complete(context.Background(), Config{Provider: Anthropic, BaseURL: cut.URL, Model: "m", APIKey: testKey}, req)
	asError(t, err, KindUnreachable)

	failing := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		sse(w, `{"choices":[{"delta":{"content":"a"}}]}`, `{"error":{"message":"overloaded for `+testKey+`"}}`)
	})
	_, err = Complete(context.Background(), Config{Provider: OpenAI, BaseURL: failing.URL, Model: "m", APIKey: testKey}, req)
	if e := asError(t, err, KindServer); e.Message != "overloaded for ••••" {
		t.Errorf("error event: %q", e.Message)
	}
}

// OpenAI-compatible: tools go as functions, the call comes back with its
// arguments, and the next request carries the assistant's tool_calls and
// a tool turn per result. A <think> block is not part of the text.
func TestOpenAIChatToolRoundTrip(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		if len(messagesOf(body)) == 2 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"search_files","arguments":"{\"keyword\": \"猫\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":90,"completion_tokens":12}}`)
			return
		}
		content := "<think>用户要猫 {x}</think>\n\n找到 1 个：f1"
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 140, "completion_tokens": 9}})
		w.Write(data)
	})
	cfg := Config{Provider: OpenAI, BaseURL: f.URL, Model: "deepseek-chat", APIKey: testKey}
	user := Message{Role: RoleUser, Content: "找猫"}
	first, err := Complete(context.Background(), cfg, chatRequest(user))
	if err != nil {
		t.Fatal(err)
	}
	if first.Stop != StopTool || first.Text != "" || len(first.ToolCalls) != 1 || first.Usage != (Usage{90, 12}) {
		t.Fatalf("first = %+v", first)
	}
	if c := first.ToolCalls[0]; c.ID != "call_a" || c.Name != "search_files" || string(c.Arguments) != `{"keyword":"猫"}` {
		t.Errorf("call = %+v %s", c, c.Arguments)
	}
	body := f.requests()[0]
	if tools := jsonOf(body["tools"]); tools != `[{"function":{"description":"按条件找文件","name":"search_files","parameters":{"properties":{"keyword":{"type":"string"}},"type":"object"}},"type":"function"}]` {
		t.Errorf("tools = %s", tools)
	}
	if body["response_format"] != nil || body["stream"] != nil || body["tool_choice"] != nil {
		t.Errorf("first request = %v", body)
	}

	second, err := Complete(context.Background(), cfg, chatRequest(user,
		Message{Role: RoleAssistant, ToolCalls: first.ToolCalls},
		Message{Role: RoleTool, ToolCallID: "call_a", Content: "f1 猫.mp4"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if second.Text != "找到 1 个：f1" || second.Stop != StopEnd || second.Usage != (Usage{140, 9}) {
		t.Errorf("second = %+v", second)
	}
	msgs := messagesOf(f.requests()[1])
	want := []string{
		`{"content":"你是网盘助手。","role":"system"}`,
		`{"content":"找猫","role":"user"}`,
		`{"content":"","role":"assistant","tool_calls":[{"function":{"arguments":"{\"keyword\":\"猫\"}","name":"search_files"},"id":"call_a","type":"function"}]}`,
		`{"content":"f1 猫.mp4","role":"tool","tool_call_id":"call_a"}`,
	}
	if len(msgs) != len(want) {
		t.Fatalf("messages = %v", msgs)
	}
	for i, m := range msgs {
		if got := jsonOf(m); got != want[i] {
			t.Errorf("message %d = %s\nwant %s", i, got, want[i])
		}
	}
}

// A streamed OpenAI-compatible answer: text pieces in order with the
// <think> block held back even when its tags are split, tool calls put
// together by index from their pieces, the usage of the last chunk.
func TestOpenAIChatStreamAccumulatesToolCalls(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		sse(w,
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"<thi"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"nk>想一想</th"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"ink>\n\n好"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":null}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"的"}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search_files","arguments":""}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"keyw"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"folder_summary","arguments":"{\"folder\":"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ord\": \"猫\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":" \"c1\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":77,"completion_tokens":21}}`,
			`[DONE]`,
		)
	})
	var deltas []string
	req := chatRequest(Message{Role: RoleUser, Content: "找猫"})
	req.OnText = func(d string) { deltas = append(deltas, d) }
	resp, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: f.URL, Model: "m", APIKey: testKey}, req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "|") != "好|的" || resp.Text != "好的" {
		t.Errorf("deltas = %q, text = %q", deltas, resp.Text)
	}
	if resp.Stop != StopTool || resp.Usage != (Usage{77, 21}) || len(resp.ToolCalls) != 2 {
		t.Fatalf("response = %+v", resp)
	}
	got := []string{}
	for _, c := range resp.ToolCalls {
		got = append(got, c.ID+" "+c.Name+" "+string(c.Arguments))
	}
	if !slices.Equal(got, []string{`call_1 search_files {"keyword":"猫"}`, `call_2 folder_summary {"folder":"c1"}`}) {
		t.Errorf("calls = %q", got)
	}
	body := f.requests()[0]
	if body["stream"] != true || jsonOf(body["stream_options"]) != `{"include_usage":true}` {
		t.Errorf("request = %v", body)
	}
}

// A 400/422 about the tools is notools, on both providers, and the key
// the service echoes is scrubbed; the same words without tools offered,
// or another 400, stay a server error.
func TestChatNoTools(t *testing.T) {
	for _, refusal := range []string{
		`{"error":{"message":"registry.ollama.ai/library/gemma:2b does not support tools (key ` + testKey + `)"}}`,
		`{"error":{"message":"\"auto\" tool choice requires --enable-auto-tool-choice"}}`,
		`{"detail":"Function calling is not enabled for this model"}`,
		`{"error":{"message":"Unrecognized request argument supplied: tool_choice"}}`,
	} {
		f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, refusal)
		})
		for _, p := range []Provider{Anthropic, OpenAI} {
			_, err := Complete(context.Background(), Config{Provider: p, BaseURL: f.URL, Model: "m", APIKey: testKey}, chatRequest(Message{Role: RoleUser, Content: "x"}))
			e := asError(t, err, KindNoTools)
			if e.Status != 400 || e.Message == "" || strings.Contains(err.Error(), testKey) {
				t.Errorf("%s %s: %+v", p, refusal, e)
			}
		}
	}

	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"tools are not supported"}}`)
	})
	plain := chatRequest(Message{Role: RoleUser, Content: "x"})
	plain.Tools = nil
	_, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: f.URL, Model: "m", APIKey: testKey}, plain)
	asError(t, err, KindServer)

	g := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"model not found"}}`)
	})
	_, err = Complete(context.Background(), Config{Provider: OpenAI, BaseURL: g.URL, Model: "m", APIKey: testKey}, chatRequest(Message{Role: RoleUser, Content: "x"}))
	asError(t, err, KindServer)
}

// A refused stream is asked again once without it, the whole text handed
// to OnText in one piece; the refusal is remembered, so the next request
// goes unstreamed straight away.
func TestChatStreamRefusalIsRemembered(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		if body["stream"] == true {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"stream_options is not supported"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"整段回答"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
	})
	cfg := Config{Provider: OpenAI, BaseURL: f.URL, Model: "no-stream", APIKey: testKey}
	for range 2 {
		var deltas []string
		req := chatRequest(Message{Role: RoleUser, Content: "x"})
		req.OnText = func(d string) { deltas = append(deltas, d) }
		resp, err := Complete(context.Background(), cfg, req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Text != "整段回答" || !slices.Equal(deltas, []string{"整段回答"}) || resp.Usage != (Usage{3, 4}) {
			t.Errorf("response = %+v, deltas %q", resp, deltas)
		}
	}
	var streamed []bool
	for _, b := range f.requests() {
		streamed = append(streamed, b["stream"] == true)
	}
	if !slices.Equal(streamed, []bool{true, false, false}) {
		t.Errorf("stream per request = %v, want refused, retried, then unstreamed straight away", streamed)
	}

	// A service that ignores stream and answers whole is taken as is.
	g := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"整段"}],"stop_reason":"end_turn"}`)
	})
	var deltas []string
	req := chatRequest(Message{Role: RoleUser, Content: "x"})
	req.OnText = func(d string) { deltas = append(deltas, d) }
	resp, err := Complete(context.Background(), Config{Provider: Anthropic, BaseURL: g.URL, Model: "m", APIKey: testKey}, req)
	if err != nil || resp.Text != "整段" || !slices.Equal(deltas, []string{"整段"}) {
		t.Errorf("whole answer: %+v %v %q", resp, err, deltas)
	}
}

// An OpenAI-compatible service on this computer needs no key, and gets
// no Authorization header; anything else still needs one.
func TestKeylessLoopback(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"answer\":\"ok\"}"},"finish_reason":"stop"}]}`)
	})
	cfg := Config{Provider: OpenAI, BaseURL: f.URL + "/v1", Model: "qwen2.5"}
	if err := Test(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := Complete(context.Background(), cfg, testRequest()); err != nil {
		t.Fatal(err)
	}
	if resp, err := Complete(context.Background(), cfg, chatRequest(Message{Role: RoleUser, Content: "x"})); err != nil || resp.Text != `{"answer":"ok"}` {
		t.Fatalf("chat: %+v %v", resp, err)
	}
	for i, h := range f.header {
		if _, ok := h["Authorization"]; ok {
			t.Errorf("request %d sent Authorization %q", i, h.Get("Authorization"))
		}
	}

	for raw, want := range map[string]bool{
		"http://localhost:11434/v1":  true,
		"http://LOCALHOST/v1":        true,
		"http://127.0.0.1:1234/v1":   true,
		"http://[::1]:8080/v1":       true,
		"https://api.openai.com/v1":  false,
		"http://192.168.1.5:11434":   false,
		"http://localhost.evil.com/": false,
		"not a url":                  false,
	} {
		if got := Keyless(OpenAI, raw); got != want {
			t.Errorf("Keyless(%q) = %v", raw, got)
		}
	}
	if Keyless(Anthropic, "http://localhost:8080") {
		t.Error("Anthropic keyless")
	}
	asError(t, Test(context.Background(), Config{Provider: OpenAI, BaseURL: "https://api.deepseek.com/v1", Model: "m"}), KindConfig)
}

// Probe tells a service that takes tools and streams from one that
// refuses both, and leaves an outage unknown.
func TestProbe(t *testing.T) {
	good := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		if body["stream"] == true {
			sse(w, `{"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}`, `[DONE]`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	})
	if got := Probe(context.Background(), Config{Provider: OpenAI, BaseURL: good.URL, Model: "m", APIKey: testKey}); got != (Capabilities{Tools: CapYes, Stream: CapYes}) {
		t.Errorf("good service: %+v", got)
	}
	var sawTool bool
	for _, b := range good.requests() {
		if strings.Contains(jsonOf(b["tools"]), probeTool.Name) {
			sawTool = true
		}
	}
	if !sawTool {
		t.Error("no probe request offered a tool")
	}

	refusing := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		w.WriteHeader(http.StatusBadRequest)
		switch {
		case body["stream"] == true:
			io.WriteString(w, `{"error":{"message":"streaming is not supported"}}`)
		case body["tools"] != nil:
			io.WriteString(w, `{"error":{"message":"this model does not support tools"}}`)
		}
	})
	cfg := Config{Provider: Anthropic, BaseURL: refusing.URL, Model: "m", APIKey: testKey}
	if got := Probe(context.Background(), cfg); got != (Capabilities{Tools: CapNo, Stream: CapNo}) {
		t.Errorf("refusing service: %+v", got)
	}

	down := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if got := Probe(context.Background(), Config{Provider: OpenAI, BaseURL: down.URL, Model: "m", APIKey: testKey}); got != (Capabilities{}) {
		t.Errorf("service down: %+v", got)
	}
}

// A chat request with a broken turn or tool is refused before sending.
func TestChatRequestChecks(t *testing.T) {
	cfg := Config{Provider: OpenAI, BaseURL: "https://example.com/v1", Model: "m", APIKey: testKey}
	for name, req := range map[string]Request{
		"no messages":      {Tools: []Tool{lookupTool}},
		"bad role":         chatRequest(Message{Role: "system", Content: "x"}),
		"orphan result":    chatRequest(Message{Role: RoleTool, Content: "x"}),
		"bad call":         chatRequest(Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "", Name: "x"}}}),
		"bad tool name":    {Messages: []Message{{Role: RoleUser, Content: "x"}}, Tools: []Tool{{Name: "a b", Schema: lookupTool.Schema}}},
		"duplicate tool":   {Messages: []Message{{Role: RoleUser, Content: "x"}}, Tools: []Tool{lookupTool, lookupTool}},
		"bad tool schema":  {Messages: []Message{{Role: RoleUser, Content: "x"}}, Tools: []Tool{{Name: "a", Schema: json.RawMessage(`[]`)}}},
		"structured tools": {Messages: []Message{{Role: RoleTool, ToolCallID: "c", Content: "x"}}, OutputName: "o", OutputSchema: testSchema},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Complete(context.Background(), cfg, req)
			asError(t, err, KindConfig)
		})
	}
}

// The <think> filter holds a block back whatever the pieces, and lets
// text that only looked like a tag through.
func TestThinkFilter(t *testing.T) {
	for _, pieces := range [][]string{
		{"<think>a</think>b"},
		{"<", "think>", "a", "</", "think", ">", "b"},
		{"  \n<think>x</think>\n", "\nb"},
		{"<think>a</think><think>c</think>b"},
	} {
		var f thinkFilter
		var out strings.Builder
		for _, p := range pieces {
			out.WriteString(f.push(p))
		}
		out.WriteString(f.finish())
		if out.String() != "b" {
			t.Errorf("%q → %q", pieces, out.String())
		}
	}
	for text, want := range map[string]string{
		"<thi":                "<thi",
		"hello <think>x":      "hello <think>x",
		"<think>never closed": "",
		"<b>bold</b>":         "<b>bold</b>",
	} {
		if got := stripThink(text); got != want {
			t.Errorf("stripThink(%q) = %q, want %q", text, got, want)
		}
	}
}
