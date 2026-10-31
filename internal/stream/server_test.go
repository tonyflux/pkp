package stream

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

// upstream serves content with Range support and records the headers it saw.
func upstream(t *testing.T, body []byte, seen *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		w.Header().Set("x-limited", "0")
		w.Header().Set("RateLimit-Quota-Used-downstream", "123")
		http.ServeContent(w, r, "clip.bin", time.Unix(0, 0), bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func proxy(t *testing.T) *Server {
	t.Helper()
	s := NewServer(nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// A player that seeks issues a Range request; if the proxy swallows it the
// upstream answers 200 with the whole object and seeking collapses into a full
// refetch, which is the single most expensive bug this proxy can have.
func TestRangeRequestIsForwarded(t *testing.T) {
	body := payload(4096)
	origin := upstream(t, body, nil)
	p := proxy(t)

	handle, err := p.Publish(Source{URL: origin.URL})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, handle.URL, nil)
	req.Header.Set("Range", "bytes=1000-1099")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Content-Range"), "bytes 1000-1099/4096"; got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", got)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, body[1000:1100]) {
		t.Errorf("body mismatch: got %d bytes", len(got))
	}
	if got, want := resp.Header.Get("Content-Length"), strconv.Itoa(100); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
}

// Credentials live on the Source, never in the player, so they must reach the
// upstream on every request including ranged ones.
func TestSourceHeadersAreInjected(t *testing.T) {
	var seen http.Header
	origin := upstream(t, payload(64), &seen)
	p := proxy(t)

	handle, err := p.Publish(Source{URL: origin.URL, Headers: map[string]string{
		"Authorization": "Bearer token-123",
		"Referer":       "https://mypikpak.com/",
		"User-Agent":    "PKP-Test/1.0",
	}})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	resp, err := http.Get(handle.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	for key, want := range map[string]string{
		"Authorization": "Bearer token-123",
		"Referer":       "https://mypikpak.com/",
		"User-Agent":    "PKP-Test/1.0",
	} {
		if got := seen.Get(key); got != want {
			t.Errorf("upstream %s = %q, want %q", key, got, want)
		}
	}
}

func TestDefaultUserAgentApplied(t *testing.T) {
	var seen http.Header
	origin := upstream(t, payload(16), &seen)
	p := proxy(t)

	handle, _ := p.Publish(Source{URL: origin.URL})
	resp, err := http.Get(handle.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	if got := seen.Get("User-Agent"); got != DefaultUserAgent {
		t.Errorf("User-Agent = %q, want %q", got, DefaultUserAgent)
	}
}

// The handle id is a capability. Once revoked it must stop serving, otherwise
// a direct link outlives the session that was allowed to use it.
func TestUnpublishRevokesHandle(t *testing.T) {
	origin := upstream(t, payload(16), nil)
	p := proxy(t)

	handle, _ := p.Publish(Source{URL: origin.URL})
	p.Unpublish(handle.ID)

	resp, err := http.Get(handle.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// Throttling signals are the whole reason the proxy sits in the path; losing
// them means the download engine cannot back off.
func TestStatsCaptureThrottlingSignals(t *testing.T) {
	body := payload(2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-limited", "1")
		w.Header().Set("Retry-After", "7")
		w.Header().Set("RateLimit-Quota-Used-downstream", "900")
		w.Header().Set("RateLimit-Quota-Limit-downstream", "1000")
		http.ServeContent(w, r, "clip.bin", time.Unix(0, 0), bytes.NewReader(body))
	}))
	defer srv.Close()

	p := proxy(t)
	handle, _ := p.Publish(Source{URL: srv.URL})
	resp, err := http.Get(handle.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	stats := p.Stats()
	if !stats.Throttled {
		t.Error("Throttled = false, want true for x-limited: 1")
	}
	if stats.RetryAfter != "7" {
		t.Errorf("RetryAfter = %q, want 7", stats.RetryAfter)
	}
	if got := stats.Quota["ratelimit-quota-used-downstream"]; got != "900" {
		t.Errorf("quota used = %q, want 900", got)
	}
	if stats.BytesServed != n {
		t.Errorf("BytesServed = %d, want %d", stats.BytesServed, n)
	}
	if stats.Requests != 1 {
		t.Errorf("Requests = %d, want 1", stats.Requests)
	}
}

func TestPublishRejectsNonHTTPScheme(t *testing.T) {
	p := proxy(t)
	if _, err := p.Publish(Source{URL: "file:///etc/passwd"}); err == nil {
		t.Fatal("expected error for file:// source")
	}
}

// A seek is one Range request; if the CDN drops it the player sees the file
// as truncated and runs to the end. One dropped connection and one 503 must
// both be absorbed before the player hears about anything.
func TestTransientUpstreamFailuresAreRetried(t *testing.T) {
	body := payload(4096)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			// Dropped connection: no response at all.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close()
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.ServeContent(w, r, "clip.bin", time.Unix(0, 0), bytes.NewReader(body))
		}
	}))
	defer srv.Close()

	p := proxy(t)
	handle, _ := p.Publish(Source{URL: srv.URL})

	req, _ := http.NewRequest(http.MethodGet, handle.URL, nil)
	req.Header.Set("Range", "bytes=1000-1099")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 after retries", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, body[1000:1100]) {
		t.Errorf("body mismatch: got %d bytes", len(got))
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("upstream calls = %d, want 3", n)
	}
	if p.Stats().LastError != "" {
		t.Errorf("LastError = %q, want empty: the player never saw a failure", p.Stats().LastError)
	}
}

// The retry budget is finite: a dead upstream still ends in a 502, and the
// player is not left hanging on a request that will never complete.
func TestPersistentUpstreamFailureIs502(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	p := proxy(t)
	handle, _ := p.Publish(Source{URL: srv.URL})
	resp, err := http.Get(handle.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if n := calls.Load(); n != int32(len(upstreamBackoff)+1) {
		t.Errorf("upstream calls = %d, want %d", n, len(upstreamBackoff)+1)
	}
	if p.Stats().LastError == "" {
		t.Error("LastError empty, want the upstream status recorded")
	}
}

// An archived object the CDN has not thawed sends 206 with the full length
// and no body. The player must get a clean failure and the handle must say
// why; an archive-class object that does read (thawed, ARCHIVE_FR) streams.
func TestArchivedObjectWithoutBodyIsReported(t *testing.T) {
	body := payload(4096)
	var readable atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tos-Storage-Class", "ARCHIVE")
		if readable.Load() {
			http.ServeContent(w, r, "clip.bin", time.Unix(0, 0), bytes.NewReader(body))
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	p := proxy(t)
	frozen, _ := p.Publish(Source{URL: srv.URL})
	resp, err := http.Get(frozen.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusLocked {
		t.Errorf("status = %d, want 423: a 5xx would have mpv retry in vain", resp.StatusCode)
	}
	if !p.Archived(frozen.ID) {
		t.Error("Archived = false for an archived object that sent no body")
	}

	readable.Store(true)
	thawed, _ := p.Publish(Source{URL: srv.URL})
	req, _ := http.NewRequest(http.MethodGet, thawed.URL, nil)
	req.Header.Set("Range", "bytes=1000-")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, body[1000:]) {
		t.Errorf("thawed: status %d, %d bytes; want 206 with %d bytes intact", resp.StatusCode, len(got), len(body)-1000)
	}
	if p.Archived(thawed.ID) {
		t.Error("Archived = true for an archive-class object that read fine")
	}
}

// A webview decides whether to render inline from Content-Type; a CDN that
// says application/octet-stream for a PDF must be overruled by the publisher.
func TestContentTypeOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=doc.pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	}))
	defer srv.Close()

	p := proxy(t)
	pinned, _ := p.Publish(Source{URL: srv.URL, ContentType: "application/pdf"})
	plain, _ := p.Publish(Source{URL: srv.URL})

	for _, tc := range []struct{ url, want string }{{pinned.URL, "application/pdf"}, {plain.URL, "application/octet-stream"}} {
		resp, err := http.Get(tc.url)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Content-Type"); got != tc.want {
			t.Errorf("Content-Type = %q, want %q", got, tc.want)
		}
		if resp.Header.Get("Content-Disposition") != "" {
			t.Error("Content-Disposition leaked through; it would force a download")
		}
	}
}
