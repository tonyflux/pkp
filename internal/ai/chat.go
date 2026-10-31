package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/tonyflux/pkp/internal/i18n"
)

// noStream remembers each base URL + model whose service refused a streamed
// request, so later requests go to it unstreamed straight away — like the
// response_format ladder, for this run of the app only.
var noStream sync.Map

// errStreamRefused is a chat request's stream refused: Complete asks again
// without it.
var errStreamRefused = errors.New("ai: the service refused to stream")

var (
	// mentionsStream finds a refusal about streaming ("stream",
	// "stream_options", "streaming" — not "upstream").
	mentionsStream = regexp.MustCompile(`(?i)\bstream`)
	// mentionsTools finds a refusal about the request's tools, in the
	// words services use for it.
	mentionsTools = regexp.MustCompile(`(?i)\btools?\b|tool_choice|tool_calls?\b|tool[ _]use|tool[ _]calling|function[ _]?call(s|ing)?\b`)
)

// Capability values: what Probe found a service does.
const (
	CapYes     = "yes"
	CapNo      = "no"
	CapUnknown = ""
)

// Capabilities is what a service does beyond plain chat, as Probe found.
type Capabilities struct {
	// Tools says chat requests may offer it tools.
	Tools string
	// Stream says it streams a chat answer's text.
	Stream string
}

// probeTool is the harmless tool Probe offers.
var probeTool = Tool{
	Name:        "current_time",
	Description: "Return the current time.",
	Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
}

// Probe asks the service, with two tiny chat requests side by side,
// whether it accepts a request offering a tool and whether it streams. A
// question the service answered with something else than a yes or a no
// (an outage, a timeout) stays CapUnknown. Probe also resets what this
// run remembers about the service's streaming.
func Probe(ctx context.Context, cfg Config) Capabilities {
	base, err := cfg.endpoint()
	if err != nil {
		return Capabilities{}
	}
	ask := func(tools []Tool, stream bool) (bool, error) {
		ctx, cancel := context.WithTimeout(ctx, TestTimeout)
		defer cancel()
		req := Request{
			Messages:  []Message{{Role: RoleUser, Content: "Reply with OK."}},
			Tools:     tools,
			MaxTokens: 16,
		}
		_, streamed, err := chatOnce(ctx, cfg, base, req, stream)
		return streamed, err
	}
	var caps Capabilities
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := ask([]Tool{probeTool}, false)
		var e *Error
		switch {
		case err == nil:
			caps.Tools = CapYes
		case errors.As(err, &e) && e.Kind == KindNoTools:
			caps.Tools = CapNo
		}
	}()
	go func() {
		defer wg.Done()
		streamed, err := ask(nil, true)
		key := base + "\x00" + cfg.Model
		switch {
		case err == nil && streamed:
			caps.Stream = CapYes
			noStream.Delete(key)
		case err == nil, errors.Is(err, errStreamRefused):
			// A plain JSON answer to a streamed request is as good as a
			// refusal: the text would only ever arrive in one piece.
			caps.Stream = CapNo
			noStream.Store(key, true)
		}
	}()
	wg.Wait()
	return caps
}

// checkChat refuses a chat request no service could answer as asked.
func (cfg Config) checkChat(req Request) error {
	if len(req.Messages) == 0 {
		return cfg.fail(KindConfig, 0, i18n.T("请求里没有消息"))
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
		case RoleAssistant:
			for _, c := range m.ToolCalls {
				if c.ID == "" || !outputName.MatchString(c.Name) {
					return cfg.fail(KindConfig, 0, i18n.F("工具调用「%s」不合要求", c.Name))
				}
			}
		case RoleTool:
			if m.ToolCallID == "" {
				return cfg.fail(KindConfig, 0, i18n.T("工具结果没有对应的调用"))
			}
		default:
			return cfg.fail(KindConfig, 0, i18n.F("消息角色「%s」不合要求", m.Role))
		}
	}
	seen := map[string]bool{}
	for _, t := range req.Tools {
		if !outputName.MatchString(t.Name) || seen[t.Name] {
			return cfg.fail(KindConfig, 0, i18n.F("工具名称「%s」不合要求", t.Name))
		}
		seen[t.Name] = true
		if _, ok := asObject(string(t.Schema)); !ok {
			return cfg.fail(KindConfig, 0, i18n.F("工具 %s 的参数格式（JSON Schema）不是一个 JSON 对象", t.Name))
		}
	}
	return nil
}

// completeChat sends a chat request, streamed when the caller wants the
// text as it comes and the service has not refused that before. A refused
// stream is asked again once without it, and remembered.
func completeChat(ctx context.Context, cfg Config, base string, req Request) (Response, error) {
	key := base + "\x00" + cfg.Model
	stream := req.OnText != nil
	if _, refused := noStream.Load(key); refused {
		stream = false
	}
	resp, _, err := chatOnce(ctx, cfg, base, req, stream)
	if errors.Is(err, errStreamRefused) {
		noStream.Store(key, true)
		resp, _, err = chatOnce(ctx, cfg, base, req, false)
	}
	return resp, err
}

// chatOnce sends one chat request. streamed says the answer did come as a
// stream; one that came whole is handed to OnText in one piece.
func chatOnce(ctx context.Context, cfg Config, base string, req Request, stream bool) (Response, bool, error) {
	if cfg.Provider == Anthropic {
		return chatAnthropic(ctx, cfg, base, req, stream)
	}
	return chatOpenAI(ctx, cfg, base, req, stream)
}

// chatRefused classifies a chat request's non-2xx answer: a 400/422 about
// streaming is errStreamRefused (when it streamed), one about tools is
// KindNoTools (when it offered any), anything else — a content check's
// refusal first, whatever words it uses — as refused says.
func (cfg Config) chatRefused(status int, body []byte, stream, tools bool) error {
	if (status == http.StatusBadRequest || status == http.StatusUnprocessableEntity) && !refusesContent.Match(body) {
		text := string(body)
		if stream && mentionsStream.MatchString(text) {
			return errStreamRefused
		}
		if tools && mentionsTools.MatchString(text) {
			message := serverMessage(body)
			if message == "" {
				message = http.StatusText(status)
			}
			return cfg.fail(KindNoTools, status, message)
		}
	}
	return cfg.refused(status, body)
}

// isEventStream says resp is a server-sent event stream; a service that
// ignored stream: true answers with plain JSON instead.
func isEventStream(resp *http.Response) bool {
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return media == "text/event-stream"
}

// readEvents calls each with every server-sent event's data (its data
// lines joined), in order, until the stream ends or each fails. Comments,
// event names and ids are not needed: both APIs repeat the type inside the
// data.
func readEvents(r io.Reader, each func(data string) error) error {
	scanner := bufio.NewScanner(io.LimitReader(r, maxResponse))
	scanner.Buffer(make([]byte, 0, 64<<10), maxResponse)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		joined := strings.Join(data, "\n")
		data = data[:0]
		return each(joined)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

// arguments is a tool call's arguments as sent back to the service: the
// object, or {} for none (or for something that is not one, which the
// caller already answered with an error result).
func arguments(raw json.RawMessage) json.RawMessage {
	if obj, ok := asObject(string(raw)); ok {
		return obj
	}
	return json.RawMessage(`{}`)
}

// received is a tool call's arguments as the model wrote them: compacted
// when they are an object, {} when empty, else the text as is.
func received(text string) json.RawMessage {
	if strings.TrimSpace(text) == "" {
		return json.RawMessage(`{}`)
	}
	if obj, ok := asObject(text); ok {
		return obj
	}
	return json.RawMessage(text)
}

// thinkFilter takes a reasoning model's <think>…</think> block out of its
// text as it streams in: a block may open only before any other text, and
// a tag may be split across pieces.
type thinkFilter struct {
	pending string
	// inside is true within a block; started once real text went out.
	inside, started bool
}

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// push takes the next piece and returns what may be shown of it so far.
func (f *thinkFilter) push(s string) string {
	if f.started {
		return s
	}
	f.pending += s
	for {
		if f.inside {
			end := strings.Index(f.pending, thinkClose)
			if end < 0 {
				// Keep only what could be the start of the closing tag.
				if keep := len(thinkClose) - 1; len(f.pending) > keep {
					f.pending = f.pending[len(f.pending)-keep:]
				}
				return ""
			}
			f.pending = f.pending[end+len(thinkClose):]
			f.inside = false
		}
		rest := strings.TrimLeft(f.pending, " \t\r\n")
		switch {
		case rest == "":
			return ""
		case strings.HasPrefix(rest, thinkOpen):
			f.pending = rest[len(thinkOpen):]
			f.inside = true
		case strings.HasPrefix(thinkOpen, rest):
			return "" // maybe "<thi" of a block: wait for more
		default:
			f.started = true
			f.pending = ""
			return rest
		}
	}
}

// finish returns what was held back at the end: a partial "<thi" that
// turned out to be text. An unclosed block is all thinking and dropped.
func (f *thinkFilter) finish() string {
	if f.started || f.inside {
		return ""
	}
	rest := strings.TrimLeft(f.pending, " \t\r\n")
	f.pending = ""
	return rest
}

// stripThink is text without its leading <think> block.
func stripThink(text string) string {
	var f thinkFilter
	return f.push(text) + f.finish()
}

// stopOf maps a service's stop reason, a call having been made or not.
func stopOf(length bool, calls int) string {
	switch {
	case length:
		return StopLength
	case calls > 0:
		return StopTool
	}
	return StopEnd
}

// whole hands a chat answer that came in one piece to OnText.
func whole(req Request, resp Response) {
	if req.OnText != nil && resp.Text != "" {
		req.OnText(resp.Text)
	}
}

// cutShort is a stream that ended before the service said it was done.
func (cfg Config) cutShort(ctx context.Context) error {
	if ctx.Err() != nil {
		return cfg.noAnswer(ctx, ctx.Err())
	}
	return cfg.fail(KindUnreachable, 0, i18n.T("模型服务的回答中途断开"))
}

// streamFailed classifies a stream that stopped with err: the service's own
// error event as it is, a broken read as no (whole) answer.
func (cfg Config) streamFailed(ctx context.Context, err error) error {
	var e *Error
	if errors.As(err, &e) {
		return err
	}
	return cfg.noAnswer(ctx, err)
}
