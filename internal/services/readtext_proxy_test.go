package services

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/netproxy/proxytest"
)

// Text preview uses the CDN: the download link points at a host netproxy must reach
// through the Proxy setting, not at loopback, so the test proxy answers for an
// alias it routes to the stub and nothing else can reach it.
func TestReadTextGoesThroughProxy(t *testing.T) {
	const text = "第一行\nsecond line\n"
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "notes.txt", time.Unix(0, 0), strings.NewReader(text))
	}))
	defer origin.Close()

	p := proxytest.HTTP(t)
	addr := origin.Listener.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	const alias = "cdn.proxy.test"
	target := net.JoinHostPort(alias, port)
	p.Route(target, addr)

	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		io.WriteString(w, `{"kind":"drive#file","id":"f1","name":"notes.txt","phase":"PHASE_TYPE_COMPLETE",
			"web_content_link":"http://`+target+`/notes.txt"}`)
	})

	// After the service, which installs the Proxy setting it was opened with.
	if err := netproxy.Set(netproxy.Config{
		Mode:   netproxy.ModeManual,
		Scheme: netproxy.HTTP,
		Host:   p.Host(),
		Port:   p.Port(),
	}); err != nil {
		t.Fatalf("set proxy: %v", err)
	}
	t.Cleanup(func() { _ = netproxy.Set(netproxy.Config{Mode: netproxy.ModeSystem}) })

	preview, err := svc.ReadText(context.Background(), "f1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if preview.Text != text {
		t.Errorf("text = %q, want %q", preview.Text, text)
	}
	if !p.Saw(alias) {
		t.Fatalf("the CDN fetch went around the proxy; it saw %v", p.Seen())
	}
}
