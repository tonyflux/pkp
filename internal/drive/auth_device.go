package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DeviceAuthorization is a short-lived authorization challenge, not a session.
// DeviceCode must stay in memory; only URL is intended for the QR display.
type DeviceAuthorization struct {
	DeviceCode string
	URL        string
	ExpiresAt  time.Time
	Interval   time.Duration
}

// DeviceAuthorizationResult describes one poll without installing credentials.
type DeviceAuthorizationResult struct {
	State string
	Token Token
}

// BeginDeviceAuthorization requests a challenge using this manager's identity.
func (m *TokenManager) BeginDeviceAuthorization(ctx context.Context) (DeviceAuthorization, error) {
	raw, _, err := m.deviceAuthorizationRequest(ctx, "/v1/auth/device/code", map[string]string{
		"client_id": m.cfg.Profile.ClientID,
		"scope":     "user",
	})
	if err != nil {
		return DeviceAuthorization{}, err
	}
	var response struct {
		DeviceCode string          `json:"device_code"`
		URL        string          `json:"verification_uri_complete"`
		ExpiresIn  json.RawMessage `json:"expires_in"`
		Interval   json.RawMessage `json:"interval"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return DeviceAuthorization{}, errors.New("drive: invalid device authorization response")
	}
	expiry, expiryOK := deviceDuration(response.ExpiresIn)
	interval, intervalOK := deviceDuration(response.Interval)
	u, urlErr := url.Parse(response.URL)
	if strings.TrimSpace(response.DeviceCode) == "" || urlErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || !expiryOK || !intervalOK || interval > expiry {
		return DeviceAuthorization{}, errors.New("drive: incomplete device authorization response")
	}
	return DeviceAuthorization{
		DeviceCode: response.DeviceCode,
		URL:        response.URL,
		ExpiresAt:  time.Now().Add(expiry),
		Interval:   interval,
	}, nil
}

// PollDeviceAuthorization exchanges a challenge for credentials. The caller
// checks the account identity before explicitly installing the returned token.
func (m *TokenManager) PollDeviceAuthorization(ctx context.Context, deviceCode string) (DeviceAuthorizationResult, error) {
	if strings.TrimSpace(deviceCode) == "" {
		return DeviceAuthorizationResult{}, errors.New("drive: device code is required")
	}
	raw, state, err := m.deviceAuthorizationRequest(ctx, "/v1/auth/token", map[string]string{
		"client_id":   m.cfg.Profile.ClientID,
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": deviceCode,
	})
	if err != nil {
		if apiErr := asAPIError(err); apiErr != nil {
			switch apiErr.Name {
			case "authorization_pending":
				if state == "WAITING_CONSENT" {
					return DeviceAuthorizationResult{State: "scanned"}, nil
				}
				return DeviceAuthorizationResult{State: "waiting"}, nil
			case "slow_down":
				return DeviceAuthorizationResult{State: "slow_down"}, nil
			case "expired_token":
				return DeviceAuthorizationResult{State: "expired"}, nil
			case "access_denied":
				return DeviceAuthorizationResult{State: "denied"}, nil
			}
		}
		return DeviceAuthorizationResult{}, err
	}
	var response struct {
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		TokenType    string          `json:"token_type"`
		Sub          string          `json:"sub"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return DeviceAuthorizationResult{}, errors.New("drive: invalid device token response")
	}
	expiry, ok := deviceDuration(response.ExpiresIn)
	if strings.TrimSpace(response.AccessToken) == "" || strings.TrimSpace(response.RefreshToken) == "" || strings.TrimSpace(response.Sub) == "" || !ok || expiry <= refreshLeeway {
		return DeviceAuthorizationResult{}, errors.New("drive: incomplete device token response")
	}
	return DeviceAuthorizationResult{State: "complete", Token: Token{
		DeviceID:     m.cfg.DeviceID,
		ClientID:     m.cfg.Profile.ClientID,
		AccessToken:  response.AccessToken,
		RefreshToken: response.RefreshToken,
		TokenType:    response.TokenType,
		UserID:       response.Sub,
		ExpiresAt:    time.Now().Add(expiry),
	}}, nil
}

// deviceDuration accepts the numeric and string seconds used by auth responses,
// without allowing truncation, negative lifetimes or time.Duration overflow.
func deviceDuration(raw json.RawMessage) (time.Duration, bool) {
	value := string(raw)
	if len(value) > 0 && value[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return 0, false
		}
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 || seconds > int64((1<<63-1)/time.Second) {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

// deviceAuthorizationRequest deliberately excludes captcha, passwords and
// cookies. Unlike general API errors, auth errors never retain response text:
// providers and gateways may echo the challenge or credentials in that text.
func (m *TokenManager) deviceAuthorizationRequest(ctx context.Context, path string, body map[string]string) ([]byte, string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.AuthBaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, "", errors.New("drive: invalid device authorization endpoint")
	}
	m.cfg.Profile.setHeaders(req.Header, m.cfg.DeviceID)
	req.Header.Set("Content-Type", "application/json")
	client := *m.cfg.HTTP
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("drive: %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, "", fmt.Errorf("drive: read %s response: %w", path, err)
	}
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Code    int             `json:"error_code"`
		Details []struct {
			State string `json:"state"`
		} `json:"details"`
	}
	decodeErr := json.Unmarshal(raw, &envelope)
	name := ""
	_ = json.Unmarshal(envelope.Error, &name)
	if resp.StatusCode/100 == 2 && decodeErr == nil && name == "" && envelope.Code == 0 && (len(envelope.Error) == 0 || string(envelope.Error) == "null") {
		return raw, "", nil
	}
	state := ""
	if len(envelope.Details) > 0 && envelope.Details[0].State == "WAITING_CONSENT" {
		state = "WAITING_CONSENT"
	}
	switch name {
	case "authorization_pending", "slow_down", "expired_token", "access_denied":
	default:
		name = "device_authorization_error"
	}
	return nil, state, &APIError{
		Status: resp.StatusCode, Method: http.MethodPost, Path: path,
		Name: name, Code: envelope.Code, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
}
