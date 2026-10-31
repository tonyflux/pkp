package tmdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tonyflux/pkp/internal/i18n"
)

// DefaultBase is TMDB's v3 API.
const DefaultBase = "https://api.themoviedb.org/3"

const (
	tmdbTimeout = 15 * time.Second
	// tmdbMaxBody bounds an answer; a page of search results is tens of KiB.
	tmdbMaxBody = 8 << 20

	searchTTL = 10 * time.Minute

	// rateLimitDefault is the wait to suggest when a 429 does not say.
	rateLimitDefault = 10
	// autoRetryLimit is the longest Retry-After waited out on the user's
	// behalf; anything longer is theirs to decide.
	autoRetryLimit = 2

	maxTitleRunes = 300
	// MaxQueryRunes is the longest search sent.
	MaxQueryRunes = 100
)

var errNoTMDB = &Error{Kind: KindConfig, text: i18n.New("还没有配置 TMDB 凭据")}

func tmdbError(kind string, status, retry int, msg string) *Error {
	return &Error{Kind: kind, Status: status, RetryAfter: retry, msg: msg}
}

// Work is one movie or TV show a search found.
type Work struct {
	Kind          string
	ID            int
	Title         string
	OriginalTitle string
	// Year is the release (movie) or first air (show) year, "" unknown.
	Year string
}

// Client is the client for TMDB's v3 API. A call reaches TMDB with the
// credential the caller passes: the user's own, which lives in the
// keychain; this side only ever puts it on the wire. Without one nothing
// is asked. Answers are cached for everyone; keys of the cache carry a
// fingerprint of the credential so a new one never sees what another
// fetched, and Reset drops it all.
type Client struct {
	base  string
	http  *http.Client
	cache *ttlCache
}

// New is a client for base ("" = DefaultBase) over hc (nil = a client of
// its own).
func New(base string, hc *http.Client) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{base: strings.TrimRight(base, "/"), http: sameHostClient(hc, tmdbTimeout), cache: newCache(cacheEntries)}
}

// ready reports whether a call with key can reach TMDB at all.
func (t *Client) ready(key string) bool { return key != "" }

// Reset forgets every cached answer, for a changed credential.
func (t *Client) Reset() { t.cache.clear() }

// Test reads the configuration with key, uncached: Test Connection must ask.
func (t *Client) Test(ctx context.Context, key string) error {
	var cfg struct {
		Images json.RawMessage `json:"images"`
	}
	return t.get(ctx, key, "configuration", nil, &cfg)
}

/* ---------------- the request ---------------- */

// get fetches path from TMDB with key and decodes the answer into out. A
// 429 with a short Retry-After is waited out once; anything else comes
// back typed.
func (t *Client) get(ctx context.Context, key, path string, query url.Values, out any) error {
	if !t.ready(key) {
		return errNoTMDB
	}
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("language", i18n.Tag())
	// A v4 read access token is a JWT; a v3 key is 32 hex digits.
	bearer := strings.HasPrefix(key, "eyJ")
	if !bearer {
		q.Set("api_key", key)
	}
	endpoint := t.base + "/" + path + "?" + q.Encode()

	for attempt := 0; ; attempt++ {
		status, header, body, err := t.exchange(ctx, endpoint, key, bearer)
		if err != nil {
			return err
		}
		switch {
		case status == http.StatusTooManyRequests:
			secs, known := retryAfter(header.Get("Retry-After"))
			if attempt == 0 && known && secs <= autoRetryLimit {
				if err := sleepCtx(ctx, time.Duration(secs)*time.Second); err != nil {
					return tmdbError(KindUnreachable, 0, 0, i18n.F("连不上 %s：%s", "TMDB", transportCause(err)))
				}
				continue
			}
			secs = max(secs, 1)
			return tmdbError(KindRateLimit, status, secs, i18n.F("TMDB 请求过于频繁，请 %d 秒后重试", secs))
		case status == http.StatusNotFound:
			return tmdbError(KindNotFound, status, 0, i18n.T("TMDB 没有这个条目"))
		case status == http.StatusUnauthorized:
			return tmdbError(KindAuth, status, 0, i18n.T("TMDB 凭据无效或已停用"))
		case status < 200 || status > 299:
			return tmdbError(KindServer, status, 0, i18n.F("TMDB 返回错误（HTTP %d）", status))
		}
		if err := json.Unmarshal(body, out); err != nil {
			return tmdbError(KindInvalid, 0, 0, i18n.T("TMDB 返回了无法识别的数据"))
		}
		return nil
	}
}

// exchange is one round trip. Its errors never quote the URL: a v3 key
// is in it.
func (t *Client) exchange(ctx context.Context, endpoint, key string, bearer bool) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, tmdbTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, nil, tmdbError(KindConfig, 0, 0, i18n.T("TMDB 地址无效"))
	}
	req.Header.Set("Accept", "application/json")
	if bearer {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		if errors.Is(err, errCrossHost) {
			return 0, nil, nil, tmdbError(KindServer, 0, 0, i18n.T("TMDB 地址跳转到了其他站点，已拒绝"))
		}
		return 0, nil, nil, tmdbError(KindUnreachable, 0, 0, i18n.F("连不上 %s：%s", "TMDB", transportCause(err, key)))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, tmdbMaxBody+1))
	if err != nil {
		return 0, nil, nil, tmdbError(KindUnreachable, 0, 0, i18n.F("连不上 %s：%s", "TMDB", transportCause(err, key)))
	}
	if len(body) > tmdbMaxBody {
		return 0, nil, nil, tmdbError(KindInvalid, 0, 0, i18n.T("TMDB 返回了无法识别的数据"))
	}
	return resp.StatusCode, resp.Header, body, nil
}

// retryAfter reads a Retry-After header, seconds or an HTTP date; false
// (and the default) when it says neither.
func retryAfter(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return rateLimitDefault, false
	}
	if n, err := strconv.Atoi(v); err == nil {
		return min(max(n, 0), 3600), true
	}
	if at, err := http.ParseTime(v); err == nil {
		secs := int(math.Ceil(time.Until(at).Seconds()))
		return min(max(secs, 0), 3600), true
	}
	return rateLimitDefault, false
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fingerprint names a credential in cache keys without keeping it there.
func fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// scope is fingerprint and the language TMDB is asked to answer in, for
// the cache keys of answers it words (titles): a switch of Interface Language never
// reads the last language's.
func scope(key string) string { return fingerprint(key) + "|" + i18n.Tag() }

/* ---------------- search ---------------- */

type rawItem struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
}

// Search answers page 1 of search/{kind} ("movie" or "tv") for query,
// which CleanLine must leave between 1 and MaxQueryRunes runes. An odd
// item is skipped, not the page.
func (t *Client) Search(ctx context.Context, key, kind, query string) ([]Work, error) {
	if kind != "movie" && kind != "tv" {
		return nil, i18n.New("作品类型无效")
	}
	query = CleanLine(query, MaxQueryRunes+1)
	if query == "" {
		return nil, i18n.New("请输入要搜索的片名")
	}
	if utf8.RuneCountInString(query) > MaxQueryRunes {
		return nil, i18n.Errorf("搜索词太长（最多 %d 个字）", MaxQueryRunes)
	}
	if !t.ready(key) {
		return nil, errNoTMDB
	}
	path := "search/" + kind
	params := url.Values{"page": {"1"}, "query": {query}, "include_adult": {"false"}}
	ck := "search|" + scope(key) + "|" + path + "?" + params.Encode()
	if v, ok := t.cache.get(ck); ok {
		return slices.Clone(v.([]Work)), nil
	}
	var body struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := t.get(ctx, key, path, params, &body); err != nil {
		return nil, err
	}
	out := []Work{}
	for _, raw := range body.Results {
		var it rawItem
		if json.Unmarshal(raw, &it) != nil || it.ID <= 0 {
			continue
		}
		title, original, date := it.Title, it.OriginalTitle, it.ReleaseDate
		if kind == "tv" {
			title, original, date = it.Name, it.OriginalName, it.FirstAirDate
		}
		w := Work{Kind: kind, ID: it.ID, Year: year(date)}
		w.Title, w.OriginalTitle = CleanLine(title, maxTitleRunes), CleanLine(original, maxTitleRunes)
		if w.Title == "" {
			w.Title = w.OriginalTitle
		}
		out = append(out, w)
	}
	t.cache.put(ck, out, searchTTL)
	return slices.Clone(out), nil
}

// year is the year of a YYYY-MM-DD date, "" for anything else.
func year(s string) string {
	if len(s) != 10 {
		return ""
	}
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return ""
	}
	return s[:4]
}
