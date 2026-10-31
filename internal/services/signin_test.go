package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/drive"
)

// memoryVault stands in for the keychain, which tests must not touch.
type memoryVault struct {
	mu        sync.Mutex
	passwords map[string]string
}

func (v *memoryVault) Password(account string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if p, ok := v.passwords[account]; ok {
		return p, nil
	}
	return "", ErrNoSavedPassword
}

func (v *memoryVault) SetPassword(account, password string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.passwords == nil {
		v.passwords = map[string]string{}
	}
	v.passwords[account] = password
	return nil
}

func (v *memoryVault) DeletePassword(account string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.passwords, account)
	return nil
}

// signInServer answers the auth endpoints and records every password a
// sign-in sent; refuse turns sign-ins down the way a changed password is.
type signInServer struct {
	*httptest.Server
	mu        sync.Mutex
	passwords []string
	refuse    bool
}

func newSignInServer(t *testing.T) *signInServer {
	t.Helper()
	s := &signInServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/signin" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.passwords = append(s.passwords, body["password"])
			refuse := s.refuse
			s.mu.Unlock()
			if refuse {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"password_not_match","error_code":4002,"error_description":"wrong password"}`)
				return
			}
		}
		if authAndTokenRoutes(w, r) {
			return
		}
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *signInServer) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.passwords...)
}

func startSignInService(t *testing.T, dir string, srv *signInServer, vault PasswordVault) *DriveService {
	t.Helper()
	svc := NewDriveService(DriveOptions{
		ConfigDir:   dir,
		APIBaseURL:  srv.URL,
		AuthBaseURL: srv.URL,
		NoIndexSync: true,
		Passwords:   vault,
		Logger:      quiet(),
	}, nil)
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc
}

// signIn signs login in through the form and fails the test on a refusal.
func signIn(t *testing.T, svc *DriveService, req SignInRequest) SignInResult {
	t.Helper()
	res, err := svc.SignIn(req)
	if err != nil {
		t.Fatalf("sign in %s: %v", req.Login, err)
	}
	if res.Failure != "" {
		t.Fatalf("sign in %s refused: %s %s", req.Login, res.Failure, res.Message)
	}
	return res
}

// Both choices start ticked. Remember Account Password keeps the account's password in
// the vault, where an empty password on its Sign In Again form then signs in; a
// sign-in with it unticked forgets the password and the next form starts
// unticked. Removing the account forgets everything, and the next launch
// is the plain sign-in page.
func TestRememberedSignIn(t *testing.T) {
	srv := newSignInServer(t)
	vault := &memoryVault{}
	dir := t.TempDir()
	svc := startSignInService(t, dir, srv, vault)
	const account = "user@example.com"

	if got := svc.SavedSignIn(""); got != (SavedSignIn{Supported: true, Remember: true, AutoSignIn: true}) {
		t.Fatalf("before any sign-in: saved = %+v, want both choices ticked", got)
	}

	id := signIn(t, svc, SignInRequest{Login: account, Password: "hunter2", SignInOptions: SignInOptions{Remember: true, AutoSignIn: true}}).Account.ID
	want := SavedSignIn{Supported: true, Account: account, HasPassword: true, Remember: true, AutoSignIn: true}
	if got := svc.SavedSignIn(id); got != want {
		t.Fatalf("saved = %+v, want %+v", got, want)
	}

	again := signIn(t, svc, SignInRequest{Login: account, Account: id, SignInOptions: SignInOptions{Remember: true}})
	if sent := srv.sent(); len(sent) != 2 || sent[1] != "hunter2" {
		t.Errorf("passwords sent = %q, want the remembered one second", sent)
	}
	if !again.Existed || again.Account.ID != id || len(svc.Accounts()) != 1 {
		t.Errorf("signing the account in again made %+v and %d accounts, want the same one", again.Account, len(svc.Accounts()))
	}

	signIn(t, svc, SignInRequest{Login: account, Password: "hunter2", Account: id})
	if got := svc.SavedSignIn(id); got != (SavedSignIn{Supported: true, Account: account}) {
		t.Errorf("after signing in without remembering: saved = %+v, want no password, unticked", got)
	}
	if got := svc.SavedSignIn(""); got != (SavedSignIn{Supported: true}) {
		t.Errorf("next form after unticking: %+v, want it unticked", got)
	}
	if _, err := vault.Password(account); !errors.Is(err, ErrNoSavedPassword) {
		t.Errorf("the vault still holds the password: %v", err)
	}
	if res, _ := svc.SignIn(SignInRequest{Login: account, Account: id}); res.Failure != "input" {
		t.Errorf("an empty password with nothing remembered: %+v, want an input failure", res)
	}

	signIn(t, svc, SignInRequest{Login: account, Password: "hunter2", Account: id, SignInOptions: SignInOptions{Remember: true}})
	if _, err := svc.RemoveAccount(id); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Password(account); !errors.Is(err, ErrNoSavedPassword) {
		t.Errorf("removing the account left its password in the vault: %v", err)
	}
	relaunched := startSignInService(t, dir, srv, vault)
	if status := relaunched.Status(); status.AccountID != "" || status.Configured || status.NeedsPassword {
		t.Errorf("relaunch after removing the only account: status = %+v, want the sign-in page", status)
	}
	if saved := relaunched.SavedSignIn(""); saved.Account != "" || saved.HasPassword {
		t.Errorf("sign-in page after removal offers %+v, want nothing of the removed account", saved)
	}
}

// Every successful sign-in accepts the invitation, as the account just
// signed in: its token and its device. A refused one sends nothing.
func TestSignInAcceptsInvite(t *testing.T) {
	const account = "user@example.com"
	type invite struct {
		auth, device string
		body         map[string]map[string]string
	}
	invites := make(chan invite, 4)
	srv := newSignInServer(t)
	auth := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/vip/v1/activity/invite" {
			var got invite
			_ = json.NewDecoder(r.Body).Decode(&got.body)
			got.auth, got.device = r.Header.Get("Authorization"), r.Header.Get("X-Device-Id")
			invites <- got
			io.WriteString(w, `{}`)
			return
		}
		auth.ServeHTTP(w, r)
	})
	svc := startSignInService(t, t.TempDir(), srv, &memoryVault{})

	signIn(t, svc, SignInRequest{Login: account, Password: "hunter2"})
	select {
	case got := <-invites:
		if got.auth != "Bearer a" || got.device != drive.DeviceIDFor(account) {
			t.Errorf("invite sent as %q from device %q", got.auth, got.device)
		}
		if code := got.body["apk_extra"]["invite_code"]; code != inviteCode {
			t.Errorf("invite body = %v, want invite_code %q", got.body, inviteCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no invite after a sign-in")
	}

	srv.mu.Lock()
	srv.refuse = true
	srv.mu.Unlock()
	if res, _ := svc.SignIn(SignInRequest{Login: account, Password: "wrong"}); res.Failure == "" {
		t.Fatal("refused sign-in went through")
	}
	select {
	case got := <-invites:
		t.Errorf("refused sign-in sent an invite: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

// Auto Sign-In picks up a session the server ended while the app was closed: the
// launch reaches the file page and the first call signs in with the
// remembered password. A password turned down there ends the session at
// once instead of being sent again with every call.
func TestAutoSignInResumesAnEndedSession(t *testing.T) {
	const account = "user@example.com"
	for _, refused := range []bool{false, true} {
		srv := newSignInServer(t)
		vault := &memoryVault{}
		dir := t.TempDir()
		first := startSignInService(t, dir, srv, vault)
		id := signIn(t, first, SignInRequest{Login: account, Password: "hunter2", SignInOptions: SignInOptions{Remember: true, AutoSignIn: true}}).Account.ID
		// The server retired the session: only the account is left on disk.
		store := drive.FileStore{Path: filepath.Join(dir, "credentials-"+id+".json")}
		if err := store.Save(drive.Token{Username: account, DeviceID: drive.DeviceIDFor(account)}); err != nil {
			t.Fatal(err)
		}
		srv.mu.Lock()
		srv.refuse = refused
		srv.mu.Unlock()

		svc := startSignInService(t, dir, srv, vault)
		if !svc.Status().Configured {
			t.Fatalf("refused=%v: the launch stopped at the sign-in form", refused)
		}
		_, err := svc.Quota()
		if !refused {
			if err != nil {
				t.Fatalf("first call: %v", err)
			}
			if sent := srv.sent(); len(sent) != 2 || sent[1] != "hunter2" {
				t.Errorf("passwords sent = %q, want the remembered one again", sent)
			}
			continue
		}
		if !errors.Is(err, drive.ErrPasswordRequired) {
			t.Fatalf("refused password: err = %v, want ErrPasswordRequired", err)
		}
		if status := svc.Status(); status.Configured || !status.NeedsPassword {
			t.Errorf("refused password: status = %+v, want Log in again", status)
		}
		_, _ = svc.Quota()
		if sent := srv.sent(); len(sent) != 2 {
			t.Errorf("passwords sent = %q, want one automatic attempt", sent)
		}
	}
}

// Without Auto Sign-In an ended session asks for the password, its form filled in.
func TestEndedSessionWithoutAutoSignInAsks(t *testing.T) {
	const account = "user@example.com"
	srv := newSignInServer(t)
	vault := &memoryVault{}
	dir := t.TempDir()
	first := startSignInService(t, dir, srv, vault)
	id := signIn(t, first, SignInRequest{Login: account, Password: "hunter2", SignInOptions: SignInOptions{Remember: true}}).Account.ID
	store := drive.FileStore{Path: filepath.Join(dir, "credentials-"+id+".json")}
	if err := store.Save(drive.Token{Username: account, DeviceID: drive.DeviceIDFor(account)}); err != nil {
		t.Fatal(err)
	}

	svc := startSignInService(t, dir, srv, vault)
	if status := svc.Status(); status.Configured || !status.NeedsPassword || status.AccountID != id {
		t.Errorf("status = %+v, want Log in again for the account", status)
	}
	if saved := svc.SavedSignIn(id); saved.Account != account || !saved.HasPassword {
		t.Errorf("saved = %+v, want the form filled in", saved)
	}
	if sent := srv.sent(); len(sent) != 1 {
		t.Errorf("passwords sent = %q, want none at launch", sent)
	}
}
