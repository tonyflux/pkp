package services

// Folder covers: a folder card shows a few of the folder's direct
// children, from the index only — never a listing per card, never a walk
// below the folder.

// coverItems is how many children a folder card draws: one large tile and
// two small ones.
const coverItems = 3

// coverBatch caps one call; the page asks for the cards on screen.
const coverBatch = 200

// FolderCover is one folder's card cover.
type FolderCover struct {
	ID string `json:"id"`
	// Children is how many live entries the folder holds directly.
	Children int `json:"children"`
	// Items are up to three of them in cover order: files with a thumbnail
	// first, then other files, then folders, newest first in each.
	Items []Entry `json:"items"`
}

// FolderCovers reads the covers of folderIDs from account's index. Before
// the index has completed a walk it refuses (errIndexNotReady): a cover
// from part of the drive would show folders as emptier than they are.
func (d *DriveService) FolderCovers(account string, folderIDs []string) ([]FolderCover, error) {
	_, store, err := d.readyIndex(account)
	if err != nil {
		return nil, err
	}
	if len(folderIDs) > coverBatch {
		folderIDs = folderIDs[:coverBatch]
	}
	covers, err := store.Covers(folderIDs, coverItems)
	if err != nil {
		return nil, err
	}
	out := make([]FolderCover, 0, len(covers))
	for _, id := range folderIDs {
		c, ok := covers[id]
		if !ok {
			continue
		}
		delete(covers, id)
		fc := FolderCover{ID: id, Children: c.Children, Items: make([]Entry, 0, len(c.Items))}
		for _, r := range c.Items {
			fc.Items = append(fc.Items, toEntry(rowToFile(r)))
		}
		out = append(out, fc)
	}
	return out, nil
}
