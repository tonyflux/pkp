package services

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

type LibraryAncestor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type LibraryFile struct {
	Entry     Entry             `json:"entry"`
	Ancestors []LibraryAncestor `json:"ancestors"`
}

type LibraryPage struct {
	Account  string        `json:"account"`
	Ready    bool          `json:"ready"`
	Revision string        `json:"revision"`
	Total    int           `json:"total"`
	Next     string        `json:"next"`
	Files    []LibraryFile `json:"files"`
}

// LibraryOverride affects only the local library, never the drive's names or layout.
type LibraryOverride struct {
	WorkID  string `json:"workId"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Year    int    `json:"year"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

type libraryOverridesFile struct {
	Version int                        `json:"version"`
	Files   map[string]LibraryOverride `json:"files"`
}

// Serializes read-modify-write across all webviews. Account removal takes this
// same lock through removeMediaLibraryFile before unlinking its record.
var mediaLibraryMu sync.Mutex

func (d *DriveService) mediaLibraryPath(key string) string {
	return d.accountFile(key, "medialibrary-", ".json")
}

// MediaLibraryPage requires an explicit owner even when the screen switches.
func (d *DriveService) MediaLibraryPage(account, after string, limit int) (LibraryPage, error) {
	page := LibraryPage{Account: account, Files: []LibraryFile{}}
	if account == "" {
		return page, errNoSuchAccount
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return page, err
	}
	idx := s.index()
	if idx == nil || !idx.syncer.Ready() {
		return page, nil
	}
	page.Ready = true
	page.Revision = idx.syncer.Progress().SyncedAt
	if limit <= 0 {
		limit = 500
	}
	limit = min(limit, index.MediaLibraryPageLimit)
	rows, total, err := idx.store.MediaLibraryPage(after, limit)
	if err != nil {
		return page, err
	}
	page.Total = total
	type parentPath struct {
		ancestors []LibraryAncestor
		path      string
	}
	paths := map[string]parentPath{}
	page.Files = make([]LibraryFile, 0, len(rows))
	for _, row := range rows {
		parent, ok := paths[row.ParentID]
		if !ok {
			crumbs, err := idx.store.Path(row.ParentID)
			if err != nil {
				return page, err
			}
			ancestors := make([]LibraryAncestor, len(crumbs))
			for i, crumb := range crumbs {
				ancestors[i] = LibraryAncestor{ID: crumb.ID, Name: crumb.Name}
			}
			parent = parentPath{ancestors: ancestors, path: folderPath(crumbs)}
			paths[row.ParentID] = parent
		}
		entry := toEntry(rowToFile(row))
		entry.Path = parent.path
		page.Files = append(page.Files, LibraryFile{Entry: entry, Ancestors: parent.ancestors})
	}
	if len(rows) == limit {
		page.Next = rows[len(rows)-1].ID
	}
	if !idx.syncer.Ready() {
		page.Ready = false
		page.Files = []LibraryFile{}
		page.Next = ""
	}
	return page, nil
}

// MediaLibraryHistory includes completed plays, using the same history merge
// and visibility rules as the playback page, but never an implicit account.
func (d *DriveService) MediaLibraryHistory(account string) ([]RecentPlay, error) {
	if account == "" {
		return nil, errNoSuchAccount
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	return d.playsOf(s, historyPage, historyPages)
}

func (d *DriveService) readMediaLibrary(key string) (map[string]LibraryOverride, error) {
	if d.configErr != nil {
		return nil, d.configErr
	}
	data, err := os.ReadFile(d.mediaLibraryPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]LibraryOverride{}, nil
	}
	if err != nil {
		return nil, err
	}
	var file libraryOverridesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Version != 1 {
		return nil, i18n.New("媒体库本地记录版本不受支持")
	}
	if file.Files == nil {
		file.Files = map[string]LibraryOverride{}
	}
	for _, value := range file.Files {
		if !validLibraryOverride(value) {
			return nil, i18n.New("媒体库分组信息无效")
		}
	}
	return file.Files, nil
}

func (d *DriveService) MediaLibraryOverrides(account string) (map[string]LibraryOverride, error) {
	if account == "" {
		return nil, errNoSuchAccount
	}
	mediaLibraryMu.Lock()
	defer mediaLibraryMu.Unlock()
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	return d.readMediaLibrary(s.key)
}

func validLibraryOverride(value LibraryOverride) bool {
	return strings.TrimSpace(value.WorkID) != "" && len(value.WorkID) <= 200 && strings.TrimSpace(value.Title) != "" && len(value.Title) <= 1000 &&
		(value.Type == "movie" || value.Type == "series") && (value.Year == 0 || value.Year >= 1800 && value.Year <= 2200) &&
		value.Season >= 0 && value.Season <= 999 && value.Episode >= 0 && value.Episode <= 9999 && (value.Type != "series" || value.Episode > 0)
}

// SetMediaLibraryOverride writes through atomically; nil resets one file.
func (d *DriveService) SetMediaLibraryOverride(account, fileID string, value *LibraryOverride) error {
	if account == "" {
		return errNoSuchAccount
	}
	mediaLibraryMu.Lock()
	defer mediaLibraryMu.Unlock()
	s, err := d.sessionFor(account)
	if err != nil {
		return err
	}
	if fileID == "" || value != nil && !validLibraryOverride(*value) {
		return i18n.New("媒体库分组信息无效")
	}
	if value != nil {
		row, found, ready := d.indexedFile(s, fileID)
		if !ready || !found || row.Trashed || row.Category != "video" || row.Phase != "PHASE_TYPE_COMPLETE" {
			return errNotIndexed
		}
		value.Title = strings.TrimSpace(value.Title)
	}
	files, err := d.readMediaLibrary(s.key)
	if err != nil {
		return err
	}
	if value == nil {
		delete(files, fileID)
	} else {
		files[fileID] = *value
	}
	data, err := json.Marshal(libraryOverridesFile{Version: 1, Files: files})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.configDir, 0o700); err != nil {
		return err
	}
	path := d.mediaLibraryPath(s.key)
	tmp, err := os.CreateTemp(d.configDir, ".medialibrary-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (d *DriveService) removeMediaLibraryFile(key string) {
	mediaLibraryMu.Lock()
	defer mediaLibraryMu.Unlock()
	if err := os.Remove(d.mediaLibraryPath(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		d.log.Warn("drive: could not remove media library record", "account", key, "error", err)
	}
}
