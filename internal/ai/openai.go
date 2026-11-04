package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/tonyflux/pkp/internal/i18n"
)

// The response_format rungs, strictest first. Services that speak
// chat/completions differ in what they accept: OpenAI takes json_schema,
// many others only json_object, some refuse the field outright. A refusal
// that names the field steps one rung down.
const (
	rungSchema = iota
	rungObject
	rungNone
)

// rungs remembers the rung that last worked for each base URL + model, so
// a service that refuses json_schema is asked without it from then on.
var rungs sync.Map

// schemaPrompt follows the system prompt on every OpenAI-compatible request,
// whatever the rung: some services accept response_format and ignore it.
const schemaPrompt = "Output only one JSON object conforming to this JSON Schema: "

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls and ToolCallID carry a chat request's tool turns.
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIFunction struct {
	Name string `json:"name"`
	// Arguments is the call's JSON object as text.
	Arguments string `json:"arguments"`
}

type openAIToolCall struct {
	ID           string              `json:"id"`
	Type         string              `json:"type"`
	Function     openAIFunction      `json:"function"`
	ExtraContent *openAIExtraContent `json:"extra_content,omitempty"`
}

type openAIExtraContent struct {
	Google struct {
		ThoughtSignature string `json:"thought_signature"`
	} `json:"google"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIRequest struct {
	Model          string               `json:"model"`
	Messages       []openAIMessage      `json:"messages"`
	ResponseFormat any                  `json:"response_format,omitempty"`
	Tools          []openAITool         `json:"tools,omitempty"`
	Stream         bool                 `json:"stream,omitempty"`
	StreamOptions  *openAIStreamOptions `json:"stream_options,omitempty"`
	Thinking       *openAIThinking      `json:"thinking,omitempty"`
}

type openAIThinking struct {
	Type string `json:"type"`
}

// DeepSeek V4 defaults to reasoning replay, which this chat history does not store.
func (cfg Config) openAIThinking(base string) *openAIThinking {
	u, err := url.Parse(base)
	if err == nil && strings.EqualFold(u.Hostname(), "api.deepseek.com") &&
		(cfg.Model == "deepseek-flash" || strings.HasPrefix(cfg.Model, "deepseek-v4")) {
		return &openAIThinking{Type: "disabled"}
	}
	return nil
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   json.RawMessage  `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

// openAIChunk is one piece of a streamed answer. A tool call's pieces
// share its index; the first carries the id and name, the rest more of
// the arguments. The last chunk (include_usage) has the usage and no
// choices.
type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				Index        *int                `json:"index"`
				ID           string              `json:"id"`
				Function     openAIFunction      `json:"function"`
				ExtraContent *openAIExtraContent `json:"extra_content"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage    `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// openAIHeader carries the key; a keyless service on this computer gets no
// Authorization header at all rather than an empty one.
func (cfg Config) openAIHeader() http.Header {
	h := http.Header{}
	if cfg.APIKey != "" {
		h.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	return h
}

func responseFormat(rung int, req Request) any {
	switch rung {
	case rungSchema:
		return map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   req.OutputName,
				"schema": req.OutputSchema,
				"strict": false,
			},
		}
	case rungObject:
		return map[string]string{"type": "json_object"}
	}
	return nil
}

// refusesFormat says a 400/422 is about response_format, not the request.
func refusesFormat(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity || refusesContent.Match(body) {
		return false
	}
	text := strings.ToLower(string(body))
	return strings.Contains(text, "response_format") || strings.Contains(text, "json_schema") || strings.Contains(text, "json_object")
}

func completeOpenAI(ctx context.Context, cfg Config, base string, req Request) (Response, error) {
	var schema bytes.Buffer
	if err := json.Compact(&schema, req.OutputSchema); err != nil {
		return Response{}, cfg.fail(KindConfig, 0, i18n.T("输出格式（JSON Schema）不是一个 JSON 对象"))
	}
	system := schemaPrompt + schema.String()
	if req.System != "" {
		system = req.System + "\n\n" + system
	}
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	messages = append(messages, openAIMessage{Role: "system", Content: system})
	for _, m := range req.Messages {
		messages = append(messages, openAIMessage{Role: m.Role, Content: m.Content})
	}

	key := base + "\x00" + cfg.Model
	rung := rungSchema
	if v, ok := rungs.Load(key); ok {
		rung = v.(int)
	}
	for {
		body := openAIRequest{Model: cfg.Model, Messages: messages, ResponseFormat: responseFormat(rung, req), Thinking: cfg.openAIThinking(base)}
		status, data, err := cfg.post(ctx, base+"/chat/completions", cfg.openAIHeader(), body)
		if err != nil {
			return Response{}, err
		}
		if status/100 == 2 {
			rungs.Store(key, rung)
			return cfg.openAIAnswer(data)
		}
		if rung < rungNone && refusesFormat(status, data) {
			rung++
			continue
		}
		return Response{}, cfg.refused(status, data)
	}
}

// openAIAnswer takes the object out of the first choice's text.
func (cfg Config) openAIAnswer(data []byte) (Response, error) {
	var answer openAIResponse
	if err := json.Unmarshal(data, &answer); err != nil {
		return Response{}, cfg.fail(KindInvalid, 0, i18n.F("服务返回的不是 JSON：%s", data))
	}
	usage := Usage{Input: answer.Usage.PromptTokens, Output: answer.Usage.CompletionTokens}
	if len(answer.Choices) == 0 {
		return Response{Usage: usage}, cfg.fail(KindInvalid, 0, i18n.F("回答里没有内容：%s", data))
	}
	choice := answer.Choices[0]
	if choice.FinishReason == "length" {
		return Response{Usage: usage}, cfg.fail(KindTruncated, 0, i18n.T("回答达到输出上限"))
	}
	text := contentText(choice.Message.Content)
	obj, ok := extractObject(text)
	if !ok {
		if strings.TrimSpace(text) == "" {
			text = i18n.T("（空）")
		}
		return Response{Usage: usage}, cfg.fail(KindInvalid, 0, i18n.F("回答里没有 JSON 对象：%s", text))
	}
	return Response{Output: obj, Usage: usage}, nil
}

// contentText reads message.content: a string, or (some services) a list of
// parts whose text parts are joined.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// openAIChatMessages are a chat request's turns: the system prompt first,
// an assistant's calls as tool_calls, each result as a tool turn.
func openAIChatMessages(req Request) []openAIMessage {
	out := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		out = append(out, openAIMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msg := openAIMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			call := openAIToolCall{
				ID:       c.ID,
				Type:     "function",
				Function: openAIFunction{Name: c.Name, Arguments: string(arguments(c.Arguments))},
			}
			if c.ThoughtSignature != "" {
				call.ExtraContent = &openAIExtraContent{}
				call.ExtraContent.Google.ThoughtSignature = c.ThoughtSignature
			}
			msg.ToolCalls = append(msg.ToolCalls, call)
		}
		out = append(out, msg)
	}
	return out
}

// chatOpenAI sends a chat request: the tools as functions, the answer
// streamed (with its usage) when asked.
func chatOpenAI(ctx context.Context, cfg Config, base string, req Request, stream bool) (Response, bool, error) {
	body := openAIRequest{Model: cfg.Model, Messages: openAIChatMessages(req), Stream: stream, Thinking: cfg.openAIThinking(base)}
	for _, t := range req.Tools {
		var tool openAITool
		tool.Type = "function"
		tool.Function.Name, tool.Function.Description, tool.Function.Parameters = t.Name, t.Description, t.Schema
		body.Tools = append(body.Tools, tool)
	}
	header := cfg.openAIHeader()
	if stream {
		body.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
		header.Set("Accept", "text/event-stream")
	}
	resp, err := cfg.send(ctx, base+"/chat/completions", header, body)
	if err != nil {
		return Response{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 && stream && isEventStream(resp) {
		out, err := cfg.openAIStream(ctx, resp, req)
		return out, true, err
	}
	status, data, err := cfg.readAll(ctx, resp)
	if err != nil {
		return Response{}, false, err
	}
	if status/100 != 2 {
		return Response{}, false, cfg.chatRefused(status, data, stream, len(req.Tools) > 0)
	}
	var answer openAIResponse
	if err := json.Unmarshal(data, &answer); err != nil {
		return Response{}, false, cfg.fail(KindInvalid, 0, i18n.F("服务返回的不是 JSON：%s", data))
	}
	out := Response{Usage: Usage{Input: answer.Usage.PromptTokens, Output: answer.Usage.CompletionTokens}}
	if len(answer.Choices) == 0 {
		return out, false, cfg.fail(KindInvalid, 0, i18n.F("回答里没有内容：%s", data))
	}
	choice := answer.Choices[0]
	out.Text = stripThink(contentText(choice.Message.Content))
	for i, c := range choice.Message.ToolCalls {
		call := ToolCall{ID: callID(c.ID, i), Name: c.Function.Name, Arguments: received(c.Function.Arguments)}
		if c.ExtraContent != nil {
			call.ThoughtSignature = c.ExtraContent.Google.ThoughtSignature
		}
		out.ToolCalls = append(out.ToolCalls, call)
	}
	out.Stop = stopOf(choice.FinishReason == "length", len(out.ToolCalls))
	whole(req, out)
	return out, false, nil
}

// openAIStream reads a streamed answer: text pieces go to OnText as they
// come (a <think> block held back), tool calls are put together by index.
func (cfg Config) openAIStream(ctx context.Context, resp *http.Response, req Request) (Response, error) {
	type call struct {
		id, name, signature string
		args                strings.Builder
	}
	calls := map[int]*call{}
	last := -1
	var out Response
	var text strings.Builder
	var think thinkFilter
	emit := func(s string) {
		if s == "" {
			return
		}
		text.WriteString(s)
		if req.OnText != nil {
			req.OnText(s)
		}
	}
	finish, done := "", false
	err := readEvents(resp.Body, func(data string) error {
		if strings.TrimSpace(data) == "[DONE]" {
			done = true
			return nil
		}
		var chunk openAIChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return nil
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return cfg.streamError(data, serverMessage([]byte(data)))
		}
		if chunk.Usage != nil {
			out.Usage = Usage{Input: chunk.Usage.PromptTokens, Output: chunk.Usage.CompletionTokens}
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		choice := chunk.Choices[0]
		emit(think.push(contentText(choice.Delta.Content)))
		for _, d := range choice.Delta.ToolCalls {
			// A service that numbers nothing sends each call whole: a new
			// id starts the next one.
			i := last
			switch {
			case d.Index != nil:
				i = *d.Index
			case d.ID != "" || last < 0:
				i = last + 1
			}
			c := calls[i]
			if c == nil {
				c = &call{}
				calls[i] = c
			}
			last = max(last, i)
			if d.ID != "" {
				c.id = d.ID
			}
			if d.Function.Name != "" {
				c.name = d.Function.Name
			}
			c.args.WriteString(d.Function.Arguments)
			if d.ExtraContent != nil && d.ExtraContent.Google.ThoughtSignature != "" {
				c.signature = d.ExtraContent.Google.ThoughtSignature
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finish = *choice.FinishReason
		}
		return nil
	})
	if err != nil {
		return out, cfg.streamFailed(ctx, err)
	}
	if !done && finish == "" {
		return out, cfg.cutShort(ctx)
	}
	emit(think.finish())
	indexes := make([]int, 0, len(calls))
	for i := range calls {
		indexes = append(indexes, i)
	}
	slices.Sort(indexes)
	for n, i := range indexes {
		c := calls[i]
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: callID(c.id, n), Name: c.name, Arguments: received(c.args.String()), ThoughtSignature: c.signature})
	}
	out.Text = text.String()
	out.Stop = stopOf(finish == "length", len(out.ToolCalls))
	return out, nil
}

// callID is the call's id, or one made up for a service that gives none
// (the result still has to name its call).
func callID(id string, n int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("call_%d", n+1)
}

func testOpenAI(ctx context.Context, cfg Config, base string) error {
	body := openAIRequest{
		Model:    cfg.Model,
		Messages: []openAIMessage{{Role: RoleUser, Content: "Reply with OK."}},
		Thinking: cfg.openAIThinking(base),
	}
	status, data, err := cfg.post(ctx, base+"/chat/completions", cfg.openAIHeader(), body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return cfg.refused(status, data)
	}
	var answer openAIResponse
	if json.Unmarshal(data, &answer) != nil || len(answer.Choices) == 0 {
		return cfg.fail(KindServer, status, i18n.F("这个地址的回答不像 OpenAI 兼容的模型服务：%s", data))
	}
	return nil
}
