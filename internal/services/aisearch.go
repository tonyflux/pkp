package services

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// The AI assistant's Find Files and Media Organize read the drive from the local index
// only. What a model contributes is an AIQuery, which arrives from the
// webview untrusted: every field is checked again here and reaches SQL as a
// parameter. Hits, paths and play positions all come from the index and the
// play record — a model can narrow what is read, never put a file into the
// answer.

const (
	aiSearchLimit       = 200 // most hits one search returns
	aiSearchDefault     = 50  // hits for a limit of zero
	aiKeywords          = 10  // keywords kept
	aiKeywordRunes      = 64  // runes kept of each keyword
	aiExts              = 20  // extensions kept
	aiAggregateLimit    = 50  // most groups of an aggregate
	aiAggregateDefault  = 10  // groups for a limit of zero
	maxTree             = 5000
	maxFolderMatches    = 20
	folderCandidates    = 200 // folders read before the shortest paths are kept
	maxResolutionIDs    = 500
	resolutionParallel  = 4
	unfinishedFrom      = 5.0    // seconds: below this a video was only opened
	unfinishedTail      = 30.0   // seconds before the end that count as watched
	rootFolderName      = "全部文件" // i18n-ignore: a path segment the AI tools read and write
	underPathsSeparator = "、"
)

var (
	errIndexNotReady = i18n.New("本地索引还在建立，完成后才能查找")
	errTreeTooLarge  = &lazyError{func() string { return i18n.F("这个范围里超过 %d 项，请选择更小的文件夹", maxTree) }}
	errNoSuchFolder  = i18n.New("找不到这个文件夹，它可能已被移动或删除")
	errTooManyIDs    = &lazyError{func() string { return i18n.F("一次最多查询 %d 个文件的清晰度", maxResolutionIDs) }}
)

// AIQuery is what Find Files asks of the index; the webview builds it from the
// model's answer.
type AIQuery struct {
	// IDs is a local-only selection: nil is unrestricted, empty matches nothing.
	IDs []string `json:"ids,omitempty"`
	// FolderID constrains the search to an exact local folder, not a name match.
	FolderID string `json:"folderId,omitempty"`
	// Keywords match when any is a case-insensitive substring of the name.
	Keywords []string `json:"keywords"`
	// Category is "" | video | image | document | other, as the file page's
	// Type chip; other is a file in none of the three.
	Category string   `json:"category"`
	Ext      []string `json:"ext"`     // lower case, no dot
	MinSize  int64    `json:"minSize"` // bytes, 0 = open
	MaxSize  int64    `json:"maxSize"`
	// The dates are "YYYY-MM-DD", inclusive days of this computer's zone.
	CreatedAfter   string `json:"createdAfter"`
	CreatedBefore  string `json:"createdBefore"`
	ModifiedAfter  string `json:"modifiedAfter"`
	ModifiedBefore string `json:"modifiedBefore"`
	// Under is a folder path from the root ("Media/Documentaries") or a folder name.
	Under   string `json:"under"`
	Starred string `json:"starred"` // "" | yes | no
	// Played is "" | any (a play record exists) | unfinished (started, not
	// watched to the end) | finished (watched to the end, credits included:
	// Watched).
	Played string `json:"played"`
	Kind   string `json:"kind"` // "" | file | folder
	// Sort is "" (= modified) | modified | created | size | name | played.
	Sort      string       `json:"sort"`
	Asc       bool         `json:"asc"`
	Limit     int          `json:"limit"` // 1..200, 0 → 50
	Aggregate *AIAggregate `json:"aggregate"`
}

// AIAggregate asks for sums instead of hits.
type AIAggregate struct {
	By      string `json:"by"`      // folder | category | ext
	Measure string `json:"measure"` // size | count
	Limit   int    `json:"limit"`   // ≤ 50, 0 → 10
}

// AISearchHit is one file or folder found.
type AISearchHit struct {
	// Entry.Path is the containing folder, "All Files / Documentaries / Ocean".
	Entry
	CreatedTime string `json:"createdTime"`
	// Position, Duration (seconds) and PlayedAt (RFC 3339 UTC) come from the
	// account's plays; zero and "" when it has none.
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	PlayedAt string  `json:"playedAt"`
}

// AIAggregateRow is one group of an aggregate. Key is the folder id ("" =
// the root), the category or the extension; Label is the folder's path, the
// category or the extension.
type AIAggregateRow struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Size  int64  `json:"size"`
	Count int    `json:"count"`
}

// AISearchResult answers an AIQuery: hits, or the aggregate when one was
// asked for.
type AISearchResult struct {
	Hits []AISearchHit `json:"hits"`
	// Total is how many matched before Limit (files only for an aggregate).
	Total     int              `json:"total"`
	Aggregate []AIAggregateRow `json:"aggregate"`
	// Under is the folder path Under resolved to, several joined by "、";
	// "" when not asked.
	Under string `json:"under"`
	// UnderMissing says Under named no folder; there are then no hits.
	UnderMissing bool `json:"underMissing"`
}

// FolderMatch is one folder a path or name led to.
type FolderMatch struct {
	ID    string  `json:"id"`
	Path  string  `json:"path"` // Canonical path from the drive root.
	Trail []Crumb `json:"trail"`
}

// readyIndex is account's index once it has completed a walk; before that
// it could only answer with part of the drive.
func (d *DriveService) readyIndex(account string) (*accountSession, *index.Store, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, nil, err
	}
	idx := s.index()
	if idx == nil || !idx.syncer.Ready() {
		return nil, nil, errIndexNotReady
	}
	return s, idx.store, nil
}

// SearchIndex answers Find Files from account's index.
func (d *DriveService) SearchIndex(account string, q AIQuery) (AISearchResult, error) {
	s, store, err := d.readyIndex(account)
	if err != nil {
		return AISearchResult{}, err
	}
	q = cleanQuery(q)
	search := q.search()
	out := AISearchResult{Hits: []AISearchHit{}}

	if q.FolderID != "" {
		row, found, err := store.Get(q.FolderID)
		if err != nil {
			return AISearchResult{}, err
		}
		if !found || row.Trashed || row.Kind != drive.KindFolder {
			out.UnderMissing = true
			return out, nil
		}
		crumbs, err := store.Path(q.FolderID)
		if err != nil {
			return AISearchResult{}, err
		}
		out.Under = folderPath(crumbs)
		search.Under = []string{q.FolderID}
	} else if q.Under != "" {
		matches, err := findFolders(store, q.Under)
		if err != nil {
			return AISearchResult{}, err
		}
		if len(matches) == 0 {
			out.UnderMissing = true
			return out, nil
		}
		paths := make([]string, len(matches))
		roots := make([]string, len(matches))
		for i, m := range matches {
			paths[i], roots[i] = m.Path, m.ID
		}
		out.Under = strings.Join(paths, underPathsSeparator)
		if !slices.Contains(roots, "") { // the root is the whole drive
			search.Under = roots
		}
	}

	var plays map[string]RecentPlay
	if q.Played != "" {
		list, err := d.playsOf(s, drive.MaxListLimit, 1)
		if err != nil {
			return AISearchResult{}, err
		}
		plays = map[string]RecentPlay{}
		search.IDs = []string{}
		for _, p := range list {
			if q.Played == "unfinished" && !unfinished(p) || q.Played == "finished" && !finished(p.Position, p.Duration) {
				continue
			}
			plays[p.FileID] = p
			if q.IDs == nil {
				search.IDs = append(search.IDs, p.FileID)
			}
		}
		if q.IDs != nil {
			for _, id := range q.IDs {
				if _, ok := plays[id]; ok {
					search.IDs = append(search.IDs, id)
				}
			}
		}
	}

	if q.Aggregate != nil {
		groups, total, err := store.Aggregate(search, q.Aggregate.By, q.Aggregate.Measure, q.Aggregate.Limit)
		if err != nil {
			return AISearchResult{}, err
		}
		out.Total = total
		out.Aggregate = make([]AIAggregateRow, 0, len(groups))
		for _, g := range groups {
			row := AIAggregateRow{Key: g.Key, Label: g.Key, Size: g.Size, Count: g.Count}
			if q.Aggregate.By == "folder" {
				crumbs, err := store.Path(g.Key)
				if err != nil {
					return AISearchResult{}, err
				}
				row.Label = folderPath(crumbs)
			}
			out.Aggregate = append(out.Aggregate, row)
		}
		return out, nil
	}

	if q.Sort == "played" {
		// The index does not know when a file was played: read every
		// played match (the play list bounds them) and order here.
		search.Sort, search.Limit = "", 0
	}
	rows, total, err := store.Search(search)
	if err != nil {
		return AISearchResult{}, err
	}
	out.Total = total
	if q.Sort == "played" {
		slices.SortFunc(rows, func(a, b index.Row) int {
			c := playedAt(plays[b.ID].PlayedAt).Compare(playedAt(plays[a.ID].PlayedAt))
			if q.Asc {
				c = -c
			}
			return cmp.Or(c, cmp.Compare(a.ID, b.ID))
		})
		rows = rows[:min(len(rows), q.Limit)]
	}

	for _, row := range rows {
		crumbs, err := store.Path(row.ParentID)
		if err != nil {
			return AISearchResult{}, err
		}
		hit := AISearchHit{Entry: toEntry(rowToFile(row)), CreatedTime: row.CreatedTime}
		hit.Path = folderPath(crumbs)
		out.Hits = append(out.Hits, hit)
	}
	if plays == nil && slices.ContainsFunc(out.Hits, func(h AISearchHit) bool { return h.Playable }) {
		// Only for showing where each video was left: a search that
		// cannot reach the cloud still answers.
		list, err := d.playsOf(s, drive.MaxListLimit, 1)
		if err != nil {
			d.log.Warn("drive: no play positions for the search", "error", err)
		}
		plays = map[string]RecentPlay{}
		for _, p := range list {
			plays[p.FileID] = p
		}
	}
	for i := range out.Hits {
		if p, ok := plays[out.Hits[i].ID]; ok {
			out.Hits[i].Position, out.Hits[i].Duration, out.Hits[i].PlayedAt = p.Position, p.Duration, p.PlayedAt
		}
	}
	return out, nil
}

// unfinished is a video started (past the first seconds) and left before
// its last half minute; with an unknown runtime nobody can say.
func unfinished(p RecentPlay) bool {
	return p.Duration > 0 && p.Position >= unfinishedFrom && p.Position < p.Duration-unfinishedTail
}

// cleanQuery brings q within what SearchIndex accepts: text trimmed and
// bounded, unknown enum values dropped to "" (no condition) rather than
// failing the search, limits clamped. Dates that do not parse are dropped
// later, by search.
func cleanQuery(q AIQuery) AIQuery {
	var keywords []string
	for _, k := range q.Keywords {
		k = strings.TrimSpace(k)
		if utf8.RuneCountInString(k) > aiKeywordRunes {
			k = string([]rune(k)[:aiKeywordRunes])
		}
		if k != "" && len(keywords) < aiKeywords {
			keywords = append(keywords, k)
		}
	}
	q.Keywords = keywords
	var exts []string
	for _, e := range q.Ext {
		e = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e)), ".")
		if e != "" && len(exts) < aiExts && !slices.Contains(exts, e) {
			exts = append(exts, e)
		}
	}
	q.Ext = exts
	q.MinSize, q.MaxSize = max(q.MinSize, 0), max(q.MaxSize, 0)
	q.Under = strings.TrimSpace(q.Under)
	q.Category = oneOf(q.Category, "video", "image", "document", "audio", "other", "otherfiles")
	q.Starred = oneOf(q.Starred, "yes", "no")
	q.Played = oneOf(q.Played, "any", "unfinished", "finished")
	q.Kind = oneOf(q.Kind, "file", "folder")
	q.Sort = oneOf(q.Sort, "modified", "created", "size", "name", "played")
	if q.Sort == "played" && q.Played == "" {
		// Ordering by when files were played lists played files.
		q.Played = "any"
	}
	switch {
	case q.Limit <= 0:
		q.Limit = aiSearchDefault
	case q.Limit > aiSearchLimit:
		q.Limit = aiSearchLimit
	}
	if a := q.Aggregate; a != nil {
		by := oneOf(a.By, "folder", "category", "ext")
		if by == "" {
			q.Aggregate = nil // nothing to group by: hits instead
		} else {
			agg := AIAggregate{By: by, Measure: cmp.Or(oneOf(a.Measure, "size", "count"), "size"), Limit: a.Limit}
			switch {
			case agg.Limit <= 0:
				agg.Limit = aiAggregateDefault
			case agg.Limit > aiAggregateLimit:
				agg.Limit = aiAggregateLimit
			}
			q.Aggregate = &agg
		}
	}
	return q
}

// oneOf is v when it is one of allowed, else "".
func oneOf(v string, allowed ...string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if slices.Contains(allowed, v) {
		return v
	}
	return ""
}

// search is a cleaned q as an index search, Under and Played not yet
// resolved.
func (q AIQuery) search() index.Search {
	out := index.Search{
		IDs:      q.IDs,
		Keywords: q.Keywords, Category: q.Category, Exts: q.Ext,
		MinSize: q.MinSize, MaxSize: q.MaxSize,
		Kind: q.Kind, Starred: q.Starred,
		Sort: q.Sort, Asc: q.Asc, Limit: q.Limit,
	}
	if out.Sort == "played" {
		out.Sort = ""
	}
	out.CreatedFrom, _ = dayStart(q.CreatedAfter, 0)
	out.CreatedBefore, _ = dayStart(q.CreatedBefore, 1)
	out.ModifiedFrom, _ = dayStart(q.ModifiedAfter, 0)
	out.ModifiedBefore, _ = dayStart(q.ModifiedBefore, 1)
	return out
}

// dayStart is the start of the local day "YYYY-MM-DD" plus days; ok is
// false (and the bound left open) for anything else.
func dayStart(day string, days int) (time.Time, bool) {
	t, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(day), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t.AddDate(0, 0, days), true
}

// FindFolders resolves a folder path from the root ("Movies & TV/Documentaries", with or
// without a leading All Files) in account's index. When no folder sits at
// that exact path, folders anywhere named like its last segment answer:
// named exactly so (case aside) first, else holding it. At most 20, the
// shortest paths first.
func (d *DriveService) FindFolders(account, path string) ([]FolderMatch, error) {
	_, store, err := d.readyIndex(account)
	if err != nil {
		return nil, err
	}
	matches, err := findFolders(store, path)
	if matches == nil {
		matches = []FolderMatch{}
	}
	return matches, err
}

func findFolders(store *index.Store, path string) ([]FolderMatch, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	var segments []string
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '／' }) {
		if seg = strings.TrimSpace(seg); seg != "" {
			segments = append(segments, seg)
		}
	}
	if len(segments) > 0 && segments[0] == rootFolderName {
		segments = segments[1:]
	}
	if len(segments) == 0 {
		return []FolderMatch{{ID: "", Path: rootFolderName, Trail: []Crumb{}}}, nil
	}
	rows, err := store.FoldersAt(segments)
	if err != nil {
		return nil, err
	}
	last := segments[len(segments)-1]
	for _, contains := range []bool{false, true} {
		if len(rows) > 0 {
			break
		}
		if rows, err = store.FoldersNamed(last, contains, folderCandidates); err != nil {
			return nil, err
		}
	}
	type found struct {
		match FolderMatch
		depth int
	}
	all := make([]found, 0, len(rows))
	for _, r := range rows {
		crumbs, err := store.Path(r.ID)
		if err != nil {
			return nil, err
		}
		trail := make([]Crumb, len(crumbs))
		for i, c := range crumbs {
			trail[i] = Crumb{ID: c.ID, Name: c.Name}
		}
		all = append(all, found{FolderMatch{ID: r.ID, Path: folderPath(crumbs), Trail: trail}, len(crumbs)})
	}
	slices.SortFunc(all, func(a, b found) int {
		return cmp.Or(
			cmp.Compare(a.depth, b.depth),
			cmp.Compare(utf8.RuneCountInString(a.match.Path), utf8.RuneCountInString(b.match.Path)),
			strings.Compare(a.match.Path, b.match.Path),
			strings.Compare(a.match.ID, b.match.ID),
		)
	})
	out := make([]FolderMatch, 0, min(len(all), maxFolderMatches))
	for _, f := range all[:min(len(all), maxFolderMatches)] {
		out = append(out, f.match)
	}
	return out, nil
}

// FolderTree is every live entry below folderID ("" = the root) in
// account's index, folders and files at any depth, each with its ParentID
// so the webview can rebuild the tree. A range over maxTree entries is
// refused: no plan can cover it.
func (d *DriveService) FolderTree(account, folderID string) ([]Entry, error) {
	_, store, err := d.readyIndex(account)
	if err != nil {
		return nil, err
	}
	if folderID != "" {
		row, found, err := store.Get(folderID)
		if err != nil {
			return nil, err
		}
		if !found || row.Trashed || row.Kind != drive.KindFolder {
			return nil, errNoSuchFolder
		}
	}
	rows, err := store.Subtree(folderID, maxTree)
	if err != nil {
		return nil, err
	}
	if len(rows) > maxTree {
		return nil, errTreeTooLarge
	}
	out := make([]Entry, len(rows))
	for i, r := range rows {
		out[i] = toEntry(rowToFile(r))
	}
	return out, nil
}

// MediaResolutions reads the picture size the server measured for each of
// ids (at most maxResolutionIDs) and names its class: id → "2160p" …
// "480p". batchGet carries the media info without any usage (measured
// 2026-10-15); ids that come back without a picture size, or not at all
// (trashed ones are left out), are absent from the answer.
func (d *DriveService) MediaResolutions(account string, ids []string) (map[string]string, error) {
	wanted := map[string]bool{}
	var unique []string
	for _, id := range ids {
		if id != "" && !wanted[id] {
			wanted[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) > maxResolutionIDs {
		return nil, errTooManyIDs
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)
	slots := make(chan struct{}, resolutionParallel)
	for chunk := range slices.Chunk(unique, drive.MaxListLimit) {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			files, err := callIn(d, s, func(ctx context.Context, client *drive.Client) ([]drive.File, error) {
				return client.BatchGetFiles(ctx, chunk, drive.GetFileParams{})
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			for _, f := range files {
				if label := resolutionOf(f); label != "" && wanted[f.ID] {
					out[f.ID] = label
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// resolutionOf names the class of a file's own picture: the original
// rendition's size, else the largest the server lists. tierOf has no 576
// class (playback never offers one), so PAL's 720×576 and 1024×576 are told
// apart from 480 here. Pictures under 480 lines have no name: "".
func resolutionOf(f drive.File) string {
	var width, height int
	for _, m := range f.Medias {
		w, h := m.Video.Width, m.Video.Height
		if w <= 0 || h <= 0 {
			continue
		}
		if m.IsOrigin {
			width, height = w, h
			break
		}
		if w*h > width*height {
			width, height = w, h
		}
	}
	switch tier := tierOf(width, height); tier {
	case 2160, 1440, 1080, 720:
		return fmt.Sprintf("%dp", tier)
	case 480:
		if min(width, height) >= 576 || max(width, height) >= 1024 {
			return "576p"
		}
		return "480p"
	}
	return ""
}
