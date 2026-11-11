package subsrelay

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// relay is a stand-in for one subsrelay deployment: node discovery answers
// what node returns, and every stream connection is handed to stream.
type relay struct {
	t      *testing.T
	srv    *httptest.Server
	auth   chan string
	node   func(w http.ResponseWriter, r *http.Request)
	stream func(conn *websocket.Conn)
}

func newRelay(t *testing.T) *relay {
	rl := &relay{t: t, auth: make(chan string, 8)}
	rl.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rl.auth <- r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/subsrelay/v1/node":
			rl.node(w, r)
		case "/subsrelay/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept: %v", err)
				return
			}
			defer conn.CloseNow()
			rl.stream(conn)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(rl.srv.Close)
	return rl
}

func (rl *relay) streamURL() string {
	return "ws" + strings.TrimPrefix(rl.srv.URL, "http") + "/subsrelay/v1/stream"
}

func TestNodeSendsTheAccountTokenAndReadsTheNode(t *testing.T) {
	rl := newRelay(t)
	rl.node = func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("gcid"); got != strings.Repeat("A", 40) {
			t.Errorf("gcid = %q", got)
		}
		w.Write([]byte(`{"protocol":"default_v1.0","name":"sg","url":"wss://n/subsrelay/v1/stream","source_language":"ja",
			"supported_languages":[{"code":"zh","name":"Chinese"}],"quota":{"exceeded":false,"limit":14400,"usage":60,"next_reset_time":"2026-10-19T00:00:00+08:00"}}`))
	}
	c, err := New(rl.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	node, err := c.Node(context.Background(), strings.Repeat("A", 40), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if got := <-rl.auth; got != "Bearer tok" {
		t.Errorf("Authorization = %q, want the account's bearer token", got)
	}
	if node.URL != "wss://n/subsrelay/v1/stream" || node.SourceLanguage != "ja" || len(node.SupportedLanguages) != 1 || node.Quota.Usage != 60 {
		t.Errorf("node = %+v", node)
	}
}

// Credentials in the configured URL replace the account: a development
// deployment's Basic Auth, never sent as part of the URL itself.
func TestBasicAuthFromTheConfiguredURL(t *testing.T) {
	rl := newRelay(t)
	rl.node = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.User != nil {
			t.Errorf("credentials leaked into the request URL")
		}
		w.Write([]byte(`{"url":"ws://n","quota":{}}`))
	}
	c, err := New(strings.Replace(rl.srv.URL, "http://", "http://user1:test@", 1) + "/")
	if err != nil {
		t.Fatal(err)
	}
	if c.UsesAccount() {
		t.Error("UsesAccount with Basic Auth configured")
	}
	if _, err := c.Node(context.Background(), strings.Repeat("A", 40), "tok"); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user1:test"))
	if got := <-rl.auth; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func TestNodeRefusals(t *testing.T) {
	rl := newRelay(t)
	c, _ := New(rl.srv.URL)

	rl.node = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"type":"error","error":"vip_required","error_description":"VIP membership is required"}`))
	}
	_, err := c.Node(context.Background(), strings.Repeat("A", 40), "tok")
	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusForbidden || e.Code != "vip_required" {
		t.Errorf("403: %v, want vip_required", err)
	}

	// Used up: a 200 without a node, the quota saying until when.
	rl.node = func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"url":"","quota":{"exceeded":true,"limit":14400,"usage":14400,"next_reset_time":"2026-10-19T00:00:00+08:00"}}`))
	}
	node, err := c.Node(context.Background(), strings.Repeat("A", 40), "tok")
	if !errors.Is(err, ErrQuotaUsedUp) || node.Quota.NextResetTime == "" {
		t.Errorf("used up: %v %+v, want ErrQuotaUsedUp with the reset time", err, node.Quota)
	}
}

// The session speaks the service's wire format: start first, then binary
// frames [u16 BE header length][{"position_ms":N}][pcm] and text controls,
// all in the order given; captions come back one message each.
func TestSessionRoundTrip(t *testing.T) {
	rl := newRelay(t)
	type frame struct {
		text string
		pos  int64
		pcm  []byte
	}
	got := make(chan frame, 16)
	rl.stream = func(conn *websocket.Conn) {
		ctx := context.Background()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				close(got)
				return
			}
			if typ == websocket.MessageText {
				got <- frame{text: string(data)}
				var m struct{ Type string }
				json.Unmarshal(data, &m)
				switch m.Type {
				case "seek":
					conn.Write(ctx, websocket.MessageText, []byte(`{"type":"tokens","tokens":[{"text":"你好","start_ms":1000,"end_ms":1400,"is_final":true,"language":"zh"},{"text":"，世界","start_ms":1400,"end_ms":2100,"is_final":true,"language":"zh"}]}`))
					conn.Write(ctx, websocket.MessageText, []byte(`{"type":"quota","quota":{"exceeded":false,"limit":14400,"usage":80}}`))
					conn.Write(ctx, websocket.MessageText, []byte(`{"type":"error","error":"soniox_unavailable","error_description":"transcription stream failed"}`))
				case "stop":
					conn.Write(ctx, websocket.MessageText, []byte(`{"type":"stop"}`))
					close(got)
					return
				}
				continue
			}
			n := int(binary.BigEndian.Uint16(data))
			var h struct {
				PositionMS int64 `json:"position_ms"`
			}
			if err := json.Unmarshal(data[2:2+n], &h); err != nil {
				t.Errorf("audio header: %v", err)
			}
			got <- frame{pos: h.PositionMS, pcm: data[2+n:]}
		}
	}

	c, _ := New(rl.srv.URL)
	s, err := c.Dial(context.Background(), rl.streamURL(), "tok", Start{GCID: strings.Repeat("B", 40), MediaID: "m1", FileID: "f1", TargetLanguage: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Audio(0, []byte{1, 2, 3, 4}) || !s.Send("seek") || !s.Audio(5000, []byte{5, 6}) {
		t.Fatal("queueing refused on an idle session")
	}

	start := <-got
	var m map[string]any
	if err := json.Unmarshal([]byte(start.text), &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"type": "start", "gcid": strings.Repeat("B", 40), "media_id": "m1", "file_id": "f1",
		"target_language": "zh", "audio_format": "pcm_s16le", "sample_rate": 16000.0, "num_channels": 1.0}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("start.%s = %v, want %v", k, m[k], v)
		}
	}
	if f := <-got; f.pos != 0 || string(f.pcm) != "\x01\x02\x03\x04" {
		t.Errorf("first frame = %+v", f)
	}
	if f := <-got; f.text != `{"type":"seek"}` {
		t.Errorf("second message = %+v, want the seek in between", f)
	}
	if f := <-got; f.pos != 5000 || string(f.pcm) != "\x05\x06" {
		t.Errorf("third frame = %+v", f)
	}

	cue := (<-s.Messages()).Cue
	if cue == nil || cue.Text != "你好，世界" || cue.StartMS != 1000 || cue.EndMS != 2100 || cue.Language != "zh" {
		t.Errorf("cue = %+v, want one caption from the message's tokens", cue)
	}
	if q := (<-s.Messages()).Quota; q == nil || q.Usage != 80 {
		t.Errorf("quota = %+v", q)
	}
	if e := (<-s.Messages()).Error; e == nil || e.Code != "soniox_unavailable" {
		t.Errorf("error = %+v", e)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Close(ctx)
	if ctx.Err() != nil {
		t.Error("Close waited for its timeout instead of the server's close")
	}
	if f := <-got; f.text != `{"type":"stop"}` {
		t.Errorf("last message = %+v, want stop", f)
	}
	if err := s.Err(); err != nil {
		t.Errorf("Err after Close = %v, want nil", err)
	}
	// The server's own stop may still be queued; after it, the channel ends.
	for range s.Messages() {
	}
}

// A refused handshake (the per-user session limit, say) comes back with the
// service's code, not as a bare dial failure.
func TestDialRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":"quota_exceeded","error_description":"Usage quota exceeded"}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	_, err := c.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), "tok", Start{})
	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusTooManyRequests || e.Code != "quota_exceeded" {
		t.Errorf("dial = %v, want 429 quota_exceeded", err)
	}
}

// A connection the server drops says so through Err.
func TestServerDropIsReported(t *testing.T) {
	rl := newRelay(t)
	rl.stream = func(conn *websocket.Conn) {
		conn.Read(context.Background())
	}
	c, _ := New(rl.srv.URL)
	s, err := c.Dial(context.Background(), rl.streamURL(), "tok", Start{})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Messages() {
	}
	if s.Err() == nil {
		t.Error("Err = nil after the server dropped the connection")
	}
	if s.Audio(0, []byte{0, 0}) {
		t.Error("Audio accepted after the connection ended")
	}
}

func TestMalformedURLsDoNotExposeCredentials(t *testing.T) {
	for _, raw := range []string{
		"https://relay-user:basic-secret@relay.example/%zz",
		"https://relay-user:basic-secret@[invalid",
		"ftp://relay-user:basic-secret@relay.example",
	} {
		if _, err := New(raw); err == nil || err.Error() != "subsrelay: bad URL" {
			t.Errorf("New returned %v, want a credential-free URL error", err)
		}
	}

	c, err := New("https://relay-user:basic-secret@relay.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"ws://relay-user:basic-secret@relay.example/%zz",
		"ws://relay.example/%zz?token=account-secret",
		"ws://relay-user:basic-secret@relay.example",
		"://relay.example?token=account-secret",
	} {
		if _, err := c.Dial(context.Background(), raw, "account-secret", Start{}); err == nil || err.Error() != "subsrelay: bad node URL" {
			t.Errorf("Dial returned %v, want a credential-free URL error", err)
		}
	}
}

func TestSessionUsesConfiguredBasicAuth(t *testing.T) {
	rl := newRelay(t)
	rl.stream = func(conn *websocket.Conn) {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil || strings.Contains(string(data), `"stop"`) {
				return
			}
		}
	}
	c, err := New(strings.Replace(rl.srv.URL, "http://", "http://relay-user:basic-secret@", 1))
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Dial(context.Background(), rl.streamURL(), "account-secret", Start{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer s.Close(ctx)
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("relay-user:basic-secret"))
	if got := <-rl.auth; got != want {
		t.Errorf("Authorization = %q, want configured Basic Auth", got)
	}
}

func trustRelayTLS(t *testing.T, c *Client, srv *httptest.Server) {
	t.Helper()
	for _, client := range []*http.Client{c.http, c.ws} {
		// netproxy's TLS dialer closes over its original transport, so isolate test trust.
		transport := srv.Client().Transport.(*http.Transport).Clone()
		client.Transport = transport
		t.Cleanup(transport.CloseIdleConnections)
	}
}

func TestHTTPSDiscoveryRejectsPlaintextNodes(t *testing.T) {
	for _, basic := range []bool{false, true} {
		for _, scheme := range []string{"ws", "http"} {
			name := scheme + "/bearer"
			if basic {
				name = scheme + "/basic"
			}
			t.Run(name, func(t *testing.T) {
				cleartextAuth := make(chan string, 1)
				plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					cleartextAuth <- r.Header.Get("Authorization")
					w.WriteHeader(http.StatusUnauthorized)
				}))
				defer plain.Close()
				nodeURL := scheme + strings.TrimPrefix(plain.URL, "http")
				discovery := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					json.NewEncoder(w).Encode(Node{URL: nodeURL})
				}))
				defer discovery.Close()
				raw := discovery.URL
				if basic {
					raw = strings.Replace(raw, "https://", "https://relay-user:synthetic-secret@", 1)
				}
				c, err := New(raw)
				if err != nil {
					t.Fatal(err)
				}
				trustRelayTLS(t, c, discovery)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				node, err := c.Node(ctx, "gcid", "synthetic-token")
				if err != nil {
					t.Fatal(err)
				}
				if sess, err := c.Dial(ctx, node.URL, "synthetic-token", Start{}); err == nil {
					sess.Close(ctx)
					t.Error("HTTPS discovery accepted a plaintext node")
				}
				select {
				case auth := <-cleartextAuth:
					t.Errorf("plaintext node received a request with Authorization %q", auth)
				default:
				}
			})
		}
	}
}

func TestSecureRequestsRejectPlaintextRedirects(t *testing.T) {
	for _, basic := range []bool{false, true} {
		for _, test := range []struct {
			name      string
			discovery bool
			localBase bool
			scheme    string
		}{
			{name: "discovery", discovery: true, scheme: "http"},
			{name: "websocket-http", scheme: "http"},
			{name: "websocket-ws", scheme: "ws"},
			{name: "local-base-secure-websocket-http", localBase: true, scheme: "http"},
			{name: "local-base-secure-websocket-ws", localBase: true, scheme: "ws"},
		} {
			name := test.name + "/bearer"
			if basic {
				name = test.name + "/basic"
			}
			t.Run(name, func(t *testing.T) {
				cleartextAuth := make(chan string, 1)
				plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					cleartextAuth <- r.Header.Get("Authorization")
					w.WriteHeader(http.StatusUnauthorized)
				}))
				defer plain.Close()
				secureAuth := make(chan string, 1)
				secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					secureAuth <- r.Header.Get("Authorization")
					http.Redirect(w, r, test.scheme+strings.TrimPrefix(plain.URL, "http"), http.StatusTemporaryRedirect)
				}))
				defer secure.Close()
				raw := secure.URL
				if test.localBase {
					raw = plain.URL
				}
				wantAuth := "Bearer synthetic-token"
				if basic {
					raw = strings.Replace(raw, "://", "://relay-user:synthetic-secret@", 1)
					wantAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("relay-user:synthetic-secret"))
				}
				c, err := New(raw)
				if err != nil {
					t.Fatal(err)
				}
				trustRelayTLS(t, c, secure)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if test.discovery {
					_, err = c.Node(ctx, "gcid", "synthetic-token")
				} else {
					var sess *Session
					sess, err = c.Dial(ctx, "wss"+strings.TrimPrefix(secure.URL, "https"), "synthetic-token", Start{})
					if sess != nil {
						sess.Close(ctx)
					}
				}
				if err == nil || !strings.Contains(err.Error(), "refusing insecure redirect") {
					t.Errorf("request error = %v, want an insecure-redirect rejection", err)
				}
				select {
				case auth := <-secureAuth:
					if auth != wantAuth {
						t.Errorf("TLS Authorization = %q, want %q", auth, wantAuth)
					}
				default:
					t.Error("request did not reach the trusted TLS endpoint")
				}
				select {
				case auth := <-cleartextAuth:
					t.Errorf("plaintext redirect received a request with Authorization %q", auth)
				default:
				}
			})
		}
	}
}

func TestHTTPSDiscoveryAndEncryptedNodeAllowSecureRedirects(t *testing.T) {
	for _, basic := range []bool{false, true} {
		for _, scheme := range []string{"wss", "https"} {
			name := scheme + "/bearer"
			if basic {
				name = scheme + "/basic"
			}
			t.Run(name, func(t *testing.T) {
				auth := make(chan string, 4)
				srv := httptest.NewUnstartedServer(nil)
				srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					auth <- r.Header.Get("Authorization")
					switch r.URL.Path {
					case "/subsrelay/v1/node":
						http.Redirect(w, r, "/node", http.StatusTemporaryRedirect)
					case "/node":
						json.NewEncoder(w).Encode(Node{URL: scheme + strings.TrimPrefix(srv.URL, "https") + "/connect"})
					case "/connect":
						http.Redirect(w, r, scheme+strings.TrimPrefix(srv.URL, "https")+"/stream", http.StatusTemporaryRedirect)
					case "/stream":
						conn, err := websocket.Accept(w, r, nil)
						if err != nil {
							t.Errorf("accept: %v", err)
							return
						}
						defer conn.CloseNow()
						ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
						defer cancel()
						if _, _, err := conn.Read(ctx); err != nil {
							t.Errorf("read start: %v", err)
						}
					default:
						http.NotFound(w, r)
					}
				})
				srv.StartTLS()
				defer srv.Close()
				raw := srv.URL
				wantAuth := "Bearer synthetic-token"
				if basic {
					raw = strings.Replace(raw, "https://", "https://relay-user:synthetic-secret@", 1)
					wantAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("relay-user:synthetic-secret"))
				}
				c, err := New(raw)
				if err != nil {
					t.Fatal(err)
				}
				trustRelayTLS(t, c, srv)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				node, err := c.Node(ctx, "gcid", "synthetic-token")
				if err != nil {
					t.Fatal(err)
				}
				sess, err := c.Dial(ctx, node.URL, "synthetic-token", Start{GCID: "gcid"})
				if err != nil {
					t.Fatal(err)
				}
				defer sess.Close(ctx)
				for range 4 {
					select {
					case got := <-auth:
						if got != wantAuth {
							t.Errorf("TLS Authorization = %q, want %q", got, wantAuth)
						}
					case <-ctx.Done():
						t.Fatal("missing authenticated TLS request")
					}
				}
			})
		}
	}
}
