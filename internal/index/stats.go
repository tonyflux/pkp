package index

import (
	"cmp"
	"fmt"
	"slices"
)

// The AI assistant's 「What's Here」 and its plan trees read the index only,
// like Cleanup: every figure is what the last walk saw.

// liveBelow is a WITH clause naming every live entry below one root (its
// one argument), each with the root's direct child it sits under: top is
// the child itself for the first level. UNION ends a parent cycle in a
// corrupt index, since a (id, top) pair cannot come round twice.
const liveBelow = `below(id, top) AS (
  SELECT id, id FROM files WHERE parent_id = ? AND trashed = 0
  UNION
  SELECT f.id, b.top FROM files f JOIN below b ON f.parent_id = b.id WHERE f.trashed = 0)`

// CategoryCount is the files of one category within a summary.
type CategoryCount struct {
	Category string
	Count    int
	Size     int64
}

// Summary is what a subtree holds.
type Summary struct {
	Files, Folders int
	Size           int64 // bytes of the files
	// Categories is one entry per stored category of the files, largest
	// first (then most files, then by name).
	Categories []CategoryCount
}

// Summarize counts the live entries below root ("" = the root) at any
// depth, in one statement: folders, files, their bytes and each category.
func (s *Store) Summarize(root string) (Summary, error) {
	rows, err := s.db.Query(`WITH RECURSIVE `+liveBelow+`
SELECT kind = ?, category, count(*), COALESCE(SUM(size), 0) FROM files JOIN below USING (id) GROUP BY 1, 2`, root, KindFolder)
	if err != nil {
		return Summary{}, fmt.Errorf("index: summarize %s: %w", root, err)
	}
	defer rows.Close()
	out := Summary{Categories: []CategoryCount{}}
	for rows.Next() {
		var isFolder bool
		var c CategoryCount
		if err := rows.Scan(&isFolder, &c.Category, &c.Count, &c.Size); err != nil {
			return Summary{}, fmt.Errorf("index: summarize %s: %w", root, err)
		}
		if isFolder {
			out.Folders += c.Count
			continue
		}
		out.Files += c.Count
		out.Size += c.Size
		out.Categories = append(out.Categories, c)
	}
	if err := rows.Err(); err != nil {
		return Summary{}, fmt.Errorf("index: summarize %s: %w", root, err)
	}
	slices.SortFunc(out.Categories, func(a, b CategoryCount) int {
		return cmp.Or(cmp.Compare(b.Size, a.Size), cmp.Compare(b.Count, a.Count), cmp.Compare(a.Category, b.Category))
	})
	return out, nil
}

// Sized is a row with the bytes it stands for: a file's own, a folder's
// files at any depth.
type Sized struct {
	Row
	Total int64
}

// Largest returns up to limit live direct children of root ("" = the
// root) by the bytes they stand for, largest first (then by name and id).
// A folder weighs what every live file below it does.
func (s *Store) Largest(root string, limit int) ([]Sized, error) {
	if limit <= 0 {
		return []Sized{}, nil
	}
	rows, err := s.db.Query(`WITH RECURSIVE `+liveBelow+`,
weights(top, total) AS (
  SELECT b.top, COALESCE(SUM(CASE WHEN f.kind = ? THEN 0 ELSE f.size END), 0)
  FROM below b JOIN files f ON f.id = b.id GROUP BY b.top)
SELECT `+selectColumns+`, w.total FROM files JOIN weights w ON files.id = w.top
ORDER BY w.total DESC, name_lower, id LIMIT ?`, root, KindFolder, limit)
	if err != nil {
		return nil, fmt.Errorf("index: largest below %s: %w", root, err)
	}
	defer rows.Close()
	out := []Sized{}
	for rows.Next() {
		var r Sized
		if err := rows.Scan(&r.ID, &r.ParentID, &r.Kind, &r.Name, &r.MimeType, &r.FileExtension, &r.Size,
			&r.Starred, &r.Trashed, &r.DeleteTime, &r.Phase, &r.CreatedTime, &r.ModifiedTime, &r.ThumbnailLink,
			&r.Hash, &r.FolderType, &r.Category, &r.Total); err != nil {
			return nil, fmt.Errorf("index: largest below %s: %w", root, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: largest below %s: %w", root, err)
	}
	return out, nil
}

// Within returns those of ids that are live, as are all their ancestors,
// and sit below root ("" = the root) at any depth; in no particular order.
// root itself is not below root.
func (s *Store) Within(root string, ids []string) ([]string, error) {
	var out []string
	for batch := range slices.Chunk(ids, inBatch) {
		args := make([]any, 0, len(batch)+1)
		for _, id := range batch {
			args = append(args, id)
		}
		args = append(args, root)
		// up walks each id's ancestors through live rows; an id reaching
		// root is below it. UNION ends a parent cycle.
		rows, err := s.db.Query(`WITH RECURSIVE up(id, at) AS (
  SELECT id, parent_id FROM files WHERE trashed = 0 AND id IN (`+placeholders(len(batch))+`)
  UNION
  SELECT up.id, f.parent_id FROM up JOIN files f ON f.id = up.at WHERE f.trashed = 0)
SELECT DISTINCT id FROM up WHERE at = ?`, args...)
		if err != nil {
			return nil, fmt.Errorf("index: within %s: %w", root, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("index: within %s: %w", root, err)
			}
			out = append(out, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("index: within %s: %w", root, err)
		}
	}
	return out, nil
}

// Children lists the live entries directly under parentID ("" = root),
// folders first, then by name — at most limit+1 of them, so that a caller
// refusing more than limit knows without reading a huge level.
func (s *Store) Children(parentID string, limit int) ([]Row, error) {
	limit = max(limit, 0)
	rows, err := s.db.Query("SELECT "+selectColumns+" FROM files WHERE parent_id = ? AND trashed = 0 ORDER BY kind != ?, name_lower, id LIMIT ?",
		parentID, KindFolder, limit+1)
	if err != nil {
		return nil, fmt.Errorf("index: children of %s: %w", parentID, err)
	}
	out, err := collect(rows)
	if err != nil {
		return nil, fmt.Errorf("index: children of %s: %w", parentID, err)
	}
	return out, nil
}

// Cover is what a folder card shows of a folder: how many live entries it
// holds directly and the first few of them in cover order.
type Cover struct {
	Children int
	Items    []Row
}

// Covers reads the cover of each folder in ids (direct children only; a
// cover never reaches into a subfolder). Cover order: files with a
// thumbnail first — the large tile wants a picture — then other files,
// then folders; newest first within each, the id breaking ties, so a
// folder shows the same entries every time it is read.
func (s *Store) Covers(ids []string, n int) (map[string]Cover, error) {
	out := make(map[string]Cover, len(ids))
	for _, id := range ids {
		if _, done := out[id]; done {
			continue
		}
		count, err := s.CountChildren(id)
		if err != nil {
			return nil, err
		}
		rows, err := s.db.Query("SELECT "+selectColumns+` FROM files WHERE parent_id = ? AND trashed = 0
  ORDER BY CASE WHEN kind = ? THEN 2 WHEN thumbnail_link != '' THEN 0 ELSE 1 END, modified_time DESC, id LIMIT ?`,
			id, KindFolder, max(n, 0))
		if err != nil {
			return nil, fmt.Errorf("index: cover of %s: %w", id, err)
		}
		items, err := collect(rows)
		if err != nil {
			return nil, fmt.Errorf("index: cover of %s: %w", id, err)
		}
		out[id] = Cover{Children: count, Items: items}
	}
	return out, nil
}
