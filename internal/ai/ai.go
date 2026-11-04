// Package ai talks to the model services AI Assistant uses: Anthropic's Messages
// API and any OpenAI-compatible chat/completions endpoint. The webview builds
// every request (so what the user consents to is what is sent); this package
// only adds the key and the provider's envelope. A request is either
// structured (OutputSchema set: the one JSON object the model was asked for
// comes back, no tools, no streaming) or chat (the model answers in text and
// may call the request's tools; the text can stream as it is written).
// Requests follow the app's proxy setting unless the caller injects its own
// Config.HTTP.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// Provider names a kind of model service.
type Provider string

const (
	// Anthropic is Anthropic's Messages API.
	Anthropic Provider = "anthropic"
	// OpenAI is any service that speaks OpenAI's chat/completions
	// (OpenAI compatible): OpenAI itself, DeepSeek, Qwen, a local Ollama….
	OpenAI Provider = "openai"
)

const (
	// DefaultOpenAIBaseURL is where an OpenAI-compatible connection points
	// until the user names another service.
	DefaultOpenAIBaseURL = "https://api.openai.com/v1"
	// DefaultAnthropicBaseURL is Anthropic's API; Config.BaseURL "" means it.
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
)

const (
	// CompleteTimeout bounds one Complete: a long plan takes a model a
	// minute or two to write, longer means something is stuck.
	CompleteTimeout = 180 * time.Second
	// TestTimeout bounds Test and each question of Probe, which ask for a
	// handful of tokens.
	TestTimeout = 20 * time.Second
	// defaultMaxTokens is Anthropic's output cap when the request names none.
	defaultMaxTokens = 8192
	// maxResponse bounds what is read of a successful answer (streamed or
	// not); maxErrorBody of a refusal, of which messageLimit bytes are kept.
	maxResponse  = 16 << 20
	maxErrorBody = 64 << 10
	messageLimit = 2048
	// keyMask stands in for the API key in every message.
	keyMask = "••••"
	// maxRetryAfter is the longest Retry-After a retry waits for; a service
	// asking for longer gets the default wait, then its refusal.
	maxRetryAfter = 10 * time.Second
)

// RetryDelays are the waits before each new attempt of a Complete whose
// request got no answer or a transient refusal (408, 409, 429, 5xx): one
// entry per retry. Tests shorten them.
var RetryDelays = []time.Duration{time.Second, 3 * time.Second}

// The kinds of Error.
const (
	KindAuth        = "auth"        // the key was refused (401/403)
	KindUnreachable = "unreachable" // no answer: DNS, connect, TLS, timeout
	KindServer      = "server"      // the service answered with an error
	KindInvalid     = "invalid"     // an answer without the object asked for
	KindTruncated   = "truncated"   // the model hit its output limit
	KindConfig      = "config"      // the connection or request is incomplete
	KindNoTools     = "notools"     // the service refused a chat request's tools
	KindRefused     = "refused"     // the service's content check turned the request down
)

// The roles of Message.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	// RoleTool is a chat request's answer to one of the assistant's tool
	// calls: Content is the result, ToolCallID the call.
	RoleTool = "tool"
)

// The values of Response.Stop.
const (
	StopEnd    = "end"    // the model finished its answer
	StopTool   = "tool"   // the model is waiting for its tool calls' results
	StopLength = "length" // the model hit its output limit
)

// Config is one connection to a model service.
type Config struct {
	Provider Provider
	// BaseURL is the OpenAI-compatible endpoint ("…/v1"); for Anthropic ""
	// means DefaultAnthropicBaseURL (tests point it at a stand-in).
	BaseURL string
	Model   string
	// APIKey may be "" only for an OpenAI-compatible service on this
	// computer (see Keyless); no Authorization header is sent then.
	APIKey string
	// HTTP is the client requests go through; nil means http.DefaultClient.
	HTTP *http.Client
	// retry lets send ask again after a transient failure. Complete sets
	// it; Test and Probe report what the service said the first time.
	retry bool
}

// Message is one turn of the conversation: Role "user" or "assistant", and
// in a chat request also "tool".
type Message struct {
	Role    string
	Content string
	// ToolCalls are the calls an assistant turn made (chat requests).
	ToolCalls []ToolCall
	// ToolCallID is the call a "tool" turn answers.
	ToolCallID string
}

// Tool is a function a chat request offers the model.
type Tool struct {
	// Name is [a-zA-Z0-9_-]{1,64}, what the model calls it by.
	Name        string
	Description string
	// Schema is the JSON Schema (an object) of the call's arguments.
	Schema json.RawMessage
}

// ToolCall is one call the model made.
type ToolCall struct {
	// ID pairs the call with its result (Message.ToolCallID).
	ID   string
	Name string
	// Arguments is what the model passed: a JSON object as it wrote it,
	// compacted when it is one. A model may write anything here, so the
	// caller checks it before acting on it.
	Arguments json.RawMessage
	// ThoughtSignature is opaque Gemini reasoning state, replayed with this call.
	ThoughtSignature string
}

// Request asks the model for one JSON object (OutputSchema set) or for a
// chat answer.
type Request struct {
	System   string
	Messages []Message
	// OutputName names the object (the tool or the schema),
	// [a-zA-Z0-9_-]{1,64}; structured requests only.
	OutputName string
	// OutputSchema is the JSON Schema of the object the model must return.
	// Setting it makes the request structured: Tools and OnText are not
	// used.
	OutputSchema json.RawMessage
	// Tools are what a chat request offers the model to call; the model
	// decides whether to.
	Tools []Tool
	// OnText, when set, asks for the chat answer's text as it is written,
	// one piece at a time, in order. A service that cannot stream hands
	// the whole text over in one piece. It is called on Complete's
	// goroutine.
	OnText func(delta string)
	// MaxTokens caps the answer on Anthropic, which requires a cap; 0 means
	// 8192. OpenAI-compatible requests send none: newer OpenAI models
	// refuse max_tokens and the others do not need it.
	MaxTokens int
}

// Usage is what the request cost, as the service counted it.
type Usage struct {
	Input, Output int
}

// Response is the model's answer.
type Response struct {
	// Output is the one JSON object the model returned, compacted
	// (structured requests).
	Output json.RawMessage
	// Text is a chat answer's text, a reasoning model's <think> block
	// taken out; ToolCalls are the calls it made, in order.
	Text      string
	ToolCalls []ToolCall
	// Stop is why a chat answer ended: StopEnd, StopTool or StopLength.
	Stop  string
	Usage Usage
}

// Error is why a request did not produce an answer.
type Error struct {
	// Kind is one of the Kind constants.
	Kind string
	// Status is the HTTP status of the service's refusal, 0 without one.
	Status int
	// Message is the service's own words where it gave any, with the API
	// key replaced by ••••, at most 2 KB.
	Message string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("ai: %s (HTTP %d): %s", e.Kind, e.Status, e.Message)
	}
	return fmt.Sprintf("ai: %s: %s", e.Kind, e.Message)
}

// Complete sends req and returns the model's answer. A cancelled ctx
// returns ctx's error as is; every other failure is an *Error.
func Complete(ctx context.Context, cfg Config, req Request) (Response, error) {
	base, err := cfg.endpoint()
	if err != nil {
		return Response{}, err
	}
	structured := len(req.OutputSchema) > 0
	if structured {
		err = cfg.checkRequest(req)
	} else {
		err = cfg.checkChat(req)
	}
	if err != nil {
		return Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, CompleteTimeout)
	defer cancel()
	cfg.retry = true
	switch {
	case !structured:
		return completeChat(ctx, cfg, base, req)
	case cfg.Provider == Anthropic:
		return completeAnthropic(ctx, cfg, base, req)
	}
	return completeOpenAI(ctx, cfg, base, req)
}

// Test sends the smallest real request the service answers — no schema, a
// few tokens — to tell a working connection from a refused key or an
// address that does not answer.
func Test(ctx context.Context, cfg Config) error {
	base, err := cfg.endpoint()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, TestTimeout)
	defer cancel()
	if cfg.Provider == Anthropic {
		return testAnthropic(ctx, cfg, base)
	}
	return testOpenAI(ctx, cfg, base)
}

// Keyless says an OpenAI-compatible service at baseURL needs no key: it
// runs on this computer (Ollama, LM Studio…), which only loopback proves.
// Anthropic always needs one.
func Keyless(p Provider, baseURL string) bool {
	if p != OpenAI {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// endpoint checks the connection is complete and returns its base URL
// without a trailing slash.
func (cfg Config) endpoint() (string, error) {
	return cfg.connectionEndpoint(true)
}

// connectionEndpoint also serves model discovery, which needs no selected model.
func (cfg Config) connectionEndpoint(requireModel bool) (string, error) {
	base := cfg.BaseURL
	switch cfg.Provider {
	case Anthropic:
		if base == "" {
			base = DefaultAnthropicBaseURL
		}
	case OpenAI:
	default:
		return "", cfg.fail(KindConfig, 0, i18n.F("不支持的模型服务「%s」", cfg.Provider))
	}
	switch {
	case strings.TrimSpace(base) == "":
		return "", cfg.fail(KindConfig, 0, i18n.T("还没有填写服务地址"))
	case requireModel && strings.TrimSpace(cfg.Model) == "":
		return "", cfg.fail(KindConfig, 0, i18n.T("还没有填写模型"))
	case cfg.APIKey == "" && !Keyless(cfg.Provider, base):
		return "", cfg.fail(KindConfig, 0, i18n.T("还没有保存 API Key"))
	}
	return strings.TrimRight(strings.TrimSpace(base), "/"), nil
}

var outputName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// checkRequest refuses a structured request no service could answer as
// asked.
func (cfg Config) checkRequest(req Request) error {
	if !outputName.MatchString(req.OutputName) {
		return cfg.fail(KindConfig, 0, i18n.F("输出名称「%s」不合要求", req.OutputName))
	}
	if _, ok := asObject(string(req.OutputSchema)); !ok {
		return cfg.fail(KindConfig, 0, i18n.T("输出格式（JSON Schema）不是一个 JSON 对象"))
	}
	if len(req.Messages) == 0 {
		return cfg.fail(KindConfig, 0, i18n.T("请求里没有消息"))
	}
	for _, m := range req.Messages {
		if m.Role != RoleUser && m.Role != RoleAssistant {
			return cfg.fail(KindConfig, 0, i18n.F("消息角色「%s」不合要求", m.Role))
		}
		if len(m.ToolCalls) > 0 || m.ToolCallID != "" {
			return cfg.fail(KindConfig, 0, i18n.T("要求固定格式输出的请求不能带工具调用"))
		}
	}
	return nil
}

// fail builds an *Error whose message carries no trace of the key.
func (cfg Config) fail(kind string, status int, message string) *Error {
	if cfg.APIKey != "" {
		message = strings.ReplaceAll(message, cfg.APIKey, keyMask)
	}
	return &Error{Kind: kind, Status: status, Message: clip(strings.TrimSpace(message), messageLimit)}
}

// send posts body as JSON and returns the service's response, whose body
// the caller closes. A request that got no answer comes back as an
// unreachable *Error, or as ctx's error when the caller called it off.
// With cfg.retry, no answer or a transient refusal is asked again after
// each of RetryDelays: nothing of the answer has been read or streamed yet,
// so the caller sees only the attempt that counted.
func (cfg Config) send(ctx context.Context, url string, header http.Header, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, cfg.fail(KindConfig, 0, err.Error())
	}
	client := cfg.HTTP
	if client == nil {
		// http.DefaultClient would ignore the app's proxy setting, and a
		// model service is exactly what a blocked network cannot reach.
		client = netproxy.Client()
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
		if err != nil {
			return nil, cfg.fail(KindConfig, 0, i18n.F("服务地址无效：%v", err))
		}
		req.Header = header
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		last := !cfg.retry || attempt >= len(RetryDelays)
		if err != nil {
			if last || ctx.Err() != nil {
				return nil, cfg.noAnswer(ctx, err)
			}
		} else {
			if last || !transient(resp.StatusCode) {
				return resp, nil
			}
			// A content check turns the same words down every time (MiniMax
			// answers some with a 500): hand that refusal on, unasked again.
			refusal, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			if refusesContent.Match(refusal) {
				resp.Body = io.NopCloser(bytes.NewReader(refusal))
				return resp, nil
			}
		}
		wait := RetryDelays[attempt]
		if resp != nil {
			wait = retryAfter(resp.Header, wait)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, cfg.noAnswer(ctx, ctx.Err())
		case <-timer.C:
		}
	}
}

// transient says a refusal may not recur: a timeout, a conflict, a rate
// limit, the service failing or overloaded (Anthropic's 529).
func transient(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || status >= 500
}

// retryAfter is the wait a refusal's Retry-After asks for in seconds, when
// it asks for no more than maxRetryAfter; otherwise fallback.
func retryAfter(h http.Header, fallback time.Duration) time.Duration {
	s, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || s < 0 || time.Duration(s)*time.Second > maxRetryAfter {
		return fallback
	}
	return time.Duration(s) * time.Second
}

// post sends body as JSON and returns the status and the whole answer.
func (cfg Config) post(ctx context.Context, url string, header http.Header, body any) (int, []byte, error) {
	resp, err := cfg.send(ctx, url, header, body)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	return cfg.readAll(ctx, resp)
}

// readAll reads an answer: a refusal up to maxErrorBody, anything else up
// to maxResponse.
func (cfg Config) readAll(ctx context.Context, resp *http.Response) (int, []byte, error) {
	limit := int64(maxResponse)
	if resp.StatusCode/100 != 2 {
		limit = maxErrorBody
	}
	answer, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return 0, nil, cfg.noAnswer(ctx, err)
	}
	return resp.StatusCode, answer, nil
}

// noAnswer classifies a request that got no (whole) answer.
func (cfg Config) noAnswer(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return cfg.fail(KindUnreachable, 0, i18n.F("等待模型服务超时：%v", err))
	}
	return cfg.fail(KindUnreachable, 0, err.Error())
}

// refused turns a non-2xx answer into an *Error: refused when the service's
// content check turned the request down, auth for a refused key, server
// with the service's own message otherwise.
func (cfg Config) refused(status int, body []byte) *Error {
	kind := KindServer
	switch {
	case refusesContent.Match(body):
		kind = KindRefused
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = KindAuth
	}
	message := serverMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}
	return cfg.fail(kind, status, message)
}

// refusesContent finds a service's content check turning a request down, in
// the words services answer with: MiniMax's input/output new_sensitive
// (1026/1027), OpenAI's, Azure's and Moonshot's content_filter and
// content_policy_violation, DashScope's data_inspection_failed, DeepSeek's
// Content Exists Risk, and the sensitive content of Zhipu and others. Asking again
// with the same words gets the same answer.
var refusesContent = regexp.MustCompile(`(?i)new_sensitive|\bcontent_filter\b|content_policy_violation|data_inspection_failed|content exists risk|敏感内容`) // i18n-ignore: words services answer with

// streamError is an error event in a streamed answer: refused when the
// content check sent it, else the service's own error.
func (cfg Config) streamError(data, message string) *Error {
	if refusesContent.MatchString(data) {
		return cfg.fail(KindRefused, 0, message)
	}
	return cfg.fail(KindServer, 0, message)
}

// serverMessage is the service's explanation: error.message (both APIs),
// a plain error string or a top-level message, else the body itself.
func serverMessage(body []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &v) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		var plain string
		switch {
		case json.Unmarshal(v.Error, &nested) == nil && nested.Message != "":
			return nested.Message
		case json.Unmarshal(v.Error, &plain) == nil && plain != "":
			return plain
		case v.Message != "":
			return v.Message
		}
	}
	return strings.TrimSpace(string(body))
}

// clip cuts s to at most limit bytes on a rune boundary.
func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// extractObject finds the one JSON object in a model's text answer. Models
// that were only asked in words wrap it: a leading <think>…</think> block,
// ```json fences, a sentence before or after. Failing the plain cases, the
// first balanced {…} that parses is taken.
func extractObject(text string) (json.RawMessage, bool) {
	s := strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(s, "<think>"); ok {
		if _, after, closed := strings.Cut(rest, "</think>"); closed {
			rest = after
		}
		s = strings.TrimSpace(rest)
	}
	if strings.HasPrefix(s, "```") {
		if _, body, ok := strings.Cut(s, "\n"); ok {
			s = body
		} else {
			s = ""
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	if obj, ok := asObject(s); ok {
		return obj, true
	}
	for i := range len(s) {
		if s[i] != '{' {
			continue
		}
		if end := balancedEnd(s[i:]); end > 0 {
			if obj, ok := asObject(s[i : i+end]); ok {
				return obj, true
			}
		}
	}
	return nil, false
}

// balancedEnd is the length of the {…} that s starts with, minding strings
// and escapes; -1 when it never closes.
func balancedEnd(s string) int {
	depth, inString, escaped := 0, false, false
	for i := range len(s) {
		c := s[i]
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// asObject is s compacted when it is exactly one JSON object.
func asObject(s string) (json.RawMessage, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") || !json.Valid([]byte(s)) {
		return nil, false
	}
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(s)); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}
