package drive

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIError is a non-2xx response from the drive API. The service answers with
// a JSON envelope whose `error` field is a stable machine-readable name, so
// callers should branch on Name (or the Is* helpers) rather than on Status.
type APIError struct {
	Status      int    `json:"-"`
	Method      string `json:"-"`
	Path        string `json:"-"`
	Name        string `json:"error"`
	Code        int    `json:"error_code"`
	Description string `json:"error_description"`
	// RetryAfter is parsed from the Retry-After header; zero when absent.
	RetryAfter time.Duration `json:"-"`
	// Quota carries the throttling counters the server attaches to a 429.
	Quota *QuotaSnapshot `json:"-"`
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "drive: %s %s: %d", e.Method, e.Path, e.Status)
	if e.Name != "" {
		b.WriteString(" " + e.Name)
	}
	if e.Description != "" {
		b.WriteString(": " + e.Description)
	}
	return b.String()
}

// Retryable reports whether repeating the same request could plausibly
// succeed. Quota rejections are excluded: they only clear on the reset
// boundary, so retrying just burns requests.
func (e *APIError) Retryable() bool {
	return e.Status >= 500 && e.Status <= 599
}

func asAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return nil
}

// IsUnauthorized reports a rejected or expired access token.
func IsUnauthorized(err error) bool {
	e := asAPIError(err)
	return e != nil && e.Status == http.StatusUnauthorized
}

// IsNotFound reports a missing file, task or share.
func IsNotFound(err error) bool {
	e := asAPIError(err)
	return e != nil && e.Status == http.StatusNotFound
}

// IsInRecycleBin reports a single-entry read refused because the entry is in
// the recycle bin. GET /drive/v1/files/{id} answers 400 file_in_recycle_bin
// for any trashed entry whatever the query, so neither its details nor a
// link can be read that way.
func IsInRecycleBin(err error) bool {
	e := asAPIError(err)
	return e != nil && e.Name == "file_in_recycle_bin"
}

// IsCaptchaRequired reports that risk control rejected the captcha token and
// wants a new one.
//
// The envelope code alone is not enough: the server reuses code 9 for
// unrelated failures such as reading a file already in the recycle bin, and
// treating those as captcha rejections costs a pointless handshake and retry
// before the real error surfaces. The captcha cases name themselves.
func IsCaptchaRequired(err error) bool {
	e := asAPIError(err)
	return e != nil && strings.HasPrefix(e.Name, "captcha")
}

// IsQuotaExceeded reports a throttling rejection. The counters, when the
// server sent them, are in APIError.Quota.
func IsQuotaExceeded(err error) bool {
	e := asAPIError(err)
	return e != nil && e.Status == http.StatusTooManyRequests
}

// QuotaDimension is one metered axis, e.g. monthly downstream bytes.
type QuotaDimension struct {
	Used  int64  `json:"used"`
	Limit int64  `json:"limit"`
	Reset string `json:"reset"`
}

// QuotaSnapshot is the throttling state the server reports on a response.
// Dimensions is keyed by the lower-cased axis name as it appears in the
// header suffix, e.g. "downstream" or "daily-downstream".
type QuotaSnapshot struct {
	Dimensions map[string]QuotaDimension `json:"dimensions"`
	RetryAfter string                    `json:"retryAfter"`
	Throttled  bool                      `json:"throttled"`
	ObservedMs int64                     `json:"observedMs"`
}

const quotaHeaderPrefix = "ratelimit-quota-"

// readQuota extracts the throttling counters from a response. It returns nil
// when the server reported none, so callers can tell "no signal" apart from
// "zero usage".
func readQuota(h http.Header) *QuotaSnapshot {
	snap := QuotaSnapshot{
		Dimensions: map[string]QuotaDimension{},
		RetryAfter: h.Get("Retry-After"),
		Throttled:  h.Get("x-limited") == "1",
		ObservedMs: time.Now().UnixMilli(),
	}

	for key, values := range h {
		if len(values) == 0 {
			continue
		}
		lower := strings.ToLower(key)
		rest, ok := strings.CutPrefix(lower, quotaHeaderPrefix)
		if !ok {
			continue
		}
		// rest is "<field>-<dimension>", and the dimension itself may contain
		// dashes ("daily-downstream"), so split on the first dash only.
		field, dimension, ok := strings.Cut(rest, "-")
		if !ok || dimension == "" {
			continue
		}

		entry := snap.Dimensions[dimension]
		switch field {
		case "used":
			entry.Used, _ = strconv.ParseInt(values[0], 10, 64)
		case "limit":
			entry.Limit, _ = strconv.ParseInt(values[0], 10, 64)
		case "reset":
			entry.Reset = values[0]
		default:
			continue
		}
		snap.Dimensions[dimension] = entry
	}

	if len(snap.Dimensions) == 0 && !snap.Throttled && snap.RetryAfter == "" {
		return nil
	}
	return &snap
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
