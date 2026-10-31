package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

// aiSearchService is a signed-in service whose stand-in API serves the
// cloud play history from events and answers batchGet with batch.
func aiSearchService(t *testing.T, events string, batch func(ids []string) string) *DriveService {
	t.Helper()
	return newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		switch {
		case r.URL.Path == "/drive/v1/events":
			io.WriteString(w, events)
		case r.URL.Path == "/drive/v1/files:batchGet" && batch != nil:
			io.WriteString(w, batch(r.URL.Query()["ids"]))
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	})
}

// fillIndex writes rows into the index of the account on screen and marks
// its walk complete.
func fillIndex(t *testing.T, svc *DriveService, rows []index.Row) {
	t.Helper()
	s := activeIndex(svc)
	if err := s.store.Upsert(rows); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetMeta(index.MetaFullAt, "2026-10-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

func indexedFolder(id, parent, name string) index.Row {
	return index.Row{ID: id, ParentID: parent, Kind: drive.KindFolder, Name: name, Category: "folder", ModifiedTime: "2026-01-01T00:00:00+08:00"}
}

func indexedVideo(id, parent, name string, size int64, modified string) index.Row {
	return index.Row{ID: id, ParentID: parent, Kind: drive.KindFile, Name: name, MimeType: "video/x-matroska",
		FileExtension: ".mkv", Size: size, Category: "video", Phase: drive.PhaseComplete,
		CreatedTime: "2026-03-01T10:00:00+08:00", ModifiedTime: modified}
}

func hitIDs(hits []AISearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func TestAIIndexCallsNeedACompletedWalk(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	if _, err := svc.SearchIndex("", AIQuery{Keywords: []string{"x"}}); !errors.Is(err, errIndexNotReady) {
		t.Errorf("search: %v, want errIndexNotReady", err)
	}
	if _, err := svc.FolderTree("", ""); !errors.Is(err, errIndexNotReady) {
		t.Errorf("tree: %v", err)
	}
	if _, err := svc.FindFolders("", "影视"); !errors.Is(err, errIndexNotReady) {
		t.Errorf("find: %v", err)
	}
}

func TestSearchIndexCleansTheQuery(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	rows := []index.Row{indexedFolder("dA", "", "影视"), indexedFolder("dB", "dA", "纪录片")}
	for i := range 250 {
		rows = append(rows, indexedVideo(fmt.Sprintf("v%03d", i), "dB", fmt.Sprintf("Ocean %03d.mkv", i), int64(i), fmt.Sprintf("2026-03-%02dT10:00:00+08:00", 1+i%28)))
	}
	doc := index.Row{ID: "doc", Kind: drive.KindFile, Name: "ocean notes.txt", FileExtension: ".txt", Category: "document", Size: 5,
		CreatedTime: "2026-03-01T10:00:00+08:00", ModifiedTime: "2026-03-01T10:00:00+08:00"}
	fillIndex(t, svc, append(rows, doc))

	keywords := []string{strings.Repeat("长", 70), "", "  "}
	for range 12 {
		keywords = append(keywords, "OCEAN")
	}
	res, err := svc.SearchIndex("", AIQuery{
		Keywords: keywords,
		Ext:      []string{" .MKV ", "mkv"},
		Category: " Video",
		Kind:     "link", Sort: "random", Starred: "maybe", Played: "sometimes",
		CreatedAfter: "yesterday", ModifiedBefore: "2026-13-01",
		MinSize: -5,
		Limit:   1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 250 || len(res.Hits) != aiSearchLimit {
		t.Fatalf("total %d hits %d, want 250 and the 200 cap", res.Total, len(res.Hits))
	}
	if res.UnderMissing || res.Under != "" || res.Aggregate != nil {
		t.Errorf("under/aggregate = %q %v %v", res.Under, res.UnderMissing, res.Aggregate)
	}
	h := res.Hits[0]
	if h.Path != "全部文件 / 影视 / 纪录片" || h.ParentID != "dB" || h.CreatedTime != "2026-03-01T10:00:00+08:00" || !h.Playable || h.PlayedAt != "" {
		t.Errorf("hit = %+v", h)
	}
	// Unknown sort fell back to modified, newest first.
	if !strings.HasPrefix(h.ModifiedTime, "2026-03-28") {
		t.Errorf("first hit modified %s, want the newest", h.ModifiedTime)
	}

	q := cleanQuery(AIQuery{Keywords: keywords, Ext: []string{" .MKV ", "mkv", ""}, Limit: -1, Aggregate: &AIAggregate{By: "folder", Measure: "weight", Limit: 99}})
	if len(q.Keywords) != aiKeywords || len([]rune(q.Keywords[0])) != aiKeywordRunes || !slices.Equal(q.Ext, []string{"mkv"}) || q.Limit != aiSearchDefault {
		t.Errorf("cleaned = %d keywords (%d runes first), ext %v, limit %d", len(q.Keywords), len([]rune(q.Keywords[0])), q.Ext, q.Limit)
	}
	if *q.Aggregate != (AIAggregate{By: "folder", Measure: "size", Limit: aiAggregateLimit}) {
		t.Errorf("aggregate = %+v", *q.Aggregate)
	}
	if q := cleanQuery(AIQuery{Aggregate: &AIAggregate{By: "colour"}}); q.Aggregate != nil {
		t.Errorf("an aggregate by nothing known should turn into hits, got %+v", q.Aggregate)
	}
	if q := cleanQuery(AIQuery{Sort: "played"}); q.Played != "any" {
		t.Errorf("sort played without played = %q, want any", q.Played)
	}
}

func TestSearchIndexResolvesUnder(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	fillIndex(t, svc, []index.Row{
		indexedFolder("dA", "", "影视"), indexedFolder("dB", "dA", "纪录片"),
		indexedFolder("dE", "", "收藏"), indexedFolder("dF", "dE", "旧"), indexedFolder("dC", "dF", "纪录片"),
		indexedVideo("v1", "dB", "a.mkv", 1, "2026-03-01T00:00:00Z"),
		indexedVideo("v2", "dC", "b.mkv", 2, "2026-03-02T00:00:00Z"),
		indexedVideo("v3", "", "c.mkv", 3, "2026-03-03T00:00:00Z"),
	})
	res, err := svc.SearchIndex("", AIQuery{Under: "电影"})
	if err != nil || !res.UnderMissing || res.Hits == nil || len(res.Hits) != 0 || res.Total != 0 {
		t.Errorf("missing under = %+v err=%v, want UnderMissing and no hits", res, err)
	}
	res, _ = svc.SearchIndex("", AIQuery{Under: " /全部文件/影视/纪录片/ ", Kind: "file"})
	if res.Under != "全部文件 / 影视 / 纪录片" || !slices.Equal(hitIDs(res.Hits), []string{"v1"}) {
		t.Errorf("exact path = %q %v", res.Under, hitIDs(res.Hits))
	}
	// No Documentaries at the root: the name reaches every folder so named,
	// shortest path first.
	res, _ = svc.SearchIndex("", AIQuery{Under: "纪录片", Kind: "file"})
	if res.Under != "全部文件 / 影视 / 纪录片、全部文件 / 收藏 / 旧 / 纪录片" || !slices.Equal(hitIDs(res.Hits), []string{"v2", "v1"}) {
		t.Errorf("by name = %q %v", res.Under, hitIDs(res.Hits))
	}
	// A folder at that exact path from the root wins over the others.
	res, _ = svc.SearchIndex("", AIQuery{Under: "收藏/旧/纪录片", Kind: "file"})
	if res.Under != "全部文件 / 收藏 / 旧 / 纪录片" || !slices.Equal(hitIDs(res.Hits), []string{"v2"}) {
		t.Errorf("deep exact path = %q %v", res.Under, hitIDs(res.Hits))
	}
	res, _ = svc.SearchIndex("", AIQuery{Under: "全部文件", Kind: "file"})
	if res.Under != "全部文件" || len(res.Hits) != 3 {
		t.Errorf("root = %q %v", res.Under, hitIDs(res.Hits))
	}
}

func TestSearchIndexExactFolderID(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	fillIndex(t, svc, []index.Row{
		indexedFolder("folder", "", "影视"),
		indexedFolder("other-folder", "", "影视"),
		indexedVideo("selected", "folder", "match.mkv", 100, "2026-03-01T00:00:00Z"),
		indexedVideo("outside", "other-folder", "match.mkv", 200, "2026-03-02T00:00:00Z"),
	})
	activePlays(t, svc).opened(playRecord{FileID: "selected", Position: 500, Duration: 1000, PlayedAt: "2026-10-12T08:00:00Z"})
	res, err := svc.SearchIndex("", AIQuery{
		FolderID: "folder", Under: "不存在", IDs: []string{"selected", "outside"},
		Keywords: []string{"match"}, Played: "unfinished",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Under != "全部文件 / 影视" || res.UnderMissing || res.Total != 1 || !slices.Equal(hitIDs(res.Hits), []string{"selected"}) {
		t.Errorf("exact folder = %+v", res)
	}
	for _, id := range []string{"missing", "selected"} {
		res, err := svc.SearchIndex("", AIQuery{FolderID: id, Under: "影视"})
		if err != nil {
			t.Fatal(err)
		}
		if !res.UnderMissing || res.Total != 0 || len(res.Hits) != 0 {
			t.Errorf("invalid folder %s = %+v", id, res)
		}
	}
}

func TestSearchIndexSelectedIDs(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	fillIndex(t, svc, []index.Row{
		indexedFolder("folder", "", "影视"),
		indexedVideo("selected", "folder", "match.mkv", 100, "2026-03-01T00:00:00Z"),
		indexedVideo("other", "folder", "match other.mkv", 200, "2026-03-02T00:00:00Z"),
		indexedVideo("wrong-name", "folder", "different.mkv", 300, "2026-03-03T00:00:00Z"),
		indexedVideo("outside", "", "match outside.mkv", 400, "2026-03-04T00:00:00Z"),
	})
	plays := activePlays(t, svc)
	for _, id := range []string{"selected", "other"} {
		plays.opened(playRecord{FileID: id, Position: 500, Duration: 1000, PlayedAt: "2026-10-12T08:00:00Z"})
	}
	for _, tt := range []struct {
		name  string
		q     AIQuery
		want  []string
		total int
	}{
		{"keywords and under", AIQuery{IDs: []string{"selected", "wrong-name", "outside"}, Keywords: []string{"match"}, Under: "影视"}, []string{"selected"}, 1},
		{"empty selection", AIQuery{IDs: []string{}}, []string{}, 0},
		{"folder does not recurse", AIQuery{IDs: []string{"folder"}}, []string{"folder"}, 1},
		{"played intersection", AIQuery{IDs: []string{"selected", "wrong-name"}, Played: "unfinished"}, []string{"selected"}, 1},
		{"empty played selection", AIQuery{IDs: []string{}, Played: "any"}, []string{}, 0},
		{"disjoint played selection", AIQuery{IDs: []string{"wrong-name"}, Played: "any"}, []string{}, 0},
		{"limit preserves selected total", AIQuery{IDs: []string{"selected", "other"}, Sort: "name", Asc: true, Limit: 1}, []string{"other"}, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := svc.SearchIndex("", tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if got := hitIDs(res.Hits); !slices.Equal(got, tt.want) || res.Total != tt.total {
				t.Errorf("hits = %v total = %d, want %v total = %d", got, res.Total, tt.want, tt.total)
			}
		})
	}
	for _, ids := range [][]string{nil, {}, {"selected"}} {
		q := cleanQuery(AIQuery{IDs: ids})
		if (q.IDs == nil) != (ids == nil) || !slices.Equal(q.IDs, ids) {
			t.Errorf("cleanQuery IDs = %#v, want %#v", q.IDs, ids)
		}
		search := q.search()
		if (search.IDs == nil) != (ids == nil) || !slices.Equal(search.IDs, ids) {
			t.Errorf("search IDs = %#v, want %#v", search.IDs, ids)
		}
	}
	for _, ids := range [][]string{{"selected"}, {}} {
		res, err := svc.SearchIndex("", AIQuery{IDs: ids, Aggregate: &AIAggregate{By: "ext", Measure: "size"}})
		if err != nil {
			t.Fatal(err)
		}
		want := []AIAggregateRow{}
		if len(ids) != 0 {
			want = append(want, AIAggregateRow{Key: "mkv", Label: "mkv", Size: 100, Count: 1})
		}
		if res.Total != len(ids) || !slices.Equal(res.Aggregate, want) {
			t.Errorf("aggregate IDs %v = %+v total %d, want %+v", ids, res.Aggregate, res.Total, want)
		}
	}
}

func TestSearchIndexPlayedUsesTheAccountsPlays(t *testing.T) {
	events := `{"events":[` + playEvent("cloud", "2026-10-12T20:00:00+08:00", 600, 7200, liveRef("cloud")) + `]}`
	svc := aiSearchService(t, events, nil)
	fillIndex(t, svc, []index.Row{
		indexedVideo("cloud", "", "cloud.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("half", "", "half.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("credits", "", "credits.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("opened", "", "opened.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("noruntime", "", "noruntime.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("never", "", "never.mkv", 1, "2026-01-01T00:00:00Z"),
	})
	plays := activePlays(t, svc)
	for _, r := range []playRecord{
		{FileID: "noruntime", Position: 100, PlayedAt: "2026-10-12T05:00:00Z"},
		{FileID: "opened", Position: 4, Duration: 1000, PlayedAt: "2026-10-12T06:00:00Z"},
		{FileID: "credits", Position: 975, Duration: 1000, PlayedAt: "2026-10-12T07:00:00Z"}, // in the last 30 s
		{FileID: "half", Position: 500, Duration: 1000, PlayedAt: "2026-10-12T08:00:00Z"},
	} {
		plays.opened(r)
	}

	res, err := svc.SearchIndex("", AIQuery{Played: "unfinished", Sort: "played"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hitIDs(res.Hits), []string{"cloud", "half"}) || res.Total != 2 {
		t.Fatalf("unfinished = %v total %d, want cloud (12:00Z) then half (08:00Z)", hitIDs(res.Hits), res.Total)
	}
	if h := res.Hits[0]; h.Position != 600 || h.Duration != 7200 || h.PlayedAt != "2026-10-12T12:00:00Z" {
		t.Errorf("cloud hit = %v/%v at %s", h.Position, h.Duration, h.PlayedAt)
	}
	// Sorting by play time lists played files, oldest first when asked.
	res, _ = svc.SearchIndex("", AIQuery{Sort: "played", Asc: true, Limit: 3})
	if !slices.Equal(hitIDs(res.Hits), []string{"noruntime", "opened", "credits"}) || res.Total != 5 {
		t.Errorf("played asc = %v total %d", hitIDs(res.Hits), res.Total)
	}
	// Without a played filter the positions still come along.
	res, _ = svc.SearchIndex("", AIQuery{Keywords: []string{"half", "never"}, Sort: "name", Asc: true})
	if len(res.Hits) != 2 || res.Hits[0].Position != 500 || res.Hits[1].PlayedAt != "" {
		t.Errorf("plain search = %+v", res.Hits)
	}
}

// watched (playback finished) is plays.go's finished rule: past 98 % or within
// the last ten seconds; the credits zone unfinished leaves out is not
// enough, and an unknown runtime never is.
func TestSearchIndexPlayedFinished(t *testing.T) {
	events := `{"events":[` + playEvent("cloud", "2026-10-12T20:00:00+08:00", 7195, 7200, liveRef("cloud")) + `]}`
	svc := aiSearchService(t, events, nil)
	fillIndex(t, svc, []index.Row{
		indexedVideo("cloud", "", "cloud.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("end", "", "end.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("credits", "", "credits.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("half", "", "half.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("noruntime", "", "noruntime.mkv", 1, "2026-01-01T00:00:00Z"),
		indexedVideo("never", "", "never.mkv", 1, "2026-01-01T00:00:00Z"),
	})
	plays := activePlays(t, svc)
	for _, r := range []playRecord{
		{FileID: "noruntime", Position: 5000, PlayedAt: "2026-10-12T05:00:00Z"},
		{FileID: "half", Position: 500, Duration: 1000, PlayedAt: "2026-10-12T06:00:00Z"},
		{FileID: "credits", Position: 975, Duration: 1000, PlayedAt: "2026-10-12T07:00:00Z"},
		{FileID: "end", Position: 1000, Duration: 1000, PlayedAt: "2026-10-12T08:00:00Z"},
	} {
		plays.opened(r)
	}
	res, err := svc.SearchIndex("", AIQuery{Played: "Finished", Sort: "played"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hitIDs(res.Hits), []string{"cloud", "end"}) || res.Total != 2 {
		t.Errorf("finished = %v total %d, want cloud then end", hitIDs(res.Hits), res.Total)
	}
	if q := cleanQuery(AIQuery{Played: "finished"}); q.Played != "finished" {
		t.Errorf("cleaned played = %q", q.Played)
	}
}

func TestSearchIndexAggregates(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	fillIndex(t, svc, []index.Row{
		indexedFolder("dA", "", "影视"), indexedFolder("dB", "dA", "纪录片"),
		indexedVideo("v1", "dB", "a.mkv", 300, "2026-03-01T00:00:00Z"),
		indexedVideo("v2", "dB", "b.mkv", 200, "2026-03-01T00:00:00Z"),
		indexedVideo("v3", "", "c.mkv", 1000, "2026-03-01T00:00:00Z"),
	})
	res, err := svc.SearchIndex("", AIQuery{Aggregate: &AIAggregate{By: "folder", Measure: "count"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []AIAggregateRow{
		{Key: "dB", Label: "全部文件 / 影视 / 纪录片", Size: 500, Count: 2},
		{Key: "", Label: "全部文件", Size: 1000, Count: 1},
	}
	if res.Total != 3 || !slices.Equal(res.Aggregate, want) || len(res.Hits) != 0 {
		t.Errorf("by folder = %+v total %d", res.Aggregate, res.Total)
	}
	res, _ = svc.SearchIndex("", AIQuery{Under: "影视", Aggregate: &AIAggregate{By: "ext", Measure: "size"}})
	if want := []AIAggregateRow{{Key: "mkv", Label: "mkv", Size: 500, Count: 2}}; !slices.Equal(res.Aggregate, want) {
		t.Errorf("by ext under Movies and TV = %+v", res.Aggregate)
	}
}

func TestFindFoldersExactPathThenName(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	fillIndex(t, svc, []index.Row{
		indexedFolder("dA", "", "影视"), indexedFolder("dB", "dA", "纪录片"), indexedFolder("dD", "dB", "纪录片"),
		indexedFolder("dX", "", "Other"), indexedFolder("dY", "dX", "纪录片 4K"),
	})
	got, err := svc.FindFolders("", "全部文件 / 影视 / 纪录片")
	if err != nil || len(got) != 1 || got[0].ID != "dB" || got[0].Path != "全部文件 / 影视 / 纪录片" {
		t.Fatalf("exact = %+v err=%v", got, err)
	}
	if want := []Crumb{{ID: "dA", Name: "影视"}, {ID: "dB", Name: "纪录片"}}; !slices.Equal(got[0].Trail, want) {
		t.Errorf("trail = %+v", got[0].Trail)
	}
	got, _ = svc.FindFolders("", "电影/纪录片")
	if ids := folderIDs(got); !slices.Equal(ids, []string{"dB", "dD"}) {
		t.Errorf("by last segment = %v, want shortest path first", ids)
	}
	got, _ = svc.FindFolders("", "4k")
	if ids := folderIDs(got); !slices.Equal(ids, []string{"dY"}) {
		t.Errorf("containing = %v", ids)
	}
	got, _ = svc.FindFolders("", "/")
	if len(got) != 1 || got[0].ID != "" || got[0].Path != "全部文件" || got[0].Trail == nil {
		t.Errorf("root = %+v", got)
	}
	got, err = svc.FindFolders("", "nothing")
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("nothing = %#v err=%v, want an empty list", got, err)
	}
}

func folderIDs(ms []FolderMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestFolderTreeRefusesMoreThan5000(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	rows := []index.Row{indexedFolder("big", "", "big"), indexedFolder("sub", "big", "sub"), indexedFolder("small", "", "small"),
		indexedVideo("s1", "small", "s1.mkv", 1, "2026-01-01T00:00:00Z")}
	for i := range maxTree {
		rows = append(rows, indexedVideo(fmt.Sprint("b", i), "sub", fmt.Sprint(i, ".mkv"), 1, "2026-01-01T00:00:00Z"))
	}
	fillIndex(t, svc, rows)
	if _, err := svc.FolderTree("", "big"); !errors.Is(err, errTreeTooLarge) {
		t.Errorf("5001 entries: %v, want errTreeTooLarge", err)
	}
	if got, err := svc.FolderTree("", "sub"); err != nil || len(got) != maxTree {
		t.Errorf("exactly 5000: %d entries, err=%v", len(got), err)
	}
	got, err := svc.FolderTree("", "small")
	if err != nil || len(got) != 1 || got[0].ID != "s1" || got[0].ParentID != "small" {
		t.Errorf("small = %+v err=%v", got, err)
	}
	if _, err := svc.FolderTree("", "s1"); !errors.Is(err, errNoSuchFolder) {
		t.Errorf("a file as the range: %v", err)
	}
}

func TestMediaResolutionsFromBatchGet(t *testing.T) {
	media := func(w, h int, origin bool) map[string]any {
		return map[string]any{"is_origin": origin, "video": map[string]any{"width": w, "height": h}}
	}
	files := map[string][]map[string]any{
		"uhd":      {media(1920, 1080, false), media(3840, 2160, true)}, // the original, not the default transcode
		"noorigin": {media(1280, 720, false), media(854, 480, false)},
		"pal":      {media(720, 576, true)},
		"ntsc":     {media(720, 480, true)},
		"tiny":     {media(320, 240, true)},
		"bare":     nil,
	}
	var (
		mu       sync.Mutex
		requests [][]string
	)
	svc := aiSearchService(t, `{"events":[]}`, func(ids []string) string {
		mu.Lock()
		requests = append(requests, ids)
		mu.Unlock()
		var out []map[string]any
		for _, id := range ids {
			if ms, ok := files[id]; ok {
				out = append(out, map[string]any{"id": id, "kind": drive.KindFile, "medias": ms})
			}
		}
		data, _ := json.Marshal(map[string]any{"files": out})
		return string(data)
	})

	ids := []string{"uhd", "noorigin", "pal", "ntsc", "tiny", "bare", "trashed", "uhd"}
	for i := range 150 {
		ids = append(ids, fmt.Sprint("filler", i))
	}
	got, err := svc.MediaResolutions("", ids)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"uhd": "2160p", "noorigin": "720p", "pal": "576p", "ntsc": "480p"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("resolutions = %v, want %v", got, want)
	}
	if len(requests) != 2 {
		t.Errorf("%d requests, want 2 for 157 ids", len(requests))
	}
	for _, r := range requests {
		if len(r) > drive.MaxListLimit {
			t.Errorf("a request carried %d ids", len(r))
		}
	}
	if _, err := svc.MediaResolutions("", make501()); !errors.Is(err, errTooManyIDs) {
		t.Errorf("501 ids: %v", err)
	}
}

func make501() []string {
	out := make([]string, maxResolutionIDs+1)
	for i := range out {
		out[i] = fmt.Sprint("id", i)
	}
	return out
}
