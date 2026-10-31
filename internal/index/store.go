// Package index keeps a local copy of the drive listing in SQLite so that
// browsing, favourites, the recycle bin, sorting, filtering and search are
// answered without a round trip. Syncer fills and maintains it; Store is the
// storage layer.
package index

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// schemaVersion is stored under the "schema" meta key. A mismatch on Open
// drops the files table: there is no migration path, only a re-walk.
// 3: starred is read from the STAR system tag; older walks stored false.
//
// Bump it whenever the column set changes. Four sites must move together:
// CREATE TABLE in schema, upsertSQL, the Upsert args and
// selectColumns/scanRow; TestUpsertRoundTripsAndOverwrites catches a drift.
const schemaVersion = "3"

// Meta keys. The walk state lives here so the index file is self-describing.
const (
	MetaSchema   = "schema"
	MetaAccount  = "account"   // drive.DeviceIDFor(account), guards against reuse across accounts
	MetaCursor   = "cursor"    // sync_time at the end of the last completed walk
	MetaDestroy  = "destroy"   // last_destroy_time seen on the last walk
	MetaFullAt   = "full_at"   // RFC3339 UTC of the last completed full walk
	MetaSyncedAt = "synced_at" // RFC3339 UTC of the last completed walk of any kind
	MetaWalk     = "walk"      // JSON walkState of a full walk under way, "" when none
)

// KindFolder mirrors drive.KindFolder; the store orders folders first.
const KindFolder = "drive#folder"

// maxLimit caps one Query page.
const maxLimit = 500

// Row is one indexed entry; column-for-column what the server listing carries
// and the services layer needs to rebuild a drive.File.
type Row struct {
	ID, ParentID, Kind, Name, MimeType, FileExtension string
	Size                                              int64
	Starred, Trashed                                  bool
	// DeleteTime is the server's scheduled purge time, set on every trashed
	// row. A descendant trashed along with its folder carries the folder's
	// value; an entry the user trashed itself has its own. That difference
	// is what tells the recycle bin's top level from what sits inside it.
	DeleteTime                                      string
	Phase, CreatedTime, ModifiedTime, ThumbnailLink string
	Hash, FolderType                                string
	// Category is services.categoryOf(file), stored so the chips filter in SQL.
	Category string
}

// Patch is a partial update; nil fields are left alone. Trashed applies to
// the row and everything below it, the way the server trashes and restores
// a folder with its contents. ParentID moves the row; its subtree follows
// because descendants point at it, not at a path.
type Patch struct {
	Name     *string
	ParentID *string
	Starred  *bool
	Trashed  *bool
}

// Crumb is one ancestor on a Path.
type Crumb struct{ ID, Name string }

// Scope selects which slice of the drive a Query reads.
type Scope string

const (
	ScopeFolder  Scope = "folder"  // children of ParentID, trashed=0
	ScopeStarred Scope = "starred" // starred=1 AND trashed=0, whole drive
	ScopeTrash   Scope = "trash"   // trashed=1, whole drive
	ScopeSearch  Scope = "search"  // trashed=0, whole drive, Needle required
	ScopeFiles   Scope = "files"   // trashed=0 files (no folders), whole drive; Needle optional
)

// Query is one page request against the index.
type Query struct {
	Scope    Scope
	ParentID string // ScopeFolder only; "" = root
	Needle   string // ScopeSearch (required) and ScopeFiles; case-insensitive substring of the name
	Category string // "" | "video" | "image" | "document" | "audio" | "other" (every file none of the others takes; folders count here in ScopeFolder)
	Sort     string // "modified" | "name" | "size"
	Asc      bool
	Offset   int
	Limit    int // clamped to maxLimit; <= 0 means maxLimit
}

// Store is the SQLite-backed index.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS files (
  id TEXT PRIMARY KEY, parent_id TEXT NOT NULL, kind TEXT NOT NULL,
  name TEXT NOT NULL, name_lower TEXT NOT NULL,
  mime_type TEXT NOT NULL, file_extension TEXT NOT NULL, size INTEGER NOT NULL,
  starred INTEGER NOT NULL, trashed INTEGER NOT NULL, delete_time TEXT NOT NULL, phase TEXT NOT NULL,
  created_time TEXT NOT NULL, modified_time TEXT NOT NULL,
  thumbnail_link TEXT NOT NULL, hash TEXT NOT NULL, folder_type TEXT NOT NULL,
  category TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS files_parent ON files(parent_id, trashed, kind);
CREATE INDEX IF NOT EXISTS files_starred ON files(starred) WHERE starred = 1;
CREATE INDEX IF NOT EXISTS files_trashed ON files(trashed) WHERE trashed = 1;
CREATE INDEX IF NOT EXISTS files_name ON files(name_lower);
CREATE INDEX IF NOT EXISTS files_modified ON files(modified_time, id);
CREATE INDEX IF NOT EXISTS files_size ON files(size, id);
CREATE INDEX IF NOT EXISTS files_library ON files(id) WHERE trashed = 0 AND kind != 'drive#folder' AND category = 'video' AND phase = 'PHASE_TYPE_COMPLETE';
CREATE TABLE IF NOT EXISTS walk_seen (id TEXT PRIMARY KEY) WITHOUT ROWID;
`

// Open creates or opens the index at path. A schema mismatch rebuilds the
// files table and forgets the walk state.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("index: open: %w", err)
	}
	// One connection: the WAL pragma is per-connection and the writer is
	// single anyway. busy_timeout covers the reader racing a Syncer batch.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("index: %s: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("index: schema: %w", err)
	}
	stored, err := s.Meta(MetaSchema)
	if err != nil {
		db.Close()
		return nil, err
	}
	if stored != "" && stored != schemaVersion {
		slog.Info("index: schema changed, re-walking", "stored", stored, "current", schemaVersion)
		// Drop rather than empty the table: a schema bump usually means the
		// column set moved, and CREATE TABLE IF NOT EXISTS would keep the old.
		if _, err := db.Exec("DROP TABLE files"); err != nil {
			db.Close()
			return nil, fmt.Errorf("index: drop stale schema: %w", err)
		}
		if _, err := db.Exec(schema); err != nil {
			db.Close()
			return nil, fmt.Errorf("index: schema: %w", err)
		}
		if err := s.Clear(); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := s.SetMeta(MetaSchema, schemaVersion); err != nil {
		db.Close()
		return nil, err
	}
	if revision, err := s.Meta("reader_categories"); err != nil {
		db.Close()
		return nil, err
	} else if revision != "1" {
		// Reclassify existing rows without discarding the user's full-drive index.
		if _, err := db.Exec(`UPDATE files SET category = CASE
		 WHEN name_lower LIKE '%.cbz' OR name_lower LIKE '%.cbr' THEN 'archive'
		 ELSE 'document' END
		 WHERE kind != 'drive#folder' AND category IN ('other','archive','document') AND
		 (name_lower LIKE '%.cbz' OR name_lower LIKE '%.cbr' OR name_lower LIKE '%.csv' OR name_lower LIKE '%.tsv'
		 OR name_lower LIKE '%.srt' OR name_lower LIKE '%.ass' OR name_lower LIKE '%.ssa' OR name_lower LIKE '%.vtt' OR name_lower LIKE '%.lrc'
		 OR name_lower LIKE '%.markdown' OR name_lower LIKE '%.mdown' OR name_lower LIKE '%.mkd' OR name_lower LIKE '%.mkdn')`); err != nil {
			db.Close()
			return nil, fmt.Errorf("index: reader categories: %w", err)
		}
		if err := s.SetMeta("reader_categories", "1"); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Meta reads one key; "" when absent.
func (s *Store) Meta(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("index: meta %s: %w", key, err)
	}
	return value, nil
}

// SetMeta writes one key.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec("INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	if err != nil {
		return fmt.Errorf("index: set meta %s: %w", key, err)
	}
	return nil
}

const upsertSQL = `INSERT INTO files(
  id, parent_id, kind, name, name_lower, mime_type, file_extension, size,
  starred, trashed, delete_time, phase, created_time, modified_time, thumbnail_link, hash, folder_type, category
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  parent_id = excluded.parent_id, kind = excluded.kind, name = excluded.name,
  name_lower = excluded.name_lower, mime_type = excluded.mime_type,
  file_extension = excluded.file_extension, size = excluded.size,
  starred = excluded.starred, trashed = excluded.trashed, delete_time = excluded.delete_time, phase = excluded.phase,
  created_time = excluded.created_time, modified_time = excluded.modified_time,
  thumbnail_link = excluded.thumbnail_link, hash = excluded.hash,
  folder_type = excluded.folder_type, category = excluded.category`

// Upsert writes rows in one transaction, replacing every column of an
// existing id.
func (s *Store) Upsert(rows []Row) error { return s.upsert(rows, false) }

// UpsertWalk is Upsert for a page of a full walk: the ids also go into
// walk_seen, which outlives the process, so a walk cut short can resume
// and still tell at the end which rows the server no longer has.
func (s *Store) UpsertWalk(rows []Row) error { return s.upsert(rows, true) }

func (s *Store) upsert(rows []Row, seen bool) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("index: upsert: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(upsertSQL)
	if err != nil {
		return fmt.Errorf("index: upsert: %w", err)
	}
	defer stmt.Close()
	var mark *sql.Stmt
	if seen {
		if mark, err = tx.Prepare("INSERT OR IGNORE INTO walk_seen(id) VALUES(?)"); err != nil {
			return fmt.Errorf("index: upsert: %w", err)
		}
		defer mark.Close()
	}
	for _, r := range rows {
		if _, err := stmt.Exec(
			r.ID, r.ParentID, r.Kind, r.Name, strings.ToLower(r.Name), r.MimeType, r.FileExtension, r.Size,
			r.Starred, r.Trashed, r.DeleteTime, r.Phase, r.CreatedTime, r.ModifiedTime, r.ThumbnailLink, r.Hash, r.FolderType, r.Category,
		); err != nil {
			return fmt.Errorf("index: upsert %s: %w", r.ID, err)
		}
		if mark != nil {
			if _, err := mark.Exec(r.ID); err != nil {
				return fmt.Errorf("index: upsert %s: %w", r.ID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: upsert: %w", err)
	}
	return nil
}

// Patch updates only the fields set in p. A missing id is not an error.
func (s *Store) Patch(id string, p Patch) error {
	var sets []string
	var args []any
	if p.Name != nil {
		sets = append(sets, "name = ?", "name_lower = ?")
		args = append(args, *p.Name, strings.ToLower(*p.Name))
	}
	if p.ParentID != nil {
		sets = append(sets, "parent_id = ?")
		args = append(args, *p.ParentID)
	}
	if p.Starred != nil {
		sets = append(sets, "starred = ?")
		args = append(args, *p.Starred)
	}
	if len(sets) > 0 {
		if _, err := s.db.Exec("UPDATE files SET "+strings.Join(sets, ", ")+" WHERE id = ?", append(args, id)...); err != nil {
			return fmt.Errorf("index: patch %s: %w", id, err)
		}
	}
	if p.Trashed != nil {
		// The purge time is the server's to assign; the next walk fills it
		// in. Until then the subtree shares "" and so reads as one bin item.
		_, err := s.db.Exec(`WITH RECURSIVE sub(id) AS (
  SELECT id FROM files WHERE id = ?
  UNION ALL SELECT f.id FROM files f JOIN sub ON f.parent_id = sub.id
) UPDATE files SET trashed = ?, delete_time = '' WHERE id IN (SELECT id FROM sub)`, id, *p.Trashed)
		if err != nil {
			return fmt.Errorf("index: patch %s: %w", id, err)
		}
	}
	return nil
}

// deleteBatch bounds the IN list of one DELETE.
const deleteBatch = 500

// Delete removes the given ids.
func (s *Store) Delete(ids []string) error {
	for start := 0; start < len(ids); start += deleteBatch {
		end := min(start+deleteBatch, len(ids))
		batch := ids[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		query := "DELETE FROM files WHERE id IN (?" + strings.Repeat(", ?", len(batch)-1) + ")"
		if _, err := s.db.Exec(query, args...); err != nil {
			return fmt.Errorf("index: delete: %w", err)
		}
	}
	return nil
}

// topLevelTrashed selects what the recycle bin shows. The server marks
// every descendant of a trashed folder as trashed too, with the folder's
// delete_time; the bin lists only entries trashed in their own right, which
// are those whose parent is live or was trashed at a different moment.
const topLevelTrashed = `trashed = 1 AND NOT EXISTS (
  SELECT 1 FROM files p WHERE p.id = files.parent_id AND p.trashed = 1 AND p.delete_time = files.delete_time)`

// DeleteTrashedNotIn removes top-level trashed rows whose id is not in
// keep, together with their descendants, and reports how many rows went.
// keep is the server's recycle bin; anything missing from it was destroyed.
func (s *Store) DeleteTrashedNotIn(keep []string) (int, error) {
	keepSet := make(map[string]struct{}, len(keep))
	for _, id := range keep {
		keepSet[id] = struct{}{}
	}
	gone, err := s.idsNotIn("SELECT id FROM files WHERE "+topLevelTrashed, keepSet)
	if err != nil {
		return 0, err
	}
	return s.DeleteTree(gone)
}

// DeleteTree removes the given ids together with everything below them and
// reports how many rows went: what destroying them on the server leaves.
// Deleting only the ids would leave their descendants behind as trashed
// ghost rows.
func (s *Store) DeleteTree(ids []string) (int, error) {
	total := 0
	for start := 0; start < len(ids); start += deleteBatch {
		batch := ids[start:min(start+deleteBatch, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		res, err := s.db.Exec(`WITH RECURSIVE sub(id) AS (
  SELECT id FROM files WHERE id IN (`+placeholders(len(batch))+`)
  UNION SELECT f.id FROM files f JOIN sub ON f.parent_id = sub.id
) DELETE FROM files WHERE id IN (SELECT id FROM sub)`, args...)
		if err != nil {
			return 0, fmt.Errorf("index: delete tree: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

// BeginWalk starts a full walk's record of what it saw from nothing.
func (s *Store) BeginWalk() error {
	if _, err := s.db.Exec("DELETE FROM walk_seen"); err != nil {
		return fmt.Errorf("index: begin walk: %w", err)
	}
	return nil
}

// WalkSeen is how many distinct rows the full walk under way has written.
func (s *Store) WalkSeen() (int, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM walk_seen").Scan(&n); err != nil {
		return 0, fmt.Errorf("index: walk seen: %w", err)
	}
	return n, nil
}

// FinishWalk ends a full walk in one transaction: every row it did not see
// goes, its record and checkpoint are dropped and full_at becomes fullAt,
// so the table converges on the server without ever being emptied. It
// reports how many rows went.
func (s *Store) FinishWalk(fullAt string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("index: finish walk: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.Exec("DELETE FROM files WHERE id NOT IN (SELECT id FROM walk_seen)")
	if err != nil {
		return 0, fmt.Errorf("index: finish walk: %w", err)
	}
	gone, _ := res.RowsAffected()
	for _, q := range []string{
		"DELETE FROM walk_seen",
		"DELETE FROM meta WHERE key = '" + MetaWalk + "'",
	} {
		if _, err := tx.Exec(q); err != nil {
			return 0, fmt.Errorf("index: finish walk: %w", err)
		}
	}
	if _, err := tx.Exec("INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", MetaFullAt, fullAt); err != nil {
		return 0, fmt.Errorf("index: finish walk: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("index: finish walk: %w", err)
	}
	return int(gone), nil
}

// idsNotIn runs selectIDs and returns the ids absent from keep.
func (s *Store) idsNotIn(selectIDs string, keep map[string]struct{}) ([]string, error) {
	rows, err := s.db.Query(selectIDs)
	if err != nil {
		return nil, fmt.Errorf("index: select ids: %w", err)
	}
	defer rows.Close()
	var gone []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("index: scan id: %w", err)
		}
		if _, ok := keep[id]; !ok {
			gone = append(gone, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: select ids: %w", err)
	}
	return gone, nil
}

// Clear empties the files table and forgets the walk state, a full walk
// under way included; the schema and account markers stay. Only a schema
// upgrade, an account change and Rebuild call it: a cleared index makes the
// drive look empty until the next full walk completes.
func (s *Store) Clear() error {
	_, err := s.db.Exec("DELETE FROM files; DELETE FROM walk_seen; DELETE FROM meta WHERE key IN ('cursor', 'destroy', 'full_at', 'synced_at', 'walk')")
	if err != nil {
		return fmt.Errorf("index: clear: %w", err)
	}
	return nil
}

// Count is the number of indexed rows, trashed included.
func (s *Store) Count() (int, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&n); err != nil {
		return 0, fmt.Errorf("index: count: %w", err)
	}
	return n, nil
}

// CountChildren is the number of live entries directly under parentID
// ("" = root), folders and files alike. files_parent answers it without
// touching the rows.
func (s *Store) CountChildren(parentID string) (int, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM files WHERE parent_id = ? AND trashed = 0", parentID).Scan(&n); err != nil {
		return 0, fmt.Errorf("index: count children of %s: %w", parentID, err)
	}
	return n, nil
}

// SubfolderCounts is how many live folders sit directly under each of ids;
// an id without any is absent from the map.
func (s *Store) SubfolderCounts(ids []string) (map[string]int, error) {
	out := make(map[string]int, len(ids))
	for start := 0; start < len(ids); start += deleteBatch {
		batch := ids[start:min(start+deleteBatch, len(ids))]
		args := []any{KindFolder}
		for _, id := range batch {
			args = append(args, id)
		}
		rows, err := s.db.Query("SELECT parent_id, COUNT(*) FROM files WHERE kind = ? AND trashed = 0 AND parent_id IN ("+
			placeholders(len(batch))+") GROUP BY parent_id", args...)
		if err != nil {
			return nil, fmt.Errorf("index: subfolder counts: %w", err)
		}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return nil, fmt.Errorf("index: subfolder counts: %w", err)
			}
			out[id] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("index: subfolder counts: %w", err)
		}
	}
	return out, nil
}

// HasChildNamed reports whether a live entry directly under parentID ("" =
// root) is called exactly name. The server tells names apart by case, so
// this does too.
func (s *Store) HasChildNamed(parentID, name string) (bool, error) {
	var one int
	err := s.db.QueryRow("SELECT 1 FROM files WHERE parent_id = ? AND name = ? AND trashed = 0 LIMIT 1", parentID, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("index: child named under %s: %w", parentID, err)
	}
	return true, nil
}

// UsageByCategory sums the size of every file per category, trashed rows
// included: the server's usage counts the recycle bin too, so only with it
// do the sums add up to the quota the account reports. Folders carry no
// bytes of their own and are left out.
func (s *Store) UsageByCategory() (map[string]int64, error) {
	rows, err := s.db.Query("SELECT category, COALESCE(SUM(size), 0) FROM files WHERE kind != ? GROUP BY category", KindFolder)
	if err != nil {
		return nil, fmt.Errorf("index: usage: %w", err)
	}
	defer rows.Close()
	sums := map[string]int64{}
	for rows.Next() {
		var category string
		var size int64
		if err := rows.Scan(&category, &size); err != nil {
			return nil, fmt.Errorf("index: usage: %w", err)
		}
		sums[category] = size
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: usage: %w", err)
	}
	return sums, nil
}

const selectColumns = `id, parent_id, kind, name, mime_type, file_extension, size,
  starred, trashed, delete_time, phase, created_time, modified_time, thumbnail_link, hash, folder_type, category`

func scanRow(sc interface{ Scan(...any) error }) (Row, error) {
	var r Row
	err := sc.Scan(&r.ID, &r.ParentID, &r.Kind, &r.Name, &r.MimeType, &r.FileExtension, &r.Size,
		&r.Starred, &r.Trashed, &r.DeleteTime, &r.Phase, &r.CreatedTime, &r.ModifiedTime, &r.ThumbnailLink, &r.Hash, &r.FolderType, &r.Category)
	return r, err
}

// Get reads one row by id.
func (s *Store) Get(id string) (Row, bool, error) {
	r, err := scanRow(s.db.QueryRow("SELECT "+selectColumns+" FROM files WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, fmt.Errorf("index: get %s: %w", id, err)
	}
	return r, true, nil
}

// Folders lists every live folder directly under parentID ("" = root), by
// name. Unpaged: a folder picker needs the whole level at once.
func (s *Store) Folders(parentID string) ([]Row, error) {
	rows, err := s.db.Query("SELECT "+selectColumns+" FROM files WHERE parent_id = ? AND kind = ? AND trashed = 0 ORDER BY name_lower, id",
		parentID, KindFolder)
	if err != nil {
		return nil, fmt.Errorf("index: folders: %w", err)
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("index: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: folders: %w", err)
	}
	return out, nil
}

// escapeLike makes needle safe inside a LIKE pattern with ESCAPE '\'.
func escapeLike(needle string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(needle)
}

// Query returns one page and the total number of rows matching the same
// conditions. Folders always precede files because the UI renders them as
// two sections and pagination must finish one before starting the other.
func (s *Store) Query(q Query) ([]Row, int, error) {
	var where []string
	var args []any
	switch q.Scope {
	case ScopeFolder:
		where = append(where, "parent_id = ?", "trashed = 0")
		args = append(args, q.ParentID)
	case ScopeStarred:
		where = append(where, "starred = 1", "trashed = 0")
	case ScopeTrash:
		where = append(where, topLevelTrashed)
	case ScopeSearch:
		if strings.TrimSpace(q.Needle) == "" {
			return nil, 0, errors.New("index: search needs a needle")
		}
		where = append(where, "trashed = 0", `name_lower LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(strings.ToLower(strings.TrimSpace(q.Needle)))+"%")
	case ScopeFiles:
		where = append(where, "trashed = 0", "kind != ?")
		args = append(args, KindFolder)
		if needle := strings.TrimSpace(q.Needle); needle != "" {
			where = append(where, `name_lower LIKE ? ESCAPE '\'`)
			args = append(args, "%"+escapeLike(strings.ToLower(needle))+"%")
		}
	default:
		return nil, 0, fmt.Errorf("index: unknown scope %q", q.Scope)
	}
	switch q.Category {
	case "":
	case "other":
		where = append(where, "category NOT IN ('video', 'image', 'document', 'audio')")
	default:
		where = append(where, "category = ?")
		args = append(args, q.Category)
	}
	cond := " WHERE " + strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM files"+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("index: count: %w", err)
	}

	key := "modified_time"
	switch q.Sort {
	case "name":
		key = "name_lower"
	case "size":
		key = "size"
	}
	dir := "DESC"
	if q.Asc {
		dir = "ASC"
	}
	limit := q.Limit
	if limit <= 0 || limit > maxLimit {
		limit = maxLimit
	}
	order := fmt.Sprintf(" ORDER BY (kind = '%s') DESC, %s %s, id LIMIT ? OFFSET ?", KindFolder, key, dir)
	if q.Scope == ScopeFiles {
		// No folders to put first; the bare key lets SQLite walk its index.
		order = fmt.Sprintf(" ORDER BY %s %s, id LIMIT ? OFFSET ?", key, dir)
	}
	rows, err := s.db.Query("SELECT "+selectColumns+" FROM files"+cond+order, append(args, limit, q.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("index: query: %w", err)
	}
	defer rows.Close()
	out := make([]Row, 0, limit)
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("index: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("index: query: %w", err)
	}
	return out, total, nil
}

// maxDepth bounds Path against a parent cycle in a corrupt index.
const maxDepth = 64

// Path returns the ancestors of id from the root down to id itself, walking
// parent_id. It stops without error at the root, at an id the index does
// not know, or after maxDepth hops, returning whatever it reached.
func (s *Store) Path(id string) ([]Crumb, error) {
	var reversed []Crumb
	for hops := 0; id != "" && hops < maxDepth; hops++ {
		var name, parent string
		err := s.db.QueryRow("SELECT name, parent_id FROM files WHERE id = ?", id).Scan(&name, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("index: path %s: %w", id, err)
		}
		reversed = append(reversed, Crumb{ID: id, Name: name})
		id = parent
	}
	out := make([]Crumb, len(reversed))
	for i, c := range reversed {
		out[len(reversed)-1-i] = c
	}
	return out, nil
}

// Search is one filtered read of the live rows: the AI assistant's Find Files.
// Its values come from a model's answer, checked by the services layer;
// every one of them still reaches SQL as a parameter, never as text.
type Search struct {
	// Keywords match when any of them is a case-insensitive substring of
	// the name.
	Keywords []string
	// Category is "" | "video" | "image" | "document" | "other"; other is
	// a file in none of the three, so folders never match it.
	Category string
	// Exts are lower case and without the dot. The name's suffix stands in
	// when the server sent no extension, as it does for the Type chips.
	Exts             []string
	MinSize, MaxSize int64 // bytes; 0 leaves that side open
	// The time bounds are instants, zero leaving that side open; From is
	// inclusive, Before exclusive. A stamp that does not parse meets none.
	CreatedFrom, CreatedBefore   time.Time
	ModifiedFrom, ModifiedBefore time.Time
	Kind                         string // "" | "file" | "folder"
	Starred                      string // "" | "yes" | "no"
	// IDs, when non-nil, keeps only these rows; an empty set matches none.
	IDs []string
	// Under, when non-empty, keeps only rows somewhere below one of these
	// folders; the folders themselves are not matches.
	Under []string
	Sort  string // "" (= modified) | "modified" | "created" | "size" | "name"
	Asc   bool
	Limit int // clamped to maxLimit; <= 0 means maxLimit
}

// Group is one row of Aggregate: the files of one key, their bytes and
// their number.
type Group struct {
	Key   string
	Size  int64
	Count int
}

// extExpr is services.extensionOf in SQL: the stored extension without its
// dot, else whatever follows the name's last dot (rtrim with every non-dot
// character of the name strips back to that dot).
const extExpr = `(CASE
  WHEN file_extension != '' THEN lower(CASE WHEN substr(file_extension, 1, 1) = '.' THEN substr(file_extension, 2) ELSE file_extension END)
  WHEN instr(name_lower, '.') > 0 THEN substr(name_lower, length(rtrim(name_lower, replace(name_lower, '.', ''))) + 1)
  ELSE '' END)`

// categoryExpr folds every category the three chips do not name into
// "other", the way Search's Category reads it.
const categoryExpr = `(CASE WHEN category IN ('video', 'image', 'document') THEN category ELSE 'other' END)`

// sqlStamp renders an instant the way SQLite's datetime() normalises a
// stored RFC 3339 stamp: UTC, whole seconds, so the two compare as text.
func sqlStamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// placeholders is "?, ?, …" for n values.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// filter renders q as an optional WITH clause and a WHERE condition. args
// holds the WITH clause's values first, then the condition's, in the order
// the two appear in the statement.
func (q Search) filter() (with, cond string, args []any, err error) {
	where := []string{"trashed = 0"}
	if len(q.Under) > 0 {
		// The recursion runs over folders only: a file is below a root when
		// its parent is the root or one of the folders below it.
		with = `WITH RECURSIVE below(id) AS (
  SELECT id FROM files WHERE parent_id IN (` + placeholders(len(q.Under)) + `) AND kind = ? AND trashed = 0
  UNION
  SELECT f.id FROM files f JOIN below b ON f.parent_id = b.id WHERE f.kind = ? AND f.trashed = 0) `
		for _, id := range q.Under {
			args = append(args, id)
		}
		args = append(args, KindFolder, KindFolder)
		where = append(where, "(parent_id IN ("+placeholders(len(q.Under))+") OR parent_id IN (SELECT id FROM below))")
		for _, id := range q.Under {
			args = append(args, id)
		}
	}
	if q.IDs != nil {
		if len(q.IDs) == 0 {
			where = append(where, "0")
		} else {
			where = append(where, "id IN ("+placeholders(len(q.IDs))+")")
			for _, id := range q.IDs {
				args = append(args, id)
			}
		}
	}
	var likes []string
	for _, k := range q.Keywords {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			likes = append(likes, `name_lower LIKE ? ESCAPE '\'`)
			args = append(args, "%"+escapeLike(k)+"%")
		}
	}
	if len(likes) > 0 {
		where = append(where, "("+strings.Join(likes, " OR ")+")")
	}
	switch q.Category {
	case "":
	case "video", "image", "document", "audio":
		where = append(where, "category = ?")
		args = append(args, q.Category)
	case "other":
		where = append(where, "kind != ?", "category NOT IN ('video', 'image', 'document')")
		args = append(args, KindFolder)
	case "otherfiles":
		where = append(where, "category NOT IN ('video', 'image', 'document', 'audio')")
	default:
		return "", "", nil, fmt.Errorf("index: unknown category %q", q.Category)
	}
	if len(q.Exts) > 0 {
		where = append(where, extExpr+" IN ("+placeholders(len(q.Exts))+")")
		for _, e := range q.Exts {
			args = append(args, e)
		}
	}
	if q.MinSize > 0 {
		where = append(where, "size >= ?")
		args = append(args, q.MinSize)
	}
	if q.MaxSize > 0 {
		where = append(where, "size <= ?")
		args = append(args, q.MaxSize)
	}
	for _, b := range []struct {
		column, op string
		at         time.Time
	}{
		{"created_time", ">=", q.CreatedFrom},
		{"created_time", "<", q.CreatedBefore},
		{"modified_time", ">=", q.ModifiedFrom},
		{"modified_time", "<", q.ModifiedBefore},
	} {
		if !b.at.IsZero() {
			where = append(where, "datetime("+b.column+") "+b.op+" ?")
			args = append(args, sqlStamp(b.at))
		}
	}
	switch q.Kind {
	case "":
	case "file":
		where = append(where, "kind != ?")
		args = append(args, KindFolder)
	case "folder":
		where = append(where, "kind = ?")
		args = append(args, KindFolder)
	default:
		return "", "", nil, fmt.Errorf("index: unknown kind %q", q.Kind)
	}
	switch q.Starred {
	case "":
	case "yes":
		where = append(where, "starred = 1")
	case "no":
		where = append(where, "starred = 0")
	default:
		return "", "", nil, fmt.Errorf("index: unknown starred %q", q.Starred)
	}
	return with, strings.Join(where, " AND "), args, nil
}

// searchOrder maps Search.Sort to its column; only these names reach SQL.
var searchOrder = map[string]string{
	"":         "modified_time",
	"modified": "modified_time",
	"created":  "created_time",
	"size":     "size",
	"name":     "name_lower",
}

// Search returns up to Limit live rows matching q and how many match in
// all. Folders and files mix in one order: the assistant lists hits, not a
// folder view.
func (s *Store) Search(q Search) ([]Row, int, error) {
	key, ok := searchOrder[q.Sort]
	if !ok {
		return nil, 0, fmt.Errorf("index: unknown sort %q", q.Sort)
	}
	with, cond, args, err := q.filter()
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRow(with+"SELECT COUNT(*) FROM files WHERE "+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("index: search count: %w", err)
	}
	dir := "DESC"
	if q.Asc {
		dir = "ASC"
	}
	limit := q.Limit
	if limit <= 0 || limit > maxLimit {
		limit = maxLimit
	}
	rows, err := s.db.Query(with+"SELECT "+selectColumns+" FROM files WHERE "+cond+
		" ORDER BY "+key+" "+dir+", id LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("index: search: %w", err)
	}
	out, err := collect(rows)
	if err != nil {
		return nil, 0, fmt.Errorf("index: search: %w", err)
	}
	return out, total, nil
}

// aggregateKey maps Aggregate's by to the grouped expression.
var aggregateKey = map[string]string{
	"folder":   "parent_id",
	"category": categoryExpr,
	"ext":      extExpr,
}

// Aggregate sums the files matching q by their containing folder (Key =
// the folder id, "" for the root), by category (other folding in what the
// chips do not name) or by extension, largest first by measure ("size" or
// "count"), at most limit groups. total is the number of matching files.
// Folders are left out: they carry no bytes of their own, and a folder
// counted as an item of its parent would double what is inside it.
func (s *Store) Aggregate(q Search, by, measure string, limit int) ([]Group, int, error) {
	key, ok := aggregateKey[by]
	if !ok {
		return nil, 0, fmt.Errorf("index: unknown aggregate %q", by)
	}
	order := "2 DESC, 3 DESC"
	switch measure {
	case "size":
	case "count":
		order = "3 DESC, 2 DESC"
	default:
		return nil, 0, fmt.Errorf("index: unknown measure %q", measure)
	}
	with, cond, args, err := q.filter()
	if err != nil {
		return nil, 0, err
	}
	cond += " AND kind != ?"
	args = append(args, KindFolder)
	var total int
	if err := s.db.QueryRow(with+"SELECT COUNT(*) FROM files WHERE "+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("index: aggregate count: %w", err)
	}
	if limit <= 0 || limit > maxLimit {
		limit = maxLimit
	}
	rows, err := s.db.Query(with+"SELECT "+key+", COALESCE(SUM(size), 0), COUNT(*) FROM files WHERE "+cond+
		" GROUP BY 1 ORDER BY "+order+", 1 LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("index: aggregate: %w", err)
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Key, &g.Size, &g.Count); err != nil {
			return nil, 0, fmt.Errorf("index: aggregate: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("index: aggregate: %w", err)
	}
	return out, total, nil
}

// Subtree returns the live entries below rootID ("" = the root), folders
// and files, at any depth, in no particular order — at most limit+1 of
// them, so that a caller refusing more than limit knows without reading a
// whole drive. UNION (not UNION ALL) ends a parent cycle in a corrupt index.
func (s *Store) Subtree(rootID string, limit int) ([]Row, error) {
	if limit < 0 {
		limit = 0
	}
	rows, err := s.db.Query(`WITH RECURSIVE below(id) AS (
  SELECT id FROM files WHERE parent_id = ? AND trashed = 0
  UNION
  SELECT f.id FROM files f JOIN below b ON f.parent_id = b.id WHERE f.trashed = 0
  LIMIT ?)
SELECT `+selectColumns+` FROM files JOIN below USING (id)`, rootID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("index: subtree of %s: %w", rootID, err)
	}
	out, err := collect(rows)
	if err != nil {
		return nil, fmt.Errorf("index: subtree of %s: %w", rootID, err)
	}
	return out, nil
}

// FoldersAt returns the live folders reached from the root by following
// segments, each compared without regard to case. Several folders may share
// a name, so a level may hold several and the walk follows them all; nil
// when a level matches nothing or there are no segments.
func (s *Store) FoldersAt(segments []string) ([]Row, error) {
	parents := []string{""}
	var found []Row
	for _, seg := range segments {
		args := []any{KindFolder, strings.ToLower(seg)}
		for _, p := range parents {
			args = append(args, p)
		}
		rows, err := s.db.Query("SELECT "+selectColumns+" FROM files WHERE kind = ? AND trashed = 0 AND name_lower = ? AND parent_id IN ("+
			placeholders(len(parents))+") ORDER BY id", args...)
		if err != nil {
			return nil, fmt.Errorf("index: folders at %q: %w", seg, err)
		}
		if found, err = collect(rows); err != nil {
			return nil, fmt.Errorf("index: folders at %q: %w", seg, err)
		}
		if len(found) == 0 {
			return nil, nil
		}
		parents = parents[:0]
		for _, r := range found {
			parents = append(parents, r.ID)
		}
	}
	return found, nil
}

// FoldersNamed returns up to limit live folders anywhere whose name is name
// (contains false) or holds it (contains true), case aside; shorter names
// first, so the closest of the loose matches survive the limit.
func (s *Store) FoldersNamed(name string, contains bool, limit int) ([]Row, error) {
	match, arg := "name_lower = ?", strings.ToLower(name)
	if contains {
		match, arg = `name_lower LIKE ? ESCAPE '\'`, "%"+escapeLike(arg)+"%"
	}
	if limit <= 0 || limit > maxLimit {
		limit = maxLimit
	}
	rows, err := s.db.Query("SELECT "+selectColumns+" FROM files WHERE kind = ? AND trashed = 0 AND "+match+
		" ORDER BY length(name_lower), name_lower, id LIMIT ?", KindFolder, arg, limit)
	if err != nil {
		return nil, fmt.Errorf("index: folders named %q: %w", name, err)
	}
	out, err := collect(rows)
	if err != nil {
		return nil, fmt.Errorf("index: folders named %q: %w", name, err)
	}
	return out, nil
}

// collect scans and closes rows.
func collect(rows *sql.Rows) ([]Row, error) {
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
