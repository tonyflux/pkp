// Package subsrelay is the client of subsrelay, PikPak's real-time AI
// subtitle service. A video is identified by its GCID; the node endpoint says
// which server takes it, and one WebSocket session per video carries its
// audio up and captions back, both on the video's own timeline. The server
// caches what it transcribed per GCID, so a video someone already watched
// costs no transcription. Both the node lookup and the WebSocket session
// follow the app's proxy setting.
package subsrelay

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/netproxy"
)

// apiTransport carries the node lookup; wsTransport carries the WebSocket
// handshake and is HTTP/1.1 only, because an Upgrade is not something
// HTTP/2 would carry. Both follow the app's proxy setting, and both are made
// once so netproxy's registry does not grow with every client.
var (
	apiTransport = sync.OnceValue(netproxy.NewTransport)
	wsTransport  = sync.OnceValue(func() *http.Transport {
		return netproxy.Install(&http.Transport{
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:   false,
			TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
			TLSHandshakeTimeout: 10 * time.Second,
		})
	})
)

// Client talks to one subsrelay deployment.
type Client struct {
	base *url.URL
	// basic is "user:password" when the configured URL carries one: a
	// development deployment's Basic Auth, which stands in for the account.
	basic string
	http  *http.Client
	// ws is what the session handshake dials with. It carries no overall
	// timeout: a session lasts as long as the video does.
	ws *http.Client
}

// New returns a client for the deployment at raw, e.g.
// https://example.com. Credentials in the URL (https://user:pw@host) are sent
// as Basic Auth instead of the account's token.
func New(raw string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("subsrelay: bad URL")
	}
	c := &Client{
		http: &http.Client{Transport: apiTransport(), CheckRedirect: checkRedirect},
		ws:   &http.Client{Transport: wsTransport(), CheckRedirect: checkRedirect},
	}
	if u.User != nil {
		password, _ := u.User.Password()
		c.basic = u.User.Username() + ":" + password
		u.User = nil
	}
	c.base = u
	return c, nil
}

// Redirects may change hosts, but must not lower the connection's security.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("subsrelay: refusing insecure redirect")
	}
	if len(via) >= 10 {
		return errors.New("subsrelay: stopped after 10 redirects")
	}
	return nil
}

// UsesAccount reports whether requests carry the account's token, as
// opposed to the configured Basic Auth.
func (c *Client) UsesAccount() bool { return c.basic == "" }

// authorization is the header that names the user: the configured Basic
// Auth, or the account's PikPak access token.
func (c *Client) authorization(token string) string {
	if c.basic != "" {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.basic))
	}
	return "Bearer " + token
}

// Error is subsrelay refusing a request: an HTTP answer before a session
// exists (Status set), or an error frame inside one (Status 0).
type Error struct {
	Status      int
	Code        string
	Description string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("subsrelay: HTTP %d %s: %s", e.Status, e.Code, e.Description)
	}
	return fmt.Sprintf("subsrelay: %s: %s", e.Code, e.Description)
}

// ErrQuotaUsedUp is the account's AI subtitle time for the period being
// used up; the Node that comes with it says when it resets.
var ErrQuotaUsedUp = errors.New("subsrelay: usage quota used up")

// Quota is the account's AI subtitle time. Limit and Usage are seconds of
// session time; NextResetTime is RFC 3339.
type Quota struct {
	Exceeded      bool   `json:"exceeded"`
	Limit         int64  `json:"limit"`
	Usage         int64  `json:"usage"`
	NextResetTime string `json:"next_reset_time"`
}

// Language is a target language the deployment can translate into.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Node is where a video's session goes, and what it can do there.
type Node struct {
	URL                string     `json:"url"`
	SourceLanguage     string     `json:"source_language"`
	SupportedLanguages []Language `json:"supported_languages"`
	Quota              Quota      `json:"quota"`
}

// Node asks which server takes the video with this GCID. token is the
// account's access token; unused under Basic Auth.
func (c *Client) Node(ctx context.Context, gcid, token string) (Node, error) {
	u := c.base.JoinPath("subsrelay/v1/node")
	u.RawQuery = url.Values{"gcid": {gcid}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Node{}, err
	}
	req.Header.Set("Authorization", c.authorization(token))
	resp, err := c.http.Do(req)
	if err != nil {
		return Node{}, fmt.Errorf("subsrelay: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Node{}, fmt.Errorf("subsrelay: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Node{}, refusal(resp.StatusCode, body)
	}
	var node Node
	if err := json.Unmarshal(body, &node); err != nil {
		return Node{}, fmt.Errorf("subsrelay: decode node: %w", err)
	}
	if node.Quota.Exceeded {
		return node, ErrQuotaUsedUp
	}
	if node.URL == "" {
		return node, errors.New("subsrelay: no node for this video")
	}
	return node, nil
}

// refusal reads an error body. The service answers {error,
// error_description}; anything else keeps the status alone.
func refusal(status int, body []byte) error {
	var e struct {
		Code        string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &e)
	if e.Code == "" {
		e.Code = http.StatusText(status)
	}
	return &Error{Status: status, Code: e.Code, Description: e.Description}
}
