package tmdb

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	v3Key = "0123456789abcdef0123456789abcdef"
	v4Key = "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJ0ZXN0In0.c2lnbmF0dXJl"
)

// tmdbStub is a stand-in TMDB: routes by path, counting hits, and records
// how each request carried its credential.
type tmdbStub struct {
	mu     sync.Mutex
	hits   map[string]int
	routes map[string]func(w http.ResponseWriter, r *http.Request)
	seen   []*http.Request
}

func newStubServer(t *testing.T) (*tmdbStub, *httptest.Server) {
	t.Helper()
	stub := &tmdbStub{hits: map[string]int{}, routes: map[string]func(http.ResponseWriter, *http.Request){}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/3/")
		stub.mu.Lock()
		stub.hits[path]++
		stub.seen = append(stub.seen, r)
		route := stub.routes[path]
		stub.mu.Unlock()
		if route == nil {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"status_code":34,"status_message":"The resource you requested could not be found."}`)
			return
		}
		route(w, r)
	}))
	t.Cleanup(srv.Close)
	return stub, srv
}

func newTMDBStub(t *testing.T) (*tmdbStub, *Client) {
	t.Helper()
	stub, srv := newStubServer(t)
	return stub, New(srv.URL+"/3", srv.Client())
}

func (s *tmdbStub) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

func (s *tmdbStub) json(path, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

func (s *tmdbStub) handle(path string, fn func(http.ResponseWriter, *http.Request)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = fn
}

func (s *tmdbStub) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func (s *tmdbStub) request(i int) *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[i]
}

// last is the query of the last request for path.
func (s *tmdbStub) last(path string) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.seen) - 1; i >= 0; i-- {
		if strings.TrimPrefix(s.seen[i].URL.Path, "/3/") == path {
			return s.seen[i].URL.Query()
		}
	}
	return nil
}

const configJSON = `{"images":{"secure_base_url":"https://img.example/t/p/"}}`

// A v3 key travels as api_key, a v4 token as a Bearer header; never both,
// and every request asks for Chinese.
func TestTMDBCredentialPlacement(t *testing.T) {
	for name, key := range map[string]string{"v3": v3Key, "v4": v4Key} {
		t.Run(name, func(t *testing.T) {
			stub, tm := newTMDBStub(t)
			stub.json("configuration", configJSON)
			if err := tm.Test(context.Background(), key); err != nil {
				t.Fatal(err)
			}
			r := stub.request(0)
			q := r.URL.Query()
			if q.Get("language") != "zh-CN" {
				t.Errorf("language = %q", q.Get("language"))
			}
			auth := r.Header.Get("Authorization")
			if name == "v3" && (q.Get("api_key") != key || auth != "") {
				t.Errorf("v3: api_key=%q auth=%q", q.Get("api_key"), auth)
			}
			if name == "v4" && (q.Has("api_key") || auth != "Bearer "+key) {
				t.Errorf("v4: api_key=%q auth=%q", q.Get("api_key"), auth)
			}
		})
	}
}

// The key is in the URL the transport would quote; neither a dropped
// connection nor a refusal may show it.
func TestTMDBErrorsNeverCarryTheKey(t *testing.T) {
	// A server that hangs up on every request.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	defer ln.Close()
	tm := New("http://"+ln.Addr().String()+"/3", nil)
	err = tm.Test(context.Background(), v3Key)
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindUnreachable || !strings.HasPrefix(err.Error(), "连不上 TMDB：") {
		t.Fatalf("hang-up: %v", err)
	}
	if strings.Contains(err.Error(), v3Key) || strings.Contains(err.Error(), "api_key") {
		t.Errorf("transport error leaks the key: %q", err)
	}

	stub, tm := newTMDBStub(t)
	stub.handle("configuration", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"status_code":7,"status_message":"Invalid API key: `+r.URL.Query().Get("api_key")+`"}`)
	})
	err = tm.Test(context.Background(), v3Key)
	if !errors.As(err, &e) || e.Kind != KindAuth || err.Error() != "TMDB 凭据无效或已停用" {
		t.Fatalf("401: %v", err)
	}
	if strings.Contains(err.Error(), v3Key) {
		t.Errorf("401 leaks the key: %q", err)
	}
}

// A long Retry-After is the user's to wait out and is reported; a short
// one is waited out once on their behalf.
func TestTMDBRateLimit(t *testing.T) {
	stub, tm := newTMDBStub(t)
	stub.handle("configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	err := tm.Test(context.Background(), v3Key)
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindRateLimit || e.RetryAfter != 30 || err.Error() != "TMDB 请求过于频繁，请 30 秒后重试" {
		t.Fatalf("Retry-After 30: %v", err)
	}
	if n := stub.count("configuration"); n != 1 {
		t.Errorf("Retry-After 30 sent %d requests, want 1", n)
	}

	var calls atomic.Int64
	stub.handle("configuration", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, configJSON)
	})
	if err := tm.Test(context.Background(), v3Key); err != nil {
		t.Fatalf("Retry-After 1: %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("Retry-After 1 sent %d requests, want 2", n)
	}
}

// Each kind reads its own title and date fields; an odd item is skipped,
// a title-less one takes its original title, and a bad date is no year.
func TestTMDBSearchBuildsWorks(t *testing.T) {
	stub, tm := newTMDBStub(t)
	stub.json("search/movie", `{"page":1,"total_pages":3,"results":[
		{"id":10,"title":"沙丘2","original_title":"Dune: Part Two","release_date":"2024-02-27"},
		"not an object",
		{"id":0,"title":"无编号"},
		{"id":12,"title":"无日期","release_date":"2024-13-45"}
	]}`)
	stub.json("search/tv", `{"page":1,"results":[{"id":11,"name":"","original_name":"Arcane","first_air_date":"2021-11-06"}]}`)
	movies, err := tm.Search(context.Background(), v3Key, "movie", "  沙丘 ")
	if err != nil {
		t.Fatal(err)
	}
	if q := stub.request(0).URL.Query(); q.Get("query") != "沙丘" || q.Get("page") != "1" || q.Get("include_adult") != "false" {
		t.Errorf("search query = %v", q)
	}
	want := []Work{
		{Kind: "movie", ID: 10, Title: "沙丘2", OriginalTitle: "Dune: Part Two", Year: "2024"},
		{Kind: "movie", ID: 12, Title: "无日期"},
	}
	if !slices.Equal(movies, want) {
		t.Errorf("movies = %+v", movies)
	}
	shows, err := tm.Search(context.Background(), v3Key, "tv", "Arcane")
	if err != nil || len(shows) != 1 || shows[0] != (Work{Kind: "tv", ID: 11, Title: "Arcane", OriginalTitle: "Arcane", Year: "2021"}) {
		t.Errorf("shows = %+v %v", shows, err)
	}
}

// A second identical search is served from the cache; a failed one is not
// kept, so the next call asks again; Reset forgets everything.
func TestTMDBCaches(t *testing.T) {
	stub, tm := newTMDBStub(t)
	stub.json("search/movie", `{"results":[{"id":1,"title":"A"}]}`)
	for range 2 {
		if _, err := tm.Search(context.Background(), v3Key, "movie", "A"); err != nil {
			t.Fatal(err)
		}
	}
	if n := stub.count("search/movie"); n != 1 {
		t.Errorf("search asked %d times, want 1", n)
	}

	var fail atomic.Bool
	fail.Store(true)
	stub.handle("search/tv", func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, `{"results":[{"id":2,"name":"B"}]}`)
	})
	if _, err := tm.Search(context.Background(), v3Key, "tv", "B"); err == nil || err.Error() != "TMDB 返回错误（HTTP 502）" {
		t.Fatalf("failing call: %v", err)
	}
	fail.Store(false)
	if got, err := tm.Search(context.Background(), v3Key, "tv", "B"); err != nil || len(got) != 1 {
		t.Fatalf("after recovery: %+v %v", got, err)
	}
	if n := stub.count("search/tv"); n != 2 {
		t.Errorf("search tv asked %d times, want 2 (failure not cached)", n)
	}

	tm.Reset()
	if _, err := tm.Search(context.Background(), v3Key, "movie", "A"); err != nil {
		t.Fatal(err)
	}
	if n := stub.count("search/movie"); n != 2 {
		t.Errorf("after Reset search asked %d times in all, want 2", n)
	}
}

func TestTMDBChecksSearches(t *testing.T) {
	tm := New("http://127.0.0.1:1/3", nil)
	for _, tc := range [][2]string{{"movie", "  "}, {"tv", strings.Repeat("长", 101)}, {"person", "A"}, {"", "A"}} {
		if _, err := tm.Search(context.Background(), v3Key, tc[0], tc[1]); err == nil {
			t.Errorf("%q %q accepted", tc[0], tc[1])
		}
	}
	if _, err := tm.Search(context.Background(), "", "movie", "A"); err == nil || err.Error() != "还没有配置 TMDB 凭据" {
		t.Errorf("no key: %v", err)
	}
}
