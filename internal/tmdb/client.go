// Package tmdb asks TMDB what a title is, for Media Organization: a search by name
// and kind, and a read of the configuration for Test Connection. TMDB is
// untrusted: every field is checked before it is used, and failures come
// back as typed errors already worded for the page, never as an empty
// result. Every request goes through the app's Proxy setting unless the
// caller injects its own client, which the tests do.
package tmdb

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// Error kinds. They tell TestTMDB what to report and the page what to
// offer next (Settings for a missing or refused credential, Retry for the rest).
const (
	KindConfig      = "config"      // nothing to work with yet: no credential
	KindAuth        = "auth"        // the credential was refused
	KindNotFound    = "notfound"    // TMDB has no such entry
	KindRateLimit   = "ratelimit"   // throttled; RetryAfter says how long, when known
	KindUnreachable = "unreachable" // no answer: DNS, TLS, reset, timeout
	KindServer      = "server"      // an HTTP error answer
	KindInvalid     = "invalid"     // an answer that is not what the API documents
)

// Error is one upstream failure. Error() is the sentence the page shows;
// it never carries a credential.
type Error struct {
	Kind string
	// Status is the HTTP status of an error answer, 0 without one.
	Status int
	// RetryAfter is the wait a rate limit asked for, in seconds.
	RetryAfter int
	msg        string
	// text, set instead of msg on a package-level error, words it in the
	// language in force when it is read.
	text error
}

func (e *Error) Error() string {
	if e.text != nil {
		return e.text.Error()
	}
	return e.msg
}

// cacheEntries bounds the in-memory cache. A page of search results is a
// few KiB, so this stays well under a few MiB.
const cacheEntries = 500

// errCrossHost stops a redirect to another host or scheme. Following one
// would send a TMDB key in the query string, or a search, to a site the
// user never chose.
var errCrossHost = errors.New("redirect to another host refused")

// transport is shared by every client made here: the timeout lives on
// the client, and one transport keeps one entry in netproxy's registry.
var transport = sync.OnceValue(netproxy.NewTransport)

// sameHostClient is hc (a fresh client when nil) with timeout as its
// overall bound and redirects kept on the host the request was sent to.
// hc is copied, never changed: tests share theirs. A client made here
// follows the app's Proxy setting, which is how TMDB is reached from a
// network that blocks it.
func sameHostClient(hc *http.Client, timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout, Transport: transport()}
	if hc != nil {
		cp := *hc
		c = &cp
		if c.Timeout == 0 {
			c.Timeout = timeout
		}
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("stopped after 5 redirects")
		}
		first := via[0].URL
		if req.URL.Scheme != first.Scheme || !strings.EqualFold(req.URL.Host, first.Host) {
			return errCrossHost
		}
		return nil
	}
	return c
}

// transportCause words a failure that got no HTTP answer, without the
// URL the transport puts in front of it (which, for TMDB, holds the key).
// secrets are scrubbed from what is left all the same.
func transportCause(err error, secrets ...string) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return i18n.T("请求超时")
	case errors.Is(err, context.Canceled):
		return i18n.T("请求已取消")
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	for _, s := range secrets {
		if s == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, s, "••••")
		msg = strings.ReplaceAll(msg, url.QueryEscape(s), "••••")
	}
	return CleanLine(msg, 200)
}

/* ---------------- text ---------------- */

// CleanLine makes untrusted text safe to show on one line: invalid UTF-8,
// control and format characters (bidi overrides included) dropped, runs of
// whitespace collapsed to one space, trimmed, and cut to max runes.
func CleanLine(s string, max int) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range strings.ToValidUTF8(s, "") {
		switch {
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == utf8.RuneError:
			continue
		}
		if n >= max {
			break
		}
		if space {
			if n+1 >= max {
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

/* ---------------- cache ---------------- */

// ttlCache keeps successful answers for a while; failures are never put
// in it, so an outage is not remembered as "nothing found". When full it
// drops the expired entries, then the oldest.
type ttlCache struct {
	mu  sync.Mutex
	max int
	m   map[string]cacheItem
}

type cacheItem struct {
	v       any
	stored  time.Time
	expires time.Time
}

func newCache(max int) *ttlCache { return &ttlCache{max: max, m: map[string]cacheItem{}} }

func (c *ttlCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.m[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(it.expires) {
		delete(c.m, key)
		return nil, false
	}
	return it.v, true
}

func (c *ttlCache) put(key string, v any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if _, ok := c.m[key]; !ok && len(c.m) >= c.max {
		for k, it := range c.m {
			if now.After(it.expires) {
				delete(c.m, k)
			}
		}
		for len(c.m) >= c.max {
			oldest, at := "", now
			for k, it := range c.m {
				if oldest == "" || it.stored.Before(at) {
					oldest, at = k, it.stored
				}
			}
			delete(c.m, oldest)
		}
	}
	c.m[key] = cacheItem{v: v, stored: now, expires: now.Add(ttl)}
}

func (c *ttlCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.m)
}
