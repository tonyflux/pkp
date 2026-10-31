package index

import "fmt"

const libraryWhere = `trashed = 0 AND kind != 'drive#folder' AND category = 'video' AND phase = 'PHASE_TYPE_COMPLETE'`

// MediaLibraryPageLimit bounds local library enumeration independently of drive queries.
const MediaLibraryPageLimit = 2000

// MediaLibraryPage walks completed videos by their stable primary key, never OFFSET.
func (s *Store) MediaLibraryPage(after string, limit int) ([]Row, int, error) {
	limit = min(max(limit, 1), MediaLibraryPageLimit)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRow("SELECT COUNT(*) FROM files WHERE " + libraryWhere).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query("SELECT "+selectColumns+" FROM files WHERE "+libraryWhere+" AND id > ? ORDER BY id LIMIT ?", after, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Row, 0, limit)
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, fmt.Errorf("index: media library: %w", err)
	}
	return out, total, tx.Commit()
}
