// Package netproxy holds the one proxy the app's outbound connections
// follow. Everything that leaves this process over TCP — the drive API and
// its token refreshes, uploads (the OSS SDK), downloads, the loopback media
// proxy's upstream, TMDB title matching and the AI Assistant's model service — asks
// Proxy where to go, so a change here reaches every one of them at once
// without rebuilding a client.
//
// Three modes: Direct never proxies; System follows the computer's own
// settings (macOS System Settings · Network · Proxies, then the usual environment
// variables), which is what a VPN or a rule-based client such as Clash sets;
// Manual is the user's own HTTP, HTTPS or SOCKS5 endpoint. Loopback is never
// proxied in any mode: the player and the preview sheet read their bytes
// from this process's own stream server.
package netproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Mode is where the proxy comes from, as stored in the settings file.
type Mode string

const (
	// System follows the computer's proxy settings; it is the default, so
	// a build that knows nothing about proxies behaves as before.
	ModeSystem Mode = "system"
	// Direct connects to every host itself, ignoring even the environment.
	ModeDirect Mode = "direct"
	// Manual uses the endpoint named in the Config.
	ModeManual Mode = "manual"
)

// The schemes a manual proxy may speak. SOCKS5 resolves host names at the
// proxy (Go treats socks5 as socks5h), which is what makes it useful where
// DNS itself is the problem.
const (
	HTTP   = "http"
	HTTPS  = "https"
	SOCKS5 = "socks5"
)

// Config is the user's choice. The password is held here only to build the
// endpoint URL; it comes from the keychain and is never written to disk by
// this package, nor put in any error message.
type Config struct {
	Mode     Mode
	Scheme   string
	Host     string
	Port     int
	Username string
	Password string
	// Bypass is Addresses That Bypass the Proxy: hosts the user keeps off the proxy, as
	// exact names, suffixes ("*.example.com", ".example.com") or networks
	// ("192.168.0.0/16"). It holds in every mode but ModeDirect, where
	// nothing is proxied anyway.
	Bypass []string
}

// ErrNoEndpoint says a manual proxy was asked for without one.
var ErrNoEndpoint = errors.New("netproxy: no proxy address")

// Check reports why the configuration cannot be used. A non-manual mode
// needs nothing else; a manual one needs a scheme, host and port.
func (c Config) Check() error {
	switch c.Mode {
	case "", ModeSystem, ModeDirect:
		return nil
	case ModeManual:
	default:
		return fmt.Errorf("netproxy: unknown proxy mode %q", c.Mode)
	}
	switch c.Scheme {
	case HTTP, HTTPS, SOCKS5:
	default:
		return fmt.Errorf("netproxy: unknown proxy protocol %q", c.Scheme)
	}
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return ErrNoEndpoint
	}
	if strings.ContainsAny(host, " \t/@?#\\") || strings.ContainsAny(host, "\r\n") {
		return fmt.Errorf("netproxy: %q is not a host name", c.Host)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil && !validHostname(host) {
		return fmt.Errorf("netproxy: %q is not a host name", c.Host)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("netproxy: port must be 1 to 65535, not %d", c.Port)
	}
	if strings.ContainsAny(c.Username, " \t\r\n:") {
		return errors.New("netproxy: the user name cannot hold spaces or a colon")
	}
	// SOCKS5 carries the pair in one byte-counted field each (RFC 1929).
	if c.Scheme == SOCKS5 && (len(c.Username) > 255 || len(c.Password) > 255) {
		return errors.New("netproxy: SOCKS5 takes at most 255 bytes of user name and password")
	}
	return nil
}

// validHostname accepts what a proxy host may look like: labels of letters,
// digits and hyphens, optionally dotted.
func validHostname(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			default:
				return false
			}
		}
	}
	return true
}

// Normalized trims the address and drops what the mode does not use, so two
// settings that mean the same thing compare equal.
func (c Config) Normalized() Config {
	c.Mode = cmpOr(c.Mode, ModeSystem)
	c.Host = strings.TrimSpace(c.Host)
	c.Username = strings.TrimSpace(c.Username)
	c.Bypass = ParseBypass(FormatBypass(c.Bypass))
	if c.Mode != ModeManual {
		return Config{Mode: c.Mode, Bypass: c.Bypass}
	}
	return c
}

// ParseBypass reads the exception list as the settings page takes it: one
// host per line, comma or space, with blanks and duplicates dropped.
func ParseBypass(raw string) []string {
	seen := map[string]bool{}
	var list []string
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		list = append(list, entry)
	}
	return list
}

// FormatBypass writes the list back the way the settings page shows it.
func FormatBypass(list []string) string { return strings.Join(list, ", ") }

// Endpoint is the manual proxy as a URL, nil in every other mode.
func (c Config) Endpoint() *url.URL {
	if c.Mode != ModeManual || c.Host == "" {
		return nil
	}
	u := &url.URL{Scheme: c.Scheme, Host: net.JoinHostPort(strings.Trim(c.Host, "[]"), strconv.Itoa(c.Port))}
	if c.Username != "" {
		u.User = url.UserPassword(c.Username, c.Password)
	}
	return u
}

// Address is the endpoint without the credentials: what logs and the
// settings page may show.
func (c Config) Address() string {
	u := c.Endpoint()
	if u == nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

var current atomic.Pointer[Config]

func init() { current.Store(&Config{Mode: ModeSystem}) }

// Set installs cfg for every connection made from now on and drops the idle
// connections of every transport built here, so nothing keeps flowing
// through the proxy the user just left. Requests already running finish on
// the connection they started on.
func Set(cfg Config) error {
	if err := cfg.Check(); err != nil {
		return err
	}
	cfg = cfg.Normalized()
	current.Store(&cfg)
	forgetSystem()
	resetNodes()
	for _, t := range transports() {
		t.CloseIdleConnections()
	}
	return nil
}

// Current is the configuration in force.
func Current() Config { return *current.Load() }

// Proxy is what every http.Transport in the app uses for Transport.Proxy.
func Proxy(req *http.Request) (*url.URL, error) {
	if req == nil {
		return nil, nil
	}
	return proxyFor(Current(), req.URL)
}

// For reports the proxy a request to u would take right now; it is for the
// paths that cannot hand over an *http.Request, such as the OSS SDK.
func For(u *url.URL) (*url.URL, error) { return proxyFor(Current(), u) }

// ProxyFor reports the proxy a request to u would take under c, whether or
// not c is the configuration in force: Test Connection asks it about the draft the
// user has not applied yet.
func (c Config) ProxyFor(u *url.URL) (*url.URL, error) { return proxyFor(c, u) }

func proxyFor(cfg Config, u *url.URL) (*url.URL, error) {
	if u == nil {
		return nil, nil
	}
	if isLoopback(u.Hostname()) {
		return nil, nil
	}
	if cfg.Mode != ModeDirect && matchesException(cfg.Bypass, u.Hostname()) {
		return nil, nil
	}
	switch cfg.Mode {
	case ModeDirect:
		return nil, nil
	case ModeManual:
		endpoint := cfg.Endpoint()
		if endpoint == nil {
			return nil, ErrNoEndpoint
		}
		return endpoint, nil
	default:
		return systemProxyFor(u)
	}
}

// isLoopback keeps this process's own stream server, the appearance route
// and any local tool off the proxy.
func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

var (
	mu         sync.Mutex
	registered []*http.Transport
)

func transports() []*http.Transport {
	mu.Lock()
	defer mu.Unlock()
	return append([]*http.Transport(nil), registered...)
}

// Install points t at the current proxy and keeps it in mind, so a later
// Set reaches it. It returns t for use in a literal.
func Install(t *http.Transport) *http.Transport {
	t.Proxy = Proxy
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	t.DialContext = nodeDial(dial)
	installNodeTLS(t)
	mu.Lock()
	registered = append(registered, t)
	mu.Unlock()
	return t
}

// NewTransport is a general-purpose transport that follows the proxy: the
// standard library's defaults with shorter dial and handshake deadlines.
func NewTransport() *http.Transport {
	return Install(&http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	})
}

var shared = sync.OnceValue(func() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: NewTransport()}
})

// Client is the shared client for one-off requests: it follows the proxy
// where http.DefaultClient would not.
func Client() *http.Client { return shared() }

// Probe makes one request to target through cfg — whatever answer comes
// back, the proxy carried it — and reports how long that took. It builds its
// own transport, so it can try a setting before Set installs it.
func Probe(ctx context.Context, cfg Config, target string) (time.Duration, error) {
	if err := cfg.Check(); err != nil {
		return 0, err
	}
	cfg = cfg.Normalized()
	t := &http.Transport{
		Proxy:                 func(req *http.Request) (*url.URL, error) { return proxyFor(cfg, req.URL) },
		DialContext:           nodeDial((&net.Dialer{Timeout: 5 * time.Second}).DialContext),
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	installNodeTLS(t)
	defer t.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	started := time.Now()
	resp, err := (&http.Client{Transport: t, Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return time.Since(started), err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return time.Since(started), nil
}

func cmpOr[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}
