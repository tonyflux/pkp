package drive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeviceAuthorizationIdentityAndNoCommit(t *testing.T) {
	profile := AndroidProfile()
	profile.ClientID = "configured-android-client"
	profile.UserAgent = "configured-android-agent"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.Header.Get("X-Client-Id") != profile.ClientID || r.Header.Get("X-Device-Id") != "stable-device" || r.UserAgent() != profile.UserAgent || r.Header.Get("X-Client-Version") != profile.ClientVersion {
			t.Errorf("request identity mismatch: %s %v", r.Method, r.Header)
		}
		for _, header := range []string{"Cookie", "Authorization", "X-Captcha-Token"} {
			if r.Header.Get(header) != "" {
				t.Errorf("unexpected %s header", header)
			}
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/v1/auth/device/code":
			if want := map[string]string{"client_id": profile.ClientID, "scope": "user"}; !reflect.DeepEqual(body, want) {
				t.Errorf("challenge body = %v, want %v", body, want)
			}
			writeJSON(w, map[string]any{"device_code": "private-challenge", "verification_uri_complete": "https://mypikpak.com/authorize?code=qr", "expires_in": 300, "interval": "2"})
		case "/v1/auth/token":
			if want := map[string]string{"client_id": profile.ClientID, "grant_type": "urn:ietf:params:oauth:grant-type:device_code", "device_code": "private-challenge"}; !reflect.DeepEqual(body, want) {
				t.Errorf("poll body = %v, want %v", body, want)
			}
			if requests == 2 {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": "authorization_pending", "error_code": 4050})
				return
			}
			writeJSON(w, map[string]any{"access_token": "access", "refresh_token": "refresh", "sub": "account-sub", "token_type": "Bearer", "expires_in": "3600"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	endpoint, _ := url.Parse(server.URL)
	jar.SetCookies(endpoint, []*http.Cookie{{Name: "existing", Value: "cookie"}})
	client := server.Client()
	client.Jar = jar
	store := &MemoryStore{}
	original := Token{AccessToken: "old-access", RefreshToken: "old-refresh", UserID: "old-sub"}
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	manager := NewTokenManager(TokenManagerConfig{AuthBaseURL: server.URL, Profile: profile, DeviceID: "stable-device", HTTP: client, Store: store, Password: func() (string, error) {
		t.Error("device authorization must not request a password")
		return "", errors.New("unexpected password request")
	}})
	manager.captcha.set("must-not-be-sent")
	before := time.Now()
	challenge, err := manager.BeginDeviceAuthorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if challenge.DeviceCode != "private-challenge" || challenge.URL != "https://mypikpak.com/authorize?code=qr" || challenge.Interval != 2*time.Second || challenge.ExpiresAt.Before(before.Add(300*time.Second)) || challenge.ExpiresAt.After(time.Now().Add(300*time.Second)) {
		t.Fatalf("invalid challenge: %#v", challenge)
	}
	for _, wantState := range []string{"waiting", "complete"} {
		result, err := manager.PollDeviceAuthorization(context.Background(), challenge.DeviceCode)
		if err != nil || result.State != wantState {
			t.Fatalf("poll state = %s, err = %v; want %s", result.State, err, wantState)
		}
		if wantState == "waiting" && result.Token != (Token{}) {
			t.Error("pending authorization returned credentials")
		}
		if wantState == "complete" && (result.Token.AccessToken != "access" || result.Token.RefreshToken != "refresh" || result.Token.UserID != "account-sub" || result.Token.DeviceID != "stable-device" || result.Token.TokenType != "Bearer" || !result.Token.Usable()) {
			t.Errorf("completed token is incomplete: %#v", result.Token)
		}
		stored, err := store.Load()
		if err != nil || stored != original || manager.token != (Token{}) || manager.loaded {
			t.Fatalf("device authorization committed credentials: stored=%#v, err=%v", stored, err)
		}
	}
	if client.Jar != jar {
		t.Error("device request mutated configured HTTP client")
	}
}

func TestDeviceAuthorizationPollStatesAndPrivacy(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		state  string
		code   int
	}{
		{"pending", 400, `{"error":"authorization_pending","error_code":4050,"error_description":"Precondition Required","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"Precondition Required"},{"@type":"type.googleapis.com/google.rpc.LocalizedMessage","locale":"en","message":"Authorizing"}]}`, "waiting", 0},
		{"scanned", 400, `{"error":"authorization_pending","error_code":4050,"details":[{"state":"WAITING_CONSENT"}]}`, "scanned", 0},
		{"pending success envelope", 200, `{"error":"authorization_pending","error_code":4050}`, "waiting", 0},
		{"slow down", 400, `{"error":"slow_down","error_code":4002}`, "slow_down", 0},
		{"expired", 400, `{"error":"expired_token","error_code":4002}`, "expired", 0},
		{"denied", 400, `{"error":"access_denied","error_code":4002}`, "denied", 0},
		{"unknown code 4002", 400, `{"error":"invalid_grant","error_code":4002,"error_description":"private-challenge"}`, "", 4002},
		{"unknown success envelope", 200, `{"error":"private-challenge","error_code":9876,"error_description":"private-challenge"}`, "", 9876},
		{"code only", 400, `{"error_code":4050,"details":[{"state":"WAITING_CONSENT"}]}`, "", 4050},
		{"gateway", 502, `private-challenge`, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			manager := NewTokenManager(TokenManagerConfig{AuthBaseURL: server.URL, HTTP: server.Client()})
			result, err := manager.PollDeviceAuthorization(context.Background(), "private-challenge")
			if tc.state != "" {
				if err != nil || result.State != tc.state || result.Token != (Token{}) {
					t.Fatalf("result = %#v, err = %v; want state %s", result, err, tc.state)
				}
				return
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.Status != tc.status {
				t.Fatalf("error = %v; want status %d, code %d", err, tc.status, tc.code)
			}
			if strings.Contains(err.Error(), "private-challenge") {
				t.Fatalf("auth error leaked response: %v", err)
			}
		})
	}
}

func TestDeviceAuthorizationRejectsIncompleteResponses(t *testing.T) {
	cases := []struct {
		name  string
		begin bool
		body  map[string]any
	}{
		{"missing device code", true, map[string]any{"verification_uri_complete": "https://example.com/auth", "expires_in": 300, "interval": 2}},
		{"missing authorization URL", true, map[string]any{"device_code": "code", "expires_in": 300, "interval": 2}},
		{"unsafe authorization URL", true, map[string]any{"device_code": "code", "verification_uri_complete": "javascript:alert(1)", "expires_in": 300, "interval": 2}},
		{"missing expiry", true, map[string]any{"device_code": "code", "verification_uri_complete": "https://example.com/auth", "interval": 2}},
		{"zero interval", true, map[string]any{"device_code": "code", "verification_uri_complete": "https://example.com/auth", "expires_in": 300, "interval": 0}},
		{"interval exceeds expiry", true, map[string]any{"device_code": "code", "verification_uri_complete": "https://example.com/auth", "expires_in": 1, "interval": 2}},
		{"missing access", false, map[string]any{"refresh_token": "refresh", "sub": "sub", "expires_in": 3600}},
		{"missing refresh", false, map[string]any{"access_token": "access", "sub": "sub", "expires_in": 3600}},
		{"missing subject", false, map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600}},
		{"blank subject", false, map[string]any{"access_token": "access", "refresh_token": "refresh", "sub": " ", "expires_in": 3600}},
		{"missing token expiry", false, map[string]any{"access_token": "access", "refresh_token": "refresh", "sub": "sub"}},
		{"unusable token expiry", false, map[string]any{"access_token": "access", "refresh_token": "refresh", "sub": "sub", "expires_in": 60}},
		{"negative token expiry", false, map[string]any{"access_token": "access", "refresh_token": "refresh", "sub": "sub", "expires_in": -1}},
		{"overflow token expiry", false, map[string]any{"access_token": "access", "refresh_token": "refresh", "sub": "sub", "expires_in": "9223372036854775807"}},
		{"fractional token expiry", false, map[string]any{"access_token": "access", "refresh_token": "refresh", "sub": "sub", "expires_in": 3600.5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tc.body) }))
			defer server.Close()
			manager := NewTokenManager(TokenManagerConfig{AuthBaseURL: server.URL, HTTP: server.Client()})
			var err error
			if tc.begin {
				_, err = manager.BeginDeviceAuthorization(context.Background())
			} else {
				_, err = manager.PollDeviceAuthorization(context.Background(), "code")
			}
			if err == nil {
				t.Fatal("incomplete response accepted")
			}
			if manager.loaded || manager.token != (Token{}) {
				t.Fatal("incomplete response changed manager credentials")
			}
		})
	}
}

func TestDeviceAuthorizationCancellationAndEmptyCode(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	manager := NewTokenManager(TokenManagerConfig{AuthBaseURL: server.URL, HTTP: server.Client()})
	if _, err := manager.PollDeviceAuthorization(context.Background(), " "); err == nil {
		t.Error("empty code accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.BeginDeviceAuthorization(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("begin cancellation = %v", err)
	}
	if _, err := manager.PollDeviceAuthorization(ctx, "code"); !errors.Is(err, context.Canceled) {
		t.Errorf("poll cancellation = %v", err)
	}
	if requests != 0 {
		t.Errorf("invalid/cancelled requests reached endpoint: %d", requests)
	}
}
