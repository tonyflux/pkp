package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// refreshLeeway is how long before the real expiry a token is treated as
// stale. One minute absorbs clock skew and a slow request without refreshing
// on every call.
const refreshLeeway = time.Minute

// storeLockRetry is how often a process waiting for the store lock looks
// again. Renewals are rare and short, so polling costs nothing and keeps the
// wait cancellable.
const storeLockRetry = 50 * time.Millisecond

// storeLockTimeout bounds the wait of the store writes that carry no context
// (Seed, Forget). A holder renews within one HTTP timeout, so this only runs
// out when the process holding the lock has hung.
const storeLockTimeout = time.Minute

// signInAction identifies the password sign-in to risk control. It names the
// production host even when AuthBaseURL points elsewhere, because the action
// is an opaque label the server matches, not a URL it resolves.
const signInAction = "POST:https://user.mypikpak.com/v1/auth/signin"

// refreshTokenInvalidCode is the envelope code for a refresh token the server
// has retired.
const refreshTokenInvalidCode = 4126

var (
	// ErrNoCredentials means nothing has been seeded yet: no stored session
	// and no account to sign in with.
	ErrNoCredentials = i18n.New("还没有登录账号")

	// ErrPasswordRequired means the stored session is dead and only the
	// account name survived. The user has to type the password again; there is
	// nothing to retry automatically.
	ErrPasswordRequired = i18n.New("登录已过期，请重新登录")
)

// Token is one session. ExpiresAt is absolute so a stored token survives
// process restarts without re-deriving a deadline from expires_in. ClientID
// and DeviceID keep refreshes bound to the identity that issued the session.
type Token struct {
	Username     string    `json:"username"`
	ClientID     string    `json:"client_id,omitempty"`
	DeviceID     string    `json:"device_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	UserID       string    `json:"user_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Usable reports whether the access token can still be sent.
func (t Token) Usable() bool {
	return t.AccessToken != "" && time.Now().Add(refreshLeeway).Before(t.ExpiresAt)
}

// TokenSource hands out access tokens and is told when one was rejected.
type TokenSource interface {
	Token(ctx context.Context) (Token, error)
	// Invalidate marks the given access token stale. The argument guards
	// against a late 401 from an in-flight request discarding a token that
	// was already refreshed by someone else.
	Invalidate(accessToken string)
}

// Store persists credentials between runs.
//
// One store can back several processes at once: every build signed in
// through the same config directory — the desktop app, the server build, a
// dev build — shares one credentials file. Whatever reads the stored session
// and writes its successor holds Lock for the whole cycle.
type Store interface {
	Load() (Token, error)
	Save(Token) error
	Clear() error
	// Lock waits until no other user of the store, in this process or
	// another, holds it, and returns the function that releases it.
	Lock(ctx context.Context) (release func(), err error)
}

// FileStore keeps credentials in a JSON file with owner-only permissions.
type FileStore struct {
	Path string
}

// Load returns a zero Token when the file does not exist yet.
func (s FileStore) Load() (Token, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Token{}, nil
	}
	if err != nil {
		return Token{}, err
	}
	var tok Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return Token{}, fmt.Errorf("drive: parse %s: %w", s.Path, err)
	}
	return tok, nil
}

// Save writes atomically: a half-written credentials file would lock the user
// out until they deleted it by hand.
func (s FileStore) Save(tok Token) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// Clear removes the stored credentials.
func (s FileStore) Clear() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Lock holds an advisory lock on a sibling of the credentials file. The
// credentials file itself is replaced on every Save, so a lock on it would
// stay behind with the old copy; the sibling is never replaced or removed.
// The OS releases the lock when its holder exits, so a crash cannot wedge the
// other processes.
func (s FileStore) Lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.Path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		locked, err := tryLockFile(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("drive: lock %s: %w", f.Name(), err)
		}
		if locked {
			return func() {
				_ = unlockFile(f)
				f.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(storeLockRetry):
		}
	}
}

// MemoryStore keeps credentials for the lifetime of the process. Managers
// sharing one behave like processes sharing a credentials file.
type MemoryStore struct {
	mu   sync.Mutex
	tok  Token
	once sync.Once
	held chan struct{}
}

func (s *MemoryStore) Load() (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok, nil
}

func (s *MemoryStore) Save(tok Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tok = tok
	return nil
}

func (s *MemoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tok = Token{}
	return nil
}

func (s *MemoryStore) Lock(ctx context.Context) (func(), error) {
	s.once.Do(func() { s.held = make(chan struct{}, 1) })
	select {
	case s.held <- struct{}{}:
		return func() { <-s.held }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TokenManagerConfig wires a TokenManager.
type TokenManagerConfig struct {
	// AuthBaseURL is the authorisation host, e.g. https://user.mypikpak.com.
	AuthBaseURL string
	// Profile is the issuing client's identity for authentication and captcha.
	// Public web clients have no ClientSecret.
	Profile ClientProfile
	// DeviceID must stay stable for an account; DeviceIDFor derives one.
	DeviceID string
	HTTP     *http.Client
	Store    Store
	// Password supplies the account password when the refresh ladder runs out.
	// Leaving it nil turns that rung into ErrPasswordRequired, which is the
	// right default: a desktop client keeps a password only when the user
	// asks it to, and then in the system keychain, not on disk.
	Password func() (string, error)
}

// TokenManager keeps one account's session alive.
//
// Every path runs under one mutex. Concurrent callers that find an expired
// token all wait for the same exchange instead of racing to mint several,
// which the server would read as a burst of device activity. Renewals also
// hold the store lock, which does the same for the other processes sharing
// the store.
type TokenManager struct {
	cfg     TokenManagerConfig
	captcha *captchaManager

	mu     sync.Mutex
	token  Token
	loaded bool
	// rejected is the last access token the API refused. The store can still
	// hold it with a deadline in the future; it must not be adopted again.
	rejected string
}

// NewTokenManager returns a manager for one account.
func NewTokenManager(cfg TokenManagerConfig) *TokenManager {
	if cfg.AuthBaseURL == "" {
		cfg.AuthBaseURL = DefaultAuthBaseURL
	}
	if !cfg.Profile.Valid() {
		cfg.Profile = AndroidProfile()
	}
	if cfg.HTTP == nil {
		// The token refreshes and the captcha exchanges have to reach the
		// same hosts the API does, so they follow the proxy setting too.
		cfg.HTTP = &http.Client{Timeout: 30 * time.Second, Transport: netproxy.NewTransport()}
	}

	m := &TokenManager{cfg: cfg}
	m.captcha = newCaptchaManager(cfg.Profile, cfg.DeviceID, cfg.AuthBaseURL, cfg.HTTP, m.currentUserID)
	return m
}

// Captcha exposes the shared captcha token so the API client sends and
// refreshes the same one the auth exchanges minted.
func (m *TokenManager) Captcha() CaptchaSource { return m.captcha }

// DeviceID is the identifier this manager presents.
func (m *TokenManager) DeviceID() string { return m.cfg.DeviceID }

// Profile is the immutable client identity used for this session's requests.
func (m *TokenManager) Profile() ClientProfile { return m.cfg.Profile }

func (m *TokenManager) currentUserID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token.UserID
}

// Seed installs credentials obtained elsewhere — a refresh token the user
// pasted, or a session restored from another tool — and persists them.
func (m *TokenManager) Seed(tok Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tok.ClientID != "" && tok.ClientID != m.cfg.Profile.ClientID {
		return errors.New("drive: token belongs to a different client")
	}
	tok.ClientID = m.cfg.Profile.ClientID
	ctx, cancel := context.WithTimeout(context.Background(), storeLockTimeout)
	defer cancel()
	release, err := m.lockStore(ctx)
	if err != nil {
		return err
	}
	defer release()

	if tok.DeviceID == "" {
		tok.DeviceID = m.cfg.DeviceID
	}
	m.token = tok
	m.loaded = true
	return m.persistLocked()
}

// Persist moves a session signed in on a manager without a store onto store
// and writes it there; password becomes the password rung's source. A
// sign-in can so be checked first — whose account it is — and only then
// kept, with the captcha token it was granted. A nil store keeps the session
// in memory, as a manager built without one does.
func (m *TokenManager) Persist(store Store, password func() (string, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), storeLockTimeout)
		defer cancel()
		release, err := store.Lock(ctx)
		if err != nil {
			return err
		}
		defer release()
		if err := store.Save(m.token); err != nil {
			return err
		}
	}
	m.cfg.Store = store
	m.cfg.Password = password
	m.loaded = true
	return nil
}

// Current returns the cached credentials without contacting the server.
func (m *TokenManager) Current() (Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(); err != nil {
		return Token{}, err
	}
	return m.token, nil
}

// Forget drops the session from memory and from the store, which ends it for
// every process sharing the store.
func (m *TokenManager) Forget() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = Token{}
	m.loaded = true
	if m.cfg.Store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeLockTimeout)
	defer cancel()
	release, err := m.lockStore(ctx)
	if err != nil {
		return err
	}
	defer release()
	return m.cfg.Store.Clear()
}

// SignIn exchanges an account and password for a session. It mints a captcha
// token first: risk control gates the sign-in endpoint itself.
func (m *TokenManager) SignIn(ctx context.Context, username, password string) (Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	release, err := m.lockStore(ctx)
	if err != nil {
		return Token{}, err
	}
	defer release()
	return m.signInLocked(ctx, username, password)
}

// Token returns a usable access token, climbing the cheapest ladder that can
// produce one: the cached session, the session another process sharing the
// store has renewed since, a refresh, then a password sign-in.
//
// Everything past the cached session holds the store lock. A refresh spends
// the refresh token — the server retires it and issues a successor — so two
// processes refreshing from one stored token get one of them refused. With
// the lock held and the store re-read first, the first process to get there
// renews and the others adopt what it stored.
func (m *TokenManager) Token(ctx context.Context) (Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.loadLocked(); err != nil {
		return Token{}, err
	}
	if m.token.ClientID != "" && m.token.ClientID != m.cfg.Profile.ClientID {
		return Token{}, ErrPasswordRequired
	}
	if m.token.Usable() {
		return m.token, nil
	}

	release, err := m.lockStore(ctx)
	if err != nil {
		return Token{}, err
	}
	defer release()
	if !m.reloadLocked() {
		return Token{}, ErrPasswordRequired
	}
	if m.token.Usable() {
		return m.token, nil
	}

	if m.token.RefreshToken != "" {
		switch err := m.refreshLocked(ctx); {
		case err == nil:
			return m.token, nil
		case isRefreshTokenDead(err):
			// The token is retired. Drop it before attempting the password
			// rung: a sign-in that then fails would otherwise leave a store
			// that replays the same dead token on every start.
			m.token.AccessToken = ""
			m.token.RefreshToken = ""
			if err := m.persistLocked(); err != nil {
				return Token{}, err
			}
		default:
			// A refresh that failed on the wire says nothing about the token.
			// Signing in over a network that just dropped a request fails the
			// same way while costing the user a password prompt.
			return Token{}, err
		}
	}

	if m.token.Username == "" {
		return Token{}, ErrNoCredentials
	}
	if m.cfg.Password == nil {
		return Token{}, ErrPasswordRequired
	}
	password, err := m.cfg.Password()
	if err != nil {
		return Token{}, err
	}
	if password == "" {
		return Token{}, ErrPasswordRequired
	}
	tok, err := m.signInLocked(ctx, m.token.Username, password)
	if err != nil && isSignInRefused(err) {
		// The password on hand was turned down. Only the user can supply a
		// better one, and trying this one again on every call would look
		// like an attack to risk control.
		return Token{}, fmt.Errorf("%w：%w", ErrPasswordRequired, err)
	}
	return tok, err
}

// isSignInRefused reports a sign-in the server turned down for good — the
// password, the captcha or a demand for human verification — as opposed to
// one a retry could get through: a dropped connection, a 5xx, throttling.
func isSignInRefused(err error) bool {
	if IsChallenge(err) {
		return true
	}
	apiErr := asAPIError(err)
	return apiErr != nil && apiErr.Status >= 400 && apiErr.Status < 500 && apiErr.Status != http.StatusTooManyRequests
}

// Invalidate drops an access token the server rejected, so the next Token
// call renews. A stale 401 naming an already-replaced token is ignored.
func (m *TokenManager) Invalidate(accessToken string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accessToken == "" || m.token.AccessToken != accessToken {
		return
	}
	m.rejected = accessToken
	m.token.AccessToken = ""
	m.token.ExpiresAt = time.Time{}
}

func (m *TokenManager) loadLocked() error {
	if m.loaded || m.cfg.Store == nil {
		m.loaded = true
		return nil
	}
	tok, err := m.cfg.Store.Load()
	if err != nil {
		return err
	}
	m.token = tok
	m.loaded = true
	return nil
}

// reloadLocked catches up with the store before a renewal. The store is the
// session's record for every process sharing it, and since this one last
// looked another may have renewed the session (spending the refresh token
// held here), signed in again, or signed out.
//
// It reports false when the store no longer holds a session of this account:
// it was signed out, or another account signed in over it. The session ends
// here too, and neither a refresh nor the password rung may run, since either
// would write it back.
func (m *TokenManager) reloadLocked() bool {
	if m.cfg.Store == nil {
		return true
	}
	stored, err := m.cfg.Store.Load()
	if err != nil {
		// Unreadable is not signed out: carry on with the session in memory,
		// and the next save replaces the damaged file.
		return true
	}
	if stored.DeviceID != m.cfg.DeviceID || (stored.ClientID != "" && stored.ClientID != m.cfg.Profile.ClientID) {
		if m.token.Username == "" && m.token.RefreshToken == "" {
			return true // never signed in here: nothing to end
		}
		m.token = Token{Username: m.token.Username, DeviceID: m.cfg.DeviceID}
		return false
	}
	if stored.AccessToken != "" && stored.AccessToken == m.rejected {
		stored.AccessToken = ""
		stored.ExpiresAt = time.Time{}
	}
	m.token = stored
	return true
}

// lockStore holds the store against every other user of it until the
// returned function runs. A manager without a store has nobody to exclude.
func (m *TokenManager) lockStore(ctx context.Context) (func(), error) {
	if m.cfg.Store == nil {
		return func() {}, nil
	}
	return m.cfg.Store.Lock(ctx)
}

func (m *TokenManager) persistLocked() error {
	if m.cfg.Store == nil {
		return nil
	}
	return m.cfg.Store.Save(m.token)
}

type tokenResponse struct {
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Sub          string `json:"sub"`
}

func (m *TokenManager) signInLocked(ctx context.Context, username, password string) (Token, error) {
	if username == "" || password == "" {
		return Token{}, errors.New("drive: account and password are required")
	}

	captchaToken, err := m.captcha.initForSignIn(ctx, username)
	if err != nil {
		return Token{}, err
	}

	parsed, err := m.exchange(ctx, "/v1/auth/signin", map[string]string{
		"client_id":     m.cfg.Profile.ClientID,
		"client_secret": m.cfg.Profile.ClientSecret,
		"grant_type":    "password",
		"username":      username,
		"password":      password,
		"captcha_token": captchaToken,
	})
	if err != nil {
		return Token{}, err
	}

	m.token.Username = username
	// The sign-in overwrites whatever the store held; a later load must not
	// replace this session with the stale one.
	m.loaded = true
	m.token.DeviceID = m.cfg.DeviceID
	m.token.ClientID = m.cfg.Profile.ClientID
	m.applyLocked(parsed)
	if err := m.persistLocked(); err != nil {
		return Token{}, err
	}
	return m.token, nil
}

func (m *TokenManager) refreshLocked(ctx context.Context) error {
	parsed, err := m.exchange(ctx, "/v1/auth/token", map[string]string{
		"client_id":     m.cfg.Profile.ClientID,
		"client_secret": m.cfg.Profile.ClientSecret,
		"grant_type":    "refresh_token",
		"refresh_token": m.token.RefreshToken,
	})
	if err != nil {
		return err
	}
	m.applyLocked(parsed)
	return m.persistLocked()
}

// applyLocked folds an exchange result into the live session. A rotated
// refresh token has to be kept, or the next refresh replays one the server
// already retired.
func (m *TokenManager) applyLocked(parsed tokenResponse) {
	lifetime := time.Duration(parsed.ExpiresIn) * time.Second
	if lifetime <= 0 {
		// Treat a missing lifetime as short rather than infinite: refreshing
		// too often is cheap, serving a dead token is not.
		lifetime = 30 * time.Minute
	}

	m.token.AccessToken = parsed.AccessToken
	m.token.TokenType = parsed.TokenType
	m.token.ExpiresAt = time.Now().Add(lifetime)
	if parsed.RefreshToken != "" {
		m.token.RefreshToken = parsed.RefreshToken
	}
	if parsed.Sub != "" {
		m.token.UserID = parsed.Sub
	}
	if m.token.DeviceID == "" {
		m.token.DeviceID = m.cfg.DeviceID
	}
}

func (m *TokenManager) exchange(ctx context.Context, path string, body map[string]string) (tokenResponse, error) {
	// The public web client has no client secret.
	if m.cfg.Profile.ClientSecret == "" {
		delete(body, "client_secret")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return tokenResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.AuthBaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return tokenResponse{}, err
	}
	m.cfg.Profile.setHeaders(req.Header, m.cfg.DeviceID)
	req.Header.Set("Content-Type", "application/json")
	if token := m.captcha.Token(); token != "" {
		req.Header.Set("X-Captcha-Token", token)
	}

	resp, err := m.cfg.HTTP.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("drive: %s: %w", path, err)
	}
	raw, _, err := decodeResponse(http.MethodPost, path, resp)
	if err != nil {
		return tokenResponse{}, err
	}

	var parsed tokenResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return tokenResponse{}, fmt.Errorf("drive: decode %s response: %w", path, err)
	}
	if parsed.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("drive: %s returned no access_token", path)
	}
	return parsed, nil
}

// isRefreshTokenDead reports whether the server retired the refresh token, as
// opposed to failing for a reason a retry could survive.
func isRefreshTokenDead(err error) bool {
	apiErr := asAPIError(err)
	if apiErr == nil {
		return false
	}
	return apiErr.Code == refreshTokenInvalidCode ||
		apiErr.Name == "invalid_grant" ||
		apiErr.Status == http.StatusUnauthorized
}
