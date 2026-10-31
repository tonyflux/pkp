package ai

import (
	"context"
	"io"
	"net"
	"net/http"
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

// Without Config.HTTP of its own, Complete must take the proxy setting
// rather than http.DefaultClient.
func TestCompleteGoesThroughProxy(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, _ map[string]any) {
		io.WriteString(w, `{"type":"message","content":[{"type":"tool_use","name":"reply","input":{"answer":"你好"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	p, base := throughProxy(t, "api.model.proxy.test", f.Server)

	cfg := Config{Provider: Anthropic, BaseURL: base, Model: "claude-x", APIKey: testKey}
	if _, err := Complete(context.Background(), cfg, testRequest()); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !p.Saw("api.model.proxy.test") {
		t.Fatalf("the model request went around the proxy; it saw %v", p.Seen())
	}
}
