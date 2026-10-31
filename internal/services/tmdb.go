package services

// The TMDB credential behind Media Organization's title matching (MatchTitles). It
// is the user's own, lives in the keychain only and never reaches the
// webview: the page learns its last characters, nothing more. Without
// one, TMDB is not asked and Media Organization goes by its rules and the model.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/tmdb"
)

// tmdbKeyAccount names the TMDB credential's keychain item, beside the AI
// key and the Remember Account Password passwords (named by login, never so).
const tmdbKeyAccount = "pkp-tmdb-key"

var (
	errTMDBNoKeychain = &lazyError{func() string {
		return i18n.F("这台电脑没有可用的%s，无法保存 TMDB 凭据", keychainName())
	}}
	errTMDBKeyStore   = &lazyError{func() string { return i18n.F("无法把 TMDB 凭据存进%s", keychainName()) }}
	errTMDBKeyForget  = &lazyError{func() string { return i18n.F("无法从%s删除 TMDB 凭据", keychainName()) }}
	errTMDBKeyFormat  = i18n.New("TMDB 凭据不能包含空格或换行")
	errTMDBKeyTooLong = i18n.New("TMDB 凭据太长")
)

// TMDBSettings is the TMDB card of Settings · AI as the page shows it.
type TMDBSettings struct {
	// TMDBKeyHint is "••••abcd", "" when no credential is stored.
	TMDBKeyHint string `json:"tmdbKeyHint"`
	// TMDBReady is whether TMDB can be asked at all: a credential is
	// stored.
	TMDBReady bool `json:"tmdbReady"`
	// Keychain is false where there is none: no credential can be kept.
	Keychain bool `json:"keychain"`
}

// TMDBTestResult is what Test Connection found. Kind is ok, auth, unreachable,
// server, ratelimit or config.
type TMDBTestResult struct {
	OK      bool   `json:"ok"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// tmdbState is TMDB's share of DriveService.
type tmdbState struct {
	client *tmdb.Client
	// key is the TMDB credential as last read from or written to the
	// keychain. Reading it execs the security tool, so it is read once.
	keyMu   sync.Mutex
	key     string
	keyRead bool
}

func newTMDBState(opts DriveOptions) *tmdbState {
	return &tmdbState{client: tmdb.New(opts.TMDBBaseURL, opts.TMDBHTTP)}
}

// TMDBSettings reads the TMDB card.
func (d *DriveService) TMDBSettings() TMDBSettings {
	out := TMDBSettings{Keychain: d.vault != nil}
	if key := d.tmdbKey(); key != "" {
		out.TMDBKeyHint = keyHint(key)
	}
	out.TMDBReady = out.TMDBKeyHint != ""
	return out
}

// SetTMDBKey stores the TMDB credential (a v3 API key or a v4 read access
// token) in the keychain; "" deletes it. Cached TMDB answers go with the
// old one.
func (d *DriveService) SetTMDBKey(key string) (TMDBSettings, error) {
	err := d.storeTMDBKey(strings.TrimSpace(key))
	return d.TMDBSettings(), err
}

func (d *DriveService) storeTMDBKey(key string) error {
	if key != "" {
		if d.vault == nil {
			return errTMDBNoKeychain
		}
		if strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return errTMDBKeyFormat
		}
		if len(key) > maxAIKeyBytes {
			return errTMDBKeyTooLong
		}
	}
	m := d.tmdb
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	if key == "" {
		if d.vault != nil {
			if err := d.vault.DeletePassword(tmdbKeyAccount); err != nil {
				d.log.Warn("tmdb: the TMDB key could not be deleted", "error", err)
				return errTMDBKeyForget
			}
		}
	} else if err := d.vault.SetPassword(tmdbKeyAccount, key); err != nil {
		// The security tool's complaint may quote what it was given.
		d.log.Warn("tmdb: the keychain did not take the TMDB key")
		return errTMDBKeyStore
	}
	m.key, m.keyRead = key, true
	m.client.Reset()
	return nil
}

// tmdbKey is the stored credential, "" without one. A keychain that
// cannot be read now is asked again next time.
func (d *DriveService) tmdbKey() string {
	m := d.tmdb
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	if m.keyRead || d.vault == nil {
		return m.key
	}
	key, err := d.vault.Password(tmdbKeyAccount)
	switch {
	case err == nil:
		m.key, m.keyRead = key, true
	case errors.Is(err, ErrNoSavedPassword):
		m.key, m.keyRead = "", true
	default:
		d.log.Warn("tmdb: the TMDB key could not be read from the keychain", "error", err)
	}
	return m.key
}

// TestTMDB reads TMDB's configuration the way MatchTitles reaches TMDB
// (with the stored credential) and says what came of it. It never fails:
// the outcome is the answer.
func (d *DriveService) TestTMDB() TMDBTestResult {
	err := d.tmdb.client.Test(context.Background(), d.tmdbKey())
	if err == nil {
		return TMDBTestResult{OK: true, Kind: "ok"}
	}
	kind := tmdb.KindServer
	var e *tmdb.Error
	if errors.As(err, &e) {
		switch e.Kind {
		case tmdb.KindConfig, tmdb.KindAuth, tmdb.KindUnreachable, tmdb.KindRateLimit:
			kind = e.Kind
		}
	}
	return TMDBTestResult{Kind: kind, Message: err.Error()}
}
