package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
)

type qrAccountsAPI struct {
	*accountsAPI
	mu                         sync.Mutex
	login, state               string
	polls, refreshes, sequence int
	devices                    map[string]string
	refreshTokens              map[string]string
	entered                    chan struct{}
	release                    chan struct{}
}

func newQRAccountsAPI(t *testing.T) *qrAccountsAPI {
	t.Helper()
	api := newAccountsAPI(t)
	q := &qrAccountsAPI{accountsAPI: api, login: "qr@example.com", state: "complete", devices: map[string]string{}, refreshTokens: map[string]string{}}
	api.add(q.login, "unused-password", "qr-user")
	api.Config.Handler = http.HandlerFunc(q.serveQR)
	return q
}

func (q *qrAccountsAPI) serveQR(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/auth/device/code" && r.URL.Path != "/v1/auth/token" {
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer web-a:") {
			if r.Header.Get("X-Client-Id") != "YUMx5nI8ZU8Ap8pm" || r.Header.Get("X-Client-Version") != "2.0.0" || r.Header.Get("X-Client-Version-Code") != "" || strings.Contains(r.UserAgent(), "ANDROID-") {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprint(w, `{"error":"invalid_client"}`)
				return
			}
			r.Header.Set("Authorization", "Bearer a:"+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer web-a:"))
		}
		q.accountsAPI.serve(w, r)
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body["grant_type"] == "refresh_token" && strings.HasPrefix(body["refresh_token"], "r:") {
		profile := drive.AndroidProfile()
		if body["client_id"] != profile.ClientID || r.Header.Get("X-Client-Id") != profile.ClientID || body["client_secret"] != profile.ClientSecret {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"invalid_client"}`)
			return
		}
		raw, _ := json.Marshal(body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		q.accountsAPI.serve(w, r)
		return
	}
	_, hasSecret := body["client_secret"]
	if body["client_id"] != "YUMx5nI8ZU8Ap8pm" || r.Header.Get("X-Client-Id") != body["client_id"] || hasSecret {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"invalid_client"}`)
		return
	}
	q.mu.Lock()
	if r.URL.Path == "/v1/auth/device/code" {
		q.sequence++
		code := fmt.Sprintf("private-device-%d", q.sequence)
		q.devices[code] = r.Header.Get("X-Device-Id")
		q.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"device_code": code, "verification_uri_complete": "https://mypikpak.com/drive/qr-login?user_code=public-display", "expires_in": 300, "interval": 2})
		return
	}
	if body["grant_type"] == "refresh_token" {
		login, found := q.refreshTokens[body["refresh_token"]]
		if !found {
			q.mu.Unlock()
			w.WriteHeader(400)
			_, _ = fmt.Fprint(w, `{"error":"invalid_grant","error_code":4126}`)
			return
		}
		delete(q.refreshTokens, body["refresh_token"])
		q.refreshes++
		token := fmt.Sprintf("qr-refresh-%d", q.refreshes)
		q.refreshTokens[token] = login
		q.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "web-a:" + login, "refresh_token": token, "expires_in": 7200, "token_type": "Bearer"})
		return
	}
	q.polls++
	login, state, entered, release := q.login, q.state, q.entered, q.release
	device := q.devices[body["device_code"]]
	q.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
		<-release
	}
	if device != r.Header.Get("X-Device-Id") || device == "" {
		w.WriteHeader(400)
		_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
		return
	}
	if state == "network" {
		w.WriteHeader(503)
		return
	}
	if state != "complete" {
		name := state
		if state == "waiting" || state == "scanned" {
			name = "authorization_pending"
		}
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": name, "error_code": 4050, "details": []map[string]string{{"state": map[bool]string{true: "WAITING_CONSENT", false: "WAITING_CONNECT"}[state == "scanned"]}}})
		return
	}
	q.accountsAPI.mu.Lock()
	sub := q.accountsAPI.subs[login]
	q.accountsAPI.mu.Unlock()
	q.mu.Lock()
	q.refreshTokens["qr-refresh-0"] = login
	q.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "web-a:" + login, "refresh_token": "qr-refresh-0", "sub": sub, "expires_in": 7200, "token_type": "Bearer"})
}

func beginQR(t *testing.T, svc *DriveService, req QRSignInRequest) QRSignInState {
	t.Helper()
	state, err := svc.BeginQRSignIn(req)
	if err != nil || state.State != "waiting" {
		t.Fatalf("begin QR: %+v %v", state, err)
	}
	return state
}

// Advance only the local scheduling deadline; no real-time sleeps or network
// retries are needed to exercise consecutive server state transitions.
func readyQR(t *testing.T, svc *DriveService, id string) *qrSignInSession {
	t.Helper()
	svc.signInMu.Lock()
	a := svc.signIn
	svc.signInMu.Unlock()
	if a == nil || a.qr == nil || a.qr.state.ID != id {
		t.Fatal("QR attempt not current")
	}
	a.qr.mu.Lock()
	a.qr.nextPoll = time.Time{}
	a.qr.mu.Unlock()
	return a.qr
}

func pollQR(t *testing.T, svc *DriveService, id, want string) QRSignInState {
	t.Helper()
	readyQR(t, svc, id)
	state, err := svc.PollQRSignIn(id)
	if err != nil || state.State != want {
		t.Fatalf("poll QR: %+v %v, want %s", state, err, want)
	}
	return state
}

func TestQRSignInRestoresAndRotatesWithoutPassword(t *testing.T) {
	api := newQRAccountsAPI(t)
	dir, vault := t.TempDir(), &memoryVault{}
	svc := startAccounts(t, dir, api.accountsAPI, vault)
	qr := beginQR(t, svc, QRSignInRequest{Label: " QR collection "})
	if svc.Status().Configured || len(svc.Accounts()) != 0 {
		t.Fatal("begin committed an account")
	}
	state := pollQR(t, svc, qr.ID, "complete")
	if state.Result == nil || state.Result.Account.ID == "" || state.Result.Account.Label != "QR collection" || !state.Result.Status.Configured {
		t.Fatalf("incomplete result: %+v", state)
	}
	encoded, _ := json.Marshal(state)
	for _, secret := range []string{"private-device-", "qr-refresh-", "a:qr@"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("QR state leaked %s", secret)
		}
	}
	if len(vault.passwords) != 0 {
		t.Fatal("QR sign-in wrote a password")
	}
	id := state.Result.Account.ID
	for range 2 {
		s := svc.byKey(id)
		token, err := s.tokens.Current()
		if err != nil {
			t.Fatal(err)
		}
		token.ExpiresAt = time.Now().Add(-time.Hour)
		if err := s.tokens.Seed(token); err != nil {
			t.Fatal(err)
		}
		svc = startAccounts(t, dir, api.accountsAPI, vault)
		if _, err := svc.Quota(); err != nil {
			t.Fatalf("restored QR session refresh: %v", err)
		}
	}
	api.mu.Lock()
	refreshes := api.refreshes
	clear(api.refreshTokens)
	api.mu.Unlock()
	if refreshes != 2 {
		t.Fatalf("refresh rotation count = %d", refreshes)
	}
	s := svc.byKey(id)
	token, _ := s.tokens.Current()
	token.ExpiresAt = time.Now().Add(-time.Hour)
	if err := s.tokens.Seed(token); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Quota()
	if !errors.Is(err, drive.ErrNoCredentials) && !errors.Is(err, drive.ErrPasswordRequired) {
		t.Fatalf("revoked refresh did not require sign-in: %v", err)
	}
	if !svc.Status().NeedsPassword || svc.Status().Configured {
		t.Fatal("expired QR account was not parked for reauthentication")
	}
}

func TestQRSignInPreservesExistingAccountAndPassword(t *testing.T) {
	api := newQRAccountsAPI(t)
	api.accountsAPI.add("other@example.com", "other-pw", "other-user")
	dir, vault := t.TempDir(), &memoryVault{}
	svc := startAccounts(t, dir, api.accountsAPI, vault)
	original := signIn(t, svc, SignInRequest{Login: api.login, Password: "unused-password", SignInOptions: SignInOptions{Remember: true, AutoSignIn: true}})
	before, _ := os.ReadFile(filepath.Join(dir, "credentials-"+original.Account.ID+".json"))
	api.mu.Lock()
	api.login = "other@example.com"
	api.mu.Unlock()
	qr := beginQR(t, svc, QRSignInRequest{Account: original.Account.ID})
	bad := pollQR(t, svc, qr.ID, "error")
	after, _ := os.ReadFile(filepath.Join(dir, "credentials-"+original.Account.ID+".json"))
	if !bytes.Equal(before, after) || len(svc.Accounts()) != 1 || svc.Status().AccountID != original.Account.ID || bad.Result != nil {
		t.Fatal("wrong QR account changed the target")
	}
	api.mu.Lock()
	api.login = "qr@example.com"
	api.mu.Unlock()
	qr = beginQR(t, svc, QRSignInRequest{Account: original.Account.ID})
	res := pollQR(t, svc, qr.ID, "complete").Result
	if res == nil || !res.Existed || res.Account.ID != original.Account.ID {
		t.Fatal("QR reauthentication did not reuse the account")
	}
	password, _ := vault.Password("qr@example.com")
	if password != "unused-password" || !svc.byKey(original.Account.ID).record().AutoSignIn {
		t.Fatal("QR sign-in changed remembered password or choices")
	}
	if _, err := svc.Quota(); err != nil {
		t.Fatalf("reauthenticated web session could not access its drive: %v", err)
	}
	originalQueue := svc.byKey(original.Account.ID).queue()
	qr = beginQR(t, svc, QRSignInRequest{})
	res = pollQR(t, svc, qr.ID, "complete").Result
	if res == nil || res.Account.ID != original.Account.ID || len(svc.Accounts()) != 1 {
		t.Fatal("duplicate QR sign-in created another account")
	}
	if svc.byKey(original.Account.ID).queue() != originalQueue {
		t.Fatal("duplicate QR login interrupted the live transfer queue")
	}
}

func TestQRSignInStatesSchedulingAndExpiry(t *testing.T) {
	api := newQRAccountsAPI(t)
	svc := startAccounts(t, t.TempDir(), api.accountsAPI, &memoryVault{})
	qr := beginQR(t, svc, QRSignInRequest{})
	if _, err := svc.PollQRSignIn(qr.ID); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	polls := api.polls
	api.mu.Unlock()
	if polls != 0 {
		t.Fatal("polled before server interval")
	}
	for _, state := range []string{"waiting", "scanned", "network", "slow_down"} {
		api.mu.Lock()
		api.state = state
		api.mu.Unlock()
		want := state
		if state == "slow_down" {
			want = "scanned"
		}
		res := pollQR(t, svc, qr.ID, want)
		if state == "slow_down" && res.Interval != 7 {
			t.Fatalf("slow-down interval = %d", res.Interval)
		}
	}
	q := readyQR(t, svc, qr.ID)
	q.mu.Lock()
	q.deadline = time.Now().Add(-time.Second)
	q.mu.Unlock()
	state, err := svc.PollQRSignIn(qr.ID)
	if err != nil || state.State != "expired" || len(svc.Accounts()) != 0 {
		t.Fatalf("expiry committed state: %+v %v", state, err)
	}
	for _, terminal := range []string{"access_denied", "expired_token"} {
		api.mu.Lock()
		api.state = terminal
		api.mu.Unlock()
		qr = beginQR(t, svc, QRSignInRequest{})
		want := map[string]string{"access_denied": "denied", "expired_token": "expired"}[terminal]
		pollQR(t, svc, qr.ID, want)
	}
}

func TestQRSignInCancellationRejectsLateAndStaleResults(t *testing.T) {
	api := newQRAccountsAPI(t)
	svc := startAccounts(t, t.TempDir(), api.accountsAPI, &memoryVault{})
	old := beginQR(t, svc, QRSignInRequest{})
	current := beginQR(t, svc, QRSignInRequest{})
	svc.CancelQRSignIn(old.ID)
	state, err := svc.PollQRSignIn(old.ID)
	if err != nil || state.State != "cancelled" {
		t.Fatal("superseded QR attempt remained active")
	}
	api.mu.Lock()
	api.entered = make(chan struct{}, 1)
	api.release = make(chan struct{})
	entered, release := api.entered, api.release
	api.mu.Unlock()
	readyQR(t, svc, current.ID)
	done := make(chan QRSignInState, 1)
	go func() { res, _ := svc.PollQRSignIn(current.ID); done <- res }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not start")
	}
	svc.CancelQRSignIn(current.ID)
	close(release)
	select {
	case res := <-done:
		if res.State != "cancelled" {
			t.Fatalf("late authorization state=%s", res.State)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop poll")
	}
	if len(svc.Accounts()) != 0 {
		t.Fatal("cancelled QR login created an account")
	}
	api.mu.Lock()
	api.entered = nil
	api.release = nil
	api.mu.Unlock()
	current = beginQR(t, svc, QRSignInRequest{})
	signIn(t, svc, SignInRequest{Login: api.login, Password: "unused-password"})
	state, err = svc.PollQRSignIn(current.ID)
	if err != nil || state.State != "cancelled" {
		t.Fatal("password sign-in did not cancel QR")
	}
	if len(svc.Accounts()) != 1 {
		t.Fatal("cancelled QR changed account count")
	}
}

func TestQRSignInAccountListFailureDoesNotCommit(t *testing.T) {
	api := newQRAccountsAPI(t)
	dir := t.TempDir()
	svc := startAccounts(t, dir, api.accountsAPI, &memoryVault{})
	qr := beginQR(t, svc, QRSignInRequest{})
	if err := os.Mkdir(svc.accountsPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	state := pollQR(t, svc, qr.ID, "error")
	if state.Result != nil || len(svc.Accounts()) != 0 || svc.Status().Configured {
		t.Fatal("failed account-list write committed a QR account")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "credentials-*.json"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("orphaned QR credentials: %v, %v", paths, err)
	}
}

func TestQRSignInRestoresIssuingDeviceFromCredentials(t *testing.T) {
	api := newQRAccountsAPI(t)
	dir := t.TempDir()
	svc := startAccounts(t, dir, api.accountsAPI, &memoryVault{})
	qr := beginQR(t, svc, QRSignInRequest{})
	result := pollQR(t, svc, qr.ID, "complete").Result
	s := svc.byKey(result.Account.ID)
	token, err := s.tokens.Current()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate credentials being replaced before the separate account-list write.
	token.DeviceID = "new-issuing-device"
	if err := s.tokens.Seed(token); err != nil {
		t.Fatal(err)
	}
	restored := startAccounts(t, dir, api.accountsAPI, &memoryVault{})
	if got := restored.Status().DeviceID; got != token.DeviceID {
		t.Fatalf("restored device = %q, want issuing device %q", got, token.DeviceID)
	}
}

func TestQRSignInCanReturnToPasswordAndRestoreLegacyCredentials(t *testing.T) {
	api := newQRAccountsAPI(t)
	dir, vault := t.TempDir(), &memoryVault{}
	svc := startAccounts(t, dir, api.accountsAPI, vault)
	qr := beginQR(t, svc, QRSignInRequest{})
	original := pollQR(t, svc, qr.ID, "complete").Result
	result := signIn(t, svc, SignInRequest{Account: original.Account.ID, Login: api.login, Password: "unused-password"})
	if result.Account.ID != original.Account.ID || len(svc.Accounts()) != 1 {
		t.Fatal("password sign-in replaced the account instead of its web session")
	}
	store := svc.credentialsStore(result.Account.ID)
	token, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Older credentials have no client id; password sessions must still restore.
	token.ClientID = ""
	token.ExpiresAt = time.Now().Add(-time.Hour)
	if err := store.Save(token); err != nil {
		t.Fatal(err)
	}
	restored := startAccounts(t, dir, api.accountsAPI, vault)
	if _, err := restored.Quota(); err != nil {
		t.Fatalf("password session restored with the wrong client: %v", err)
	}
}

func TestQRExistingAccountPersistenceFailureKeepsSession(t *testing.T) {
	for _, mode := range []string{"android-to-web", "web-reauth", "duplicate-live", "parked"} {
		for _, failure := range []string{"accounts", "credentials"} {
			if mode == "duplicate-live" && failure == "credentials" {
				continue // Reusing a live grant does not rewrite credentials.
			}
			t.Run(mode+"/"+failure, func(t *testing.T) {
				api := newQRAccountsAPI(t)
				api.accountsAPI.add("other@example.com", "other-pw", "other-user")
				dir := t.TempDir()
				svc := startAccounts(t, dir, api.accountsAPI, &memoryVault{})
				original := signIn(t, svc, SignInRequest{Login: api.login, Password: "unused-password"})
				if mode != "android-to-web" {
					qr := beginQR(t, svc, QRSignInRequest{Account: original.Account.ID})
					pollQR(t, svc, qr.ID, "complete")
				}
				s := svc.byKey(original.Account.ID)
				if mode == "parked" {
					s.mu.Lock()
					s.client, s.needsPassword = nil, true
					s.mu.Unlock()
				}
				other := signIn(t, svc, SignInRequest{Login: "other@example.com", Password: "other-pw"})
				beforeRecord := s.record()
				beforeToken, err := s.tokens.Current()
				if err != nil {
					t.Fatal(err)
				}
				beforeTokens, beforeClient := s.tokens, s.client
				beforeUploads, beforeDownloads := s.uploads, s.downloads
				beforeParked := s.needsPassword
				credentials := svc.credentialsPath(s.key)
				beforeCredentials, err := os.ReadFile(credentials)
				if err != nil {
					t.Fatal(err)
				}
				beforeAccounts, err := os.ReadFile(svc.accountsPath())
				if err != nil {
					t.Fatal(err)
				}
				request := QRSignInRequest{Account: s.key, Label: "must not persist"}
				if mode == "duplicate-live" {
					request.Account = ""
				}
				qr := beginQR(t, svc, request)
				blocked := svc.accountsPath() + ".tmp"
				if failure == "credentials" {
					blocked = credentials
					if err := os.Rename(credentials, credentials+".before"); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Mkdir(blocked, 0700); err != nil {
					t.Fatal(err)
				}
				state := pollQR(t, svc, qr.ID, "error")
				if state.Result != nil || svc.Status().AccountID != other.Account.ID || s.record() != beforeRecord || s.tokens != beforeTokens || s.client != beforeClient || s.uploads != beforeUploads || s.downloads != beforeDownloads || s.needsPassword != beforeParked {
					t.Fatal("failed QR commit changed live account, current selection or queues")
				}
				if token, err := s.tokens.Current(); err != nil || token != beforeToken {
					t.Fatal("failed QR commit replaced live credentials")
				}
				afterAccounts, err := os.ReadFile(svc.accountsPath())
				if err != nil || !bytes.Equal(beforeAccounts, afterAccounts) {
					t.Fatal("failed QR commit changed account list on disk")
				}
				if err := os.Remove(blocked); err != nil {
					t.Fatal(err)
				}
				if failure == "credentials" {
					if err := os.Rename(credentials+".before", credentials); err != nil {
						t.Fatal(err)
					}
				}
				afterCredentials, err := os.ReadFile(credentials)
				if err != nil || !bytes.Equal(beforeCredentials, afterCredentials) {
					t.Fatal("failed QR commit changed stored credentials")
				}
				restored := startAccounts(t, dir, api.accountsAPI, &memoryVault{})
				if restored.Status().AccountID != other.Account.ID || restored.byKey(s.key).record().Label != beforeRecord.Label {
					t.Fatal("restart adopted failed QR metadata")
				}
				if _, err := restored.SwitchAccount(s.key); err != nil {
					t.Fatal(err)
				}
				if _, err := restored.Quota(); err != nil {
					t.Fatalf("previous credentials unusable after failure: %v", err)
				}
				qr = beginQR(t, svc, request)
				if result := pollQR(t, svc, qr.ID, "complete").Result; result == nil || result.Account.ID != s.key {
					t.Fatal("retry after storage recovery failed")
				}
			})
		}
	}
}
