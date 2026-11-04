package ai

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// Gemini rejects tool continuations without the original per-call signatures.
// Stream metadata can arrive after the arguments, or be absent on parallel calls.
func TestGeminiSignedToolContinuation(t *testing.T) {
	for _, mode := range []string{"json", "stream"} {
		t.Run(mode, func(t *testing.T) {
			f := newFake(t, func(w http.ResponseWriter, body map[string]any) {
				messages := messagesOf(body)
				if len(messages) == 2 {
					if mode == "stream" {
						sse(w,
							`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"search_files","arguments":"{\"keyword\":"},"extra_content":{"google":{"thought_signature":"c2lnbmF0dXJlLWE="}}}]}}]}`,
							`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"search_files","arguments":"{\"keyword\":\"dog\"}"}}]}}]}`,
							`{"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_c","function":{"name":"search_files","arguments":"{\"keyword\":\"bird\"}"}}]}}]}`,
							`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"cat\"}"}}]}}]}`,
							`{"choices":[{"delta":{"tool_calls":[{"index":1,"extra_content":{"google":{"thought_signature":"c2lnbmF0dXJlLWI="}}}]}}]}`,
							`{"choices":[{"delta":{"tool_calls":[{"index":0,"extra_content":{"google":{}}}]},"finish_reason":"tool_calls"}]}`,
							`[DONE]`,
						)
					} else {
						io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"id":"call_a","type":"function","function":{"name":"search_files","arguments":"{\"keyword\":\"cat\"}"},"extra_content":{"google":{"thought_signature":"c2lnbmF0dXJlLWE="}}},{"id":"call_b","type":"function","function":{"name":"search_files","arguments":"{\"keyword\":\"dog\"}"},"extra_content":{"google":{"thought_signature":"c2lnbmF0dXJlLWI="}}},{"id":"call_c","type":"function","function":{"name":"search_files","arguments":"{\"keyword\":\"bird\"}"}}]},"finish_reason":"tool_calls"}]}`)
					}
					return
				}

				assistant := messages[2]
				calls, _ := assistant["tool_calls"].([]any)
				if len(calls) != 3 {
					http.Error(w, `{"error":{"message":"missing parallel tool calls"}}`, http.StatusBadRequest)
					return
				}
				for i, signature := range []string{"c2lnbmF0dXJlLWE=", "c2lnbmF0dXJlLWI=", ""} {
					call, _ := calls[i].(map[string]any)
					extra, _ := call["extra_content"].(map[string]any)
					google, _ := extra["google"].(map[string]any)
					if signature == "" && call["extra_content"] != nil || signature != "" && google["thought_signature"] != signature {
						http.Error(w, `{"error":{"message":"missing or mismatched thought signature"}}`, http.StatusBadRequest)
						return
					}
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"Found the three files."},"finish_reason":"stop"}],"usage":{"prompt_tokens":75,"completion_tokens":6}}`)
			})
			cfg := Config{Provider: OpenAI, BaseURL: f.URL + "/v1beta/openai", Model: "gemini-3.8-flash", APIKey: testKey}
			user := Message{Role: RoleUser, Content: "Find cat, dog and bird files"}
			req := chatRequest(user)
			if mode == "stream" {
				req.OnText = func(string) {}
			}
			first, err := Complete(context.Background(), cfg, req)
			if err != nil || first.Stop != StopTool || len(first.ToolCalls) != 3 {
				t.Fatalf("tool answer = %+v, %v", first, err)
			}
			req.Messages = append(req.Messages, Message{Role: RoleAssistant, ToolCalls: first.ToolCalls})
			for _, call := range first.ToolCalls {
				req.Messages = append(req.Messages, Message{Role: RoleTool, ToolCallID: call.ID, Content: "Found one matching file."})
			}
			last, err := Complete(context.Background(), cfg, req)
			if err != nil || last.Stop != StopEnd || last.Text != "Found the three files." || last.Usage != (Usage{75, 6}) {
				t.Fatalf("tool continuation = %+v, %v", last, err)
			}
		})
	}
}
