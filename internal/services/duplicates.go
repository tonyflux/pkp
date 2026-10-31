package services

// Cleanup · Duplicate Files: files with the same content hash
// and size, found in the account's index alone — no request leaves the
// machine for a scan. Each group gets one default copy to keep; the rest
// are suggested for removal, and RemoveDuplicates refuses to take a whole
// group.

import (
	"cmp"
	"errors"
	"slices"
	"time"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// DupQuery is one scan of duplicate files and the page of it wanted.
type DupQuery struct {
	FolderID   string `json:"folderId"`   // "" = whole drive; else that folder and below
	MinSize    int64  `json:"minSize"`    // bytes; smaller groups are only counted (Small)
	Sort       string `json:"sort"`       // "waste" (default) | "copies" | "modified"
	SameFolder bool   `json:"sameFolder"` // only groups whose copies all share one parent_id
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"` // ≤ 500, default 50
}

// DupCopy is one copy of a group.
type DupCopy struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	// Path is the folder holding the copy, "All Files / A / B"; Depth is how
	// many folders down that is.
	Path         string `json:"path"`
	Depth        int    `json:"depth"`
	Size         int64  `json:"size"`
	CreatedTime  string `json:"createdTime"`
	ModifiedTime string `json:"modifiedTime"`
	Starred      bool   `json:"starred"`
	// Played says PKP's own play record (continue watching) has the file.
	Played bool `json:"played"`
	// Keep marks the copy kept by default, exactly one per group.
	Keep bool `json:"keep"`
}

// DupGroup is one set of copies of the same content.
type DupGroup struct {
	Hash       string    `json:"hash"`
	Name       string    `json:"name"`     // most common name among copies
	Category   string    `json:"category"` // video/image/audio/document/archive/other
	Thumbnail  string    `json:"thumbnail"`
	Size       int64     `json:"size"` // one copy
	Count      int       `json:"count"`
	Waste      int64     `json:"waste"` // Size × (Count − 1)
	SameFolder bool      `json:"sameFolder"`
	Copies     []DupCopy `json:"copies"` // the kept one first
}

// DupPage is one page of a scan and the figures of all of it.
type DupPage struct {
	Groups     []DupGroup `json:"groups"`
	Total      int        `json:"total"`  // groups matching (after MinSize/SameFolder)
	Copies     int        `json:"copies"` // redundant copies over all matching groups
	Waste      int64      `json:"waste"`  // reclaimable over all matching groups
	Small      int        `json:"small"`  // groups left out by MinSize
	SmallWaste int64      `json:"smallWaste"`
	Offset     int        `json:"offset"`
	SyncedAt   string     `json:"syncedAt"` // RFC 3339, last completed index walk; "" unknown
}

const (
	dupPageSize = 50
	dupPageMax  = 500
)

var errScanNotReady = i18n.New("索引还没建完，建完才能扫描")

// dupScan is a scan kept for paging: the query without its page and every
// group it found.
type dupScan struct {
	query  DupQuery
	result index.DupResult
}

// ScanDuplicates answers one page of duplicate files from an account's index. A
// first page (Offset 0) scans afresh — that is what rescan asks for; a
// later page of the same query pages the kept scan, whose groups are read
// again so that the copies are current.
func (d *DriveService) ScanDuplicates(account string, q DupQuery) (DupPage, error) {
	s, store, err := d.readyIndex(account)
	if errors.Is(err, errIndexNotReady) {
		return DupPage{}, errScanNotReady
	}
	if err != nil {
		return DupPage{}, err
	}
	if q.Sort == "" {
		q.Sort = "waste"
	}
	if q.Sort != "waste" && q.Sort != "copies" && q.Sort != "modified" {
		return DupPage{}, i18n.Errorf("不支持的排序方式「%s」", q.Sort)
	}
	q.MinSize = max(q.MinSize, 0)
	q.Offset = max(q.Offset, 0)
	if q.Limit <= 0 {
		q.Limit = dupPageSize
	}
	q.Limit = min(q.Limit, dupPageMax)

	result, err := d.dupResult(s.key, store, q)
	if err != nil {
		return DupPage{}, err
	}
	page := DupPage{
		Groups:     []DupGroup{},
		Total:      len(result.Groups),
		Small:      result.Small,
		SmallWaste: result.SmallWaste,
		Offset:     q.Offset,
		SyncedAt:   syncedAt(store),
	}
	for _, g := range result.Groups {
		page.Copies += g.Count - 1
		page.Waste += g.Waste
	}
	slice := result.Groups[min(q.Offset, len(result.Groups)):min(q.Offset+q.Limit, len(result.Groups))]
	if len(slice) == 0 {
		return page, nil
	}
	hashes := make([]string, len(slice))
	for i, g := range slice {
		hashes[i] = g.Hash
	}
	rows, err := store.DupCopies(hashes, q.FolderID)
	if err != nil {
		return DupPage{}, err
	}
	byHash := map[string][]index.Row{}
	for _, row := range rows {
		byHash[row.Hash] = append(byHash[row.Hash], row)
	}
	played := map[string]bool{}
	if plays := s.playLog(); plays != nil {
		for _, p := range plays.list() {
			played[p.FileID] = true
		}
	}
	folders := map[string]folderAt{}
	for _, g := range slice {
		group, err := dupGroup(store, g, byHash[g.Hash], played, folders)
		if err != nil {
			return DupPage{}, err
		}
		if group.Count > 1 { // a copy may have gone since the scan
			page.Groups = append(page.Groups, group)
		}
	}
	return page, nil
}

// dupResult is the scan for q: the kept one when q pages it, a fresh one
// otherwise. Scans hold dupMu throughout, so a remove run ending meanwhile
// (dropDups) cannot be overtaken by a scan it made stale.
func (d *DriveService) dupResult(key string, store *index.Store, q DupQuery) (index.DupResult, error) {
	want := q
	want.Offset, want.Limit = 0, 0
	d.dupMu.Lock()
	defer d.dupMu.Unlock()
	if kept := d.dups[key]; kept != nil && kept.query == want && q.Offset > 0 {
		return kept.result, nil
	}
	result, err := store.Duplicates(index.DupScan{Root: q.FolderID, MinSize: q.MinSize, SameFolder: q.SameFolder, Sort: q.Sort})
	if err != nil {
		return index.DupResult{}, err
	}
	for _, h := range result.Mismatched {
		d.log.Warn("cleanup: duplicate group dropped, its copies differ in size", "hash", h)
	}
	if d.dups == nil {
		d.dups = map[string]*dupScan{}
	}
	d.dups[key] = &dupScan{query: want, result: result}
	return result, nil
}

// dropDups forgets an account's kept scan.
func (d *DriveService) dropDups(key string) {
	d.dupMu.Lock()
	delete(d.dups, key)
	d.dupMu.Unlock()
}

// folderAt is a folder's path as a copy shows it, and its depth.
type folderAt struct {
	path  string
	depth int
}

// dupGroup builds one group from its copies, the default keep first: a
// starred copy, else one PKP played, else the shallowest, else the earliest
// created, else the smallest id.
func dupGroup(store *index.Store, g index.DupGroup, rows []index.Row, played map[string]bool, folders map[string]folderAt) (DupGroup, error) {
	copies := make([]DupCopy, 0, len(rows))
	for _, row := range rows {
		at, ok := folders[row.ParentID]
		if !ok {
			crumbs, err := store.Path(row.ParentID)
			if err != nil {
				return DupGroup{}, err
			}
			at = folderAt{path: folderPath(crumbs), depth: len(crumbs)}
			folders[row.ParentID] = at
		}
		copies = append(copies, DupCopy{
			ID: row.ID, Name: row.Name, ParentID: row.ParentID, Path: at.path, Depth: at.depth,
			Size: row.Size, CreatedTime: row.CreatedTime, ModifiedTime: row.ModifiedTime,
			Starred: row.Starred, Played: played[row.ID],
		})
	}
	slices.SortFunc(copies, keepOrder)
	out := DupGroup{Hash: g.Hash, Size: g.Size, Count: len(copies), Copies: copies}
	if len(copies) == 0 {
		return out, nil
	}
	copies[0].Keep = true
	out.Waste = out.Size * int64(out.Count-1)
	out.SameFolder = true
	names := map[string]int{}
	for _, c := range copies {
		names[c.Name]++
		out.SameFolder = out.SameFolder && c.ParentID == copies[0].ParentID
	}
	for _, c := range copies {
		if names[c.Name] > names[out.Name] {
			out.Name = c.Name
		}
	}
	byID := make(map[string]index.Row, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	out.Category = categoryOf(rowToFile(byID[copies[0].ID]))
	for _, c := range copies {
		if link := byID[c.ID].ThumbnailLink; link != "" {
			out.Thumbnail = link
			break
		}
	}
	return out, nil
}

// keepOrder sorts the copy to keep first (see dupGroup).
func keepOrder(a, b DupCopy) int {
	if a.Starred != b.Starred {
		return boolFirst(a.Starred)
	}
	if a.Played != b.Played {
		return boolFirst(a.Played)
	}
	if c := cmp.Compare(a.Depth, b.Depth); c != 0 {
		return c
	}
	if c := compareStamps(a.CreatedTime, b.CreatedTime); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func boolFirst(a bool) int {
	if a {
		return -1
	}
	return 1
}

// compareStamps orders two RFC 3339 stamps as instants; one that does not
// parse goes after one that does, two such by text.
func compareStamps(a, b string) int {
	ta, ea := time.Parse(time.RFC3339, a)
	tb, eb := time.Parse(time.RFC3339, b)
	switch {
	case ea == nil && eb == nil:
		return ta.Compare(tb)
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return cmp.Compare(a, b)
}

// syncedAt is when the index last completed a walk; "" when unknown.
func syncedAt(store *index.Store) string {
	at, err := store.Meta(index.MetaSyncedAt)
	if err != nil {
		return ""
	}
	return at
}

// RemoveDuplicates is RemoveFiles behind the keep-one guard: when
// ids hold every live copy of some content as the index has it now, the
// whole request is refused and nothing is removed.
func (d *DriveService) RemoveDuplicates(account string, fileIDs []string, permanent bool) (RemoveReport, error) {
	phase := phaseTrash
	if permanent {
		phase = phaseDelete
	}
	return d.remove(account, fileIDs, phase, d.keepOne)
}

// keepOne refuses ids that would leave some duplicate group with no copy.
func (d *DriveService) keepOne(s *accountSession, ids []string) error {
	idx := s.index()
	if idx == nil || !idx.syncer.Ready() {
		return errScanNotReady
	}
	rows, err := idx.store.Rows(ids)
	if err != nil {
		return err
	}
	var hashes []string
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row.Hash) == 40 && !seen[row.Hash] {
			seen[row.Hash] = true
			hashes = append(hashes, row.Hash)
		}
	}
	copies, err := idx.store.DupCopies(hashes, "")
	if err != nil {
		return err
	}
	picked := make(map[string]bool, len(ids))
	for _, id := range ids {
		picked[id] = true
	}
	left := map[string]int{}
	name := map[string]string{}
	for _, c := range copies {
		name[c.Hash] = c.Name
		if !picked[c.ID] {
			left[c.Hash]++
		}
	}
	for _, h := range hashes {
		if n, ok := name[h]; ok && left[h] == 0 {
			return i18n.Errorf("每组至少要保留一份：「%s」的所有副本都被选中了，没有删除任何文件", n)
		}
	}
	return nil
}
