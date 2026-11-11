package subsrelay

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/netproxy/proxytest"
)

// throughSOCKS puts this process behind a recording SOCKS5 proxy and hands
// back the relay's address under a name that is not loopback — netproxy never
// proxies loopback, so a test aimed at 127.0.0.1 would prove nothing. SOCKS5
// tunnels raw TCP, which is what a WebSocket upgrade needs; the proxy dials
// the relay when asked for that name, so no DNS is involved.
func throughSOCKS(t *testing.T, alias string, rl *relay) (*proxytest.Proxy, string) {
	t.Helper()
	p := proxytest.SOCKS5(t)
	addr := rl.srv.Listener.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	target := net.JoinHostPort(alias, port)
	p.Route(target, addr)
	if err := netproxy.Set(netproxy.Config{
		Mode:   netproxy.ModeManual,
		Scheme: netproxy.SOCKS5,
		Host:   p.Host(),
		Port:   p.Port(),
	}); err != nil {
		t.Fatalf("set proxy: %v", err)
	}
	t.Cleanup(func() { _ = netproxy.Set(netproxy.Config{Mode: netproxy.ModeSystem}) })
	return p, target
}

// Both legs must take the proxy setting: the node lookup over HTTP
// and, the one that is easy to miss, the WebSocket handshake, which would
// otherwise be dialled by http.DefaultClient.
func TestNodeAndWebsocketDialGoThroughProxy(t *testing.T) {
	rl := newRelay(t)
	rl.node = func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"url":"ws://n/subsrelay/v1/stream","quota":{}}`))
	}
	accepted := make(chan struct{}, 1)
	rl.stream = func(conn *websocket.Conn) {
		accepted <- struct{}{}
		// Stay on the line like the real relay — reading what the session
		// sends and hanging up on "stop" — so what the assertion below is
		// about is the handshake alone.
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil || strings.Contains(string(data), `"stop"`) {
				return
			}
		}
	}

	p, target := throughSOCKS(t, "relay.proxy.test", rl)
	c, err := New("http://" + target)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Node(context.Background(), strings.Repeat("A", 40), "tok"); err != nil {
		t.Fatalf("node: %v", err)
	}
	if !p.Saw("relay.proxy.test") {
		t.Fatalf("the node lookup went around the proxy; it saw %v", p.Seen())
	}
	before := len(p.Seen())

	sess, err := c.Dial(context.Background(), "ws://"+target+"/subsrelay/v1/stream", "tok",
		Start{GCID: strings.Repeat("A", 40), TargetLanguage: "zh"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay never accepted the session")
	}
	// The test proxy's tunnel holds the socket open until both directions
	// end, so the relay hanging up does not reach us; a short bound keeps
	// the teardown from waiting that out.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	sess.Close(ctx)

	if len(p.Seen()) <= before {
		t.Fatalf("the WebSocket handshake went around the proxy; it saw %v", p.Seen())
	}
}
