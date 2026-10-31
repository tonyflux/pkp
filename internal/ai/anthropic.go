package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/tonyflux/pkp/internal/i18n"
)

// anthropicVersion is the Messages API version the requests are written for.
const anthropicVersion = "2023-06-01"

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// anthropicMessage is one turn on the wire: Content is the plain text of a
// structured request's turn, or a chat turn's content blocks.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// anthropicBlock is a content block a chat request sends: text, an
// assistant's tool_use, or the tool_result that answers it.
type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicRequest struct {
	Model      string               `json:"model"`
	MaxTokens  int                  `json:"max_tokens"`
	System     string               `json:"system,omitempty"`
	Messages   []anthropicMessage   `json:"messages"`
	Tools      []anthropicTool      `json:"tools,omitempty"`
	ToolChoice *anthropicToolChoice `json:"tool_choice,omitempty"`
	Stream     bool                 `json:"stream,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	Type    string `json:"type"`
	Content []struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Text  string          `json:"text"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      anthropicUsage `json:"usage"`
}

// anthropicEvent is any event of a streamed answer; each type fills its
// own fields (content_block_delta and message_delta share Delta).
type anthropicEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Text string `json:"text"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage anthropicUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (cfg Config) anthropicHeader() http.Header {
	h := http.Header{}
	h.Set("x-api-key", cfg.APIKey)
	h.Set("anthropic-version", anthropicVersion)
	return h
}

func maxTokensOf(req Request) int {
	if req.MaxTokens <= 0 {
		return defaultMaxTokens
	}
	return req.MaxTokens
}

// plainMessages are a structured request's turns, text only.
func plainMessages(msgs []Message) []anthropicMessage {
	out := make([]anthropicMessage, len(msgs))
	for i, m := range msgs {
		out[i] = anthropicMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

// completeAnthropic forces the answer through a tool whose input schema is
// the output schema: the API then returns the object itself, already parsed,
// as the tool call's input.
func completeAnthropic(ctx context.Context, cfg Config, base string, req Request) (Response, error) {
	body := anthropicRequest{
		Model:     cfg.Model,
		MaxTokens: maxTokensOf(req),
		System:    req.System,
		Messages:  plainMessages(req.Messages),
		Tools: []anthropicTool{{
			Name:        req.OutputName,
			Description: "Return the requested result: the tool input is the object to return.",
			InputSchema: req.OutputSchema,
		}},
		ToolChoice: &anthropicToolChoice{Type: "tool", Name: req.OutputName},
	}
	status, data, err := cfg.post(ctx, base+"/v1/messages", cfg.anthropicHeader(), body)
	if err != nil {
		return Response{}, err
	}
	if status/100 != 2 {
		return Response{}, cfg.refused(status, data)
	}
	var answer anthropicResponse
	if err := json.Unmarshal(data, &answer); err != nil {
		return Response{}, cfg.fail(KindInvalid, 0, i18n.F("服务返回的不是 JSON：%s", data))
	}
	usage := Usage{Input: answer.Usage.InputTokens, Output: answer.Usage.OutputTokens}
	if answer.StopReason == "max_tokens" {
		return Response{Usage: usage}, cfg.fail(KindTruncated, 0, i18n.T("回答达到输出上限"))
	}
	for _, block := range answer.Content {
		if block.Type != "tool_use" {
			continue
		}
		if obj, ok := asObject(string(block.Input)); ok {
			return Response{Output: obj, Usage: usage}, nil
		}
		return Response{Usage: usage}, cfg.fail(KindInvalid, 0, i18n.F("工具调用的输入不是 JSON 对象：%s", block.Input))
	}
	return Response{Usage: usage}, cfg.fail(KindInvalid, 0, i18n.F("回答里没有工具调用：%s", anthropicText(answer)))
}

// anthropicText is the answer's plain text, for the message of an answer
// that skipped the tool.
func anthropicText(answer anthropicResponse) string {
	var b strings.Builder
	for _, block := range answer.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	if b.Len() == 0 {
		return i18n.T("（空）")
	}
	return b.String()
}

// chatMessages are a chat request's turns as content blocks. The API wants
// user and assistant turns to alternate and tool results in a user turn,
// so consecutive turns of one side (several tool results, a tool result
// and the user's next words) are merged; empty turns are left out.
func chatMessages(msgs []Message) []anthropicMessage {
	var out []anthropicMessage
	for _, m := range msgs {
		role := m.Role
		var blocks []anthropicBlock
		switch m.Role {
		case RoleUser:
			if m.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
		case RoleAssistant:
			if m.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, c := range m.ToolCalls {
				blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: arguments(c.Arguments)})
			}
		case RoleTool:
			role = RoleUser
			blocks = append(blocks, anthropicBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content})
		}
		if len(blocks) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content.([]anthropicBlock), blocks...)
			continue
		}
		out = append(out, anthropicMessage{Role: role, Content: blocks})
	}
	return out
}

// chatAnthropic sends a chat request: the tools offered with tool_choice
// auto, the answer streamed when asked.
func chatAnthropic(ctx context.Context, cfg Config, base string, req Request, stream bool) (Response, bool, error) {
	body := anthropicRequest{
		Model:     cfg.Model,
		MaxTokens: maxTokensOf(req),
		System:    req.System,
		Messages:  chatMessages(req.Messages),
		Stream:    stream,
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	if len(body.Tools) > 0 {
		body.ToolChoice = &anthropicToolChoice{Type: "auto"}
	}
	header := cfg.anthropicHeader()
	if stream {
		header.Set("Accept", "text/event-stream")
	}
	resp, err := cfg.send(ctx, base+"/v1/messages", header, body)
	if err != nil {
		return Response{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 && stream && isEventStream(resp) {
		out, err := cfg.anthropicStream(ctx, resp, req)
		return out, true, err
	}
	status, data, err := cfg.readAll(ctx, resp)
	if err != nil {
		return Response{}, false, err
	}
	if status/100 != 2 {
		return Response{}, false, cfg.chatRefused(status, data, stream, len(req.Tools) > 0)
	}
	var answer anthropicResponse
	if err := json.Unmarshal(data, &answer); err != nil {
		return Response{}, false, cfg.fail(KindInvalid, 0, i18n.F("服务返回的不是 JSON：%s", data))
	}
	out := Response{Usage: Usage{Input: answer.Usage.InputTokens, Output: answer.Usage.OutputTokens}}
	var text strings.Builder
	for _, block := range answer.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Arguments: received(string(block.Input))})
		}
	}
	out.Text = text.String()
	out.Stop = stopOf(answer.StopReason == "max_tokens", len(out.ToolCalls))
	whole(req, out)
	return out, false, nil
}

// anthropicStream reads a streamed answer: text deltas go to OnText as
// they come, a tool_use block's input is put together from its JSON
// pieces, usage is message_start's input and message_delta's output.
func (cfg Config) anthropicStream(ctx context.Context, resp *http.Response, req Request) (Response, error) {
	type block struct {
		kind, id, name string
		text, input    strings.Builder
	}
	blocks := map[int]*block{}
	var out Response
	var stopReason string
	done := false
	err := readEvents(resp.Body, func(data string) error {
		var ev anthropicEvent
		if json.Unmarshal([]byte(data), &ev) != nil {
			return nil // not an event of the API's; the end decides
		}
		switch ev.Type {
		case "message_start":
			out.Usage.Input = ev.Message.Usage.InputTokens
			out.Usage.Output = ev.Message.Usage.OutputTokens
		case "content_block_start":
			b := &block{kind: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			blocks[ev.Index] = b
			if b.kind == "text" && ev.ContentBlock.Text != "" {
				b.text.WriteString(ev.ContentBlock.Text)
				if req.OnText != nil {
					req.OnText(ev.ContentBlock.Text)
				}
			}
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				b.text.WriteString(ev.Delta.Text)
				if req.OnText != nil && ev.Delta.Text != "" {
					req.OnText(ev.Delta.Text)
				}
			case "input_json_delta":
				b.input.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			// The counts here are the totals so far.
			if ev.Usage.InputTokens > 0 {
				out.Usage.Input = ev.Usage.InputTokens
			}
			if ev.Usage.OutputTokens > 0 {
				out.Usage.Output = ev.Usage.OutputTokens
			}
		case "message_stop":
			done = true
		case "error":
			message := ev.Error.Message
			if message == "" {
				message = data
			}
			return cfg.streamError(data, message)
		}
		return nil
	})
	if err != nil {
		return out, cfg.streamFailed(ctx, err)
	}
	if !done && stopReason == "" {
		return out, cfg.cutShort(ctx)
	}
	indexes := make([]int, 0, len(blocks))
	for i := range blocks {
		indexes = append(indexes, i)
	}
	slices.Sort(indexes)
	var text strings.Builder
	for _, i := range indexes {
		switch b := blocks[i]; b.kind {
		case "text":
			text.WriteString(b.text.String())
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.id, Name: b.name, Arguments: received(b.input.String())})
		}
	}
	out.Text = text.String()
	out.Stop = stopOf(stopReason == "max_tokens", len(out.ToolCalls))
	return out, nil
}

func testAnthropic(ctx context.Context, cfg Config, base string) error {
	body := anthropicRequest{
		Model:     cfg.Model,
		MaxTokens: 16,
		Messages:  []anthropicMessage{{Role: RoleUser, Content: "Reply with OK."}},
	}
	status, data, err := cfg.post(ctx, base+"/v1/messages", cfg.anthropicHeader(), body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return cfg.refused(status, data)
	}
	var answer anthropicResponse
	if json.Unmarshal(data, &answer) != nil || answer.Type != "message" {
		return cfg.fail(KindServer, status, i18n.F("这个地址的回答不像 Anthropic 模型服务：%s", data))
	}
	return nil
}
