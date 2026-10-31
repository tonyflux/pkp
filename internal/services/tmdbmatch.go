package services

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/tmdb"
)

// Media Organization without a model (L0): the file-name parser in the webview finds a
// title and maybe a year, and MatchTitles asks TMDB — with the user's own
// credential — whether exactly one work answers
// to it. Nothing is guessed: a near miss or two
// works of one name are "not found", and the plan keeps the parsed name.
// What TMDB answers goes back to the webview only; it never reaches a model.

const (
	maxTitleQueries = 50
	matchParallel   = 4
)

var (
	errTooManyTitles = &lazyError{func() string { return i18n.F("一次最多匹配 %d 个作品", maxTitleQueries) }}
	// errMatchUnavailable is any failure to reach TMDB; the webview shows
	// metadata matching unavailable and plans go on with the parsed names.
	errMatchUnavailable = i18n.New("资料匹配不可用")
)

// TitleQuery is one title the webview wants matched. Key is the caller's,
// echoed back; Year 0 is unknown; Kind is "movie", "tv" or "" (either).
type TitleQuery struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Year  int    `json:"year"`
	Kind  string `json:"kind"`
}

// TitleMatch answers one TitleQuery. Found is false unless exactly one
// work matched; the other fields are then zero.
type TitleMatch struct {
	Key           string `json:"key"`
	Found         bool   `json:"found"`
	Kind          string `json:"kind"`
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	Year          int    `json:"year"`
}

// MatchTitles looks each query up in TMDB (page 1 of search/movie and/or
// search/tv) and answers in query order. A candidate qualifies when its
// title or original title equals the query's, both folded by titleKey;
// with a year, only candidates of that year stay (movies ±1: a festival
// premiere and the release often straddle New Year). Found takes exactly
// one survivor. TMDB unreachable, unconfigured or refusing fails the whole
// call with errMatchUnavailable; a query that cannot be searched (no
// title, too long) is simply not found.
func (d *DriveService) MatchTitles(queries []TitleQuery) ([]TitleMatch, error) {
	if len(queries) > maxTitleQueries {
		return nil, errTooManyTitles
	}
	key := d.tmdbKey()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make([]TitleMatch, len(queries))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	slots := make(chan struct{}, matchParallel)
	for i, q := range queries {
		out[i] = TitleMatch{Key: q.Key}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			m, err := d.matchTitle(ctx, key, q)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel() // the answer is an error now; the rest need not ask
				}
				mu.Unlock()
				return
			}
			out[i] = m
		}()
	}
	wg.Wait()
	if firstErr != nil {
		// The cause stays in the log, at debug level: it may quote a title.
		d.log.Debug("tmdb: title match failed", "error", firstErr)
		return nil, errMatchUnavailable
	}
	return out, nil
}

// matchTitle answers one query; an error is TMDB's, never the query's.
func (d *DriveService) matchTitle(ctx context.Context, key string, q TitleQuery) (TitleMatch, error) {
	miss := TitleMatch{Key: q.Key}
	want := titleKey(q.Title)
	title := tmdb.CleanLine(q.Title, tmdb.MaxQueryRunes+1)
	if want == "" || title == "" || utf8.RuneCountInString(title) > tmdb.MaxQueryRunes {
		return miss, nil
	}
	kinds := []string{"movie", "tv"}
	switch q.Kind {
	case "movie", "tv":
		kinds = []string{q.Kind}
	}
	var found []tmdb.Work
	for _, kind := range kinds {
		works, err := d.tmdb.client.Search(ctx, key, kind, title)
		if err != nil {
			return miss, err
		}
		for _, w := range works {
			if titleKey(w.Title) != want && titleKey(w.OriginalTitle) != want {
				continue
			}
			if q.Year > 0 && !yearFits(w, q.Year) {
				continue
			}
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		return miss, nil
	}
	w := found[0]
	year, _ := strconv.Atoi(w.Year)
	return TitleMatch{Key: q.Key, Found: true, Kind: w.Kind, ID: w.ID, Title: w.Title, OriginalTitle: w.OriginalTitle, Year: year}, nil
}

// yearFits says whether w came out in year: a show's first air year
// exactly, a movie's release year ±1. A work without a date never fits a
// query that names a year.
func yearFits(w tmdb.Work, year int) bool {
	y, err := strconv.Atoi(w.Year)
	if err != nil || y == 0 {
		return false
	}
	if w.Kind == "movie" {
		return y >= year-1 && y <= year+1
	}
	return y == year
}

// titleKey folds a title for comparison: NFKC (full-width letters and
// digits become plain ones), lower case, and nothing but letters, digits
// and combining marks — spaces and punctuation differ between a file name
// and TMDB's title without the title differing.
func titleKey(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
