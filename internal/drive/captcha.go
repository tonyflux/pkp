package drive

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// signPrefix is the algorithm version the server expects in front of the
// digest.
const signPrefix = "1."

// CaptchaSource supplies the X-Captcha-Token header and replaces it when the
// server rejects one.
type CaptchaSource interface {
	// Token returns the current token, empty when none has been minted.
	Token() string
	// Refresh mints a new token for action, unless rejected is already stale —
	// that is, another caller replaced it first.
	Refresh(ctx context.Context, action, rejected string) error
}

// signCaptcha folds the identity and a timestamp through the profile's salt
// cascade. Each round hashes the previous digest concatenated with the next
// salt, so both the salt order and the seed layout are part of the contract.
func signCaptcha(profile ClientProfile, deviceID string, timestampMillis int64) string {
	seed := profile.ClientID + profile.ClientVersion + profile.PackageName + deviceID +
		strconv.FormatInt(timestampMillis, 10)

	digest := seed
	for _, salt := range profile.CaptchaSalts {
		sum := md5.Sum([]byte(digest + salt))
		digest = hex.EncodeToString(sum[:])
	}
	return signPrefix + digest
}

// DeviceIDFor derives the device identifier from an account name.
//
// Deriving instead of randomising is deliberate: the same account reaching the
// server from the same machine after a reinstall keeps one device identity,
// and the value is reproducible by any client that follows the same
// convention, so moving between tools does not look like a new device every
// time.
func DeviceIDFor(account string) string {
	sum := md5.Sum([]byte(account))
	return hex.EncodeToString(sum[:])
}

// captchaManager owns the current captcha token. Refreshes are serialised and
// deduplicated: when a dozen in-flight requests are all rejected over the same
// token, exactly one of them mints a replacement and the rest adopt it.
type captchaManager struct {
	profile  ClientProfile
	deviceID string
	authBase string
	http     *http.Client
	// userID is consulted lazily because a token is minted before sign-in,
	// when no user id exists yet.
	userID func() string

	mu    sync.Mutex
	token string
}

func newCaptchaManager(profile ClientProfile, deviceID, authBase string, httpClient *http.Client, userID func() string) *captchaManager {
	if userID == nil {
		userID = func() string { return "" }
	}
	return &captchaManager{
		profile:  profile,
		deviceID: deviceID,
		authBase: authBase,
		http:     httpClient,
		userID:   userID,
	}
}

func (m *captchaManager) Token() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token
}

func (m *captchaManager) set(token string) {
	m.mu.Lock()
	m.token = token
	m.mu.Unlock()
}

// Refresh replaces rejected, the token the failed request carried. The caller
// passes the rejected value rather than letting this read the current one: a
// goroutine arriving after someone else's refresh would otherwise read the
// fresh token and refresh that in turn.
func (m *captchaManager) Refresh(ctx context.Context, action, rejected string) error {
	// Lock order is TokenManager.mu → captchaManager.mu (exchange reads the
	// token with its mutex held), so the user id, which takes TokenManager.mu,
	// must be read before ours is taken.
	uid := m.userID()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != rejected {
		return nil
	}

	timestamp := time.Now().UnixMilli()
	body := map[string]any{
		"action":        action,
		"captcha_token": m.token,
		"client_id":     m.profile.ClientID,
		"device_id":     m.deviceID,
		"meta": map[string]any{
			"captcha_sign":   signCaptcha(m.profile, m.deviceID, timestamp),
			"user_id":        uid,
			"package_name":   m.profile.PackageName,
			"client_version": m.profile.ClientVersion,
			"timestamp":      strconv.FormatInt(timestamp, 10),
		},
	}

	token, err := m.initCaptcha(ctx, body)
	if err != nil {
		return err
	}
	m.token = token
	return nil
}

// initForSignIn mints the token the password sign-in needs. The sign-in
// handshake is identified by the account rather than by a signature: there is
// no session yet to sign with.
func (m *captchaManager) initForSignIn(ctx context.Context, username string) (string, error) {
	body := map[string]any{
		"client_id": m.profile.ClientID,
		"device_id": m.deviceID,
		"action":    signInAction,
		"meta":      map[string]any{"username": username},
	}
	token, err := m.initCaptcha(ctx, body)
	if err != nil {
		return "", err
	}
	m.set(token)
	return token, nil
}

type captchaInitResponse struct {
	CaptchaToken string `json:"captcha_token"`
	ExpiresIn    int    `json:"expires_in"`
	// URL is non-empty when risk control wants a human: the token only becomes
	// usable after that page is completed.
	URL string `json:"url"`
}

func (m *captchaManager) initCaptcha(ctx context.Context, body map[string]any) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	endpoint := m.authBase + "/v1/shield/captcha/init?client_id=" + m.profile.ClientID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	m.profile.setHeaders(req.Header, m.deviceID)
	req.Header.Set("Content-Type", "application/json")

	client := m.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("drive: captcha init: %w", err)
	}
	raw, _, err := decodeResponse(http.MethodPost, "/v1/shield/captcha/init", resp)
	if err != nil {
		return "", err
	}

	var parsed captchaInitResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("drive: decode captcha init: %w", err)
	}
	if parsed.URL != "" {
		return "", &ChallengeError{URL: parsed.URL}
	}
	if parsed.CaptchaToken == "" {
		return "", errors.New("drive: captcha init returned no token")
	}
	return parsed.CaptchaToken, nil
}

// ChallengeError means risk control wants a human to complete a verification
// page before this client may continue. There is nothing to retry.
type ChallengeError struct {
	URL string
}

func (e *ChallengeError) Error() string {
	return "drive: risk control requires a manual verification at " + e.URL
}

// IsChallenge reports whether the failure is a human-verification demand.
func IsChallenge(err error) bool {
	var challenge *ChallengeError
	return errors.As(err, &challenge)
}
