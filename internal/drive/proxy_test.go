package drive

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
// proxies loopback, so a test that aimed at 127.0.0.1 would prove nothing.
// The proxy dials srv whenever it is asked for that name, so no DNS is
// involved and nothing but the proxy can reach the server.
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

// File API and Cloud Additions: the client the app builds for itself (no injected
// HTTPClient) must take the proxy setting, or a user behind a blocked network
// sees an empty drive.
func TestFileAPIAndCloudAddGoThroughProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			io.WriteString(w, `{"task":{"id":"t1","phase":"PHASE_TYPE_RUNNING"},"file":{"id":"f1"}}`)
			return
		}
		io.WriteString(w, `{"kind":"drive#fileList","files":[{"id":"f1","name":"a.mkv"}]}`)
	}))
	t.Cleanup(srv.Close)

	p, base := throughProxy(t, "api-drive.proxy.test", srv)

	client, err := New(Config{
		APIBaseURL:  base,
		AuthBaseURL: base,
		DAVBaseURL:  base,
		Profile:     AndroidProfile(),
		DeviceID:    "0123456789abcdef0123456789abcdef",
		Captcha:     &fakeCaptcha{token: "captcha-0"},
		Logger:      quiet(),
	}, newStaticTokens("tok"))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if _, err := client.ListFiles(context.Background(), ListFilesParams{Limit: 10}); err != nil {
		t.Fatalf("list files: %v", err)
	}
	if !p.Saw("api-drive.proxy.test") {
		t.Fatalf("the file list went around the proxy; it saw %v", p.Seen())
	}

	if _, err := client.CreateOfflineTask(context.Background(), OfflineTaskParams{URL: "magnet:?xt=urn:btih:abc"}); err != nil {
		t.Fatalf("cloud add: %v", err)
	}
	if n := len(p.Seen()); n < 2 {
		t.Errorf("Cloud add did not reach the proxy: it saw %v", p.Seen())
	}
}

// Sign-In: the sign-in and the token refreshes behind it are the first thing a
// blocked network stops, so the TokenManager's own client must be proxied too.
func TestSignInGoesThroughProxy(t *testing.T) {
	srv := newAuthServer(t)
	p, base := throughProxy(t, "user.proxy.test", srv.Server)

	m := NewTokenManager(TokenManagerConfig{
		AuthBaseURL: base,
		Profile:     AndroidProfile(),
		DeviceID:    DeviceIDFor("user@example.com"),
		Store:       &MemoryStore{},
	})
	if _, err := m.SignIn(context.Background(), "user@example.com", "secret"); err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if !p.Saw("user.proxy.test") {
		t.Fatalf("Login went around the proxy; it saw %v", p.Seen())
	}
	// The captcha exchange shares that client, so it crossed the proxy too.
	if n := len(p.Seen()); n < 2 {
		t.Errorf("the captcha handshake did not reach the proxy: %v", p.Seen())
	}
}
