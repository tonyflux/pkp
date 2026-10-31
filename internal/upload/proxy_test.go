package upload

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/netproxy/proxytest"
)

// manualProxy puts this process behind a recording HTTP proxy for the rest of
// the test and gives it back.
func manualProxy(t *testing.T) *proxytest.Proxy {
	t.Helper()
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
	return p
}

// Upload: the OSS SDK's own transport proxies nothing, so the client the queue
// hands it is the only thing that can put an upload behind the proxy setting.
// The object store is reached under a name that is not loopback, which
// netproxy never proxies, and the test proxy dials the stub when asked for
// it, so no DNS is involved.
func TestUploadGoesThroughProxy(t *testing.T) {
	content := "local file bytes that have to cross the proxy"
	stored := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		stored <- string(body)
		w.Header().Set("ETag", `"uploaded"`)
	}))
	defer server.Close()

	p := manualProxy(t)
	addr := server.Listener.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	const alias = "oss.proxy.test"
	// The SDK may address the bucket by path or as a subdomain; both names
	// lead to the same stub.
	p.Route(net.JoinHostPort(alias, port), addr)
	p.Route(net.JoinHostPort("bucket."+alias, port), addr)

	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	events := make(chan Task, 64)
	q := New(ossBackend{endpoint: "http://" + net.JoinHostPort(alias, port)}, 1, func(task Task) { events <- task })
	task, err := q.Add(path, Target{})
	if err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, events, task.ID, func(tk Task) bool { return !tk.Active() })
	if final.State != StateDone || final.FileID != "uploaded" {
		t.Fatalf("upload ended as %+v", final)
	}
	select {
	case got := <-stored:
		if got != content {
			t.Fatalf("uploaded bytes = %q, want %q", got, content)
		}
	default:
		t.Fatal("completed without storing the file")
	}

	var crossed bool
	for _, target := range p.Seen() {
		if host, _, err := net.SplitHostPort(target); err == nil && strings.HasSuffix(host, alias) {
			crossed = true
		}
	}
	if !crossed {
		t.Fatalf("the upload went around the proxy; it saw %v", p.Seen())
	}
}

// The client the queue hands the SDK asks netproxy per request: a proxy for
// the object store, none for loopback. One client per queue, so a long
// session does not leave a transport behind for every file.
func TestOSSClientAsksNetproxyPerRequest(t *testing.T) {
	p := manualProxy(t)
	q := New(ossBackend{endpoint: "http://oss.example.com"}, 1, nil)

	transport, ok := q.oss.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the SDK's client carries a %T, not an *http.Transport", q.oss.Transport)
	}
	if transport.MaxConnsPerHost == 0 || transport.IdleConnTimeout == 0 {
		t.Errorf("the SDK's own tuning was lost: %+v", transport)
	}

	remote, _ := url.Parse("https://oss-cn-beijing.aliyuncs.com/bucket/object")
	got, err := transport.Proxy(&http.Request{URL: remote})
	if err != nil {
		t.Fatalf("proxy for %s: %v", remote, err)
	}
	want, err := netproxy.For(remote)
	if err != nil {
		t.Fatalf("netproxy.For: %v", err)
	}
	if got == nil || want == nil || got.String() != want.String() {
		t.Fatalf("proxy for %s = %v, want netproxy's %v", remote, got, want)
	}
	if got.Host != p.Addr {
		t.Errorf("proxy host = %s, want the one in Settings (%s)", got.Host, p.Addr)
	}

	local, _ := url.Parse("http://127.0.0.1:7890/s/abc")
	if got, err := transport.Proxy(&http.Request{URL: local}); err != nil || got != nil {
		t.Fatalf("loopback would be proxied through %v (err %v)", got, err)
	}
}
