// Package drive is a from-scratch client for the PikPak drive HTTP API.
//
// It depends on the standard library only, save golang.org/x/sys for the
// credentials-file lock on Windows. Everything the server needs —
// bearer token, captcha token, device identity, client identity — is attached
// per request by the client; callers deal in Go types.
//
// The client built here follows the app's proxy setting (internal/netproxy),
// so switching proxies in Settings reaches the API and the token refreshes at
// once. A caller that injects its own HTTPClient owns that choice itself.
package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tonyflux/pkp/internal/netproxy"
)

const (
	// DefaultAPIBaseURL is the drive API host.
	DefaultAPIBaseURL = "https://api-drive.mypikpak.com"
	// DefaultAuthBaseURL is the authorisation host.
	DefaultAuthBaseURL = "https://user.mypikpak.com"
	// DefaultDAVBaseURL is the WebDAV management host.
	DefaultDAVBaseURL = "https://api-dav.mypikpak.com"

	// maxResponseBytes caps a decoded JSON body. Real responses are a few
	// hundred KiB at most; anything larger is a proxy error page.
	maxResponseBytes = 8 << 20

	// idempotentRetries is how many extra attempts a GET gets after a
	// transport error, a 5xx or a stall.
	idempotentRetries = 2
)

// stallTimeout is how long a GET gives the server to start answering before
// that attempt is dropped and the request sent again. The API mostly
// answers within a second and a 500-task page within a few; now and then a
// request sits 11–15 s before the first byte while the same request sent
// again answers in half a second (/drive/v1/tasks, measured 2026-10-15).
// The last attempt waits as long as the caller allows: a slow answer beats
// none. A variable so tests can shorten it.
var stallTimeout = 10 * time.Second

// errStalled is an attempt the server did not start answering in time.
var errStalled = errors.New("no response in time")

// Config describes how the client identifies itself.
type Config struct {
	// APIBaseURL, AuthBaseURL and DAVBaseURL default to the production
	// hosts. Point them at a mock or a staging environment in tests.
	APIBaseURL  string
	AuthBaseURL string
	DAVBaseURL  string

	// Profile is the application identity. It defaults to AndroidProfile.
	Profile ClientProfile
	// DeviceID must be stable for an account. DeviceIDFor derives one.
	DeviceID string

	// Captcha supplies and refreshes the X-Captcha-Token header. Without it
	// the client cannot recover from a risk-control rejection.
	Captcha CaptchaSource

	HTTPClient *http.Client
	Logger     *slog.Logger

	// OnQuota, if set, receives every throttling snapshot the server sends.
	// It runs on the request goroutine, so it must not block.
	OnQuota func(QuotaSnapshot)
}

// Client talks to the drive API. It is safe for concurrent use.
type Client struct {
	cfg    Config
	api    *url.URL
	auth   *url.URL
	dav    *url.URL
	http   *http.Client
	tokens TokenSource
	log    *slog.Logger
}

// New validates the configuration and returns a client. tokens may be nil for
// endpoints that need no authorisation.
func New(cfg Config, tokens TokenSource) (*Client, error) {
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = DefaultAPIBaseURL
	}
	if cfg.AuthBaseURL == "" {
		cfg.AuthBaseURL = DefaultAuthBaseURL
	}
	if cfg.DAVBaseURL == "" {
		cfg.DAVBaseURL = DefaultDAVBaseURL
	}
	if !cfg.Profile.Valid() {
		cfg.Profile = AndroidProfile()
	}
	if cfg.DeviceID == "" {
		return nil, errors.New("drive: DeviceID is required and must be stable for the account")
	}

	api, err := url.Parse(strings.TrimRight(cfg.APIBaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("drive: bad APIBaseURL: %w", err)
	}
	auth, err := url.Parse(strings.TrimRight(cfg.AuthBaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("drive: bad AuthBaseURL: %w", err)
	}
	dav, err := url.Parse(strings.TrimRight(cfg.DAVBaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("drive: bad DAVBaseURL: %w", err)
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
			Transport: netproxy.Install(&http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          128,
				MaxIdleConnsPerHost:   32,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
			}),
		}
	}

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	return &Client{cfg: cfg, api: api, auth: auth, dav: dav, http: httpClient, tokens: tokens, log: log}, nil
}

// Config returns the effective configuration.
func (c *Client) Config() Config { return c.cfg }

// request is one API call.
type request struct {
	method string
	path   string
	query  url.Values
	// body is marshalled to JSON when non-nil.
	body any
	// anonymous skips the Authorization header (share endpoints).
	anonymous bool
	// base overrides the drive API host; nil means c.api. The WebDAV
	// management service and the account service live on their own hosts
	// with the same auth.
	base *url.URL
}

// captchaAction is the label risk control matches a captcha token against.
func (r request) captchaAction() string { return r.method + ":" + r.path }

func (c *Client) do(ctx context.Context, req request, out any) error {
	var payload []byte
	if req.body != nil {
		var err error
		payload, err = json.Marshal(req.body)
		if err != nil {
			return fmt.Errorf("drive: encode %s %s: %w", req.method, req.path, err)
		}
	}

	base := c.api
	if req.base != nil {
		base = req.base
	}
	// Concatenate rather than assign url.URL.Path: callers already
	// PathEscape ids, and String() would encode the '%' a second time.
	endpoint := base.String() + req.path
	if len(req.query) > 0 {
		endpoint += "?" + req.query.Encode()
	}

	// A GET carries no side effect, so it may be replayed after a transport
	// error, a 5xx or a stall. Anything else is sent exactly once, and waits
	// as long as the caller allows.
	attempts := 1
	if req.method == http.MethodGet {
		attempts += idempotentRetries
	}

	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff(attempt)); err != nil {
				return tried(fmt.Errorf("drive: %s %s: %w", req.method, req.path, err), attempt)
			}
		}

		err := c.attempt(ctx, req, endpoint, payload, out, attempt < attempts-1)
		if err == nil {
			return nil
		}
		lastErr = err

		var apiErr *APIError
		if errors.As(err, &apiErr) && !apiErr.Retryable() {
			return err
		}
		if ctx.Err() != nil {
			// The caller's time is up, or it gave up.
			return tried(err, attempt+1)
		}
	}
	return tried(lastErr, attempts)
}

// tried notes how many attempts a failure took, when it took more than one.
func tried(err error, attempts int) error {
	if attempts < 2 {
		return err
	}
	return fmt.Errorf("%w (%d attempts)", err, attempts)
}

// attempt performs a single round trip, recovering once from a rejected
// access token and once from a rejected captcha token. Both are replaced by
// the shared source, so parallel callers hitting the same rejection converge
// on one replacement instead of minting several. With stall set, a round
// trip the server does not start answering within stallTimeout fails.
func (c *Client) attempt(ctx context.Context, req request, endpoint string, payload []byte, out any, stall bool) error {
	refreshedAuth, refreshedCaptcha := false, false
	for {
		var accessToken string
		if !req.anonymous && c.tokens != nil {
			tok, err := c.tokens.Token(ctx)
			if err != nil {
				return err
			}
			accessToken = tok.AccessToken
		}
		// Snapshot the captcha token before sending: by the time a rejection
		// comes back another goroutine may have replaced it, and refreshing
		// the replacement would skip the one that actually failed.
		var captchaToken string
		if c.cfg.Captcha != nil {
			captchaToken = c.cfg.Captcha.Token()
		}

		status, body, quota, err := c.exchange(ctx, req, endpoint, payload, accessToken, captchaToken, stall)
		if quota != nil && c.cfg.OnQuota != nil {
			c.cfg.OnQuota(*quota)
		}

		if status == http.StatusUnauthorized && !refreshedAuth && !req.anonymous && c.tokens != nil {
			// The token was rejected mid-flight: drop it and try once with a
			// fresh one before surfacing the failure.
			refreshedAuth = true
			if err := c.tokens.Invalidate(ctx, accessToken); err != nil {
				return err
			}
			continue
		}

		if err != nil {
			if IsCaptchaRequired(err) && !refreshedCaptcha && c.cfg.Captcha != nil {
				refreshedCaptcha = true
				if err := c.cfg.Captcha.Refresh(ctx, req.captchaAction(), captchaToken); err != nil {
					return err
				}
				continue
			}
			return err
		}

		if out == nil {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("drive: decode %s %s: %w", req.method, req.path, err)
		}
		return nil
	}
}

// exchange sends one request and reads its answer: the status, the body, the
// throttling counters and the error the answer carries. With stall set the
// server gets stallTimeout to start answering, or the exchange ends with
// errStalled; the body then takes as long as it takes.
func (c *Client) exchange(ctx context.Context, req request, endpoint string, payload []byte, accessToken, captchaToken string, stall bool) (int, []byte, *QuotaSnapshot, error) {
	var timer *time.Timer
	if stall {
		var cancel context.CancelCauseFunc
		ctx, cancel = context.WithCancelCause(ctx)
		defer cancel(nil)
		timer = time.AfterFunc(stallTimeout, func() { cancel(errStalled) })
	}
	// Cut short, the transport's error is the whole URL and noise; what cut
	// it says more.
	failed := func(err error) error {
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		return fmt.Errorf("drive: %s %s: %w", req.method, req.path, err)
	}

	httpReq, err := c.newHTTPRequest(ctx, req, endpoint, payload, accessToken, captchaToken)
	if err != nil {
		return 0, nil, nil, err
	}
	resp, err := c.http.Do(httpReq)
	if timer != nil {
		timer.Stop()
	}
	if err != nil {
		return 0, nil, nil, failed(err)
	}
	body, quota, err := decodeResponse(req.method, req.path, resp)
	if err != nil && body == nil && context.Cause(ctx) != nil {
		err = failed(err)
	}
	return resp.StatusCode, body, quota, err
}

func (c *Client) newHTTPRequest(ctx context.Context, req request, endpoint string, payload []byte, accessToken, captchaToken string) (*http.Request, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.method, endpoint, bodyReader)
	if err != nil {
		return nil, err
	}

	h := httpReq.Header
	c.cfg.Profile.setHeaders(h, c.cfg.DeviceID)
	if payload != nil {
		h.Set("Content-Type", "application/json")
	}
	if captchaToken != "" {
		h.Set("X-Captcha-Token", captchaToken)
	}
	if accessToken != "" {
		h.Set("Authorization", "Bearer "+accessToken)
	}
	return httpReq, nil
}

// decodeResponse reads one API response and closes it. A non-2xx becomes the
// *APIError its body describes; a 2xx returns the body, plus the error
// envelope when one rides on it — a risk-control rejection can arrive that
// way, and the envelope is the only place it shows. The quota counters are
// returned whenever the server sent them, error or not.
func decodeResponse(method, path string, resp *http.Response) (body []byte, quota *QuotaSnapshot, err error) {
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("drive: %s %s: %w", method, path, err)
	}
	quota = readQuota(resp.Header)

	if resp.StatusCode/100 != 2 {
		apiErr := &APIError{Status: resp.StatusCode, Method: method, Path: path, Quota: quota}
		if err := json.Unmarshal(body, apiErr); err != nil || apiErr.Name == "" {
			// Not the JSON envelope: a gateway or proxy answered. Keep a short
			// excerpt so the failure is still diagnosable.
			apiErr.Description = excerpt(body)
		}
		apiErr.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return body, quota, apiErr
	}

	var apiErr APIError
	if err := json.Unmarshal(body, &apiErr); err != nil || (apiErr.Name == "" && apiErr.Code == 0) {
		return body, quota, nil
	}
	apiErr.Status = resp.StatusCode
	apiErr.Method = method
	apiErr.Path = path
	return body, quota, &apiErr
}

func excerpt(body []byte) string {
	const limit = 200
	text := strings.TrimSpace(string(body))
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}

// backoff grows with the attempt number and carries jitter so that a burst of
// parallel calls does not re-synchronise on every retry.
func backoff(attempt int) time.Duration {
	base := time.Duration(1<<(attempt-1)) * 150 * time.Millisecond
	return base + time.Duration(rand.Int64N(int64(base/2+1)))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
