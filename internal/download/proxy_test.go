package download

import (
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

// Downloads: the queue's own client (no injected HTTPClient) must take the Proxy
// setting, and every ranged thread with it.
func TestDownloadGoesThroughProxy(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{})
	p, base := throughProxy(t, "cdn.proxy.test", cdn.Server)

	q := New(&fakeBackend{url: base}, Options{Parallel: 1, Threads: 2})
	t.Cleanup(q.Close)

	task := add(t, q, Source{FileID: "f1", Name: "movie.mkv", Dir: t.TempDir()})
	done := waitTask(t, q, task.ID, stopped)
	if done.State != StateDone {
		t.Fatalf("download ended as %+v", done)
	}
	samePayload(t, done.Path)
	if !p.Saw("cdn.proxy.test") {
		t.Fatalf("the download went around the proxy; it saw %v", p.Seen())
	}
}
