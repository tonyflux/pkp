package services

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/tonyflux/pkp/internal/i18n"
)

// Standalone PKP password: a password of this computer's PKP alone,
// unrelated to the PikPak account and never sent anywhere. Only an Argon2id
// verifier with a random salt is kept — in the system's credential store
// (lockSecretStore) — so the password itself can be neither read back nor
// reversed. Wrong guesses at it are counted across restarts: five in a row
// cost a cooldown that doubles with every round.

const (
	lockMethodSystem   = "system"
	lockMethodPassword = "password"

	minLockPassword = 8
	maxLockPassword = 128

	// lockMaxFails wrong passwords in a row start a cooldown of
	// lockCooldown, doubling each further round up to lockCooldownCap.
	lockMaxFails    = 5
	lockCooldown    = 30 * time.Second
	lockCooldownCap = 15 * time.Minute
)

// Argon2id's cost (RFC 9106's second recommendation): about a tenth of a
// second here, so a guess is slow and an unlock is not.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// lockSecretStore keeps the verifier: the login keychain on macOS, a file
// sealed with DPAPI for this Windows user, elsewhere a file only this user
// may read.
type lockSecretStore interface {
	// Load returns the verifier, or errNoLockSecret.
	Load() (string, error)
	Save(verifier string) error
	// Delete removes it; having none is not an error.
	Delete() error
}

var errNoLockSecret = errors.New("applock: no password set on this computer")

// LockThrottle counts wrong passwords; it lives in settings.json, so quitting
// does not reset it.
type LockThrottle struct {
	// Fails since the last success or cooldown.
	Fails int `json:"fails,omitempty"`
	// Rounds of lockMaxFails since the last success: the next cooldown is
	// lockCooldown doubled this many times.
	Rounds int `json:"rounds,omitempty"`
	// Until is the end of the cooldown under way, unix milliseconds.
	Until int64 `json:"until,omitempty"`
}

// cooling is how much of the cooldown is left at now; 0 when none.
func (t LockThrottle) cooling(now time.Time) time.Duration {
	if t.Until == 0 {
		return 0
	}
	return max(0, time.UnixMilli(t.Until).Sub(now))
}

// attemptsLeft is how many wrong passwords remain before the next cooldown.
func (t LockThrottle) attemptsLeft() int {
	return max(0, lockMaxFails-t.Fails)
}

// failed counts one wrong password at now.
func (t LockThrottle) failed(now time.Time) LockThrottle {
	t.Fails++
	if t.Fails < lockMaxFails {
		return t
	}
	wait := lockCooldown << min(t.Rounds, 10)
	t.Fails = 0
	t.Rounds++
	t.Until = now.Add(min(wait, lockCooldownCap)).UnixMilli()
	return t
}

// lockPasswordProblem says what is wrong with a new password; "" when it may
// be used. Only the length counts: it stays on this computer, and it is the
// owner's to choose.
func lockPasswordProblem(password string) string {
	switch n := utf8.RuneCountInString(password); {
	case n < minLockPassword:
		return i18n.F("密码至少 %d 位", minLockPassword)
	case n > maxLockPassword:
		return i18n.F("密码最多 %d 位", maxLockPassword)
	}
	return ""
}

// newLockVerifier derives the PHC-format verifier of password with a fresh
// random salt.
func newLockVerifier(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// lockVerifierMatches reports whether password is the one verifier was made
// from, in time independent of where they differ. The cost is read from the
// verifier, so a later build may raise its own without locking anyone out.
func lockVerifierMatches(verifier, password string) (bool, error) {
	parts := strings.Split(verifier, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("applock: the stored password record is not one PKP wrote")
	}
	var version int
	var memory, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("applock: the stored password record has an unknown version")
	}
	// Bounded, so a damaged record cannot ask for gigabytes or minutes.
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil ||
		passes == 0 || passes > 16 || threads == 0 || memory < 8*uint32(threads) || memory > 1<<20 {
		return false, errors.New("applock: the stored password record has unreadable parameters")
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("applock: the stored password record is damaged")
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errors.New("applock: the stored password record is damaged")
	}
	got := argon2.IDKey([]byte(password), salt, passes, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
