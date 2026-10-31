package ai

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// Current DeepSeek thinking mode rejects tool continuations without reasoning replay.
func TestDeepSeekV4ToolContinuation(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
		if jsonOf(body["thinking"]) != `{"type":"disabled"}` {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"reasoning_content is missing"}}`)
			return
		}
		if len(messagesOf(body)) == 2 {
			io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search_files","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			io.WriteString(w, `{"choices":[{"message":{"content":"Found the file."},"finish_reason":"stop"}]}`)
		}
	})
	p, base := throughProxy(t, "api.deepseek.com", f.Server)
	cfg := Config{Provider: OpenAI, BaseURL: base, Model: "deepseek-flash", APIKey: testKey}
	user := Message{Role: RoleUser, Content: "Find the file"}
	first, err := Complete(context.Background(), cfg, chatRequest(user))
	if err != nil {
		t.Fatal(err)
	}
	if first.Stop != StopTool || len(first.ToolCalls) != 1 {
		t.Fatalf("first = %+v", first)
	}
	second, err := Complete(context.Background(), cfg, chatRequest(user, Message{Role: RoleAssistant, ToolCalls: first.ToolCalls}, Message{Role: RoleTool, ToolCallID: first.ToolCalls[0].ID, Content: `{"id":"f1"}`}))
	if err != nil || second.Text != "Found the file." || second.Stop != StopEnd {
		t.Fatalf("continuation = %+v, %v", second, err)
	}
	if !p.Saw("api.deepseek.com") {
		t.Fatal("request bypassed proxy")
	}
}

func TestOtherServicesKeepTheirThinkingDefaults(t *testing.T) {
	for _, base := range []string{"https://api.openai.com/v1", "https://custom.example/v1", "https://api.deepseek.com.evil/v1"} {
		f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
			if body["thinking"] != nil {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
		})
		client := &http.Client{Transport: modelsTransport(func(r *http.Request) (*http.Response, error) {
			target := *r.URL
			target.Scheme = f.Server.URL[:4]
			target.Host = f.Server.Listener.Addr().String()
			r.URL = &target
			return http.DefaultTransport.RoundTrip(r)
		})}
		got, err := Complete(context.Background(), Config{Provider: OpenAI, BaseURL: base, Model: "deepseek-flash", APIKey: testKey, HTTP: client}, Request{Messages: []Message{{Role: RoleUser, Content: "Hi"}}})
		if err != nil || got.Text != "OK" {
			t.Fatalf("%s: %+v %v", base, got, err)
		}
	}
}
