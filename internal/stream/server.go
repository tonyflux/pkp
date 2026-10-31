// Package stream publishes remote media on a loopback HTTP endpoint.
//
// Players (mpv here) fetch plain, unauthenticated 127.0.0.1 URLs and issue
// ordinary Range requests; the server attaches the credentials, user agent and
// referer the upstream demands, and observes the throttling signals PikPak
// returns (the RateLimit-Quota-* family and x-limited) so the rest of the app
// can react to them. Keeping that in one place is what later lets a segmented
// download engine sit behind the same URL without the player noticing.
//
// The upstream side follows the app's Proxy setting (internal/netproxy); the
// loopback side never does, so the player always reaches this process direct.
package stream

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tonyflux/pkp/internal/netproxy"
)

// DefaultUserAgent is sent upstream when a Source does not specify one.
const DefaultUserAgent = "PKP-Desktop/0.1.0"

// upstreamBackoff is the pause before each retry of a failed upstream
// request; its length is the retry budget. Short, because a player is
// waiting with a stalled seek behind every one of these.
var upstreamBackoff = []time.Duration{250 * time.Millisecond, 750 * time.Millisecond}

// requestHeaders are forwarded from the local player to the upstream verbatim.
// Range is the one that matters: without it seeking degrades to a full refetch.
var requestHeaders = []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"}

// responseHeaders are copied back to the player. Content-Range and
// Accept-Ranges are what tell mpv the stream is seekable.
var responseHeaders = []string{
	"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges",
	"ETag", "Last-Modified", "Cache-Control",
}

// Source is one publishable upstream object.
type Source struct {
	// URL is the upstream media URL, e.g. a PikPak web_content_link.
	URL string
	// Headers are sent upstream on every request. Typical keys: User-Agent,
	// Referer, Authorization.
	Headers map[string]string
	// ContentType, when set, replaces the upstream Content-Type. A CDN that
	// labels every download application/octet-stream makes the webview
	// refuse to render a PDF inline; the publisher knows better.
	ContentType string
}

// Handle identifies a published source and carries the local URL to hand to a
// player.
type Handle struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Stats is a snapshot of proxy activity. Quota mirrors the upstream
// RateLimit-Quota-* headers, keyed by the lower-cased header name.
type Stats struct {
	Requests     int64             `json:"requests"`
	BytesServed  int64             `json:"bytesServed"`
	LastStatus   int               `json:"lastStatus"`
	Throttled    bool              `json:"throttled"`
	RetryAfter   string            `json:"retryAfter"`
	Quota        map[string]string `json:"quota"`
	LastError    string            `json:"lastError"`
	LastUpdateMs int64             `json:"lastUpdateMs"`
}

// Server is the loopback proxy. The zero value is not usable; call NewServer.
type Server struct {
	log    *slog.Logger
	client *http.Client

	ln   net.Listener
	srv  *http.Server
	base string

	mu       sync.RWMutex
	sources  map[string]Source
	archived map[string]bool

	requests    atomic.Int64
	bytesServed atomic.Int64

	statsMu sync.RWMutex
	stats   Stats
}

// NewServer returns a proxy that has not started listening yet.
func NewServer(log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	// The upstream fetches carry every byte the player and the preview
	// sheet show, so they follow the app's Proxy setting; what the player
	// asks this server for stays on loopback, which netproxy never proxies.
	transport := netproxy.Install(&http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	})
	return &Server{
		log:      log,
		client:   &http.Client{Transport: transport}, // no overall timeout: bodies are long-lived
		sources:  make(map[string]Source),
		archived: make(map[string]bool),
	}
}

// Start binds a random loopback port and serves until Close.
func (s *Server) Start() error {
	if s.ln != nil {
		return errors.New("stream: already started")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("stream: listen: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/s/", s.handleStream)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	s.ln = ln
	s.base = "http://" + ln.Addr().String()
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("stream: serve", "error", err)
		}
	}()
	return nil
}

// Close stops the listener and drops every published source.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.srv.Shutdown(ctx)

	s.mu.Lock()
	s.sources = make(map[string]Source)
	s.archived = make(map[string]bool)
	s.mu.Unlock()
	return err
}

// BaseURL is the proxy root, e.g. http://127.0.0.1:54321. Empty before Start.
func (s *Server) BaseURL() string { return s.base }

// Publish registers a source and returns the loopback URL that serves it. The
// random id doubles as a capability: other local processes cannot guess it.
func (s *Server) Publish(src Source) (Handle, error) {
	if s.base == "" {
		return Handle{}, errors.New("stream: server not started")
	}
	parsed, err := url.Parse(src.URL)
	if err != nil {
		return Handle{}, fmt.Errorf("stream: bad source url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Handle{}, fmt.Errorf("stream: unsupported scheme %q", parsed.Scheme)
	}

	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Handle{}, err
	}
	id := hex.EncodeToString(raw[:])

	s.mu.Lock()
	s.sources[id] = src
	s.mu.Unlock()

	return Handle{ID: id, URL: s.base + "/s/" + id}, nil
}

// Unpublish revokes a handle. In-flight responses are unaffected.
func (s *Server) Unpublish(id string) {
	s.mu.Lock()
	delete(s.sources, id)
	delete(s.archived, id)
	s.mu.Unlock()
}

// Archived reports whether the upstream of a handle turned out to be an
// archived (cold storage) object the CDN cannot read until it is thawed.
func (s *Server) Archived(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.archived[id]
}

// Stats returns a snapshot of proxy activity.
func (s *Server) Stats() Stats {
	s.statsMu.RLock()
	out := s.stats
	s.statsMu.RUnlock()

	out.Requests = s.requests.Load()
	out.BytesServed = s.bytesServed.Load()
	if out.Quota == nil {
		out.Quota = map[string]string{}
	}
	return out
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Published URLs are random bearer capabilities; no cookies or credentials
	// are accepted from the browser. Readers need Range metadata across origins.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges, ETag")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Range, If-Range, If-None-Match, If-Modified-Since")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/s/")
	s.mu.RLock()
	src, ok := s.sources[id]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	s.requests.Add(1)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, src.URL, nil)
	if err != nil {
		s.recordError(err)
		http.Error(w, "bad upstream url", http.StatusBadGateway)
		return
	}
	for k, v := range src.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", DefaultUserAgent)
	}
	for _, h := range requestHeaders {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	resp, err := s.fetch(r.Context(), req)
	if err != nil {
		s.recordError(err)
		s.log.Warn("stream: upstream request failed", "id", id, "range", r.Header.Get("Range"), "error", err)
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	s.recordResponse(resp)

	body := io.Reader(resp.Body)
	if r.Method == http.MethodGet && resp.StatusCode < 300 && resp.ContentLength != 0 && archiveClass(resp.Header) {
		// An archived object the CDN has not thawed answers 206 with the
		// full length and then closes without a byte; passed on, the player
		// only sees a broken file. Peek before committing the headers.
		buf := make([]byte, 32<<10)
		n, err := io.ReadAtLeast(resp.Body, buf, 1)
		if n == 0 {
			s.mu.Lock()
			if _, live := s.sources[id]; live {
				s.archived[id] = true
			}
			s.mu.Unlock()
			s.recordError(errors.New("upstream object is archived"))
			s.log.Warn("stream: upstream object is archived", "id", id, "error", err)
			// A 4xx: mpv retries 5xx (reconnect_on_http_error), and no
			// retry thaws an archive; it would only delay the message.
			http.Error(w, "upstream object is archived", http.StatusLocked)
			return
		}
		body = io.MultiReader(bytes.NewReader(buf[:n]), resp.Body)
	}

	header := w.Header()
	for _, h := range responseHeaders {
		if v := resp.Header.Get(h); v != "" {
			header.Set(h, v)
		}
	}
	if src.ContentType != "" {
		header.Set("Content-Type", src.ContentType)
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}

	n, err := io.Copy(w, body)
	s.bytesServed.Add(n)
	if err != nil && !errors.Is(err, context.Canceled) {
		// A player that seeks or stops mid-body aborts the connection; that is
		// routine, so it is logged at debug level rather than recorded as a
		// proxy error.
		s.log.Debug("stream: body copy ended early", "id", id, "bytes", n, "error", err)
	}
}

// fetch performs the upstream request, retrying the transient failures a
// CDN produces under load: a dropped or refused connection, or a 502/503/504.
// Every request here is a GET or HEAD, so repeating one is safe. The retries
// matter because a player does not retry on its own: ffmpeg treats a failed
// Range request on seek as a truncated file and runs playback to the end.
func (s *Server) fetch(ctx context.Context, req *http.Request) (*http.Response, error) {
	var last error
	for attempt := 0; ; attempt++ {
		resp, err := s.client.Do(req)
		if err == nil && !transientStatus(resp.StatusCode) {
			return resp, nil
		}
		if err == nil {
			last = fmt.Errorf("upstream status %d", resp.StatusCode)
			resp.Body.Close()
		} else {
			last = err
		}
		if ctx.Err() != nil || attempt >= len(upstreamBackoff) {
			return nil, last
		}
		s.log.Warn("stream: upstream attempt failed, retrying", "attempt", attempt+1, "error", last)
		select {
		case <-ctx.Done():
			return nil, last
		case <-time.After(upstreamBackoff[attempt]):
		}
	}
}

func transientStatus(code int) bool {
	return code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
}

// archiveClass reports a storage class that may need a restore before it can
// be read: TOS "ARCHIVE"/"COLD_ARCHIVE", OSS "Archive"/"ColdArchive". The
// classes that read directly (TOS ARCHIVE_FR) pass the body peek untouched.
func archiveClass(h http.Header) bool {
	for _, name := range []string{"X-Tos-Storage-Class", "X-Oss-Storage-Class"} {
		if strings.Contains(strings.ToLower(h.Get(name)), "archive") {
			return true
		}
	}
	return false
}

func (s *Server) recordResponse(resp *http.Response) {
	quota := map[string]string{}
	for k, v := range resp.Header {
		key := strings.ToLower(k)
		if len(v) > 0 && strings.HasPrefix(key, "ratelimit-quota-") {
			quota[key] = v[0]
		}
	}

	s.statsMu.Lock()
	s.stats.LastStatus = resp.StatusCode
	s.stats.Throttled = resp.Header.Get("x-limited") == "1" || resp.StatusCode == http.StatusTooManyRequests
	s.stats.RetryAfter = resp.Header.Get("Retry-After")
	s.stats.Quota = quota
	s.stats.LastUpdateMs = time.Now().UnixMilli()
	if resp.StatusCode < 400 {
		s.stats.LastError = ""
	} else {
		s.stats.LastError = "upstream status " + resp.Status
	}
	s.statsMu.Unlock()
}

func (s *Server) recordError(err error) {
	s.statsMu.Lock()
	s.stats.LastError = err.Error()
	s.stats.LastUpdateMs = time.Now().UnixMilli()
	s.statsMu.Unlock()
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
