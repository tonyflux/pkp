package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
)

// QRSignInRequest adds an account, or restores exactly Account when it is set.
type QRSignInRequest struct {
	Account string `json:"account"`
	Label   string `json:"label"`
}

// QRSignInState exposes presentation data, never device codes or credentials.
type QRSignInState struct {
	ID        string        `json:"id"`
	URL       string        `json:"url"`
	ExpiresAt string        `json:"expiresAt"`
	Interval  int           `json:"interval"`
	State     string        `json:"state"`
	Message   string        `json:"message"`
	Result    *SignInResult `json:"result"`
}

type qrSignInSession struct {
	mu       sync.Mutex
	ctx      context.Context
	trial    *drive.TokenManager
	target   *accountSession
	label    string
	code     string
	deadline time.Time
	nextPoll time.Time
	scanned  bool
	state    QRSignInState
}

func signInID() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return hex.EncodeToString(id[:])
}

// BeginQRSignIn starts an anonymous grant for the web QR client. The same
// client and stable device identity are retained for token refresh.
func (d *DriveService) BeginQRSignIn(req QRSignInRequest) (QRSignInState, error) {
	if d.configErr != nil {
		return QRSignInState{}, d.configErr
	}
	label := strings.TrimSpace(req.Label)
	if utf8.RuneCountInString(label) > maxLabelRunes {
		return QRSignInState{State: "error", Message: errLabelTooLong.Error()}, nil
	}
	var target *accountSession
	if req.Account != "" {
		target = d.byKey(req.Account)
		if target == nil {
			return QRSignInState{}, errNoSuchAccount
		}
	}
	deviceID := signInID()
	if target != nil && target.record().DeviceID != "" {
		deviceID = target.record().DeviceID
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &qrSignInSession{
		ctx: ctx, target: target, label: label,
		trial: drive.NewTokenManager(drive.TokenManagerConfig{AuthBaseURL: d.opts.AuthBaseURL, Profile: drive.WebProfile(), DeviceID: deviceID}),
		state: QRSignInState{ID: signInID(), State: "waiting"},
	}
	a := &signInAttempt{cancel: cancel, qr: q}
	q.mu.Lock()
	defer q.mu.Unlock()
	d.registerSignIn(a)
	// Publication to Poll happens only after the creation request is finished.
	creation, stop := context.WithTimeout(ctx, signInTimeout)
	code, err := q.trial.BeginDeviceAuthorization(creation)
	stop()
	if d.signInCancelled(a) {
		return QRSignInState{ID: q.state.ID, State: "cancelled"}, nil
	}
	if err != nil {
		kind, message := signInFailure(err)
		q.state.State, q.state.Message = "error", message
		if kind == "network" {
			q.state.State = "network"
		}
		d.endQRSignIn(a)
		return q.state, nil
	}
	q.code, q.deadline = code.DeviceCode, code.ExpiresAt
	q.nextPoll = time.Now().Add(code.Interval)
	q.state.URL, q.state.ExpiresAt = code.URL, code.ExpiresAt.UTC().Format(time.RFC3339Nano)
	q.state.Interval = max(1, int((code.Interval+time.Second-1)/time.Second))
	return q.state, nil
}

// PollQRSignIn advances at most one exchange. A per-session lock and server-side
// next-poll deadline prevent overlapping UI calls from consuming extra grants.
func (d *DriveService) PollQRSignIn(id string) (QRSignInState, error) {
	d.signInMu.Lock()
	a := d.signIn
	if a == nil || a.qr == nil || id == "" || a.qr.state.ID != id {
		d.signInMu.Unlock()
		return QRSignInState{ID: id, State: "cancelled"}, nil
	}
	q := a.qr
	d.signInMu.Unlock()
	q.mu.Lock()
	defer q.mu.Unlock()
	if d.signInCancelled(a) {
		return QRSignInState{ID: id, State: "cancelled"}, nil
	}
	if !time.Now().Before(q.deadline) {
		q.state.State, q.state.Message, q.state.URL, q.code = "expired", "", "", ""
		d.endQRSignIn(a)
		return q.state, nil
	}
	if time.Now().Before(q.nextPoll) {
		return q.state, nil
	}
	pollCtx, stop := context.WithDeadline(q.ctx, q.deadline)
	result, err := q.trial.PollDeviceAuthorization(pollCtx, q.code)
	stop()
	if d.signInCancelled(a) {
		return QRSignInState{ID: id, State: "cancelled"}, nil
	}
	if !time.Now().Before(q.deadline) {
		q.state.State, q.state.Message, q.state.URL, q.code = "expired", "", "", ""
		d.endQRSignIn(a)
		return q.state, nil
	}
	q.nextPoll = time.Now().Add(time.Duration(q.state.Interval) * time.Second)
	if err != nil {
		kind, message := signInFailure(err)
		if kind == "network" || kind == "busy" {
			var apiErr *drive.APIError
			if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
				q.state.Interval = max(q.state.Interval, int((apiErr.RetryAfter+time.Second-1)/time.Second))
				q.nextPoll = time.Now().Add(time.Duration(q.state.Interval) * time.Second)
			}
			q.state.State, q.state.Message = "network", message
			return q.state, nil
		}
		q.state.State, q.state.Message, q.state.URL, q.code = "error", message, "", ""
		d.endQRSignIn(a)
		return q.state, nil
	}
	q.state.Message = ""
	switch result.State {
	case "waiting", "scanned", "slow_down":
		q.scanned = q.scanned || result.State == "scanned"
		q.state.State = "waiting"
		if q.scanned {
			q.state.State = "scanned"
		}
		if result.State == "slow_down" {
			q.state.Interval += 5
			q.nextPoll = time.Now().Add(time.Duration(q.state.Interval) * time.Second)
		}
	case "expired", "denied":
		q.state.State, q.state.URL, q.code = result.State, "", ""
		d.endQRSignIn(a)
	case "complete":
		// No password is supplied or forgotten by a QR sign-in. A matching
		// account retains its login name and existing keychain preferences.
		res, commitErr := d.finishSignIn(q.trial, result.Token, "", "", q.label, q.target, a, nil)
		q.code, q.state.URL = "", ""
		if commitErr != nil {
			d.log.Error("drive: QR sign-in commit failed", "error", commitErr)
			q.state.State, q.state.Message = "error", i18n.T("没能保存登录信息，请重试")
			d.endQRSignIn(a)
			return q.state, nil
		}
		if res.Failure != "" {
			q.state.State, q.state.Message = "error", res.Message
			if res.Failure == "cancelled" {
				q.state.State = "cancelled"
			}
		} else {
			q.state.State, q.state.Result = "complete", &res
		}
		d.endQRSignIn(a)
	default:
		q.state.State, q.state.Message, q.state.URL, q.code = "error", i18n.T("登录没有成功，请稍后再试"), "", ""
		d.endQRSignIn(a)
	}
	return q.state, nil
}

// CancelQRSignIn is scoped: a disappearing old view cannot cancel its successor.
func (d *DriveService) CancelQRSignIn(id string) {
	d.signInMu.Lock()
	defer d.signInMu.Unlock()
	if a := d.signIn; a != nil && a.qr != nil && id != "" && a.qr.state.ID == id {
		a.cancelled = true
		a.cancel()
		d.signIn = nil
	}
}

func (d *DriveService) endQRSignIn(a *signInAttempt) {
	d.signInMu.Lock()
	if d.signIn == a {
		d.signIn = nil
	}
	d.signInMu.Unlock()
	a.cancel()
}

// commitQRSignIn holds signInCommitMu through preparation and publication.
// Write the proposed account list before credentials; a failed credential write
// restores only that list, never a stale token snapshot over a concurrent refresh.
func (d *DriveService) commitQRSignIn(existing *accountSession, trial *drive.TokenManager, tok drive.Token, login, password, label string) (*accountSession, error) {
	s := existing
	if s == nil {
		key := d.newKey(trial.DeviceID())
		s = &accountSession{key: key, rec: accountRecord{Key: key, AddedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	}
	s.mu.RLock()
	liveTokens, live := s.tokens, s.client != nil
	s.mu.RUnlock()
	reuse := live && liveTokens != nil && liveTokens.DeviceID() == trial.DeviceID() && liveTokens.Profile().ClientID == trial.Profile().ClientID
	var client *drive.Client
	var err error
	if !reuse {
		client, err = d.newSessionClient(trial)
		if err != nil {
			return nil, err
		}
	}

	err = func() error {
		d.accountsMu.Lock()
		defer d.accountsMu.Unlock()
		before, readErr := os.ReadFile(d.accountsPath())
		missing := errors.Is(readErr, os.ErrNotExist)
		if readErr != nil && !missing {
			return readErr
		}
		proposed := s.record()
		proposed.Login, proposed.UserID, proposed.DeviceID = login, tok.UserID, trial.DeviceID()
		proposed.UsedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if label != "" {
			proposed.Label = label
		}
		d.mu.RLock()
		file := accountsFile{Version: accountsVersion, Current: s.key, Accounts: make([]accountRecord, 0, len(d.accounts)+1)}
		for _, account := range d.accounts {
			if account == s {
				file.Accounts = append(file.Accounts, proposed)
			} else {
				file.Accounts = append(file.Accounts, account.record())
			}
		}
		d.mu.RUnlock()
		if existing == nil {
			file.Accounts = append(file.Accounts, proposed)
		}
		if err := d.writeAccounts(file); err != nil {
			return err
		}
		var credentialErr error
		if reuse {
			if liveTokens != trial {
				credentialErr = liveTokens.Seed(tok)
			}
		} else {
			credentialErr = trial.Persist(d.credentialsStore(s.key), func() (string, error) { return d.passwordFor(s) })
		}
		if credentialErr != nil {
			var restoreErr error
			if missing {
				restoreErr = os.Remove(d.accountsPath())
			} else {
				restoreErr = d.replaceAccounts(before)
			}
			if restoreErr != nil {
				return errors.Join(credentialErr, fmt.Errorf("restore account list after failed QR sign-in: %w", restoreErr))
			}
			return credentialErr
		}
		// Publish only sign-in-owned fields; profile/consent edits can occur
		// while persistence runs and will save again after accountsMu is released.
		d.mu.Lock()
		s.mu.Lock()
		s.rec.Login, s.rec.UserID, s.rec.DeviceID, s.rec.UsedAt = proposed.Login, proposed.UserID, proposed.DeviceID, proposed.UsedAt
		if label != "" {
			s.rec.Label = label
		}
		s.password = password
		s.mu.Unlock()
		if existing == nil {
			d.accounts = append(d.accounts, s)
		}
		d.current = s
		d.mu.Unlock()
		return nil
	}()
	if err != nil {
		return nil, err
	}
	if !reuse {
		d.installSession(s, trial, client)
	}
	return s, nil
}
