package netproxy_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/netproxy/proxytest"
)

// reset puts the package back to its default after a test changed it.
func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if err := netproxy.Set(netproxy.Config{Mode: netproxy.ModeSystem}); err != nil {
			t.Fatalf("reset: %v", err)
		}
	})
}

func TestManualProxyCarriesRequestsAndSwitchesLive(t *testing.T) {
	reset(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	// A host name so the request is not taken for loopback; the proxies
	// are told where it really lives instead of asking DNS.
	alias := hostAlias(t, upstream.URL)
	target := "http://" + alias + "/file"

	first := proxytest.HTTP(t)
	second := proxytest.SOCKS5(t)
	address := strings.TrimPrefix(upstream.URL, "http://")
	first.Route(alias, address)
	second.Route(alias, address)
	client := &http.Client{Transport: netproxy.NewTransport()}

	if err := netproxy.Set(netproxy.Config{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: first.Host(), Port: first.Port()}); err != nil {
		t.Fatalf("set http proxy: %v", err)
	}
	if body := get(t, client, target); body != "ok" {
		t.Fatalf("through http proxy: body %q", body)
	}
	if !first.Saw("localhost.test") {
		t.Fatalf("the http proxy saw %v, not the request", first.Seen())
	}

	// The same client, already holding a connection through the first
	// proxy, must follow the new setting at once.
	if err := netproxy.Set(netproxy.Config{Mode: netproxy.ModeManual, Scheme: netproxy.SOCKS5, Host: second.Host(), Port: second.Port()}); err != nil {
		t.Fatalf("set socks proxy: %v", err)
	}
	if body := get(t, client, target); body != "ok" {
		t.Fatalf("through socks proxy: body %q", body)
	}
	if !second.Saw("localhost.test") {
		t.Fatalf("the socks proxy saw %v, not the request", second.Seen())
	}
	if len(first.Seen()) != 1 {
		t.Fatalf("the old proxy kept carrying requests: %v", first.Seen())
	}

	// Loopback never goes through a proxy: the player and the preview
	// sheet read from this process's own stream server.
	if body := get(t, client, upstream.URL+"/file"); body != "ok" {
		t.Fatalf("loopback: body %q", body)
	}
	if len(second.Seen()) != 1 {
		t.Fatalf("loopback went through the proxy: %v", second.Seen())
	}
}

func TestProxyCredentialsAndDirectMode(t *testing.T) {
	reset(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	alias := hostAlias(t, upstream.URL)
	target := "http://" + alias + "/file"

	socks := proxytest.SOCKS5WithAuth(t, "me", "secret")
	socks.Route(alias, strings.TrimPrefix(upstream.URL, "http://"))
	client := &http.Client{Transport: netproxy.NewTransport()}
	cfg := netproxy.Config{Mode: netproxy.ModeManual, Scheme: netproxy.SOCKS5, Host: socks.Host(), Port: socks.Port(), Username: "me", Password: "secret"}
	if err := netproxy.Set(cfg); err != nil {
		t.Fatalf("set: %v", err)
	}
	if body := get(t, client, target); body != "ok" {
		t.Fatalf("with credentials: body %q", body)
	}
	if got := netproxy.Current().Address(); got != "socks5://"+socks.Addr {
		t.Fatalf("address %q keeps or drops the wrong part", got)
	}

	wrong := cfg
	wrong.Password = "nope"
	if _, err := netproxy.Probe(context.Background(), wrong, target); err == nil {
		t.Fatal("a wrong password still connected")
	}
	if _, err := netproxy.Probe(context.Background(), cfg, target); err != nil {
		t.Fatalf("probe with the right password: %v", err)
	}

	// Direct ignores even the environment.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	before := len(socks.Seen())
	if err := netproxy.Set(netproxy.Config{Mode: netproxy.ModeDirect}); err != nil {
		t.Fatalf("set direct: %v", err)
	}
	if proxy, err := netproxy.For(mustURL(t, target)); err != nil || proxy != nil {
		t.Fatalf("direct resolved %v, %v", proxy, err)
	}
	if body := get(t, client, upstream.URL+"/file"); body != "ok" {
		t.Fatalf("direct: body %q", body)
	}
	if len(socks.Seen()) != before {
		t.Fatalf("direct still used the proxy: %v", socks.Seen())
	}
}

func TestCheckRefusesWhatCannotBeDialled(t *testing.T) {
	bad := []netproxy.Config{
		{Mode: netproxy.ModeManual, Scheme: "ftp", Host: "127.0.0.1", Port: 1080},
		{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Port: 1080},
		{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: "127.0.0.1", Port: 0},
		{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: "127.0.0.1", Port: 70000},
		{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: "http://1.2.3.4", Port: 8080},
		{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: "1.2.3.4", Port: 8080, Username: "a:b"},
		{Mode: "pac", Host: "1.2.3.4"},
	}
	for _, cfg := range bad {
		if err := cfg.Check(); err == nil {
			t.Fatalf("%+v was accepted", cfg)
		}
	}
	good := netproxy.Config{Mode: netproxy.ModeManual, Scheme: netproxy.SOCKS5, Host: " proxy.example.com ", Port: 1080}
	if err := good.Check(); err != nil {
		t.Fatalf("a plain host was refused: %v", err)
	}
	if got := good.Normalized().Address(); got != "socks5://proxy.example.com:1080" {
		t.Fatalf("normalized address %q", got)
	}
	leftOver := netproxy.Config{Mode: netproxy.ModeDirect, Host: "left over"}
	if got := leftOver.Normalized().Endpoint(); got != nil {
		t.Fatalf("a non-manual mode kept an endpoint: %v", got)
	}
}

func TestEnvironmentIsTheFallbackForSystemMode(t *testing.T) {
	reset(t)
	if info := netproxy.System(); info.FromEnv && info.Any() {
		t.Skip("this computer has proxy settings of its own; the environment is not reached")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	proxy := proxytest.HTTP(t)
	t.Setenv("HTTP_PROXY", proxy.URL("http"))
	t.Setenv("NO_PROXY", "")
	// http.ProxyFromEnvironment reads the environment once per process, so
	// this only proves the plumbing when nothing has read it yet; both
	// outcomes are fine as long as nothing panics.
	if _, err := netproxy.For(mustURL(t, "https://example.com/")); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func get(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("get %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return string(body)
}

// hostAlias turns a test server's 127.0.0.1 address into a name that is not
// loopback to the proxy rules but still resolves there.
func hostAlias(t *testing.T, server string) string {
	t.Helper()
	u := mustURL(t, server)
	return "localhost.test:" + u.Port()
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u
}

func TestBypassKeepsHostsOffTheProxy(t *testing.T) {
	reset(t)
	proxy := proxytest.HTTP(t)
	cfg := netproxy.Config{
		Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: proxy.Host(), Port: proxy.Port(),
		Bypass: netproxy.ParseBypass("example.com, *.cdn.example.net\n192.168.0.0/16"),
	}
	if err := netproxy.Set(cfg); err != nil {
		t.Fatalf("set: %v", err)
	}
	direct := []string{"http://example.com/a", "https://img.cdn.example.net/b", "http://192.168.1.9/c"}
	for _, raw := range direct {
		if got, err := netproxy.For(mustURL(t, raw)); err != nil || got != nil {
			t.Fatalf("%s should skip the proxy, got %v (%v)", raw, got, err)
		}
	}
	proxied := []string{"https://api-drive.mypikpak.com/x", "https://example.net/b", "http://10.0.0.1/c"}
	for _, raw := range proxied {
		got, err := netproxy.For(mustURL(t, raw))
		if err != nil || got == nil {
			t.Fatalf("%s should take the proxy, got %v (%v)", raw, got, err)
		}
	}
	// The list survives a round trip through the settings file's one string.
	if got := netproxy.FormatBypass(netproxy.Current().Bypass); got != "example.com, *.cdn.example.net, 192.168.0.0/16" {
		t.Fatalf("formatted bypass %q", got)
	}
}

func TestHandshakeSeparatesTheProxyFromTheDestination(t *testing.T) {
	reset(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	alias := hostAlias(t, upstream.URL)
	address := strings.TrimPrefix(upstream.URL, "http://")

	for _, scheme := range []string{netproxy.HTTP, netproxy.SOCKS5} {
		var proxy *proxytest.Proxy
		if scheme == netproxy.HTTP {
			proxy = proxytest.HTTPWithAuth(t, "me", "secret")
		} else {
			proxy = proxytest.SOCKS5WithAuth(t, "me", "secret")
		}
		proxy.Route(alias, address)
		good := netproxy.Config{Mode: netproxy.ModeManual, Scheme: scheme, Host: proxy.Host(), Port: proxy.Port(), Username: "me", Password: "secret"}

		if err := netproxy.Handshake(context.Background(), good.Endpoint(), alias); err != nil {
			t.Fatalf("%s: handshake through a working proxy: %v", scheme, err)
		}
		wrong := good
		wrong.Password = "nope"
		if err := netproxy.Handshake(context.Background(), wrong.Endpoint(), alias); !errorsIs(err, netproxy.ErrProxyAuth) {
			t.Fatalf("%s: bad credentials gave %v, not ErrProxyAuth", scheme, err)
		}
		// The proxy is fine; the far end is not listening.
		// Port 9 on loopback is closed, so the proxy reaches nothing.
		if err := netproxy.Handshake(context.Background(), good.Endpoint(), "127.0.0.1:9"); !errorsIs(err, netproxy.ErrDestination) {
			t.Fatalf("%s: a dead destination gave %v, not ErrDestination", scheme, err)
		}
	}

	// No proxy listening at all is neither of those: it is the proxy leg.
	dead := netproxy.Config{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: "127.0.0.1", Port: 9}
	err := netproxy.Handshake(context.Background(), dead.Endpoint(), alias)
	if err == nil || errorsIs(err, netproxy.ErrDestination) || errorsIs(err, netproxy.ErrProxyAuth) {
		t.Fatalf("an unreachable proxy gave %v", err)
	}
}

func errorsIs(err, target error) bool { return errors.Is(err, target) }
