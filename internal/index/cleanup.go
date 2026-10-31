package index

import (
	"fmt"
	"slices"
	"strings"
)

// Cleanup (duplicate files and empty folders) reads the index only: every
// figure here is what the last walk saw, never a server request.

// dupCond is what counts as a copy: a live, fully uploaded file with the
// server's 40-character content hash and some bytes. An upload still in
// flight carries a random placeholder hash, and zero-byte files all share
// one, so both would make meaningless groups.
const dupCond = `kind != 'drive#folder' AND trashed = 0 AND phase = 'PHASE_TYPE_COMPLETE' AND length(hash) = 40 AND size > 0`

// belowWith is a WITH clause naming the live folders below one root; scoped
// keeps the rows whose parent is the root or one of them. Both take the
// root as their one argument.
const (
	belowWith = `below(id) AS (
  SELECT id FROM files WHERE parent_id = ? AND kind = 'drive#folder' AND trashed = 0
  UNION
  SELECT f.id FROM files f JOIN below b ON f.parent_id = b.id WHERE f.kind = 'drive#folder' AND f.trashed = 0)`
	scoped = `(parent_id = ? OR parent_id IN (SELECT id FROM below))`
)

// DupScan selects the duplicate groups Duplicates reads.
type DupScan struct {
	// Root is "" for the whole drive, else that folder and everything
	// below it: a group then counts only the copies in there.
	Root string
	// MinSize moves groups whose copies are smaller out of Groups and into
	// the Small figures.
	MinSize int64
	// SameFolder keeps only groups whose copies all share one parent.
	SameFolder bool
	// Sort is "" or "waste" (bytes reclaimable, largest first), "copies"
	// (most copies first) or "modified" (most recently modified copy first).
	Sort string
}

// DupGroup is one set of files with the same content.
type DupGroup struct {
	Hash       string
	Size       int64 // of one copy
	Count      int
	Waste      int64 // Size × (Count − 1)
	SameFolder bool
	// Modified is the newest modified_time of the copies, as SQLite's
	// datetime() renders it (UTC, comparable as text).
	Modified string
}

// DupResult is every group a DupScan matches, in its order.
type DupResult struct {
	Groups     []DupGroup
	Small      int   // groups left out by MinSize
	SmallWaste int64 // what those would reclaim
	// Mismatched are hashes whose copies differ in size. The server has
	// never been seen to do that; such a group is dropped, not guessed at.
	Mismatched []string
}

var dupOrder = map[string]string{
	"":         "min(size) * (count(*) - 1) DESC, hash",
	"waste":    "min(size) * (count(*) - 1) DESC, hash",
	"copies":   "count(*) DESC, min(size) * (count(*) - 1) DESC, hash",
	"modified": "6 DESC, hash",
}

// Duplicates groups the live files by content hash, one statement over the
// whole table (or the subtree), and returns every group in q's order;
// paging is the caller's.
func (s *Store) Duplicates(q DupScan) (DupResult, error) {
	order, ok := dupOrder[q.Sort]
	if !ok {
		return DupResult{}, fmt.Errorf("index: unknown duplicate sort %q", q.Sort)
	}
	with, cond, args := "", dupCond, []any{}
	if q.Root != "" {
		with = "WITH RECURSIVE " + belowWith + " "
		cond += " AND " + scoped
		args = append(args, q.Root, q.Root)
	}
	rows, err := s.db.Query(with+`SELECT hash, min(size), max(size), count(*), count(DISTINCT parent_id),
  COALESCE(max(datetime(modified_time)), '') FROM files WHERE `+cond+`
GROUP BY hash HAVING count(*) > 1 ORDER BY `+order, args...)
	if err != nil {
		return DupResult{}, fmt.Errorf("index: duplicates: %w", err)
	}
	defer rows.Close()
	out := DupResult{Groups: []DupGroup{}}
	for rows.Next() {
		var g DupGroup
		var maxSize int64
		var parents int
		if err := rows.Scan(&g.Hash, &g.Size, &maxSize, &g.Count, &parents, &g.Modified); err != nil {
			return DupResult{}, fmt.Errorf("index: duplicates: %w", err)
		}
		if g.Size != maxSize {
			out.Mismatched = append(out.Mismatched, g.Hash)
			continue
		}
		g.SameFolder = parents == 1
		g.Waste = g.Size * int64(g.Count-1)
		if q.SameFolder && !g.SameFolder {
			continue
		}
		if g.Size < q.MinSize {
			out.Small++
			out.SmallWaste += g.Waste
			continue
		}
		out.Groups = append(out.Groups, g)
	}
	if err := rows.Err(); err != nil {
		return DupResult{}, fmt.Errorf("index: duplicates: %w", err)
	}
	return out, nil
}

// inBatch bounds the values of one IN list.
const inBatch = 500

// DupCopies returns the copies of the given hashes, under root ("" = the
// whole drive) as Duplicates counts them, by hash then id.
func (s *Store) DupCopies(hashes []string, root string) ([]Row, error) {
	var out []Row
	for start := 0; start < len(hashes); start += inBatch {
		batch := hashes[start:min(start+inBatch, len(hashes))]
		with, args := "", []any{}
		cond := dupCond + " AND hash IN (" + placeholders(len(batch)) + ")"
		if root != "" {
			with = "WITH RECURSIVE " + belowWith + " "
			args = append(args, root)
		}
		for _, h := range batch {
			args = append(args, h)
		}
		if root != "" {
			cond += " AND " + scoped
			args = append(args, root)
		}
		rows, err := s.db.Query(with+"SELECT "+selectColumns+" FROM files WHERE "+cond+" ORDER BY hash, id", args...)
		if err != nil {
			return nil, fmt.Errorf("index: copies: %w", err)
		}
		got, err := collect(rows)
		if err != nil {
			return nil, fmt.Errorf("index: copies: %w", err)
		}
		out = append(out, got...)
	}
	return out, nil
}

// Rows reads the rows of the given ids that the index holds, trashed ones
// included, in no particular order.
func (s *Store) Rows(ids []string) ([]Row, error) {
	var out []Row
	for start := 0; start < len(ids); start += inBatch {
		batch := ids[start:min(start+inBatch, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := s.db.Query("SELECT "+selectColumns+" FROM files WHERE id IN ("+placeholders(len(batch))+")", args...)
		if err != nil {
			return nil, fmt.Errorf("index: rows: %w", err)
		}
		got, err := collect(rows)
		if err != nil {
			return nil, fmt.Errorf("index: rows: %w", err)
		}
		out = append(out, got...)
	}
	return out, nil
}

// EmptyFolder is a top-level empty folder: nothing live below it but other
// empty folders, and its own parent is not empty.
type EmptyFolder struct {
	ID, ParentID, Name, CreatedTime string
	// Nested is how many empty folders sit somewhere inside it.
	Nested int
	// HasTrashed says its subtree holds recycle-bin entries: restoring
	// them needs the folder to be there.
	HasTrashed bool
}

// EmptyResult is what EmptyFolders found.
type EmptyResult struct {
	Folders []EmptyFolder // top level only, by id
	Total   int           // every empty folder, nested ones included
}

// EmptyFolders finds the live NORMAL folders that hold no live file at any
// depth, below root ("" = the whole drive; root itself is never one). Every
// live file marks all its ancestors as holders in one recursive pass; the
// live folders left over are empty. Special folders (anything not NORMAL,
// such as the offline-download target) never count, nor does the root,
// which has no row.
func (s *Store) EmptyFolders(root string) (EmptyResult, error) {
	with := `WITH RECURSIVE holder(id) AS (
  SELECT DISTINCT parent_id FROM files WHERE kind != 'drive#folder' AND trashed = 0 AND parent_id != ''
  UNION
  SELECT f.parent_id FROM files f JOIN holder h ON f.id = h.id WHERE f.parent_id != '')`
	cond := `kind = 'drive#folder' AND trashed = 0 AND folder_type = 'NORMAL' AND id != '' AND id NOT IN (SELECT id FROM holder)`
	var args []any
	if root != "" {
		with += ", " + belowWith
		cond += " AND id IN (SELECT id FROM below)"
		args = append(args, root)
	}
	rows, err := s.db.Query(with+` SELECT id, parent_id, name, created_time,
  EXISTS (SELECT 1 FROM files t WHERE t.parent_id = files.id AND t.trashed = 1)
FROM files WHERE `+cond, args...)
	if err != nil {
		return EmptyResult{}, fmt.Errorf("index: empty folders: %w", err)
	}
	defer rows.Close()
	all := map[string]*EmptyFolder{}
	for rows.Next() {
		var f EmptyFolder
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedTime, &f.HasTrashed); err != nil {
			return EmptyResult{}, fmt.Errorf("index: empty folders: %w", err)
		}
		all[f.ID] = &f
	}
	if err := rows.Err(); err != nil {
		return EmptyResult{}, fmt.Errorf("index: empty folders: %w", err)
	}
	// A trashed entry anywhere inside, and every nested empty folder,
	// count for the top-level folder above them.
	type tally struct {
		nested  int
		trashed bool
	}
	tops := map[string]*tally{}
	for _, f := range all {
		top := f
		for range maxDepth {
			up, ok := all[top.ParentID]
			if !ok {
				break
			}
			top = up
		}
		t := tops[top.ID]
		if t == nil {
			t = &tally{}
			tops[top.ID] = t
		}
		if top != f {
			t.nested++
		}
		t.trashed = t.trashed || f.HasTrashed
	}
	out := EmptyResult{Folders: make([]EmptyFolder, 0, len(tops)), Total: len(all)}
	for id, t := range tops {
		f := *all[id]
		f.Nested, f.HasTrashed = t.nested, t.trashed
		out.Folders = append(out.Folders, f)
	}
	slices.SortFunc(out.Folders, func(a, b EmptyFolder) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// TrashSummary is what the recycle bin holds: count is its top level, as
// the bin lists it, and size the bytes of every trashed file.
func (s *Store) TrashSummary() (count int, size int64, err error) {
	err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM files WHERE `+topLevelTrashed+`),
  (SELECT COALESCE(SUM(size), 0) FROM files WHERE trashed = 1 AND kind != 'drive#folder')`).Scan(&count, &size)
	if err != nil {
		return 0, 0, fmt.Errorf("index: trash summary: %w", err)
	}
	return count, size, nil
}

// TreeSize sums the bytes of the files among ids and below them, trashed
// or not: what destroying those entries frees.
func (s *Store) TreeSize(ids []string) (int64, error) {
	var total int64
	for start := 0; start < len(ids); start += inBatch {
		batch := ids[start:min(start+inBatch, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		var n int64
		err := s.db.QueryRow(`WITH RECURSIVE sub(id) AS (
  SELECT id FROM files WHERE id IN (`+placeholders(len(batch))+`)
  UNION SELECT f.id FROM files f JOIN sub ON f.parent_id = sub.id
) SELECT COALESCE(SUM(size), 0) FROM files WHERE kind != 'drive#folder' AND id IN (SELECT id FROM sub)`, args...).Scan(&n)
		if err != nil {
			return 0, fmt.Errorf("index: tree size: %w", err)
		}
		total += n
	}
	return total, nil
}
