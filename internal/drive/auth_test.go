package drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// authServer stands in for user.mypikpak.com: the captcha handshake plus the
// two grant types.
type authServer struct {
	*httptest.Server

	mu            sync.Mutex
	captchaInits  int
	signIns       int
	refreshes     int
	lastSignIn    map[string]any
	lastCaptcha   map[string]any
	refreshIsDead bool
	// liveRefresh, once set, makes the token endpoint behave like the real
	// one: it accepts only this refresh token, and spending it retires it in
	// favour of a numbered successor.
	liveRefresh  string
	signInError  string
	challengeURL string
}

func newAuthServer(t *testing.T) *authServer {
	t.Helper()
	s := &authServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		s.mu.Lock()
		defer s.mu.Unlock()

		switch r.URL.Path {
		case "/v1/shield/captcha/init":
			s.captchaInits++
			s.lastCaptcha = body
			if s.challengeURL != "" {
				writeJSON(w, map[string]any{"url": s.challengeURL, "captcha_token": "pending", "expires_in": 300})
				return
			}
			writeJSON(w, map[string]any{"captcha_token": "captcha-" + strconv.Itoa(s.captchaInits), "expires_in": 300})

		case "/v1/auth/signin":
			s.signIns++
			s.lastSignIn = body
			if s.signInError != "" {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": s.signInError, "error_code": 4002, "error_description": "wrong password"})
				return
			}
			writeJSON(w, map[string]any{
				"token_type": "Bearer", "access_token": "access-signin", "refresh_token": "refresh-signin",
				"expires_in": 7200, "sub": "user-1",
			})

		case "/v1/auth/token":
			s.refreshes++
			if s.refreshIsDead {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": "invalid_grant", "error_code": refreshTokenInvalidCode})
				return
			}
			if s.liveRefresh != "" {
				if body["refresh_token"] != s.liveRefresh {
					w.WriteHeader(http.StatusBadRequest)
					writeJSON(w, map[string]any{
						"error": "invalid_grant", "error_code": refreshTokenInvalidCode,
						"error_description": "invalidate refresh token token does not match",
					})
					return
				}
				s.liveRefresh = fmt.Sprintf("refresh-%d", s.refreshes)
				writeJSON(w, map[string]any{
					"token_type": "Bearer", "access_token": fmt.Sprintf("access-%d", s.refreshes),
					"refresh_token": s.liveRefresh, "expires_in": 7200, "sub": "user-1",
				})
				return
			}
			writeJSON(w, map[string]any{
				"token_type": "Bearer", "access_token": "access-refresh", "refresh_token": "refresh-rotated",
				"expires_in": 7200, "sub": "user-1",
			})

		default:
			t.Errorf("unexpected auth path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *authServer) counts() (captcha, signIn, refresh int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.captchaInits, s.signIns, s.refreshes
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// gatedStore is a MemoryStore whose Lock waits until the test opens the
// gate, which parks a renewal inside TokenManager.mu before it reaches the
// captcha token.
type gatedStore struct {
	MemoryStore
	entered chan struct{}
	gate    chan struct{}
}

func (s *gatedStore) Lock(ctx context.Context) (func(), error) {
	close(s.entered)
	<-s.gate
	return s.MemoryStore.Lock(ctx)
}

// Captcha.Refresh must not hold its own mutex while asking the TokenManager
// for the user id: a renewal takes TokenManager.mu first and then reads the
// captcha token (exchange), so the reverse order deadlocks whenever a
// refresh is in flight while a captcha rejection comes back.
func TestCaptchaRefreshDoesNotDeadlockWithRenewal(t *testing.T) {
	srv := newAuthServer(t)
	store := &gatedStore{entered: make(chan struct{}), gate: make(chan struct{})}
	m := NewTokenManager(TokenManagerConfig{AuthBaseURL: srv.URL, DeviceID: "d", Store: store})
	// Seed through the memory store directly: Seed takes the gated lock too.
	if err := store.MemoryStore.Save(Token{Username: "u", DeviceID: "d", RefreshToken: "seed"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m.captcha.set("stale")

	renewed := make(chan error, 1)
	go func() {
		_, err := m.Token(context.Background())
		renewed <- err
	}()
	<-store.entered

	refreshed := make(chan error, 1)
	go func() { refreshed <- m.Captcha().Refresh(context.Background(), "GET:/x", "stale") }()
	// Give Refresh time to reach userID() while the renewal holds m.mu.
	time.Sleep(50 * time.Millisecond)
	close(store.gate)

	for _, ch := range []chan error{renewed, refreshed} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("renewal and captcha refresh deadlocked")
		}
	}
	if got := m.Captcha().Token(); got != "captcha-1" {
		t.Errorf("captcha token = %q, want captcha-1", got)
	}
}

func newManager(t *testing.T, srv *authServer, password func() (string, error)) *TokenManager {
	t.Helper()
	return NewTokenManager(TokenManagerConfig{
		AuthBaseURL: srv.URL,
		Profile:     AndroidProfile(),
		DeviceID:    DeviceIDFor("user@example.com"),
		Store:       &MemoryStore{},
		Password:    password,
	})
}

// processManager is one process signed in through the credentials file at
// path: every process keeps its own memory, only the file is shared.
func processManager(srv *authServer, path string, password func() (string, error)) *TokenManager {
	return NewTokenManager(TokenManagerConfig{
		AuthBaseURL: srv.URL,
		DeviceID:    DeviceIDFor("user@example.com"),
		Store:       FileStore{Path: path},
		Password:    password,
	})
}

// Password sign-in is gated by risk control: the captcha token has to be
// minted first and sent with the grant, or the server refuses it.
func TestSignInMintsCaptchaFirst(t *testing.T) {
	srv := newAuthServer(t)
	m := newManager(t, srv, nil)

	tok, err := m.SignIn(context.Background(), "user@example.com", "hunter2")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if tok.AccessToken != "access-signin" || tok.UserID != "user-1" {
		t.Errorf("token = %+v", tok)
	}
	if tok.Username != "user@example.com" {
		t.Errorf("Username = %q, want the account", tok.Username)
	}
	if tok.DeviceID != DeviceIDFor("user@example.com") {
		t.Errorf("DeviceID = %q, want it derived from the account", tok.DeviceID)
	}

	captcha, signIns, _ := srv.counts()
	if captcha != 1 || signIns != 1 {
		t.Fatalf("captcha inits = %d, sign ins = %d, want 1 each", captcha, signIns)
	}

	srv.mu.Lock()
	body, meta := srv.lastSignIn, srv.lastCaptcha
	srv.mu.Unlock()

	if body["grant_type"] != "password" || body["username"] != "user@example.com" || body["password"] != "hunter2" {
		t.Errorf("sign-in body = %v", body)
	}
	if body["captcha_token"] != "captcha-1" {
		t.Errorf("captcha_token = %v, want the freshly minted one", body["captcha_token"])
	}
	// The secret belongs to the profile; sending the id without it is refused.
	if body["client_id"] != AndroidProfile().ClientID || body["client_secret"] != AndroidProfile().ClientSecret {
		t.Errorf("client identity = %v / %v", body["client_id"], body["client_secret"])
	}
	// The sign-in handshake identifies itself by account, not by signature:
	// there is no session yet to sign with.
	if meta["action"] != signInAction {
		t.Errorf("captcha action = %v, want %q", meta["action"], signInAction)
	}
	if nested, _ := meta["meta"].(map[string]any); nested["username"] != "user@example.com" {
		t.Errorf("captcha meta = %v", meta["meta"])
	}
}

// A dead refresh token must fall through to the password rung rather than
// surface as a failure the user cannot act on.
func TestDeadRefreshTokenFallsBackToPassword(t *testing.T) {
	srv := newAuthServer(t)
	srv.refreshIsDead = true

	m := newManager(t, srv, func() (string, error) { return "hunter2", nil })
	if err := m.Seed(Token{Username: "user@example.com", RefreshToken: "retired"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tok, err := m.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if tok.AccessToken != "access-signin" {
		t.Errorf("AccessToken = %q, want the one from the password sign-in", tok.AccessToken)
	}

	_, signIns, refreshes := srv.counts()
	if refreshes != 1 || signIns != 1 {
		t.Errorf("refreshes = %d, sign ins = %d, want 1 each", refreshes, signIns)
	}
}

// Without a password there is nothing to fall back to. The caller must be
// able to tell that apart from a generic failure, because the only recovery
// is to ask the user.
func TestDeadRefreshTokenWithoutPasswordAsksForOne(t *testing.T) {
	srv := newAuthServer(t)
	srv.refreshIsDead = true

	store := &MemoryStore{}
	m := NewTokenManager(TokenManagerConfig{
		AuthBaseURL: srv.URL,
		DeviceID:    DeviceIDFor("user@example.com"),
		Store:       store,
	})
	if err := m.Seed(Token{Username: "user@example.com", RefreshToken: "retired"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := m.Token(context.Background()); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("err = %v, want ErrPasswordRequired", err)
	}
	// The retired token must be gone from the store, or every cold start
	// replays it.
	saved, _ := store.Load()
	if saved.RefreshToken != "" {
		t.Errorf("retired refresh token survived in the store: %q", saved.RefreshToken)
	}
	if saved.Username != "user@example.com" {
		t.Errorf("Username = %q, want it kept so the UI can pre-fill", saved.Username)
	}
}

// A password the server refuses ends the session the same way: retrying it
// on every call would hammer the sign-in endpoint with a password known to
// be wrong. The refusal stays readable in the error.
func TestRefusedPasswordAsksForOne(t *testing.T) {
	srv := newAuthServer(t)
	srv.refreshIsDead = true
	srv.signInError = "password_not_match"

	m := NewTokenManager(TokenManagerConfig{
		AuthBaseURL: srv.URL,
		DeviceID:    DeviceIDFor("user@example.com"),
		Store:       &MemoryStore{},
		Password:    func() (string, error) { return "changed-elsewhere", nil },
	})
	if err := m.Seed(Token{Username: "user@example.com", RefreshToken: "retired"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := m.Token(context.Background())
	if !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("err = %v, want ErrPasswordRequired", err)
	}
	if !strings.Contains(err.Error(), "wrong password") {
		t.Errorf("error text dropped the server's reason: %v", err)
	}
}

// A refresh that fails on the wire says nothing about the token. Signing in
// over a network that just dropped a request fails the same way, while
// costing the user a password prompt.
func TestTransportFailureDoesNotPromptForPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	var passwordAsked atomic.Int64
	m := NewTokenManager(TokenManagerConfig{
		AuthBaseURL: srv.URL,
		DeviceID:    "d",
		Store:       &MemoryStore{},
		Password: func() (string, error) {
			passwordAsked.Add(1)
			return "hunter2", nil
		},
	})
	if err := m.Seed(Token{Username: "user@example.com", RefreshToken: "live"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := m.Token(context.Background()); err == nil {
		t.Fatal("expected the transport failure to surface")
	}
	if got := passwordAsked.Load(); got != 0 {
		t.Errorf("password supplier called %d times, want 0", got)
	}
}

func TestRefreshRotatesAndPersists(t *testing.T) {
	srv := newAuthServer(t)
	store := &MemoryStore{}
	m := NewTokenManager(TokenManagerConfig{
		AuthBaseURL: srv.URL,
		DeviceID:    "d",
		Store:       store,
	})
	if err := m.Seed(Token{Username: "user@example.com", RefreshToken: "seed-refresh"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := m.Token(context.Background()); err != nil {
		t.Fatalf("token: %v", err)
	}
	saved, _ := store.Load()
	if saved.RefreshToken != "refresh-rotated" {
		t.Errorf("stored refresh token = %q, want the rotated one", saved.RefreshToken)
	}

	if _, err := m.Token(context.Background()); err != nil {
		t.Fatalf("second token: %v", err)
	}
	if _, _, refreshes := srv.counts(); refreshes != 1 {
		t.Errorf("refreshes = %d, want the cached token reused", refreshes)
	}
}

// Every view fetches concurrently. If each expired caller minted its own
// token the auth server would see a burst on every expiry, which is exactly
// the pattern risk control looks for.
func TestConcurrentCallersRefreshOnce(t *testing.T) {
	srv := newAuthServer(t)
	m := NewTokenManager(TokenManagerConfig{AuthBaseURL: srv.URL, DeviceID: "d", Store: &MemoryStore{}})
	if err := m.Seed(Token{Username: "u", RefreshToken: "seed"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Token(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	if _, _, refreshes := srv.counts(); refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", refreshes)
	}
}

// Every build signed in through one config directory — the desktop app, the
// server build, a dev build — shares one credentials file, and the server
// retires a refresh token as soon as it is spent. A process renewing with the
// token it read at start-up is refused once another process has renewed; that
// refusal used to read as a dead session and wiped the file, so the next
// launch came up signed out.
func TestRenewalAdoptsWhatAnotherProcessStored(t *testing.T) {
	srv := newAuthServer(t)
	srv.liveRefresh = "refresh-0"
	path := filepath.Join(t.TempDir(), "credentials.json")

	desktop := processManager(srv, path, nil)
	if err := desktop.Seed(Token{Username: "user@example.com", RefreshToken: "refresh-0"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	server := processManager(srv, path, nil)
	if _, err := server.Current(); err != nil {
		t.Fatalf("start-up load: %v", err)
	}

	renewed, err := desktop.Token(context.Background())
	if err != nil {
		t.Fatalf("first process: %v", err)
	}
	got, err := server.Token(context.Background())
	if err != nil {
		t.Fatalf("second process: %v", err)
	}
	if got.AccessToken != renewed.AccessToken {
		t.Errorf("second process got %q, want the session the first one stored (%q)", got.AccessToken, renewed.AccessToken)
	}
	if _, _, refreshes := srv.counts(); refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", refreshes)
	}
	if stored, _ := (FileStore{Path: path}).Load(); stored.RefreshToken != renewed.RefreshToken {
		t.Errorf("stored refresh token = %q, want the live one %q", stored.RefreshToken, renewed.RefreshToken)
	}
}

// Processes whose access token runs out together all reach for the one
// refresh token. Exactly one may spend it; the rest wait and take its result.
func TestProcessesRenewingTogetherSpendTheRefreshTokenOnce(t *testing.T) {
	srv := newAuthServer(t)
	srv.liveRefresh = "refresh-0"
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := (FileStore{Path: path}).Save(Token{
		Username: "user@example.com", DeviceID: DeviceIDFor("user@example.com"), RefreshToken: "refresh-0",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	procs := make([]*TokenManager, 4)
	for i := range procs {
		procs[i] = processManager(srv, path, nil)
		if _, err := procs[i].Current(); err != nil {
			t.Fatalf("start-up load: %v", err)
		}
	}

	got := make([]Token, len(procs))
	errs := make([]error, len(procs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, m := range procs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got[i], errs[i] = m.Token(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("process %d: %v", i, err)
		}
	}
	if _, _, refreshes := srv.counts(); refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", refreshes)
	}
	for i := range got {
		if got[i].AccessToken != got[0].AccessToken {
			t.Errorf("process %d holds %q, process 0 holds %q", i, got[i].AccessToken, got[0].AccessToken)
		}
	}
	if stored, _ := (FileStore{Path: path}).Load(); stored.RefreshToken != got[0].RefreshToken {
		t.Errorf("stored refresh token = %q, want the live one %q", stored.RefreshToken, got[0].RefreshToken)
	}
}

// Signing out anywhere ends the session everywhere. A process still holding
// it must neither renew it nor sign in again: either would write it back for
// the next launch to pick up.
func TestSignOutInAnotherProcessIsNotUndone(t *testing.T) {
	srv := newAuthServer(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	password := func() (string, error) { return "hunter2", nil }

	other := processManager(srv, path, password)
	if err := other.Seed(Token{Username: "user@example.com", RefreshToken: "refresh-0"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := processManager(srv, path, password)
	if _, err := m.Current(); err != nil {
		t.Fatalf("start-up load: %v", err)
	}

	if err := other.Forget(); err != nil {
		t.Fatalf("sign out: %v", err)
	}

	if _, err := m.Token(context.Background()); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("err = %v, want ErrPasswordRequired", err)
	}
	if _, signIns, refreshes := srv.counts(); signIns != 0 || refreshes != 0 {
		t.Errorf("sign ins = %d, refreshes = %d, want neither", signIns, refreshes)
	}
	if stored, _ := (FileStore{Path: path}).Load(); stored != (Token{}) {
		t.Errorf("the signed-out session came back: %+v", stored)
	}
}

// A 401 from a request that was already in flight when someone else
// refreshed must not throw away the new token.
func TestInvalidateIgnoresStaleToken(t *testing.T) {
	m := NewTokenManager(TokenManagerConfig{DeviceID: "d", Store: &MemoryStore{}})
	if err := m.Seed(Token{AccessToken: "current", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m.Invalidate("superseded")
	if tok, _ := m.Current(); tok.AccessToken != "current" {
		t.Errorf("a stale 401 cleared the live token: %+v", tok)
	}

	m.Invalidate("current")
	if tok, _ := m.Current(); tok.AccessToken != "" {
		t.Errorf("token = %q, want it cleared", tok.AccessToken)
	}
}

// A 401 means the server no longer takes that access token, whatever its
// deadline says. Catching up with the store before renewing must not hand
// the same token back.
func TestRejectedAccessTokenIsNotTakenBackFromTheStore(t *testing.T) {
	srv := newAuthServer(t)
	m := NewTokenManager(TokenManagerConfig{AuthBaseURL: srv.URL, DeviceID: "d", Store: &MemoryStore{}})
	if err := m.Seed(Token{
		Username: "u", AccessToken: "revoked", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m.Invalidate("revoked")
	tok, err := m.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if tok.AccessToken == "revoked" {
		t.Fatal("the rejected access token was handed out again")
	}
}

func TestTokenWithoutCredentials(t *testing.T) {
	m := NewTokenManager(TokenManagerConfig{DeviceID: "d", Store: &MemoryStore{}})
	if _, err := m.Token(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("err = %v, want ErrNoCredentials", err)
	}
}

func TestWrongPasswordSurfacesServerError(t *testing.T) {
	srv := newAuthServer(t)
	srv.signInError = "password_not_match"

	m := newManager(t, srv, nil)
	_, err := m.SignIn(context.Background(), "user@example.com", "nope")
	apiErr := asAPIError(err)
	if apiErr == nil || apiErr.Name != "password_not_match" {
		t.Fatalf("err = %v, want the server's own error name", err)
	}
	if !strings.Contains(err.Error(), "wrong password") {
		t.Errorf("error text dropped the description: %v", err)
	}
}

// Risk control can demand a human. Retrying is pointless, so the failure has
// its own type and carries the page to open.
func TestHumanChallengeIsReported(t *testing.T) {
	srv := newAuthServer(t)
	srv.challengeURL = "https://user.mypikpak.com/verify?x=1"

	m := newManager(t, srv, nil)
	_, err := m.SignIn(context.Background(), "user@example.com", "hunter2")
	if !IsChallenge(err) {
		t.Fatalf("err = %v, want a ChallengeError", err)
	}
	if !strings.Contains(err.Error(), srv.challengeURL) {
		t.Errorf("error text lost the verification URL: %v", err)
	}
	if _, signIns, _ := srv.counts(); signIns != 0 {
		t.Errorf("sign ins = %d, want none once a challenge was raised", signIns)
	}
}

// The signature is a cascade: every salt is folded in, in order, over a seed
// built from the identity and the timestamp. Any change to the inputs has to
// change the digest, or the cascade is not actually consuming them.
func TestCaptchaSignatureShapeAndSensitivity(t *testing.T) {
	profile := AndroidProfile()
	const device = "0123456789abcdef0123456789abcdef"
	const stamp = int64(1760000000000)

	sign := signCaptcha(profile, device, stamp)
	if !regexp.MustCompile(`^1\.[0-9a-f]{32}$`).MatchString(sign) {
		t.Fatalf("sign = %q, want 1.<32 lowercase hex>", sign)
	}
	if again := signCaptcha(profile, device, stamp); again != sign {
		t.Error("signature is not deterministic")
	}

	for name, mutate := range map[string]func(*ClientProfile, *string, *int64){
		"device":  func(_ *ClientProfile, d *string, _ *int64) { *d = "ffffffffffffffffffffffffffffffff" },
		"stamp":   func(_ *ClientProfile, _ *string, s *int64) { *s = stamp + 1 },
		"version": func(p *ClientProfile, _ *string, _ *int64) { p.ClientVersion = "9.9.9" },
		"package": func(p *ClientProfile, _ *string, _ *int64) { p.PackageName = "com.example" },
		"salts":   func(p *ClientProfile, _ *string, _ *int64) { p.CaptchaSalts = []string{"a", "b"} },
	} {
		p, d, s := profile, device, stamp
		mutate(&p, &d, &s)
		if got := signCaptcha(p, d, s); got == sign {
			t.Errorf("changing the %s left the signature unchanged", name)
		}
	}

	// Salt order is part of the contract: the server folds them in the order
	// it handed out, so a reversed list must not collide.
	reversed := profile
	reversed.CaptchaSalts = make([]string, len(profile.CaptchaSalts))
	for i, salt := range profile.CaptchaSalts {
		reversed.CaptchaSalts[len(profile.CaptchaSalts)-1-i] = salt
	}
	if got := signCaptcha(reversed, device, stamp); got == sign {
		t.Error("salt order does not affect the signature")
	}
}

func TestDeviceIDIsDerivedAndStable(t *testing.T) {
	first := DeviceIDFor("user@example.com")
	if len(first) != 32 {
		t.Fatalf("device id = %q, want 32 hex characters", first)
	}
	if again := DeviceIDFor("user@example.com"); again != first {
		t.Error("the same account produced two device ids")
	}
	if other := DeviceIDFor("other@example.com"); other == first {
		t.Error("two accounts collided on one device id")
	}
}

func TestFileStoreRoundTripIsOwnerOnly(t *testing.T) {
	path := t.TempDir() + "/nested/credentials.json"
	store := FileStore{Path: path}

	// A missing file is a cold start, not an error.
	if tok, err := store.Load(); err != nil || tok.AccessToken != "" {
		t.Fatalf("cold load = %+v, %v", tok, err)
	}

	want := Token{
		Username: "user@example.com", DeviceID: "d", AccessToken: "a", RefreshToken: "r",
		UserID: "u", ExpiresAt: time.Now().Add(time.Hour).Round(time.Second),
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Username != want.Username || got.RefreshToken != want.RefreshToken || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials mode = %o, want 600", perm)
	}

	if err := store.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if tok, err := store.Load(); err != nil || tok.RefreshToken != "" {
		t.Errorf("after clear: %+v, %v", tok, err)
	}
}

// The lock is what keeps two processes from spending one refresh token: it
// must hold off a second taker, and a taker must be able to give up waiting.
func TestFileStoreLockExcludesAndCanBeAbandoned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "credentials.json")

	release, err := FileStore{Path: path}.Lock(context.Background())
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := (FileStore{Path: path}).Lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock while held: err = %v, want DeadlineExceeded", err)
	}

	release()
	again, err := FileStore{Path: path}.Lock(context.Background())
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}

// A password must never reach the disk; only the tokens derived from it do.
func TestStoredCredentialsCarryNoPassword(t *testing.T) {
	srv := newAuthServer(t)
	store := &MemoryStore{}
	m := NewTokenManager(TokenManagerConfig{
		AuthBaseURL: srv.URL,
		DeviceID:    DeviceIDFor("user@example.com"),
		Store:       store,
	})
	if _, err := m.SignIn(context.Background(), "user@example.com", "hunter2"); err != nil {
		t.Fatalf("sign in: %v", err)
	}

	saved, _ := store.Load()
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "hunter2") {
		t.Errorf("the password reached the credentials file: %s", encoded)
	}
}
