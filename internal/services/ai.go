package services

// AI Assistant's connection to a model service: which service and
// model, the API key, how far one request may go, the Movies & TV organization naming
// style, each account's Before Sending to AI choices, and the proxy every model
// request goes through. The webview builds each request — the authorization card
// shows the very object that is sent — and Go only adds the key and the
// provider's envelope, so the key never reaches the webview.

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/ai"
	"github.com/tonyflux/pkp/internal/i18n"
)

const (
	// aiKeyAccount names the API key's keychain item, beside the
	// Remember account passwords (which are named by login, never like this).
	aiKeyAccount = "pkp-ai-api-key"
	// The range of Per-conversation limit, in tokens; 0 (unlimitedAITokens) is Unlimited.
	minAITokenLimit     = 1000
	maxAITokenLimit     = 2000000
	defaultAITokenLimit = 50000
	unlimitedAITokens   = 0
	// The Movies & TV organization naming styles.
	namingPlex    = "plex"
	namingChinese = "chinese"
	namingCustom  = "custom"
	// maxAIKeyBytes bounds a pasted key; real ones are a few hundred bytes.
	maxAIKeyBytes = 4096
	// keyHintRunes is how much of the key the settings page may show, and
	// only of a key at least twice that long.
	keyHintRunes = 4
	// maxAICapabilities bounds the services settings.json remembers; past
	// it only the one just tested stays.
	maxAICapabilities = 32
	// The levels of Before Sending to AI: L1 metadata (names, paths, sizes, times)
	// and L2 content (subtitle text).
	aiConsentMetadata = "metadata"
	aiConsentContent  = "content"
)

// EventAIDelta carries a streamed AIChat answer's text as it is written.
const EventAIDelta = "ai:delta"

// AIDelta is the payload of EventAIDelta: the next piece of the text of
// the AIChat running under ID.
type AIDelta struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

var (
	errAINotConfigured = i18n.New("还没有配置模型")
	errAINoModel       = i18n.New("还没有填写模型")
	errAINoKey         = i18n.New("还没有保存 API Key")
	errAINoKeychain    = &lazyError{func() string { return i18n.F("这台电脑没有可用的%s，无法保存 API Key", keychainName()) }}
	errAIKeyStore      = &lazyError{func() string { return i18n.F("无法把 API Key 存进%s", keychainName()) }}
	errAIKeyForget     = &lazyError{func() string { return i18n.F("无法从%s删除 API Key", keychainName()) }}
	errAIKeyFormat     = i18n.New("API Key 不能包含空格或换行")
	errAIKeyTooLong    = i18n.New("API Key 太长")
	errAIProvider      = i18n.New("请选择 Anthropic 或 OpenAI 兼容服务")
	errAIBaseURL       = i18n.New("服务地址需要以 http:// 或 https:// 开头")
	errAIModel         = i18n.New("模型名称不能包含换行等控制字符")
	errAITokenLimit    = i18n.New("单个对话上限需在 1,000 到 2,000,000 tokens 之间，或选择不限制")
	errAINaming        = i18n.New("请选择 Plex 风格、中文风格或自定义命名")
	errAICancelled     = i18n.New("已取消")
	errAICallInFlight  = i18n.New("这次请求已经在进行")
	// errAINoTools is matched on by the webview, which then carries the
	// conversation on without tools: keep the words exactly.
	errAINoTools      = i18n.New("这个模型不支持工具调用")
	errAIConsentLevel = i18n.New("授权级别只能是 metadata 或 content")
)

// emitAIDelta sends a streamed piece to the webview; tests replace it.
var emitAIDelta = func(delta AIDelta) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventAIDelta, delta)
	}
}

// AICapability is what a model service was found to do: "yes", "no" or
// "" (not known yet).
type AICapability struct {
	// Tools says chat requests may offer it tools.
	Tools string `json:"tools,omitempty"`
	// Stream says it streams a chat answer's text.
	Stream string `json:"stream,omitempty"`
}

// AISettings is Settings · AI as the webview shows it. The key itself never
// leaves Go: KeyHint is all the page learns of it.
type AISettings struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	// TokenLimit is Per-conversation limit; 0 is Unlimited.
	TokenLimit int `json:"tokenLimit"`
	// KeyHint is "••••8E4C" (the key's last four characters), "" when no
	// key is stored for the current provider and base URL.
	KeyHint string `json:"keyHint"`
	// Keychain is false where there is no keychain: a key cannot be saved.
	Keychain bool `json:"keychain"`
	// Configured means a model is set and the service can be asked: a key
	// is stored for it, or it is an OpenAI-compatible service on this
	// computer (localhost, 127.0.0.1, ::1), which needs none.
	Configured     bool   `json:"configured"`
	Naming         string `json:"naming"`
	SeriesTemplate string `json:"seriesTemplate"`
	MovieTemplate  string `json:"movieTemplate"`
	// Tools and Stream are what the current service was found to do:
	// "yes", "no" or "" (not tested yet). Tools "no" means AIChat cannot
	// offer it tools.
	Tools  string `json:"tools"`
	Stream string `json:"stream"`
}

// AITestResult is what Test Connection found. Kind is "ok", "auth" (the key was
// refused), "unreachable", "server" (the service answered with an error) or
// "config" (nothing to test yet); Message is the service's own words, the
// key scrubbed out. A working connection is also asked whether it takes
// tools and streams: Tools and Stream are "yes", "no" or "" (unknown).
type AITestResult struct {
	OK      bool   `json:"ok"`
	Kind    string `json:"kind"`
	Status  int    `json:"status"`
	Message string `json:"message"`
	Tools   string `json:"tools"`
	Stream  string `json:"stream"`
}

// AIMessage is one turn of a model request: Role "user" or "assistant".
type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AIRequest is a model request as the webview built it.
type AIRequest struct {
	System   string      `json:"system"`
	Messages []AIMessage `json:"messages"`
	// OutputName names the object asked for, [a-zA-Z0-9_-]{1,64};
	// OutputSchema is its JSON Schema, as JSON text.
	OutputName   string `json:"outputName"`
	OutputSchema string `json:"outputSchema"`
	// MaxTokens caps an Anthropic answer; 0 means 8192.
	MaxTokens int `json:"maxTokens"`
}

// AIReply is the model's answer: the object as JSON text, which the
// webview parses and checks field by field, and what it cost.
type AIReply struct {
	Output       string `json:"output"`
	Input        int    `json:"input"`
	OutputTokens int    `json:"outputTokens"`
}

// AITool is a function an AIChat offers the model.
type AITool struct {
	// Name is [a-zA-Z0-9_-]{1,64}.
	Name        string `json:"name"`
	Description string `json:"description"`
	// Schema is the JSON Schema (an object) of its arguments, as JSON text.
	Schema string `json:"schema"`
}

// AIToolCall is one call the model made.
type AIToolCall struct {
	// ID pairs the call with its result (AIChatMessage.ToolCallID).
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments is the JSON object the model passed, as text — as the
	// model wrote it, so the webview checks it before acting on it.
	Arguments        string `json:"arguments"`
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

// AIChatMessage is one turn of a conversation: Role "user", "assistant"
// (with the calls it made) or "tool" (the result of the call ToolCallID).
type AIChatMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []AIToolCall `json:"toolCalls"`
	ToolCallID string       `json:"toolCallId"`
}

// AIChatRequest is a conversation turn as the webview built it. Stream
// asks for the answer's text on EventAIDelta as it is written.
type AIChatRequest struct {
	System   string          `json:"system"`
	Messages []AIChatMessage `json:"messages"`
	Tools    []AITool        `json:"tools"`
	Stream   bool            `json:"stream"`
}

// AIChatReply is the model's answer: its text, the calls it made, why it
// stopped ("end", "tool" — waiting for the calls' results — or "length")
// and what it cost.
type AIChatReply struct {
	Text         string       `json:"text"`
	ToolCalls    []AIToolCall `json:"toolCalls"`
	Stop         string       `json:"stop"`
	Input        int          `json:"input"`
	OutputTokens int          `json:"outputTokens"`
}

// AISettings reads Settings · AI.
func (d *DriveService) AISettings() AISettings {
	d.settingsMu.Lock()
	s := d.readSettings()
	d.settingsMu.Unlock()
	return d.aiSettings(s)
}

// SetAIConnection sets the model service, its base URL (OpenAI compatible only;
// "" means the default) and the model. A stored key that was entered for
// another provider or base URL is deleted first: a key never goes to an
// endpoint it was not entered for.
func (d *DriveService) SetAIConnection(provider, baseURL, model string) (AISettings, error) {
	provider = strings.TrimSpace(provider)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	model = strings.TrimSpace(model)
	if provider != string(ai.Anthropic) && provider != string(ai.OpenAI) {
		return d.AISettings(), errAIProvider
	}
	if baseURL == "" {
		baseURL = ai.DefaultOpenAIBaseURL
	} else if !validBaseURL(baseURL) {
		return d.AISettings(), errAIBaseURL
	}
	if strings.IndexFunc(model, unicode.IsControl) >= 0 {
		return d.AISettings(), errAIModel
	}
	return d.changeAISettings(func(s *LocalSettings) error {
		if s.AIKeyFor != "" && s.AIKeyFor != aiKeyFor(provider, baseURL) {
			if err := d.forgetAIKey(); err != nil {
				return err
			}
			s.AIKeyFor = ""
		}
		s.AIProvider, s.AIBaseURL, s.AIModel = provider, baseURL, model
		return nil
	})
}

// SetAIKey stores the API key in the keychain for the current provider and
// base URL; "" deletes it. It never touches settings.json beyond noting
// which endpoint the key belongs to.
func (d *DriveService) SetAIKey(key string) (AISettings, error) {
	key = strings.TrimSpace(key)
	if key != "" {
		if d.vault == nil {
			return d.AISettings(), errAINoKeychain
		}
		if strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return d.AISettings(), errAIKeyFormat
		}
		if len(key) > maxAIKeyBytes {
			return d.AISettings(), errAIKeyTooLong
		}
	}
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	s := d.readSettings()
	if key == "" {
		if err := d.forgetAIKey(); err != nil {
			return d.aiSettings(s), err
		}
		s.AIKeyFor = ""
		err := d.writeSettings(s)
		return d.aiSettings(d.readSettings()), err
	}
	// The security tool's complaint may quote what it was given, so the
	// error is not passed on: neither the page nor the log may see it.
	if err := d.vault.SetPassword(aiKeyAccount, key); err != nil {
		d.log.Warn("ai: the keychain did not take the API key")
		return d.aiSettings(s), errAIKeyStore
	}
	s.AIKeyFor = aiKeyFor(s.AIProvider, s.AIBaseURL)
	if err := d.writeSettings(s); err != nil {
		// A key settings.json does not bind to anything would be sent
		// nowhere and never shown; take it back out.
		_ = d.vault.DeletePassword(aiKeyAccount)
		return d.aiSettings(d.readSettings()), err
	}
	return d.aiSettings(s), nil
}

// SetAITokenLimit sets Per-conversation limit; unlimitedAITokens (0) is Unlimited.
func (d *DriveService) SetAITokenLimit(limit int) (AISettings, error) {
	if limit != unlimitedAITokens && (limit < minAITokenLimit || limit > maxAITokenLimit) {
		return d.AISettings(), errAITokenLimit
	}
	return d.changeAISettings(func(s *LocalSettings) error {
		s.AITokenLimit = limit
		return nil
	})
}

// SetAINaming sets the Movies & TV organization naming style and the custom templates,
// which are kept as given (the webview checks them before saving) so that
// switching to a preset and back keeps them.
func (d *DriveService) SetAINaming(style, series, movie string) (AISettings, error) {
	if !validNaming(style) {
		return d.AISettings(), errAINaming
	}
	return d.changeAISettings(func(s *LocalSettings) error {
		s.AINaming, s.AISeriesTemplate, s.AIMovieTemplate = style, series, movie
		return nil
	})
}

// TestAIConnection sends the smallest real request to the configured
// service and says what came of it; a working one is then asked whether it
// takes tools and streams, and the answers are kept for that service. It
// never fails: the outcome is the answer.
func (d *DriveService) TestAIConnection() AITestResult {
	cfg, capKey, err := d.aiConfig()
	if err != nil {
		return AITestResult{Kind: ai.KindConfig, Message: err.Error()}
	}
	err = ai.Test(context.Background(), cfg)
	if err == nil {
		found := ai.Probe(context.Background(), cfg)
		caps := d.rememberAICapability(capKey, func(c *AICapability) {
			// An unknown answer (an outage) keeps what was found before.
			if found.Tools != ai.CapUnknown {
				c.Tools = found.Tools
			}
			if found.Stream != ai.CapUnknown {
				c.Stream = found.Stream
			}
		})
		return AITestResult{OK: true, Kind: "ok", Tools: caps.Tools, Stream: caps.Stream}
	}
	var e *ai.Error
	if !errors.As(err, &e) {
		return AITestResult{Kind: ai.KindUnreachable, Message: err.Error()}
	}
	kind := e.Kind
	if kind == ai.KindInvalid || kind == ai.KindTruncated || kind == ai.KindRefused {
		kind = ai.KindServer
	}
	return AITestResult{Kind: kind, Status: e.Status, Message: e.Message}
}

// SetAIConsent sets the account's Before Sending to AI choice for one level of data:
// "metadata" (L1: names, paths, sizes, times) or "content" (L2: subtitle
// text). always = Always Allow, otherwise each task asks first. Each account
// decides for its own files; the choice goes with the account when it is
// removed.
func (d *DriveService) SetAIConsent(account, level string, always bool) error {
	var field func(*accountRecord) *bool
	switch level {
	case aiConsentMetadata:
		field = func(r *accountRecord) *bool { return &r.AIConsent }
	case aiConsentContent:
		field = func(r *accountRecord) *bool { return &r.AIContentConsent }
	default:
		return errAIConsentLevel
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return err
	}
	s.mu.Lock()
	before := *field(&s.rec)
	*field(&s.rec) = always
	s.mu.Unlock()
	if err := d.saveAccounts(); err != nil {
		s.mu.Lock()
		*field(&s.rec) = before
		s.mu.Unlock()
		return err
	}
	d.emitAccounts()
	return nil
}

// AIComplete sends the webview's request to the configured model and
// returns the object it answered with. id is the caller's handle for
// AICancel; "" cannot be cancelled. A call is bounded by ai.CompleteTimeout.
func (d *DriveService) AIComplete(id string, req AIRequest) (AIReply, error) {
	cfg, _, err := d.aiConfig()
	if err != nil {
		return AIReply{}, errAINotConfigured
	}
	ctx, done, err := d.startAICall(id)
	if err != nil {
		return AIReply{}, err
	}
	defer done()
	messages := make([]ai.Message, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = ai.Message{Role: m.Role, Content: m.Content}
	}
	resp, err := ai.Complete(ctx, cfg, ai.Request{
		System:       req.System,
		Messages:     messages,
		OutputName:   req.OutputName,
		OutputSchema: json.RawMessage(req.OutputSchema),
		MaxTokens:    req.MaxTokens,
	})
	if err != nil {
		if ctx.Err() != nil {
			return AIReply{}, errAICancelled
		}
		return AIReply{}, d.aiFailure(err)
	}
	return AIReply{Output: string(resp.Output), Input: resp.Usage.Input, OutputTokens: resp.Usage.Output}, nil
}

// AIChat sends one step of a conversation to the configured model: the
// turns so far and the tools it may call. With Stream the answer's text
// goes out on EventAIDelta under id while it is written (a service that
// cannot stream sends it in one piece); the reply carries all of it
// either way. id is the caller's handle for AICancel, shared with
// AIComplete; "" cannot be cancelled. A service that refuses tools fails
// with This model does not support tool calls, and is remembered as such.
func (d *DriveService) AIChat(id string, req AIChatRequest) (AIChatReply, error) {
	cfg, capKey, err := d.aiConfig()
	if err != nil {
		return AIChatReply{}, errAINotConfigured
	}
	ctx, done, err := d.startAICall(id)
	if err != nil {
		return AIChatReply{}, err
	}
	defer done()
	areq := ai.Request{System: req.System, Messages: make([]ai.Message, len(req.Messages))}
	for i, m := range req.Messages {
		msg := ai.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, ai.ToolCall{ID: c.ID, Name: c.Name, Arguments: json.RawMessage(c.Arguments), ThoughtSignature: c.ThoughtSignature})
		}
		areq.Messages[i] = msg
	}
	for _, t := range req.Tools {
		areq.Tools = append(areq.Tools, ai.Tool{Name: t.Name, Description: t.Description, Schema: json.RawMessage(t.Schema)})
	}
	if req.Stream {
		areq.OnText = func(text string) { emitAIDelta(AIDelta{ID: id, Text: text}) }
	}
	resp, err := ai.Complete(ctx, cfg, areq)
	if err != nil {
		if ctx.Err() != nil {
			return AIChatReply{}, errAICancelled
		}
		var e *ai.Error
		if errors.As(err, &e) && e.Kind == ai.KindNoTools {
			d.rememberAICapability(capKey, func(c *AICapability) { c.Tools = ai.CapNo })
		}
		return AIChatReply{}, d.aiFailure(err)
	}
	reply := AIChatReply{Text: resp.Text, Stop: resp.Stop, Input: resp.Usage.Input, OutputTokens: resp.Usage.Output}
	for _, c := range resp.ToolCalls {
		reply.ToolCalls = append(reply.ToolCalls, AIToolCall{ID: c.ID, Name: c.Name, Arguments: string(c.Arguments), ThoughtSignature: c.ThoughtSignature})
	}
	return reply, nil
}

// AICancel calls off the AIComplete or AIChat running under id; nothing
// running under it is not an error.
func (d *DriveService) AICancel(id string) {
	d.aiCallsMu.Lock()
	cancel := d.aiCalls[id]
	d.aiCallsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

/* ---------------- helpers ---------------- */

// startAICall registers a model call under id for AICancel ("" registers
// nothing) and returns its context and what to call when it ends.
func (d *DriveService) startAICall(id string) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	if id == "" {
		return ctx, cancel, nil
	}
	d.aiCallsMu.Lock()
	defer d.aiCallsMu.Unlock()
	if _, busy := d.aiCalls[id]; busy {
		cancel()
		return nil, nil, errAICallInFlight
	}
	if d.aiCalls == nil {
		d.aiCalls = map[string]context.CancelFunc{}
	}
	d.aiCalls[id] = cancel
	return ctx, func() {
		cancel()
		d.aiCallsMu.Lock()
		delete(d.aiCalls, id)
		d.aiCallsMu.Unlock()
	}, nil
}

// changeAISettings applies change to the saved settings and saves them; a
// change that fails saves nothing.
func (d *DriveService) changeAISettings(change func(*LocalSettings) error) (AISettings, error) {
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	s := d.readSettings()
	if err := change(&s); err != nil {
		return d.aiSettings(s), err
	}
	if err := d.writeSettings(s); err != nil {
		return d.aiSettings(d.readSettings()), err
	}
	return d.aiSettings(s), nil
}

// rememberAICapability updates what is known of the service capKey names
// and saves it; it returns what is known now. A failed save is only
// logged: the answer still stands for this call.
func (d *DriveService) rememberAICapability(capKey string, change func(*AICapability)) AICapability {
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	s := d.readSettings()
	c, known := s.AICapabilities[capKey]
	change(&c)
	if s.AICapabilities == nil || (!known && len(s.AICapabilities) >= maxAICapabilities) {
		s.AICapabilities = map[string]AICapability{}
	}
	s.AICapabilities[capKey] = c
	if err := d.writeSettings(s); err != nil {
		d.log.Warn("ai: what the model service can do was not saved", "error", err)
	}
	return c
}

// aiSettings is s as Settings · AI shows it, the key hint read from the
// keychain (it is never stored).
func (d *DriveService) aiSettings(s LocalSettings) AISettings {
	caps := s.AICapabilities[aiCapabilityKey(s)]
	out := AISettings{
		Provider:       s.AIProvider,
		BaseURL:        s.AIBaseURL,
		Model:          s.AIModel,
		TokenLimit:     s.AITokenLimit,
		Keychain:       d.vault != nil,
		Naming:         s.AINaming,
		SeriesTemplate: s.AISeriesTemplate,
		MovieTemplate:  s.AIMovieTemplate,
		Tools:          caps.Tools,
		Stream:         caps.Stream,
	}
	if key := d.storedAIKey(s); key != "" {
		out.KeyHint = keyHint(key)
	}
	out.Configured = out.Model != "" && (out.KeyHint != "" || aiKeyless(s))
	return out
}

// aiConfig is the connection every model request goes through: the saved
// settings and the key stored for them (none for a keyless service on
// this computer), and the key its capabilities are kept under.
func (d *DriveService) aiConfig() (ai.Config, string, error) {
	d.settingsMu.Lock()
	s := d.readSettings()
	d.settingsMu.Unlock()
	if s.AIModel == "" {
		return ai.Config{}, "", errAINoModel
	}
	key := d.storedAIKey(s)
	if key == "" && !aiKeyless(s) {
		return ai.Config{}, "", errAINoKey
	}
	cfg := ai.Config{Provider: ai.Provider(s.AIProvider), Model: s.AIModel, APIKey: key}
	if cfg.Provider == ai.OpenAI {
		cfg.BaseURL = s.AIBaseURL
	}
	return cfg, aiCapabilityKey(s), nil
}

// aiKeyless says s points at an OpenAI-compatible service on this
// computer, which is asked without a key.
func aiKeyless(s LocalSettings) bool {
	return ai.Keyless(ai.Provider(s.AIProvider), s.AIBaseURL)
}

// aiCapabilityKey names the service s points at in AICapabilities: the
// endpoint as a key is bound to it, and the model.
func aiCapabilityKey(s LocalSettings) string {
	return aiKeyFor(s.AIProvider, s.AIBaseURL) + " " + s.AIModel
}

// storedAIKey is the key kept for s's provider and base URL, "" when there
// is none or it was entered for another endpoint.
func (d *DriveService) storedAIKey(s LocalSettings) string {
	if d.vault == nil || s.AIKeyFor == "" || s.AIKeyFor != aiKeyFor(s.AIProvider, s.AIBaseURL) {
		return ""
	}
	key, err := d.vault.Password(aiKeyAccount)
	if err != nil {
		if !errors.Is(err, ErrNoSavedPassword) {
			d.log.Warn("ai: the API key could not be read from the keychain", "error", err)
		}
		return ""
	}
	return key
}

// forgetAIKey deletes the stored key; having none is not an error.
func (d *DriveService) forgetAIKey() error {
	if d.vault == nil {
		return nil
	}
	if err := d.vault.DeletePassword(aiKeyAccount); err != nil {
		d.log.Warn("ai: the API key could not be deleted", "error", err)
		return errAIKeyForget
	}
	return nil
}

// aiFailure words a model error for the page, by kind.
func (d *DriveService) aiFailure(err error) error {
	var e *ai.Error
	if !errors.As(err, &e) {
		return err
	}
	d.log.Warn("ai: model request failed", "kind", e.Kind, "status", e.Status)
	switch e.Kind {
	case ai.KindAuth:
		return i18n.Errorf("API Key 无效或没有权限：%s", e.Message)
	case ai.KindUnreachable:
		return i18n.Errorf("连不上模型服务：%s", e.Message)
	case ai.KindServer:
		return i18n.Errorf("模型服务返回错误（HTTP %d）：%s", e.Status, e.Message)
	case ai.KindTruncated:
		return i18n.New("模型输出不完整（达到输出上限）")
	case ai.KindInvalid:
		return i18n.Errorf("模型没有按要求的格式回答：%s", e.Message)
	case ai.KindNoTools:
		return errAINoTools
	case ai.KindRefused:
		// The webview matches the start (CONTENT_REFUSED in agent.ts): keep
		// the words exactly; the translations start like agent.ts's.
		return i18n.Errorf("模型服务的内容审核拒绝了这次请求：%s", e.Message)
	}
	return errors.New(e.Message)
}

// aiKeyFor names the endpoint a key is bound to. Anthropic's is fixed, so
// the OpenAI-compatible base URL kept meanwhile does not unbind its key.
func aiKeyFor(provider, baseURL string) string {
	if provider == string(ai.Anthropic) {
		baseURL = ai.DefaultAnthropicBaseURL
	}
	return provider + " " + baseURL
}

func validBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func validNaming(style string) bool {
	return style == namingPlex || style == namingChinese || style == namingCustom
}

// keyHint shows the key's last few characters, and only "••••" of a key
// too short to give away that much.
func keyHint(key string) string {
	if utf8.RuneCountInString(key) < 2*keyHintRunes {
		return "••••"
	}
	runes := []rune(key)
	return "••••" + string(runes[len(runes)-keyHintRunes:])
}
