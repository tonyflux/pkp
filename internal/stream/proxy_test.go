package stream

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/netproxy/proxytest"
)

// throughProxy puts this process behind a recording HTTP proxy and hands back
// a base URL for srv under a name that is not loopback — netproxy never
// proxies loopback, so a test aimed at 127.0.0.1 would prove nothing. The
// proxy dials srv when asked for that name, so no DNS is involved.
func throughProxy(t *testing.T, alias string, srv *httptest.Server) (*proxytest.Proxy, string) {
	t.Helper()
	p := proxytest.HTTP(t)
	addr := srv.Listener.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	target := net.JoinHostPort(alias, port)
	p.Route(target, addr)
	if err := netproxy.Set(netproxy.Config{
		Mode:   netproxy.ModeManual,
		Scheme: netproxy.HTTP,
		Host:   p.Host(),
		Port:   p.Port(),
	}); err != nil {
		t.Fatalf("set proxy: %v", err)
	}
	t.Cleanup(func() { _ = netproxy.Set(netproxy.Config{Mode: netproxy.ModeSystem}) })
	return p, "http://" + target
}

// Online Playback and Player Media Requests: what the player asks this server for stays on
// loopback, and the bytes this server fetches for it cross the Proxy. A proxy
// that swallowed the loopback leg would break mpv on every network.
func TestPlaybackUpstreamGoesThroughProxyAndLoopbackDoesNot(t *testing.T) {
	body := payload(4096)
	origin := upstream(t, body, nil)
	p, base := throughProxy(t, "media.proxy.test", origin)

	s := proxy(t)
	handle, err := s.Publish(Source{URL: base + "/clip.bin"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, handle.URL, nil)
	req.Header.Set("Range", "bytes=100-199")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, body[100:200]) {
		t.Fatalf("body mismatch: got %d bytes", len(got))
	}

	if !p.Saw("media.proxy.test") {
		t.Fatalf("the upstream fetch went around the proxy; it saw %v", p.Seen())
	}
	// The handle mpv is given must stay direct even while a proxy is on.
	local, err := url.Parse(handle.URL)
	if err != nil {
		t.Fatalf("parse handle: %v", err)
	}
	if through, err := netproxy.For(local); err != nil || through != nil {
		t.Fatalf("the loopback handle would be proxied through %v (err %v)", through, err)
	}
	if p.Saw("127.0.0.1") {
		t.Errorf("the proxy carried a loopback request: %v", p.Seen())
	}
}
