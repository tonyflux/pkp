package tmdb

import (
	"context"
	"net"
	"net/http/httptest"
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

// A blocked network stops TMDB, so a client built without an injected one
// must follow the Proxy setting.
func TestRequestGoesThroughProxy(t *testing.T) {
	stub, srv := newStubServer(t)
	stub.json("configuration", `{"images":{"secure_base_url":"https://image.tmdb.org/t/p/","poster_sizes":["w500"],"backdrop_sizes":["w780"]}}`)

	p, base := throughProxy(t, "api.tmdb.example.test", srv)

	tm := New(base+"/3", nil)
	if err := tm.Test(context.Background(), v3Key); err != nil {
		t.Fatalf("tmdb test: %v", err)
	}
	if !p.Saw("api.tmdb.example.test") {
		t.Fatalf("the TMDB request went around the proxy; it saw %v", p.Seen())
	}
	if stub.total() != 1 {
		t.Errorf("the stub answered %d requests, want 1", stub.total())
	}
}

// A client the caller injects is used verbatim, proxy setting or not: tests
// and the app's own seam depend on it.
func TestInjectedClientIsNotRewired(t *testing.T) {
	stub, srv := newStubServer(t)
	stub.json("configuration", `{"images":{"secure_base_url":"https://image.tmdb.org/t/p/"}}`)
	p := proxytest.HTTP(t)
	if err := netproxy.Set(netproxy.Config{
		Mode:   netproxy.ModeManual,
		Scheme: netproxy.HTTP,
		Host:   p.Host(),
		Port:   p.Port(),
	}); err != nil {
		t.Fatalf("set proxy: %v", err)
	}
	t.Cleanup(func() { _ = netproxy.Set(netproxy.Config{Mode: netproxy.ModeSystem}) })

	tm := New(srv.URL+"/3", srv.Client())
	if err := tm.Test(context.Background(), v3Key); err != nil {
		t.Fatalf("tmdb test: %v", err)
	}
	if stub.total() != 1 {
		t.Errorf("the stub answered %d requests, want 1", stub.total())
	}
}
