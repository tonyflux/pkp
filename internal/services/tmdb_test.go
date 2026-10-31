package services

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/tmdb"
)

const tmdbV3Key = "0123456789abcdef0123456789abcdef"

// newTMDBService is a service with a stand-in keychain (tests must not
// touch the real one) against api, nothing signed in; tune adjusts the
// options before it starts.
func newTMDBService(t *testing.T, api http.HandlerFunc, tune func(*DriveOptions)) *DriveService {
	t.Helper()
	if api == nil {
		api = func(w http.ResponseWriter, r *http.Request) {
			if !authAndTokenRoutes(w, r) {
				http.NotFound(w, r)
			}
		}
	}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	opts := DriveOptions{
		ConfigDir:   t.TempDir(),
		APIBaseURL:  srv.URL,
		AuthBaseURL: srv.URL,
		NoIndexSync: true,
		Passwords:   &memoryVault{},
		Logger:      quiet(),
	}
	if tune != nil {
		tune(&opts)
	}
	svc := NewDriveService(opts, nil)
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	return svc
}

// countingVault counts keychain reads: each is an exec on macOS.
type countingVault struct {
	memoryVault
	mu    sync.Mutex
	reads int
}

func (v *countingVault) Password(account string) (string, error) {
	v.mu.Lock()
	v.reads++
	v.mu.Unlock()
	return v.memoryVault.Password(account)
}

func (v *countingVault) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}

// search is the movie search MatchTitles makes, with the stored
// credential.
func search(svc *DriveService) ([]tmdb.Work, error) {
	return svc.tmdb.client.Search(context.Background(), svc.tmdbKey(), "movie", "甲")
}

// tmdbStandIn answers configuration and a movie search with key only,
// refusing any other the way TMDB does, and counts searches.
func tmdbStandIn(t *testing.T, key string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	searches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != key {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"status_code":7,"status_message":"Invalid API key"}`)
			return
		}
		switch r.URL.Path {
		case "/configuration":
			io.WriteString(w, `{"images":{"secure_base_url":"https://img.example/p/","poster_sizes":["w500"],"backdrop_sizes":["w1280"]}}`)
		case "/search/movie":
			mu.Lock()
			searches++
			mu.Unlock()
			io.WriteString(w, `{"page":1,"total_pages":1,"results":[{"id":1,"title":"甲","release_date":"2020-01-01"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return searches
	}
}

// The credential is kept in the keychain only and read from it once; the
// page sees its last characters. A new one drops what the old one fetched.
func TestTMDBKeyLivesInTheKeychain(t *testing.T) {
	srv, searches := tmdbStandIn(t, tmdbV3Key)
	vault := &countingVault{}
	svc := newTMDBService(t, nil, func(o *DriveOptions) {
		o.Passwords = vault
		o.TMDBBaseURL = srv.URL
	})

	got := svc.TMDBSettings()
	if got.TMDBReady || got.TMDBKeyHint != "" || !got.Keychain {
		t.Fatalf("defaults = %+v", got)
	}
	if r := svc.TestTMDB(); r.OK || r.Kind != "config" || r.Message != "还没有配置 TMDB 凭据" {
		t.Errorf("test without a key = %+v", r)
	}
	if _, err := search(svc); err == nil || err.Error() != "还没有配置 TMDB 凭据" {
		t.Errorf("search without a key: %v", err)
	}
	for _, bad := range []string{"abc def", "abc\x00def", strings.Repeat("k", 4097)} {
		if _, err := svc.SetTMDBKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	got, err := svc.SetTMDBKey("  wrong-key-0000\n")
	if err != nil || !got.TMDBReady || got.TMDBKeyHint != "••••0000" {
		t.Fatalf("set = %+v %v", got, err)
	}
	if stored, _ := vault.memoryVault.Password(tmdbKeyAccount); stored != "wrong-key-0000" {
		t.Errorf("keychain holds %q", stored)
	}
	if r := svc.TestTMDB(); r.OK || r.Kind != "auth" || r.Message != "TMDB 凭据无效或已停用" {
		t.Errorf("test with a refused key = %+v", r)
	}

	if _, err := svc.SetTMDBKey(tmdbV3Key); err != nil {
		t.Fatal(err)
	}
	if r := svc.TestTMDB(); !r.OK || r.Kind != "ok" {
		t.Errorf("test = %+v", r)
	}
	works, err := search(svc)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || works[0].Title != "甲" || works[0].Year != "2020" {
		t.Errorf("works = %+v", works)
	}
	if _, err := search(svc); err != nil {
		t.Fatal(err)
	}
	if n := searches(); n != 1 {
		t.Errorf("searched %d times, want 1 (cached)", n)
	}
	// Saving the key again drops the cache.
	if _, err := svc.SetTMDBKey(tmdbV3Key); err != nil {
		t.Fatal(err)
	}
	if _, err := search(svc); err != nil {
		t.Fatal(err)
	}
	if n := searches(); n != 2 {
		t.Errorf("searched %d times after a new key, want 2", n)
	}

	// A fresh start reads the keychain once, however often it is used.
	again := newTMDBService(t, nil, func(o *DriveOptions) {
		o.Passwords = vault
		o.TMDBBaseURL = srv.URL
	})
	before := vault.readCount()
	for range 3 {
		if s := again.TMDBSettings(); s.TMDBKeyHint != "••••cdef" {
			t.Fatalf("restarted settings = %+v", s)
		}
	}
	if _, err := search(again); err != nil {
		t.Fatal(err)
	}
	if n := vault.readCount() - before; n != 1 {
		t.Errorf("keychain read %d times, want 1", n)
	}

	got, err = svc.SetTMDBKey("")
	if err != nil || got.TMDBReady || got.TMDBKeyHint != "" {
		t.Errorf("deleted = %+v %v", got, err)
	}
	if _, err := vault.memoryVault.Password(tmdbKeyAccount); !errors.Is(err, ErrNoSavedPassword) {
		t.Errorf("keychain after delete: %v", err)
	}
}

// Without a keychain nothing can be stored, and the page is told.
func TestTMDBKeyWithoutKeychain(t *testing.T) {
	svc := newTMDBService(t, nil, nil)
	svc.vault = nil
	got, err := svc.SetTMDBKey(tmdbV3Key)
	if err == nil || got.Keychain || got.TMDBReady {
		t.Errorf("set without a keychain = %+v %v", got, err)
	}
}
