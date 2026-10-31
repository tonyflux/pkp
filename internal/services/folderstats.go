package services

import (
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

// “What's here”: what a folder holds, from the index, before anyone asks a
// model anything. Only the play record may reach the cloud (its history
// page); without it the figure is the local plays' alone.

const folderLargest = 5

// CategoryStat is the files of one category: video, image, audio,
// document, archive or other, as the file page's icons class them.
type CategoryStat struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
	Size     int64  `json:"size"`
}

// FolderStats is what a folder holds at any depth.
type FolderStats struct {
	Files   int   `json:"files"`
	Folders int   `json:"folders"`
	Size    int64 `json:"size"` // bytes of the files
	// Categories are largest first.
	Categories []CategoryStat `json:"categories"`
	// The duplicates are Cleanup's: same content hash, counted within the
	// folder; DupCopies and DupWaste are what deleting all but one of each
	// would remove and free.
	DupGroups int   `json:"dupGroups"`
	DupCopies int   `json:"dupCopies"`
	DupWaste  int64 `json:"dupWaste"`
	// Unfinished is the videos in here started and not watched to the end.
	Unfinished int `json:"unfinished"`
	// Largest are up to five of the folder's direct children, biggest
	// first; a folder's Size is then everything below it.
	Largest []Entry `json:"largest"`
}

// FolderStats sums folderID ("" = the whole drive) from account's index.
func (d *DriveService) FolderStats(account, folderID string) (FolderStats, error) {
	s, store, err := d.readyIndex(account)
	if err != nil {
		return FolderStats{}, err
	}
	if folderID != "" {
		row, found, err := store.Get(folderID)
		if err != nil {
			return FolderStats{}, err
		}
		if !found || row.Trashed || row.Kind != drive.KindFolder {
			return FolderStats{}, errNoSuchFolder
		}
	}
	sum, err := store.Summarize(folderID)
	if err != nil {
		return FolderStats{}, err
	}
	out := FolderStats{Files: sum.Files, Folders: sum.Folders, Size: sum.Size,
		Categories: make([]CategoryStat, len(sum.Categories)), Largest: []Entry{}}
	for i, c := range sum.Categories {
		out.Categories[i] = CategoryStat(c)
	}

	dups, err := store.Duplicates(index.DupScan{Root: folderID})
	if err != nil {
		return FolderStats{}, err
	}
	for _, g := range dups.Groups {
		out.DupGroups++
		out.DupCopies += g.Count - 1
		out.DupWaste += g.Waste
	}

	largest, err := store.Largest(folderID, folderLargest)
	if err != nil {
		return FolderStats{}, err
	}
	for _, r := range largest {
		e := toEntry(rowToFile(r.Row))
		e.Size = r.Total
		out.Largest = append(out.Largest, e)
	}

	// Stats answer without positions rather than not at all.
	plays, err := d.playsOf(s, drive.MaxListLimit, 1)
	if err != nil {
		d.log.Warn("drive: no play positions for the folder stats", "error", err)
		return out, nil
	}
	var started []string
	for _, p := range plays {
		if unfinished(p) {
			started = append(started, p.FileID)
		}
	}
	if len(started) > 0 {
		inside, err := store.Within(folderID, started)
		if err != nil {
			return FolderStats{}, err
		}
		out.Unfinished = len(inside)
	}
	return out, nil
}
