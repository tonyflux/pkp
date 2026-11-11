package subsrelay

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Audio goes up as 16 kHz mono signed 16-bit PCM, the format both of the
// service's transcription backends take.
const (
	audioFormat = "pcm_s16le"
	sampleRate  = 16000
)

const (
	// queueSize bounds the messages waiting for the network. Audio may fill
	// only half, so a control never waits behind it.
	queueSize    = 256
	writeTimeout = 10 * time.Second
)

// Start opens the session for one video.
type Start struct {
	GCID    string
	MediaID string
	FileID  string
	// TargetLanguage is the caption language, a code from
	// Node.SupportedLanguages; "" captions what is spoken, untranslated.
	TargetLanguage string
}

// Cue is one caption, shown from StartMS to EndMS of media time. The server
// never revises one it sent, and sends again what it had already sent when a
// session moves (start, seek), so the receiver drops repeats.
type Cue struct {
	StartMS  int64
	EndMS    int64
	Text     string
	Language string
}

// Message is one thing the server said; exactly one of Cue, Quota, Error
// and Stop is set.
type Message struct {
	Cue   *Cue
	Quota *Quota
	// Error is a refusal inside the session. "soniox_unavailable" leaves
	// the session open (the next audio retries); the others are followed
	// by Stop.
	Error *Error
	// Stop is the server ending the session. Reason is "quota_exceeded"
	// when the account's time ran out.
	Stop   bool
	Reason string
}

type outgoing struct {
	typ  websocket.MessageType
	data []byte
}

// Session is one video's connection. One goroutine feeds it (Audio, Send)
// and drains Messages; Close may come from any.
type Session struct {
	conn    *websocket.Conn
	out     chan outgoing
	in      chan Message
	closing chan struct{}
	done    chan struct{}

	once sync.Once
	err  error
}

// Dial opens a session on a node's URL and sends start.
func (c *Client) Dial(ctx context.Context, nodeURL, token string, start Start) (*Session, error) {
	u, err := url.Parse(nodeURL)
	if err != nil || u.Host == "" || u.User != nil ||
		(u.Scheme != "ws" && u.Scheme != "wss" && u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("subsrelay: bad node URL")
	}
	if c.base.Scheme == "https" && u.Scheme != "wss" && u.Scheme != "https" {
		return nil, errors.New("subsrelay: HTTPS deployment requires an encrypted node")
	}
	conn, resp, err := websocket.Dial(ctx, nodeURL, &websocket.DialOptions{
		// Without a client of our own the handshake would use
		// http.DefaultClient and go around the app's proxy setting.
		HTTPClient: c.ws,
		HTTPHeader: http.Header{"Authorization": {c.authorization(token)}},
	})
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			body, _ := io.ReadAll(resp.Body)
			return nil, refusal(resp.StatusCode, body)
		}
		return nil, fmt.Errorf("subsrelay: %w", err)
	}
	// A seek replays the cached captions of the next minutes at once.
	conn.SetReadLimit(4 << 20)
	msg, err := json.Marshal(struct {
		Type           string `json:"type"`
		GCID           string `json:"gcid"`
		MediaID        string `json:"media_id"`
		FileID         string `json:"file_id"`
		TargetLanguage string `json:"target_language"`
		AudioFormat    string `json:"audio_format"`
		SampleRate     int    `json:"sample_rate"`
		NumChannels    int    `json:"num_channels"`
	}{"start", start.GCID, start.MediaID, start.FileID, start.TargetLanguage, audioFormat, sampleRate, 1})
	if err == nil {
		err = conn.Write(ctx, websocket.MessageText, msg)
	}
	if err != nil {
		_ = conn.CloseNow()
		return nil, fmt.Errorf("subsrelay: start: %w", err)
	}
	s := &Session{
		conn:    conn,
		out:     make(chan outgoing, queueSize),
		in:      make(chan Message, 64),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go s.read()
	go s.write()
	return s, nil
}

// Audio queues PCM whose first sample is at posMS of media time. It reports
// false, queuing nothing, while the network is behind or once the session
// has ended; the caller offers the same audio again later.
func (s *Session) Audio(posMS int64, pcm []byte) bool {
	if len(s.out) >= queueSize/2 {
		return false
	}
	header := `{"position_ms":` + strconv.FormatInt(posMS, 10) + `}`
	frame := make([]byte, 2+len(header)+len(pcm))
	binary.BigEndian.PutUint16(frame, uint16(len(header)))
	copy(frame[2:], header)
	copy(frame[2+len(header):], pcm)
	return s.enqueue(outgoing{websocket.MessageBinary, frame})
}

// Send queues a control: "seek" before audio from a new position, "pause",
// "resume" before audio after a pause, "finalize" after the last audio.
func (s *Session) Send(control string) bool {
	return s.enqueue(outgoing{websocket.MessageText, []byte(`{"type":"` + control + `"}`)})
}

func (s *Session) enqueue(m outgoing) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.out <- m:
		return true
	case <-s.done:
		return false
	}
}

// Messages delivers what the server says; it is closed when the connection
// ends, and Err then says why.
func (s *Session) Messages() <-chan Message { return s.in }

// Err is why the connection ended: nil after Close or the server's own end.
func (s *Session) Err() error {
	<-s.done
	return s.err
}

// Close ends the session as the server expects: stop behind the audio
// already queued, then the server closes. ctx bounds the wait; the server
// frees the account's session slot only once it has closed.
func (s *Session) Close(ctx context.Context) {
	s.once.Do(func() {
		close(s.closing)
		select {
		case s.out <- outgoing{websocket.MessageText, []byte(`{"type":"stop"}`)}:
			select {
			case <-s.done:
			case <-ctx.Done():
			}
		case <-s.done:
		case <-ctx.Done():
		}
		_ = s.conn.CloseNow()
		<-s.done
	})
}

func (s *Session) read() {
	defer close(s.done)
	defer close(s.in)
	for {
		typ, data, err := s.conn.Read(context.Background())
		if err != nil {
			select {
			case <-s.closing:
			default:
				s.err = fmt.Errorf("subsrelay: connection lost: %w", err)
			}
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		m, ok := parse(data)
		if !ok {
			continue
		}
		select {
		case s.in <- m:
		case <-s.closing:
		}
	}
}

func (s *Session) write() {
	for {
		select {
		case <-s.done:
			return
		case m := <-s.out:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := s.conn.Write(ctx, m.typ, m.data)
			cancel()
			if err != nil {
				// The reader then fails too and reports the connection lost.
				_ = s.conn.CloseNow()
				return
			}
		}
	}
}

func parse(data []byte) (Message, bool) {
	var m struct {
		Type   string `json:"type"`
		Tokens []struct {
			Text     string `json:"text"`
			StartMS  int64  `json:"start_ms"`
			EndMS    int64  `json:"end_ms"`
			Language string `json:"language"`
		} `json:"tokens"`
		Quota       *Quota `json:"quota"`
		Reason      string `json:"reason"`
		Code        string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(data, &m) != nil {
		return Message{}, false
	}
	switch m.Type {
	case "tokens":
		// One message is one caption: its tokens in order, spaces included.
		var text strings.Builder
		for _, t := range m.Tokens {
			text.WriteString(t.Text)
		}
		if len(m.Tokens) == 0 || strings.TrimSpace(text.String()) == "" {
			return Message{}, false
		}
		first, last := m.Tokens[0], m.Tokens[len(m.Tokens)-1]
		return Message{Cue: &Cue{
			StartMS:  first.StartMS,
			EndMS:    max(last.EndMS, first.StartMS),
			Text:     strings.TrimSpace(text.String()),
			Language: first.Language,
		}}, true
	case "quota":
		return Message{Quota: m.Quota}, m.Quota != nil
	case "stop":
		return Message{Stop: true, Reason: m.Reason}, true
	case "error":
		return Message{Error: &Error{Code: m.Code, Description: m.Description}}, true
	}
	return Message{}, false
}
