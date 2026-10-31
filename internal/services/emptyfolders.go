package services

// Cleanup · Empty folders: folders holding no live file at any
// depth, found in the account's index. Only the top of each empty subtree
// is listed; trashing it takes the rest along (RemoveEmptyFolders).

import (
	"cmp"
	"errors"
	"slices"
	"time"
)

// EmptyFolder is one top-level empty folder.
type EmptyFolder struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ParentID    string `json:"parentId"`
	Path        string `json:"path"` // Full canonical path including the folder.
	CreatedTime string `json:"createdTime"`
	Nested      int    `json:"nested"` // empty folders inside it
	// Recent (made in the last 24 hours) and HasTrashed (recycle-bin
	// entries inside, which a restore would put back here) are listed but
	// not picked by default.
	Recent     bool `json:"recent"`
	HasTrashed bool `json:"hasTrashed"`
}

// EmptyFolderPage is a scan of Empty folders.
type EmptyFolderPage struct {
	Folders  []EmptyFolder `json:"folders"` // top level only, by Path
	Total    int           `json:"total"`   // all empty folders, nested ones included
	SyncedAt string        `json:"syncedAt"`
}

// recentFolder is how new a folder is still being filled, probably.
const recentFolder = 24 * time.Hour

// ScanEmptyFolders lists the empty folders of an account below folderID
// ("" = the whole drive), from its index alone. Special folders and the
// root never count.
func (d *DriveService) ScanEmptyFolders(account string, folderID string) (EmptyFolderPage, error) {
	_, store, err := d.readyIndex(account)
	if errors.Is(err, errIndexNotReady) {
		return EmptyFolderPage{}, errScanNotReady
	}
	if err != nil {
		return EmptyFolderPage{}, err
	}
	found, err := store.EmptyFolders(folderID)
	if err != nil {
		return EmptyFolderPage{}, err
	}
	page := EmptyFolderPage{Folders: make([]EmptyFolder, 0, len(found.Folders)), Total: found.Total, SyncedAt: syncedAt(store)}
	now := time.Now()
	for _, f := range found.Folders {
		crumbs, err := store.Path(f.ID)
		if err != nil {
			return EmptyFolderPage{}, err
		}
		created, err := time.Parse(time.RFC3339, f.CreatedTime)
		page.Folders = append(page.Folders, EmptyFolder{
			ID: f.ID, Name: f.Name, ParentID: f.ParentID, Path: folderPath(crumbs), CreatedTime: f.CreatedTime,
			Nested: f.Nested, HasTrashed: f.HasTrashed,
			Recent: err == nil && now.Sub(created) < recentFolder,
		})
	}
	slices.SortFunc(page.Folders, func(a, b EmptyFolder) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.ID, b.ID))
	})
	return page, nil
}
