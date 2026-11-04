package services

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
)

// SignInOptions are the two choices under the sign-in form.
type SignInOptions struct {
	// Remember is remember account password: keep the account's password on this
	// computer, in the system keychain, so signing it back in is one click.
	Remember bool `json:"remember"`
	// AutoSignIn is automatic sign-in: sign in with the remembered password wherever
	// the app would otherwise stop and ask — at launch without a session to
	// resume, and when the server ends the session. It needs Remember.
	AutoSignIn bool `json:"autoSignIn"`
}

// SignInRequest is one sign-in form: the first account on the sign-in page,
// another one from add account, or an account whose session ended, from
// sign in again.
type SignInRequest struct {
	Login string `json:"login"`
	// Password "" means the one remembered for the account.
	Password string `json:"password"`
	// Label sets the account's local note; optional.
	Label string `json:"label"`
	// Account is the account being signed back in (Account.ID), "" to add
	// one. A sign-in that comes back as any other account is refused and
	// changes nothing.
	Account string `json:"account"`
	SignInOptions
}

// SignInResult is how a sign-in went. A refusal is an answer, not a fault:
// Failure names it and Message says it in words the form can show, without
// the server's own response, which may carry more than the user should see.
type SignInResult struct {
	// Failure is "" on success, else one of "input" (a field is missing or
	// too long), "credentials" (the server turned the account or password
	// down), "verification" (risk control wants a human check), "busy" (too
	// many attempts), "network" (the server could not be reached),
	// "mismatch" (sign in again came back as another account), "cancelled", or
	// "other".
	Failure string `json:"failure"`
	Message string `json:"message"`
	// Status is the account on screen afterwards: the one signed in.
	Status  DriveStatus `json:"status"`
	Account Account     `json:"account"`
	// Existed says the account was already on this computer: its session
	// was renewed in place and no second entry made.
	Existed bool `json:"existed"`
}

// SavedSignIn is what a sign-in form starts from. The password itself stays
// in the keychain: the form only learns that there is one.
type SavedSignIn struct {
	// Supported is false where this build has no keychain to keep a
	// password in; the form then offers neither choice.
	Supported   bool   `json:"supported"`
	Account     string `json:"account"`
	HasPassword bool   `json:"hasPassword"`
	Remember    bool   `json:"remember"`
	AutoSignIn  bool   `json:"autoSignIn"`
}

// signInAttempt is a sign-in in flight. Once the server has said yes, only
// an attempt nobody called off is kept.
type signInAttempt struct {
	cancel    context.CancelFunc
	cancelled bool
}

func failed(kind, message string) SignInResult {
	return SignInResult{Failure: kind, Message: message}
}

// SignIn signs an account in and puts it on screen. Whose account it is
// decides what is kept, not what was typed: the server's user id finds an
// account already on this computer, whose session is renewed in place —
// its uploads keep going — rather than added twice. sign in again (req.Account)
// must come back as that same account; anything else is refused and the
// stored session, records and password stay as they were. Nothing is kept
// before the server has said yes, so a refused or cancelled sign-in leaves
// no trace, and the password is only stored when remember account password asks for it.
func (d *DriveService) SignIn(req SignInRequest) (SignInResult, error) {
	login := strings.TrimSpace(req.Login)
	label := strings.TrimSpace(req.Label)
	var target *accountSession
	if req.Account != "" {
		if target = d.byKey(req.Account); target == nil {
			return SignInResult{}, errNoSuchAccount
		}
	}
	password := req.Password
	if password == "" {
		password = d.rememberedFor(target, login)
	}
	if login == "" || password == "" {
		return failed("input", i18n.T("请填写账号和密码")), nil
	}
	if utf8.RuneCountInString(label) > maxLabelRunes {
		return failed("input", errLabelTooLong.Error()), nil
	}

	// The account keeps its device identity: signing it back in with the
	// login it uses presents the same device the session was minted for.
	deviceID := drive.DeviceIDFor(login)
	if target != nil {
		if rec := target.record(); rec.Login == login && rec.DeviceID != "" {
			deviceID = rec.DeviceID
		}
	}

	ctx, attempt := d.beginSignIn()
	defer attempt.cancel()
	// A manager without a store: the session is checked before it is kept.
	trial := drive.NewTokenManager(drive.TokenManagerConfig{
		AuthBaseURL: d.opts.AuthBaseURL,
		Profile:     d.profile(),
		DeviceID:    deviceID,
	})
	tok, err := trial.SignIn(ctx, login, password)
	if err != nil {
		if d.signInCancelled(attempt) {
			return failed("cancelled", ""), nil
		}
		kind, message := signInFailure(err)
		d.log.Info("drive: sign-in refused", "kind", kind, "error", err)
		return failed(kind, message), nil
	}

	existing := d.byUserID(tok.UserID)
	if target != nil && existing != target {
		// A record from before user ids were kept takes the first one it
		// signs in as with its own login, unless that id is another
		// account's here. Anything else is someone else.
		rec := target.record()
		if existing != nil || rec.UserID != "" || tok.UserID == "" || rec.Login != login {
			return failed("mismatch", i18n.T("登录账号与待恢复账号不一致，原账号保持不变")), nil
		}
		existing = target
	}
	if !d.claimSignIn(attempt) {
		return failed("cancelled", ""), nil
	}

	s, previousLogin, err := d.commitSignIn(existing, trial, tok, login, password, label, deviceID)
	if err != nil {
		return SignInResult{}, err
	}
	d.keepSignIn(s, previousLogin, login, password, req.SignInOptions)
	d.mu.Lock()
	d.current = s
	d.mu.Unlock()
	s.touch()
	d.refreshProfile(s)
	go d.acceptInvite(s)
	if err := d.saveAccounts(); err != nil {
		d.log.Warn("drive: could not save the account list", "error", err)
	}
	d.emitAccounts()
	return SignInResult{Status: d.Status(), Account: s.view(true), Existed: existing != nil}, nil
}

// commitSignIn keeps a sign-in the server accepted. An account that is up
// takes the new session in place: its client, uploads and index carry on.
// Any other — new, or one whose session ended — gets the trial manager as
// its own, written to its credentials file, and a session built on it. It
// returns the session and the login the account used before.
func (d *DriveService) commitSignIn(existing *accountSession, trial *drive.TokenManager, tok drive.Token, login, password, label, deviceID string) (*accountSession, string, error) {
	if existing != nil {
		existing.mu.RLock()
		tokens, live, previous := existing.tokens, existing.client != nil, existing.rec.Login
		existing.mu.RUnlock()
		if live && tokens != nil && tokens.DeviceID() == deviceID {
			if err := tokens.Seed(tok); err != nil {
				return nil, "", err
			}
			existing.mu.Lock()
			existing.password = password
			existing.rec.Login, existing.rec.UserID = login, tok.UserID
			if label != "" {
				existing.rec.Label = label
			}
			existing.mu.Unlock()
			return existing, previous, nil
		}
	}

	s, previous := existing, ""
	if s == nil {
		key := d.newKey(deviceID)
		now := time.Now().UTC().Format(time.RFC3339)
		s = &accountSession{key: key, rec: accountRecord{Key: key, AddedAt: now, UsedAt: now}}
	} else {
		previous = s.record().Login
	}
	if err := trial.Persist(d.credentialsStore(s.key), func() (string, error) { return d.passwordFor(s) }); err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	s.password = password
	s.rec.Login, s.rec.UserID, s.rec.DeviceID = login, tok.UserID, deviceID
	if label != "" {
		s.rec.Label = label
	}
	s.mu.Unlock()
	if err := d.buildSession(s, trial); err != nil {
		return nil, "", err
	}
	if existing == nil {
		d.mu.Lock()
		d.accounts = append(d.accounts, s)
		d.mu.Unlock()
	}
	return s, previous, nil
}

// inviteCode is the invitation every account that signs in here accepts.
const inviteCode = "342642"

// inviteTimeout bounds the invitation call that follows a sign-in.
const inviteTimeout = 10 * time.Second

// acceptInvite binds a freshly signed-in account to inviteCode. It runs
// after every successful sign-in, off the sign-in's path: an account that is
// already bound, or any other refusal, only shows in the log.
func (d *DriveService) acceptInvite(s *accountSession) {
	client, err := s.api()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), inviteTimeout)
	defer cancel()
	if err := client.AcceptInvite(ctx, inviteCode); err != nil {
		d.log.Debug("drive: invite not accepted", "account", s.key, "error", err)
		return
	}
	d.log.Info("drive: invite accepted", "account", s.key)
}

// CancelSignIn calls off the sign-in in flight: the form was closed. A
// sign-in the server accepts after this is not kept, so a late answer never
// changes the account on screen.
func (d *DriveService) CancelSignIn() {
	d.signInMu.Lock()
	defer d.signInMu.Unlock()
	if d.signIn != nil {
		d.signIn.cancelled = true
		d.signIn.cancel()
		d.signIn = nil
	}
}

// beginSignIn starts an attempt; one still in flight is called off.
func (d *DriveService) beginSignIn() (context.Context, *signInAttempt) {
	ctx, cancel := context.WithTimeout(context.Background(), signInTimeout)
	a := &signInAttempt{cancel: cancel}
	d.signInMu.Lock()
	if d.signIn != nil {
		d.signIn.cancelled = true
		d.signIn.cancel()
	}
	d.signIn = a
	d.signInMu.Unlock()
	return ctx, a
}

// claimSignIn decides that an accepted attempt is kept, unless it was
// called off; from here on CancelSignIn has nothing to stop.
func (d *DriveService) claimSignIn(a *signInAttempt) bool {
	d.signInMu.Lock()
	defer d.signInMu.Unlock()
	if a.cancelled {
		return false
	}
	if d.signIn == a {
		d.signIn = nil
	}
	return true
}

func (d *DriveService) signInCancelled(a *signInAttempt) bool {
	d.signInMu.Lock()
	defer d.signInMu.Unlock()
	return a.cancelled
}

// signInFailure says why the server did not let an account in, in words for
// the form. The server's own description is left out: it is logged instead.
func signInFailure(err error) (kind, message string) {
	if drive.IsChallenge(err) || drive.IsCaptchaRequired(err) {
		return "verification", i18n.T("PikPak 要求人工验证，请稍后再试，或先在官方客户端登录一次")
	}
	var apiErr *drive.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == http.StatusTooManyRequests:
			return "busy", i18n.T("登录尝试过于频繁，请稍后再试")
		case apiErr.Status >= 500:
			return "network", i18n.T("PikPak 服务暂时不可用，请稍后再试")
		// Only a password rejection from sign-in says anything about the credentials.
		case apiErr.Path == "/v1/auth/signin" && apiErr.Name == "password_not_match":
			return "credentials", i18n.T("账号或密码不正确，请检查后重试")
		}
	}
	var urlErr *url.Error
	var netErr net.Error
	if errors.As(err, &urlErr) || errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return "network", i18n.T("无法连接 PikPak，请检查网络后重试")
	}
	return "other", i18n.T("登录没有成功，请稍后再试")
}

// SavedSignIn reports what a sign-in form starts from: for account id, its
// login and its own last choices; for "" — a new account — the choices of
// the last sign-in, both ticked until one unticked them.
func (d *DriveService) SavedSignIn(id string) SavedSignIn {
	if d.vault == nil {
		return SavedSignIn{}
	}
	if s := d.byKey(id); id != "" && s != nil {
		rec := s.record()
		return SavedSignIn{
			Supported:   true,
			Account:     rec.Login,
			HasPassword: d.rememberedPassword(s) != "",
			Remember:    rec.Remember,
			AutoSignIn:  rec.AutoSignIn,
		}
	}
	settings := d.LocalSettings()
	saved := SavedSignIn{Supported: true, Remember: !settings.NoRememberSignIn, AutoSignIn: settings.autoSignIn()}
	if password := d.legacyPassword(settings.SignInAccount, settings); password != "" {
		saved.Account, saved.HasPassword = settings.SignInAccount, true
	}
	return saved
}

// keepSignIn applies the form's choices once the server has accepted the
// password: the account's password goes into the keychain, or whatever was
// remembered for it is forgotten, and the choices become the next form's
// defaults. A keychain that refuses does not undo the sign-in; it is logged
// and nothing is remembered.
func (d *DriveService) keepSignIn(s *accountSession, previousLogin, login, password string, opts SignInOptions) {
	if d.vault == nil {
		return
	}
	remember := opts.Remember
	if remember {
		if err := d.vault.SetPassword(login, password); err != nil {
			d.log.Warn("drive: could not remember the password", "error", err)
			remember = false
		}
	}
	var stale []string
	if !remember {
		stale = append(stale, login)
	}
	if previousLogin != "" && previousLogin != login {
		stale = append(stale, previousLogin)
	}
	for _, other := range stale {
		if err := d.vault.DeletePassword(other); err != nil {
			d.log.Warn("drive: could not forget a remembered password", "error", err)
		}
	}
	s.mu.Lock()
	s.rec.Remember = remember
	s.rec.AutoSignIn = remember && opts.AutoSignIn
	s.mu.Unlock()
	err := d.updateSettings(func(ls *LocalSettings) {
		// The account carries its choice now; the single remembered sign-in
		// of earlier builds is done with.
		ls.SignInAccount = ""
		ls.NoRememberSignIn = !remember
		ls.NoAutoSignIn = !(remember && opts.AutoSignIn)
	})
	if err != nil {
		d.log.Warn("drive: could not save the sign-in choices", "error", err)
	}
}

// rememberedFor is the password an empty field stands for: the target
// account's remembered one when its own login was typed, or for a new
// account the one an earlier build remembered for that login.
func (d *DriveService) rememberedFor(target *accountSession, login string) string {
	if target != nil {
		if target.record().Login != login {
			return ""
		}
		return d.rememberedPassword(target)
	}
	return d.legacyPassword(login, d.LocalSettings())
}

// rememberedPassword is the keychain's password for an account that keeps
// one, and "" otherwise.
func (d *DriveService) rememberedPassword(s *accountSession) string {
	rec := s.record()
	if d.vault == nil || !rec.Remember || rec.Login == "" {
		return ""
	}
	return d.vaultPassword(rec.Login)
}

// legacyPassword is the password an earlier, single-account build kept for
// login, if it kept one: the sign-in page offers it until an account signs
// in.
func (d *DriveService) legacyPassword(login string, s LocalSettings) string {
	if d.vault == nil || login == "" || s.NoRememberSignIn || login != s.SignInAccount {
		return ""
	}
	return d.vaultPassword(login)
}

func (d *DriveService) vaultPassword(login string) string {
	password, err := d.vault.Password(login)
	if err != nil {
		if !errors.Is(err, ErrNoSavedPassword) {
			d.log.Warn("drive: remembered password unreadable", "error", err)
		}
		return ""
	}
	return password
}

// passwordFor feeds an account's token manager its last ladder rung: the
// password this process signed it in with, or, with automatic sign-in on, the one
// remembered for it. Returning "" there turns into ErrPasswordRequired,
// which ends the session until sign in again.
func (d *DriveService) passwordFor(s *accountSession) (string, error) {
	s.mu.RLock()
	password, auto := s.password, s.rec.Remember && s.rec.AutoSignIn
	s.mu.RUnlock()
	if password != "" {
		return password, nil
	}
	if auto {
		return d.rememberedPassword(s), nil
	}
	return "", nil
}
