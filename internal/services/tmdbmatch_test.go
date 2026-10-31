package services

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// tmdbSearchStandIn is TMDB answering search/movie and search/tv for the
// stored test credential from movies and shows, keyed by titleKey of the
// query; it fails every request with status when that is set, and
// records what it was asked.
type tmdbSearchStandIn struct {
	mu     sync.Mutex
	movies map[string]string // titleKey(query) → results JSON
	shows  map[string]string
	status int
	asked  []string // path?query of every search
}

func (f *tmdbSearchStandIn) serve(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		if r.URL.Query().Get("api_key") != tmdbV3Key {
			t.Errorf("TMDB asked without the stored credential: %s", r.URL.RawQuery)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/3"), "/")
		if path == "configuration" {
			io.WriteString(w, `{"images":{"secure_base_url":"https://img.example/p/"}}`)
			return
		}
		var table map[string]string
		switch path {
		case "search/movie":
			table = f.movies
		case "search/tv":
			table = f.shows
		default:
			t.Errorf("unexpected TMDB request %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Only these go out (a year is matched here, never sent).
		for name := range r.URL.Query() {
			if !slices.Contains([]string{"api_key", "language", "page", "query", "include_adult"}, name) {
				t.Errorf("search sent %q: %s", name, r.URL.RawQuery)
			}
		}
		f.asked = append(f.asked, path+"?"+r.URL.Query().Get("query"))
		fmt.Fprintf(w, `{"page":1,"total_pages":1,"results":[%s]}`, table[titleKey(r.URL.Query().Get("query"))])
	}
}

func (f *tmdbSearchStandIn) searches() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// matchService is a service with tmdbV3Key stored, asking f as TMDB.
func matchService(t *testing.T, f *tmdbSearchStandIn) *DriveService {
	t.Helper()
	api := httptest.NewServer(f.serve(t))
	t.Cleanup(api.Close)
	svc := newTMDBService(t, nil, func(o *DriveOptions) { o.TMDBBaseURL = api.URL })
	if _, err := svc.SetTMDBKey(tmdbV3Key); err != nil {
		t.Fatal(err)
	}
	return svc
}

func movieJSON(id int, title, original, date string) string {
	return fmt.Sprintf(`{"id":%d,"title":%q,"original_title":%q,"release_date":%q}`, id, title, original, date)
}

func showJSON(id int, name, original, date string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"original_name":%q,"first_air_date":%q}`, id, name, original, date)
}

func TestMatchTitlesExactUniqueAndInOrder(t *testing.T) {
	f := &tmdbSearchStandIn{
		movies: map[string]string{
			// Two films of one name: only a year tells them apart.
			"dune": movieJSON(1, "沙丘", "Dune", "2021-09-15") + "," + movieJSON(2, "沙丘", "Dune", "1984-12-14") + "," +
				movieJSON(3, "沙丘2", "Dune: Part Two", "2024-02-27"),
			"沙丘":   movieJSON(1, "沙丘", "Dune", "2021-09-15") + "," + movieJSON(3, "沙丘2", "Dune: Part Two", "2024-02-27"),
			"hero": movieJSON(7, "英雄本色", "A Better Tomorrow", "1986-08-02") + "," + movieJSON(8, "Hero Worship", "Hero Worship", "2020-01-01"),
			"绝命毒师": "",
		},
		shows: map[string]string{
			"breakingbad": showJSON(10, "绝命毒师", "Breaking Bad", "2008-01-20"),
			"绝命毒师":        showJSON(10, "绝命毒师", "Breaking Bad", "2008-01-20"),
			"沙丘":          showJSON(20, "沙丘：预言", "Dune: Prophecy", "2024-11-17"),
		},
	}
	svc := matchService(t, f)

	queries := []TitleQuery{
		{Key: "ambiguous", Title: "Dune", Kind: "movie"},               // 2021 and 1984 both match
		{Key: "year", Title: "dune", Year: 2022, Kind: "movie"},        // ±1 keeps 2021 only
		{Key: "far", Title: "Dune", Year: 2000, Kind: "movie"},         // neither within a year
		{Key: "original", Title: "BREAKING  BAD!", Kind: "tv"},         // original title, folded
		{Key: "fullwidth", Title: "Ｂｒｅａｋｉｎｇ Ｂａｄ", Year: 2008},          // NFKC; both kinds asked
		{Key: "tvyear", Title: "Breaking Bad", Year: 2009, Kind: "tv"}, // shows match the year exactly
		{Key: "title", Title: "沙丘"}, // Chinese movie title; the TV series has a different subtitle.
		{Key: "loose", Title: "Hero", Kind: "movie"},                   // contains, never equals
		{Key: "empty", Title: " .. "},                                  // nothing to search
		{Key: "chinese", Title: "绝命毒师", Year: 2008, Kind: "weird"},     // unknown kind: both
	}
	got, err := svc.MatchTitles(queries)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(queries) {
		t.Fatalf("got %d answers for %d queries", len(got), len(queries))
	}
	for i, q := range queries {
		if got[i].Key != q.Key {
			t.Errorf("answer %d is %q, want %q (query order)", i, got[i].Key, q.Key)
		}
	}
	want := map[string]TitleMatch{
		"ambiguous": {Key: "ambiguous"},
		"year":      {Key: "year", Found: true, Kind: "movie", ID: 1, Title: "沙丘", OriginalTitle: "Dune", Year: 2021},
		"far":       {Key: "far"},
		"original":  {Key: "original", Found: true, Kind: "tv", ID: 10, Title: "绝命毒师", OriginalTitle: "Breaking Bad", Year: 2008},
		"fullwidth": {Key: "fullwidth", Found: true, Kind: "tv", ID: 10, Title: "绝命毒师", OriginalTitle: "Breaking Bad", Year: 2008},
		"tvyear":    {Key: "tvyear"},
		"title":     {Key: "title", Found: true, Kind: "movie", ID: 1, Title: "沙丘", OriginalTitle: "Dune", Year: 2021},
		"loose":     {Key: "loose"},
		"empty":     {Key: "empty"},
		"chinese":   {Key: "chinese", Found: true, Kind: "tv", ID: 10, Title: "绝命毒师", OriginalTitle: "Breaking Bad", Year: 2008},
	}
	for _, m := range got {
		if m != want[m.Key] {
			t.Errorf("%s = %+v, want %+v", m.Key, m, want[m.Key])
		}
	}
	for _, s := range f.searches() {
		if strings.HasSuffix(s, "?") || strings.Contains(s, "..") {
			t.Errorf("an empty title was searched: %q", s)
		}
	}
	if s := f.searches(); !slices.Contains(s, "search/movie?Ｂｒｅａｋｉｎｇ Ｂａｄ") || !slices.Contains(s, "search/tv?Ｂｒｅａｋｉｎｇ Ｂａｄ") {
		t.Errorf("a query without a kind should ask both: %q", s)
	}

	if _, err := svc.MatchTitles(make([]TitleQuery, maxTitleQueries+1)); !errors.Is(err, errTooManyTitles) {
		t.Errorf("51 queries: %v, want errTooManyTitles", err)
	}
	if got, err := svc.MatchTitles(nil); err != nil || len(got) != 0 {
		t.Errorf("no queries = %v %v", got, err)
	}
}

// Without TMDB — no credential, or TMDB failing — the whole call says
// metadata matching unavailable, never a partial answer.
func TestMatchTitlesUnavailable(t *testing.T) {
	bare := newTMDBService(t, nil, nil)
	if _, err := bare.MatchTitles([]TitleQuery{{Key: "a", Title: "Dune"}}); !errors.Is(err, errMatchUnavailable) {
		t.Errorf("unconfigured: %v, want errMatchUnavailable", err)
	}

	f := &tmdbSearchStandIn{status: http.StatusBadGateway}
	svc := matchService(t, f)
	queries := make([]TitleQuery, 12)
	for i := range queries {
		queries[i] = TitleQuery{Key: fmt.Sprint(i), Title: fmt.Sprint("Title ", i)}
	}
	got, err := svc.MatchTitles(queries)
	if !errors.Is(err, errMatchUnavailable) || got != nil {
		t.Errorf("TMDB failing: %v %v, want errMatchUnavailable", got, err)
	}
	if err != nil && err.Error() != "资料匹配不可用" {
		t.Errorf("message %q", err)
	}
}

func TestTitleKey(t *testing.T) {
	for in, want := range map[string]string{
		"The Office (US)": "theofficeus",
		"Ｄｕｎｅ：Ｐａｒｔ　Ｔｗｏ":   "duneparttwo",
		"进击的巨人 · 最终季":     "进击的巨人最终季",
		"Amélie":          "amélie",
		"Spider-Man: 2":   "spiderman2",
		"!!!":             "",
	} {
		if got := titleKey(in); got != want {
			t.Errorf("titleKey(%q) = %q, want %q", in, got, want)
		}
	}
}
