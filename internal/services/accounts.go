package services

// Several PikPak accounts stay signed in on this computer at once — a main
// account and a spare, or accounts kept apart by content — and one of them is
// on screen (Multi-Account Management). Each account is a session of its
// own: its token manager, API client, upload and download queues, play
// record and local index live together, so switching what is on screen stops
// nothing another account is doing, and nothing of one account is ever read
// through another.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/upload"
)

// EventAccounts says the account list changed: an account signed in, was
// switched to, renamed or removed, its session ended, or its profile came
// in. The webview reads Accounts and Status again.
const EventAccounts = "drive:accounts"

const (
	// accountsFileName lists the accounts of this computer. It holds no
	// secret: tokens stay in each account's credentials file and remembered
	// passwords in the system keychain.
	accountsFileName = "accounts.json"
	accountsVersion  = 1
	// legacyCredentials is the one session file of the single-account
	// layout; the first start of a multi-account build adopts it.
	legacyCredentials = "credentials.json"
	// maxLabelRunes bounds a local note.
	maxLabelRunes = 24
)

var (
	errNoSuchAccount = i18n.New("这台电脑上没有这个账号")
	errLabelRequired = i18n.New("请填写账号备注")
	errLabelTooLong  = &lazyError{func() string { return i18n.F("账号备注最多 %d 个字符", maxLabelRunes) }}
)

// lazyError is a sentinel whose text takes arguments: it is formatted in
// the language in force when read, and errors.Is still compares identity.
type lazyError struct{ text func() string }

func (e *lazyError) Error() string { return e.text() }

// Account is one account on this computer as the switcher, the account
// manager, the transfer list and the player show it. Passwords and tokens
// never leave Go.
type Account struct {
	// ID is this computer's handle for the account, stable while it stays
	// here. It names the account in every call that acts for one.
	ID string `json:"id"`
	// Login is what the account signs in with: an email or a phone number.
	// The page shows it masked.
	Login string `json:"login"`
	// Label is the local note, "" when none was set; the page then shows the
	// nickname, or the masked login.
	Label    string `json:"label"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	// Current marks the account on screen.
	Current bool `json:"current"`
	// NeedsPassword marks an account whose session ended: it stays listed,
	// its uploads wait, and it needs sign-in again before anything of it works.
	NeedsPassword bool `json:"needsPassword"`
	// UsedAt is when the account was last on screen, RFC 3339.
	UsedAt string `json:"usedAt"`
	// AIConsent is the account's Before Sending to AI choice for L1 metadata
	// (names, paths, sizes, times): true = Always Allow, false = ask once per
	// task. AIContentConsent is the same for L2 content (subtitle text).
	AIConsent        bool `json:"aiConsent"`
	AIContentConsent bool `json:"aiContentConsent"`
}

// accountRecord is what accounts.json keeps of one account.
type accountRecord struct {
	// Key names the account's files — credentials-<key>.json,
	// index-<key>.sqlite, uploads-<key>.json, plays-<key>.json — and is
	// Account.ID. It is the device id the account was first signed in with,
	// the name those files had before there were several accounts.
	Key string `json:"key"`
	// UserID is the server's id of the account: what tells two sign-ins of
	// one account apart from two accounts, whatever login was typed.
	UserID string `json:"userId"`
	Login  string `json:"login"`
	// DeviceID is what the session presents; md5 of the login it last
	// signed in with (drive.DeviceIDFor).
	DeviceID string `json:"deviceId"`
	Label    string `json:"label,omitempty"`
	// Nickname and Avatar are the server profile as last read.
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	// Remember is Remember Account Password for this account: its password is in the
	// system keychain. AutoSignIn is Auto Sign-In, which needs Remember.
	Remember   bool   `json:"remember,omitempty"`
	AutoSignIn bool   `json:"autoSignIn,omitempty"`
	AddedAt    string `json:"addedAt"`
	UsedAt     string `json:"usedAt"`
	// AIConsent is Before Sending to AI · Always Allow for this account's L1 metadata:
	// the assistant sends without asking each task. AIContentConsent is the
	// same for L2 content. Each account decides for its own files.
	AIConsent        bool `json:"aiConsent,omitempty"`
	AIContentConsent bool `json:"aiContentConsent,omitempty"`
}

type accountsFile struct {
	Version  int             `json:"version"`
	Current  string          `json:"current"`
	Accounts []accountRecord `json:"accounts"`
}

// accountSession is one account on this computer and everything that works
// for it. Its fields are guarded by mu; DriveService.mu, when both are held,
// is taken first.
type accountSession struct {
	// key is rec.Key, which never changes: readable without the lock.
	key string

	mu  sync.RWMutex
	rec accountRecord
	// tokens is set from the first sign-in or restore on; client is nil
	// while the session needs a password.
	tokens *drive.TokenManager
	client *drive.Client
	// password is the one this process signed this account in with, kept
	// in memory so a refresh token the server retires mid-session can be
	// replaced without asking.
	password      string
	needsPassword bool
	idx           *indexSession
	// uploads is the account's queue. While the session needs a password
	// it is a closed one that only lists what is waiting.
	uploads *upload.Queue
	// downloads is the account's Download to Computer queue; while the session needs
	// a password, a closed one that only lists the records.
	downloads *download.Queue
	plays     *playLog
}

func (s *accountSession) record() accountRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rec
}

// api is the account's client, or why there is none.
func (s *accountSession) api() (*drive.Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client != nil {
		return s.client, nil
	}
	if s.needsPassword {
		return nil, drive.ErrPasswordRequired
	}
	return nil, drive.ErrNoCredentials
}

func (s *accountSession) index() *indexSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.idx
}

func (s *accountSession) queue() *upload.Queue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.uploads
}

// liveQueue is the queue for actions that reach the server, which an
// account whose session ended cannot take.
func (s *accountSession) liveQueue() (*upload.Queue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch {
	case s.needsPassword:
		return nil, drive.ErrPasswordRequired
	case s.uploads == nil || s.client == nil:
		return nil, drive.ErrNoCredentials
	}
	return s.uploads, nil
}

func (s *accountSession) downloadQueue() *download.Queue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.downloads
}

// liveDownloads is the download queue for actions that reach the drive,
// which an account whose session ended cannot take.
func (s *accountSession) liveDownloads() (*download.Queue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch {
	case s.needsPassword:
		return nil, drive.ErrPasswordRequired
	case s.downloads == nil || s.client == nil:
		return nil, drive.ErrNoCredentials
	}
	return s.downloads, nil
}

func (s *accountSession) playLog() *playLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.plays
}

// touch records that the account is on screen now.
func (s *accountSession) touch() {
	s.mu.Lock()
	s.rec.UsedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
}

func (s *accountSession) view(current bool) Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Account{
		ID:               s.key,
		Login:            s.rec.Login,
		Label:            s.rec.Label,
		Nickname:         s.rec.Nickname,
		Avatar:           s.rec.Avatar,
		Current:          current,
		NeedsPassword:    s.needsPassword,
		UsedAt:           s.rec.UsedAt,
		AIConsent:        s.rec.AIConsent,
		AIContentConsent: s.rec.AIContentConsent,
	}
}

/* ---------------- what the webview calls ---------------- */

// Accounts lists the accounts on this computer in the order they were added.
func (d *DriveService) Accounts() []Account {
	d.mu.RLock()
	list, current := slices.Clone(d.accounts), d.current
	d.mu.RUnlock()
	out := make([]Account, 0, len(list))
	for _, s := range list {
		out = append(out, s.view(s == current))
	}
	return out
}

// SwitchAccount puts another account on screen. Nothing of the account
// leaving stops: its uploads and its index carry on in the background, and
// the player is the webview's to settle first.
func (d *DriveService) SwitchAccount(id string) (DriveStatus, error) {
	d.mu.Lock()
	s := d.byKeyLocked(id)
	if s != nil {
		d.current = s
	}
	d.mu.Unlock()
	if s == nil {
		return d.Status(), errNoSuchAccount
	}
	s.touch()
	if err := d.saveAccounts(); err != nil {
		d.log.Warn("drive: could not remember the account on screen", "error", err)
	}
	d.emitAccounts()
	return d.Status(), nil
}

// RenameAccount sets an account's local note: 1–24 characters once trimmed. It
// lives on this computer only; the server's nickname is not touched.
func (d *DriveService) RenameAccount(id, label string) (Account, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return Account{}, errLabelRequired
	}
	if utf8.RuneCountInString(label) > maxLabelRunes {
		return Account{}, errLabelTooLong
	}
	s := d.byKey(id)
	if s == nil {
		return Account{}, errNoSuchAccount
	}
	s.mu.Lock()
	before := s.rec.Label
	s.rec.Label = label
	s.mu.Unlock()
	if err := d.saveAccounts(); err != nil {
		s.mu.Lock()
		s.rec.Label = before
		s.mu.Unlock()
		return Account{}, err
	}
	d.emitAccounts()
	return s.view(d.isCurrent(s)), nil
}

// RemoveAccount signs an account out of this computer and forgets it: its
// stored session, remembered password, upload and download records, play
// record and index. Its transfers stop and unfinished downloads are deleted;
// nothing is asked of the server, so cloud tasks keep running there and the
// drive, its files and its shares are untouched. Finished downloads stay.
// When it was on screen, the most recently used account that is still
// signed in takes its place, or failing that any other; with none left
// there is no account on screen. If its session cannot be cleared, nothing
// is removed.
func (d *DriveService) RemoveAccount(id string) (DriveStatus, error) {
	s := d.byKey(id)
	if s == nil {
		return d.Status(), errNoSuchAccount
	}
	// Resolve accepted sync writes while this account's queues and credentials
	// are still available. Removing a relation never deletes completed files.
	if manager, err := d.syncManager(); err != nil {
		return d.Status(), err
	} else if err := manager.RemoveAccount(context.Background(), id); err != nil {
		return d.Status(), err
	}
	s.mu.RLock()
	tokens, rec := s.tokens, s.rec
	s.mu.RUnlock()
	if tokens != nil {
		if err := tokens.Forget(); err != nil {
			return d.Status(), i18n.Errorf("没能清除这个账号的登录信息，账号已保留：%w", err)
		}
	}

	d.idxMu.Lock()
	s.mu.Lock()
	uploads, downloads := s.uploads, s.downloads
	s.client, s.uploads, s.downloads, s.plays, s.password = nil, nil, nil, nil, ""
	s.mu.Unlock()
	d.closeIndexLocked(s)
	d.idxMu.Unlock()
	if uploads != nil {
		uploads.Close()
	}
	if downloads != nil {
		downloads.Purge()
	}

	d.mu.Lock()
	d.accounts = slices.DeleteFunc(d.accounts, func(o *accountSession) bool { return o == s })
	if d.current == s {
		d.current = d.fallbackLocked()
		if d.current != nil {
			d.current.touch()
		}
	}
	d.mu.Unlock()

	if d.vault != nil {
		if err := d.vault.DeletePassword(rec.Login); err != nil {
			d.log.Warn("drive: could not forget a remembered password", "error", err)
		}
	}
	if err := d.updateSettings(func(ls *LocalSettings) {
		if ls.SignInAccount == rec.Login {
			ls.SignInAccount = ""
		}
	}); err != nil {
		d.log.Warn("drive: could not save the sign-in choices", "error", err)
	}
	d.removeAccountFiles(s.key)
	if err := d.saveAccounts(); err != nil {
		d.log.Warn("drive: could not save the account list", "error", err)
	}
	d.emitAccounts()
	return d.Status(), nil
}

// fallbackLocked picks the account to show after the one on screen went:
// the most recently used that is signed in, else the most recently used.
// Caller holds d.mu.
func (d *DriveService) fallbackLocked() *accountSession {
	var best, bestReady *accountSession
	var bestAt, readyAt time.Time
	for _, s := range d.accounts {
		s.mu.RLock()
		stamp, ready := s.rec.UsedAt, !s.needsPassword
		s.mu.RUnlock()
		// An unreadable stamp is the zero time: used longest ago.
		at, _ := time.Parse(time.RFC3339Nano, stamp)
		if best == nil || at.After(bestAt) {
			best, bestAt = s, at
		}
		if ready && (bestReady == nil || at.After(readyAt)) {
			bestReady, readyAt = s, at
		}
	}
	if bestReady != nil {
		return bestReady
	}
	return best
}

// removeAccountFiles deletes what this computer kept of an account. The
// credentials lock file stays: it is never removed, so that processes
// sharing it agree on one file (drive.FileStore.Lock).
func (d *DriveService) removeAccountFiles(key string) {
	if d.configErr != nil {
		return
	}
	d.removeMediaLibraryFile(key)
	paths := []string{
		d.credentialsPath(key),
		d.accountFile(key, "index-", ".sqlite"),
		d.accountFile(key, "index-", ".sqlite-wal"),
		d.accountFile(key, "index-", ".sqlite-shm"),
		d.accountFile(key, "plays-", ".json"),
		d.accountFile(key, "uploads-", ".json"),
		d.accountFile(key, "downloads-", ".json"),
		d.organizePath(key),
		d.conversationsPath(key),
		d.inboxPath(key),
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			d.log.Warn("drive: could not remove an account file", "path", path, "error", err)
		}
	}
	resume := d.accountFile(key, "uploads-", ".resume")
	if err := os.RemoveAll(resume); err != nil {
		d.log.Warn("drive: could not remove an account file", "path", resume, "error", err)
	}
}

/* ---------------- lookups ---------------- */

// active is the session on screen.
func (d *DriveService) active() (*accountSession, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.current == nil {
		return nil, drive.ErrNoCredentials
	}
	return d.current, nil
}

// sessionFor is the session of account id; "" means the one on screen.
func (d *DriveService) sessionFor(id string) (*accountSession, error) {
	if id == "" {
		return d.active()
	}
	if s := d.byKey(id); s != nil {
		return s, nil
	}
	return nil, errNoSuchAccount
}

func (d *DriveService) byKey(key string) *accountSession {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.byKeyLocked(key)
}

func (d *DriveService) byKeyLocked(key string) *accountSession {
	for _, s := range d.accounts {
		if s.key == key {
			return s
		}
	}
	return nil
}

// byUserID finds the account a sign-in came back as; "" finds nothing.
func (d *DriveService) byUserID(userID string) *accountSession {
	if userID == "" {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, s := range d.accounts {
		if s.record().UserID == userID {
			return s
		}
	}
	return nil
}

func (d *DriveService) isCurrent(s *accountSession) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.current == s
}

func (d *DriveService) sessions() []*accountSession {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return slices.Clone(d.accounts)
}

// newKey names a new account's files after the device id it signs in
// with, unless another account already has that name.
func (d *DriveService) newKey(deviceID string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	key := deviceID
	for n := 2; d.byKeyLocked(key) != nil; n++ {
		key = deviceID + "-" + strconv.Itoa(n)
	}
	return key
}

/* ---------------- files ---------------- */

func (d *DriveService) accountsPath() string {
	return filepath.Join(d.configDir, accountsFileName)
}

// accountFile is one of an account's files in the config dir.
func (d *DriveService) accountFile(key, prefix, ext string) string {
	return filepath.Join(d.configDir, prefix+key+ext)
}

// credentialsPath is where an account's token manager keeps its session.
func (d *DriveService) credentialsPath(key string) string {
	return d.accountFile(key, "credentials-", ".json")
}

// credentialsStore is that file as a token store; nil without a config dir,
// when sessions live in memory only.
func (d *DriveService) credentialsStore(key string) drive.Store {
	if d.configErr != nil {
		return nil
	}
	return drive.FileStore{Path: d.credentialsPath(key)}
}

// saveAccounts writes the account list and which one is on screen.
func (d *DriveService) saveAccounts() error {
	if d.configErr != nil {
		return nil
	}
	d.mu.RLock()
	file := accountsFile{Version: accountsVersion, Accounts: make([]accountRecord, 0, len(d.accounts))}
	if d.current != nil {
		file.Current = d.current.key
	}
	for _, s := range d.accounts {
		file.Accounts = append(file.Accounts, s.record())
	}
	d.mu.RUnlock()

	d.accountsMu.Lock()
	defer d.accountsMu.Unlock()
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.configDir, 0o700); err != nil {
		return err
	}
	tmp := d.accountsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.accountsPath())
}

func readAccounts(path string) (accountsFile, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return accountsFile{}, false, nil
	}
	if err != nil {
		return accountsFile{}, true, err
	}
	var file accountsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return accountsFile{}, true, err
	}
	if file.Version != accountsVersion {
		return accountsFile{}, true, fmt.Errorf("accounts version %d is not %d", file.Version, accountsVersion)
	}
	return file, true, nil
}

// loadAccounts reads the account list. Without one, the single session of
// the layout it replaces becomes the first account: credentials.json moves
// to the account's own name, and its index, upload records and play record
// — already named by its device id — carry on as they were. A list that
// cannot be read is moved aside, like an unreadable upload journal; the
// credentials files stay, and signing in again finds the same files.
func (d *DriveService) loadAccounts() accountsFile {
	file, found, err := readAccounts(d.accountsPath())
	if err != nil {
		d.log.Warn("drive: account list unreadable, starting empty", "error", err)
		if err := os.Rename(d.accountsPath(), d.accountsPath()+".bad"); err != nil {
			d.log.Warn("drive: could not move the unreadable account list aside", "error", err)
		}
		return accountsFile{Version: accountsVersion}
	}
	if found {
		return file
	}
	return d.adoptLegacySession()
}

// adoptLegacySession turns the single-account session file into the first
// account's. Nothing to adopt is an empty list.
func (d *DriveService) adoptLegacySession() accountsFile {
	file := accountsFile{Version: accountsVersion}
	legacy := filepath.Join(d.configDir, legacyCredentials)
	tok, err := drive.FileStore{Path: legacy}.Load()
	if err != nil {
		d.log.Warn("drive: earlier session unreadable", "error", err)
		return file
	}
	if tok.Username == "" {
		return file
	}
	key := tok.DeviceID
	if key == "" {
		key = drive.DeviceIDFor(tok.Username)
	}
	if err := os.Rename(legacy, d.credentialsPath(key)); err != nil {
		d.log.Warn("drive: could not adopt the earlier session", "error", err)
		return file
	}
	settings := d.LocalSettings()
	remember := !settings.NoRememberSignIn && settings.SignInAccount == tok.Username
	now := time.Now().UTC().Format(time.RFC3339)
	file.Current = key
	file.Accounts = []accountRecord{{
		Key: key, UserID: tok.UserID, Login: tok.Username, DeviceID: key,
		Remember: remember, AutoSignIn: remember && !settings.NoAutoSignIn,
		AddedAt: now, UsedAt: now,
	}}
	if remember {
		// The account carries its own choice now.
		if err := d.updateSettings(func(s *LocalSettings) { s.SignInAccount = "" }); err != nil {
			d.log.Warn("drive: could not save the sign-in choices", "error", err)
		}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err == nil {
		err = os.WriteFile(d.accountsPath(), data, 0o600)
	}
	if err != nil {
		d.log.Warn("drive: could not save the account list", "error", err)
	}
	return file
}

/* ---------------- sessions ---------------- */

// restore brings every account back from its stored session without
// contacting the server: a cold start that is merely offline must still
// reach the UI. An account whose refresh token is gone needs its password,
// unless Auto Sign-In has one to sign in with on the first call.
func (d *DriveService) restore() error {
	if d.configErr != nil {
		return d.configErr
	}
	file := d.loadAccounts()
	for _, rec := range file.Accounts {
		if rec.Key == "" || d.byKey(rec.Key) != nil {
			continue
		}
		if rec.DeviceID == "" {
			rec.DeviceID = drive.DeviceIDFor(rec.Login)
		}
		s := &accountSession{key: rec.Key, rec: rec}
		d.mu.Lock()
		d.accounts = append(d.accounts, s)
		d.mu.Unlock()
		d.resume(s)
	}
	d.mu.Lock()
	d.current = d.byKeyLocked(file.Current)
	if d.current == nil && len(d.accounts) > 0 {
		d.current = d.fallbackLocked()
	}
	empty := d.current == nil
	d.mu.Unlock()
	if empty {
		return drive.ErrNoCredentials
	}
	return nil
}

// resume restores one account's session from its credentials file.
func (d *DriveService) resume(s *accountSession) {
	rec := s.record()
	tokens := d.newTokenManager(s, rec.DeviceID, d.profile(), d.credentialsStore(s.key))
	stored, err := tokens.Current()
	// Credentials are authoritative if a reauthentication persisted before the
	// separate account-list write. Keep both its issuing device and client.
	if err == nil {
		profile := tokens.Profile()
		if stored.ClientID != "" && stored.ClientID != profile.ClientID {
			if stored.ClientID == drive.WebClientID {
				profile = drive.WebProfile()
			} else {
				err = drive.ErrPasswordRequired
			}
		}
		deviceID := rec.DeviceID
		if stored.DeviceID != "" {
			deviceID = stored.DeviceID
		}
		if deviceID != rec.DeviceID || profile.ClientID != tokens.Profile().ClientID {
			s.mu.Lock()
			s.rec.DeviceID = deviceID
			s.mu.Unlock()
			tokens = d.newTokenManager(s, deviceID, profile, d.credentialsStore(s.key))
		}
	}
	switch {
	case err != nil:
		d.log.Warn("drive: stored session unreadable", "account", s.key, "error", err)
	case stored.RefreshToken != "" || (rec.AutoSignIn && d.rememberedPassword(s) != ""):
		// Auto Sign-In without a refresh token signs in with the remembered
		// password on the first call, the token manager's last rung.
		if err := d.buildSession(s, tokens); err == nil {
			return
		} else {
			d.log.Warn("drive: could not restore a session", "account", s.key, "error", err)
		}
	}
	s.mu.Lock()
	s.tokens = tokens
	s.needsPassword = true
	s.mu.Unlock()
	d.parkQueues(s)
}

// newTokenManager builds the token manager of an account's session. The
// password rung asks the session: the password this process signed in
// with, or the remembered one when Auto Sign-In is on.
func (d *DriveService) newTokenManager(s *accountSession, deviceID string, profile drive.ClientProfile, store drive.Store) *drive.TokenManager {
	return drive.NewTokenManager(drive.TokenManagerConfig{
		AuthBaseURL: d.opts.AuthBaseURL,
		Profile:     profile,
		DeviceID:    deviceID,
		Store:       store,
		Password:    func() (string, error) { return d.passwordFor(s) },
	})
}

// buildSession brings an account's client up on tokens, with its upload and
// download queues, play record and index.
func (d *DriveService) buildSession(s *accountSession, tokens *drive.TokenManager) error {
	profile := tokens.Profile()
	client, err := drive.New(drive.Config{
		APIBaseURL:  d.opts.APIBaseURL,
		AuthBaseURL: d.opts.AuthBaseURL,
		Profile:     profile,
		DeviceID:    tokens.DeviceID(),
		Captcha:     tokens.Captcha(),
		Logger:      d.log,
	}, tokens)
	if err != nil {
		return err
	}

	// The old queues write their journals before the new ones read them:
	// they are the same files. Close waits on jobs that call back into the
	// service, so no lock is held.
	s.mu.Lock()
	oldUploads, oldDownloads := s.uploads, s.downloads
	s.uploads, s.downloads = nil, nil
	s.mu.Unlock()
	if oldUploads != nil {
		oldUploads.Close()
	}
	if oldDownloads != nil {
		oldDownloads.Close()
	}
	settings := d.LocalSettings()
	uploads := upload.New(client, settings.ParallelUploads, func(t upload.Task) { d.publishUpload(s, t) })
	downloads := download.New(client, download.Options{
		Parallel:  settings.ParallelDownloads,
		Threads:   settings.DownloadThreads,
		UserAgent: profile.UserAgent,
		OnChange:  func(t download.Task) { d.publishDownload(s, t) },
	})
	if d.configErr == nil {
		uploads.Open(d.accountFile(s.key, "uploads-", ".json"), d.log)
		downloads.Open(d.accountFile(s.key, "downloads-", ".json"), d.log)
	}
	uploads.SetResumable(settings.ResumableUploads)

	s.mu.Lock()
	s.tokens = tokens
	s.client = client
	s.uploads = uploads
	s.downloads = downloads
	s.needsPassword = false
	if s.plays == nil {
		s.plays = openPlayLog(d.playsPath(s.key), d.log)
	}
	s.mu.Unlock()

	d.replaceIndex(s, client)
	return nil
}

// parkQueues lists an account's transfer records while it cannot run them:
// its journals in closed queues, which restore and show what was waiting but
// start nothing. Signing back in replaces them with working ones.
func (d *DriveService) parkQueues(s *accountSession) {
	uploads := upload.New(parkedBackend{}, 1, nil)
	downloads := download.New(parkedBackend{}, download.Options{Parallel: 1, Threads: 1})
	if d.configErr == nil {
		uploads.Open(d.accountFile(s.key, "uploads-", ".json"), d.log)
		downloads.Open(d.accountFile(s.key, "downloads-", ".json"), d.log)
	}
	uploads.Close()
	downloads.Close()
	s.mu.Lock()
	if s.uploads == nil {
		s.uploads = uploads
	}
	if s.downloads == nil {
		s.downloads = downloads
	}
	if s.plays == nil {
		s.plays = openPlayLog(d.playsPath(s.key), d.log)
	}
	s.mu.Unlock()
}

func (d *DriveService) playsPath(key string) string {
	if d.configErr != nil {
		return ""
	}
	return d.accountFile(key, "plays-", ".json")
}

// parkedBackend stands in for the client of an account that needs its
// password. Parked queues are closed and never call it; if they did, the
// answer would be the truth.
type parkedBackend struct{}

func (parkedBackend) CreateUpload(context.Context, drive.UploadParams) (*drive.UploadSlot, error) {
	return nil, drive.ErrPasswordRequired
}

func (parkedBackend) EnsureFolder(context.Context, string, string) (*drive.File, error) {
	return nil, drive.ErrPasswordRequired
}

func (parkedBackend) Trash(context.Context, []string) error {
	return drive.ErrPasswordRequired
}

func (parkedBackend) GetFile(context.Context, string, drive.GetFileParams) (*drive.File, error) {
	return nil, drive.ErrPasswordRequired
}

// noteProfile keeps the server's nickname and avatar with the account, for
// the switcher to show without asking the server for every account.
func (d *DriveService) noteProfile(s *accountSession, p *drive.Profile) {
	if p == nil {
		return
	}
	s.mu.Lock()
	changed := s.rec.Nickname != p.Name || s.rec.Avatar != p.Picture
	s.rec.Nickname, s.rec.Avatar = p.Name, p.Picture
	s.mu.Unlock()
	if !changed {
		return
	}
	if err := d.saveAccounts(); err != nil {
		d.log.Warn("drive: could not save the account list", "error", err)
	}
	d.emitAccounts()
}

// profileTimeout bounds the profile read that follows a sign-in: the
// nickname is nice to have on the switcher at once, not worth a slow
// sign-in.
const profileTimeout = 5 * time.Second

// refreshProfile reads an account's profile and keeps its nickname and
// avatar. A failure only means the switcher shows the masked login.
func (d *DriveService) refreshProfile(s *accountSession) {
	client, err := s.api()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), profileTimeout)
	defer cancel()
	p, err := client.Me(ctx)
	if err != nil {
		d.log.Debug("drive: profile unavailable", "account", s.key, "error", d.classify(s, client, err))
		return
	}
	d.noteProfile(s, p)
}

func (d *DriveService) emitAccounts() {
	d.closeUnavailableViewers()
	if app := application.Get(); app != nil {
		app.Event.Emit(EventAccounts)
	}
}
