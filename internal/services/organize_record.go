package services

// The record of each Organize run, kept per account in
// organize-<key>.json, and Undo from it. The record is what makes undo
// possible after the page is gone or the app restarted: it says where each
// entry was, where the run left it, what went to the trash and which
// folders the run made. Undo checks each step against the server first and
// skips, with the reason, whatever is no longer as the run left it.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

const (
	// organizeKeep is how many runs an account's record keeps.
	organizeKeep    = 30
	organizeVersion = 1
)

var (
	errRecordMissing = i18n.New("找不到这次整理的记录")
	errUndone        = i18n.New("这次整理已经撤销过了")
)

// Why Undo left something as it is.
func probMovedSince() string   { return i18n.T("已不在本次整理后的位置，未处理") }
func probPlaceTaken() string   { return i18n.T("原位置已有同名项，未处理") }
func probRestoreTaken() string { return i18n.T("原位置已有同名项，未恢复") }
func probNotInBin() string     { return i18n.T("回收站里已经没有这一项") }
func probParentAway() string   { return i18n.T("原来的文件夹没有恢复，未恢复") }
func probStillHolds() string   { return i18n.T("里面还有其他内容，已保留") }
func probUnread() string       { return i18n.T("读不到这一项，未处理") }
func fmtRestoredAs(name string) string {
	return i18n.F("已恢复；原位置已有同名项，云盘改名为「%s」", name)
}

// OrganizeRecord is one run of Organize as kept for Recent Tasks and Undo.
type OrganizeRecord struct {
	ID       string `json:"id"`
	At       string `json:"at"` // RFC 3339
	FolderID string `json:"folderId"`
	// Scope is the folder's path as the index knows it ("All Files / Unsorted"),
	// or its id when the index does not.
	Scope  string `json:"scope"`
	Task   string `json:"task"`
	Tokens int    `json:"tokens"`
	// Items follow the request's changes, one each.
	Items []OrganizeRecordItem `json:"items"`
	// Created are the folders this run made, parents before children.
	Created []OrganizeFolder `json:"created"`
	// Finished is false while the run goes, and in a record left by a run
	// the app did not see to its end; its items may be pending then.
	Finished bool          `json:"finished"`
	Stopped  bool          `json:"stopped"`
	Undo     *OrganizeUndo `json:"undo"`
}

// OrganizeRecordItem is one change of a run: where the entry was, what was
// asked, and where the run left it.
type OrganizeRecordItem struct {
	FileID string `json:"fileId"`
	Folder bool   `json:"folder"`
	Op     string `json:"op"`
	// Name and ParentID are where it was before.
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	// NewName and ToPath are what was asked.
	NewName string   `json:"newName"`
	ToPath  []string `json:"toPath"`
	// AtName, AtParentID and Trashed are where the run left it.
	AtName     string `json:"atName"`
	AtParentID string `json:"atParentId"`
	Trashed    bool   `json:"trashed"`
	// Done says the item is settled; with Error "" it happened, otherwise
	// Error says why not, or why it ended elsewhere than asked.
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

// OrganizeFolder is a folder a run made.
type OrganizeFolder struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	// Path is its folder names below the run's FolderID.
	Path []string `json:"path"`
}

// OrganizeUndo is what Undo did to a run.
type OrganizeUndo struct {
	At       string `json:"at"`
	Finished bool   `json:"finished"`
	Stopped  bool   `json:"stopped"`
	// Restored came back from the trash, Moved went back to their old
	// place and name, Removed are made folders that went to the trash.
	Restored int            `json:"restored"`
	Moved    int            `json:"moved"`
	Removed  int            `json:"removed"`
	Skipped  []OrganizeSkip `json:"skipped"`
}

// OrganizeSkip is one step Undo left alone, or did otherwise than asked.
type OrganizeSkip struct {
	FileID string `json:"fileId"`
	Name   string `json:"name"`
	Error  string `json:"error"`
}

type organizeFile struct {
	Version int              `json:"version"`
	Records []OrganizeRecord `json:"records"`
}

// OrganizeHistory lists an account's recorded runs ("" is the account on
// screen), newest first.
func (d *DriveService) OrganizeHistory(account string) ([]OrganizeRecord, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	return d.organizeRecords(s.key)
}

// UndoOrganize undoes a recorded run of an account, once: it restores what
// the run put in the trash, moves and renames back what is still where and
// as the run left it, then puts the folders the run made in the trash,
// deepest first, if they are empty. Every step is checked first; what no
// longer fits is skipped and said so. It shares Organize's one run per
// account and its progress event, and returns the record with Undo filled.
func (d *DriveService) UndoOrganize(account, recordID string) (OrganizeRecord, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return OrganizeRecord{}, err
	}
	ctx, end, err := d.beginOrganize(s.key)
	if err != nil {
		return OrganizeRecord{}, err
	}
	defer end()

	records, err := d.organizeRecords(s.key)
	if err != nil {
		return OrganizeRecord{}, err
	}
	i := slices.IndexFunc(records, func(r OrganizeRecord) bool { return r.ID == recordID })
	if i < 0 {
		return OrganizeRecord{}, errRecordMissing
	}
	rec := records[i]
	if rec.Undo != nil {
		return OrganizeRecord{}, errUndone
	}
	u := &OrganizeUndo{At: time.Now().Format(time.RFC3339), Skipped: []OrganizeSkip{}}
	rec.Undo = u
	// Written before the first step: whatever happens next, the run is
	// not undone a second time.
	if err := d.keepOrganizeRecord(s.key, rec); err != nil {
		return OrganizeRecord{}, i18n.Errorf("没能写下整理记录，未撤销：%w", err)
	}

	var restore, back, made []*orgItem
	for _, it := range rec.Items {
		item := &orgItem{id: it.FileID, isFolder: it.Folder, name: it.AtName, parent: it.AtParentID}
		switch {
		case it.Trashed:
			restore = append(restore, item)
		case it.AtName != "" && (it.AtName != it.Name || it.AtParentID != it.ParentID):
			item.newName, item.to = it.Name, it.ParentID
			back = append(back, item)
		}
	}
	for _, f := range rec.Created {
		made = append(made, &orgItem{id: f.ID, op: opTrashIfEmpty, isFolder: true, name: f.Name, parent: f.ParentID})
	}

	r := d.newRun(ctx, s, rec.FolderID, len(restore)+len(back)+len(made))
	r.undo = true
	fill := func() {
		*u = OrganizeUndo{At: u.At, Finished: u.Finished, Stopped: r.halted, Skipped: []OrganizeSkip{}}
		skip := func(it *orgItem) {
			if it.problem != "" {
				u.Skipped = append(u.Skipped, OrganizeSkip{FileID: it.id, Name: it.name, Error: it.problem})
			}
		}
		for _, it := range restore {
			if it.restored {
				u.Restored++
			}
			skip(it)
		}
		for _, it := range back {
			if it.finished && it.problem == "" {
				u.Moved++
			}
			skip(it)
		}
		for _, it := range made {
			if it.trashed {
				u.Removed++
			}
			skip(it)
		}
	}
	r.save = func() error {
		fill()
		return d.keepOrganizeRecord(s.key, rec)
	}

	r.restore(restore)
	r.putBack(back)
	r.removeMade(made)

	u.Finished = true
	r.checkpoint(true)
	fill()
	requestSync(s)
	d.announce(notice{Kind: noticeUndo, Account: s.key, Done: u.Restored + u.Moved + u.Removed, Faults: len(u.Skipped), Stopped: u.Stopped})
	return rec, nil
}

// restore brings the entries a run trashed back from the bin, each where
// the bin says it came from, unless that folder holds its name by now (the
// server would number it). A folder comes back before anything that was in
// it.
func (r *organizeRun) restore(items []*orgItem) {
	rows := map[*orgItem]drive.File{}
	var pending []*orgItem
	for _, it := range items {
		if r.stopped() {
			r.stop(it)
			continue
		}
		row, err := r.binRow(it.id)
		switch {
		case err != nil:
			r.finish(it, cause(err))
		case row == nil:
			r.finish(it, probNotInBin())
		default:
			rows[it] = *row
			pending = append(pending, it)
		}
	}
	away := map[string]bool{}
	for len(pending) > 0 {
		if r.stopped() {
			for _, it := range pending {
				r.stop(it)
			}
			return
		}
		waiting := map[string]bool{}
		for _, it := range pending {
			waiting[it.id] = true
		}
		var level, later []*orgItem
		for _, it := range pending {
			if p := rows[it].ParentID; waiting[p] && p != it.id {
				later = append(later, it)
			} else {
				level = append(level, it)
			}
		}
		if len(level) == 0 {
			level, later = pending, nil
		}
		var ready []*orgItem
		for _, it := range level {
			row := rows[it]
			if away[row.ParentID] {
				r.finish(it, probParentAway())
				away[it.id] = true
				continue
			}
			if err := r.list(row.ParentID); err != nil {
				r.finish(it, cause(err))
				away[it.id] = true
				continue
			}
			if !r.names.free(row.ParentID, row.Name, it.id) {
				r.finish(it, probRestoreTaken())
				away[it.id] = true
				continue
			}
			it.parent, it.name = row.ParentID, row.Name
			r.names.add(it.parent, it.name, it.id)
			ready = append(ready, it)
		}
		var sent []sentBatch
		for start := 0; start < len(ready); start += moveBatch {
			sent = append(sent, r.send(ready[start:min(start+moveBatch, len(ready))], "", batchUntrash, func(it *orgItem, err error) {
				r.names.remove(it.parent, it.name, it.id)
				r.finish(it, cause(err))
				away[it.id] = true
			})...)
		}
		got, _ := r.await(sent)
		for _, b := range sent {
			for _, it := range b.items {
				f, ok := got[it.id]
				if !ok || f.Trashed {
					r.names.remove(it.parent, it.name, it.id)
					r.finish(it, probUnconfirmed())
					away[it.id] = true
					continue
				}
				it.restored = true
				trashed := false
				r.d.patchIndex(r.s, []string{it.id}, index.Patch{Trashed: &trashed})
				if f.ParentID != it.parent {
					r.names.remove(it.parent, it.name, it.id)
					it.parent = f.ParentID
					r.names.add(it.parent, it.name, it.id)
				}
				if f.Name != "" && f.Name != it.name {
					r.renamedTo(it, f.Name)
					r.finish(it, fmtRestoredAs(f.Name))
					continue
				}
				r.finish(it, "")
			}
		}
		r.checkpoint(false)
		pending = later
	}
}

func batchUntrash(ctx context.Context, client *drive.Client, ids []string) (string, error) {
	return client.BatchUntrash(ctx, ids)
}

// binRow reads an entry of the recycle bin, nil when it is not there. The
// bin listing narrowed to the id answers where a direct read is refused.
func (r *organizeRun) binRow(id string) (*drive.File, error) {
	list, err := callIn(r.d, r.s, func(ctx context.Context, client *drive.Client) (*drive.FileList, error) {
		return client.ListFiles(ctx, drive.ListFilesParams{
			ParentID: drive.WholeTree,
			Filters:  drive.NewFilters().Eq("trashed", true).Eq("id", id),
		})
	})
	if err != nil {
		return nil, err
	}
	for i := range list.Files {
		if list.Files[i].ID == id {
			return &list.Files[i], nil
		}
	}
	return nil, nil
}

// putBack moves and renames back the entries that are exactly where and as
// the run left them, through the same checks and rounds as Organize.
func (r *organizeRun) putBack(items []*orgItem) {
	if len(items) == 0 {
		return
	}
	var folders []string
	for _, it := range items {
		folders = append(folders, it.parent, it.to)
	}
	if err := r.listMany(folders); err != nil {
		for _, it := range items {
			r.finish(it, cause(err))
		}
		return
	}
	r.execute(r.settle(r.nest(r.check(items))))
}

// removeMade puts the folders the run made in the trash once empty,
// deepest first. One moved since stays.
func (r *organizeRun) removeMade(items []*orgItem) {
	if len(items) == 0 {
		return
	}
	got, answered := r.d.readBack(r.s, idsOf(items))
	var live []*orgItem
	for _, it := range items {
		f, ok := got[it.id]
		switch {
		case r.stopped():
			r.stop(it)
		case !answered[it.id]:
			r.finish(it, probUnread())
		case !ok || f.ParentID != it.parent:
			r.finish(it, probMovedSince())
		default:
			live = append(live, it)
		}
	}
	r.trashEmptied(live, probStillHolds())
}

/* ---------------- the record file ---------------- */

func newRecordID() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

func (d *DriveService) organizePath(key string) string {
	return d.accountFile(key, "organize-", ".json")
}

// organizeScope names a run's folder for the record.
func (d *DriveService) organizeScope(s *accountSession, folderID string) string {
	if folderID == "" {
		return folderPath(nil)
	}
	if idx := s.index(); idx != nil {
		crumbs, err := idx.store.Path(folderID)
		if err == nil && len(crumbs) > 0 && crumbs[len(crumbs)-1].ID == folderID {
			return folderPath(crumbs)
		}
	}
	return folderID
}

// recordItems is a run's items as the record keeps them.
func recordItems(changes []OrganizeChange, items []*orgItem) []OrganizeRecordItem {
	out := make([]OrganizeRecordItem, len(items))
	for i, it := range items {
		c := changes[i]
		out[i] = OrganizeRecordItem{
			FileID: c.FileID, Folder: it.isFolder, Op: c.Op,
			Name: c.Name, ParentID: c.ParentID, NewName: c.NewName, ToPath: c.ToPath,
			AtName: it.name, AtParentID: it.parent, Trashed: it.trashed,
			Done: it.finished, Error: it.problem,
		}
	}
	return out
}

// organizeRecords reads an account's runs, newest first. An unreadable
// file is moved aside: the next run starts a new one.
func (d *DriveService) organizeRecords(key string) ([]OrganizeRecord, error) {
	if d.configErr != nil {
		return nil, d.configErr
	}
	path := d.organizePath(key)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []OrganizeRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	var file organizeFile
	if err := json.Unmarshal(data, &file); err != nil {
		d.log.Warn("organize: record unreadable, starting empty", "path", path, "error", err)
		if err := os.Rename(path, path+".bad"); err != nil {
			d.log.Warn("organize: could not move the unreadable record aside", "error", err)
		}
		return []OrganizeRecord{}, nil
	}
	if file.Records == nil {
		file.Records = []OrganizeRecord{}
	}
	return file.Records, nil
}

// keepOrganizeRecord writes rec into an account's record, in place of the
// one with its id or as the newest, keeping organizeKeep. The file is
// replaced atomically. Runs of one account are serial (beginOrganize), so
// nothing else writes it meanwhile.
func (d *DriveService) keepOrganizeRecord(key string, rec OrganizeRecord) error {
	records, err := d.organizeRecords(key)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(records, func(r OrganizeRecord) bool { return r.ID == rec.ID }); i >= 0 {
		records[i] = rec
	} else {
		records = append([]OrganizeRecord{rec}, records...)
	}
	if len(records) > organizeKeep {
		records = records[:organizeKeep]
	}
	data, err := json.Marshal(organizeFile{Version: organizeVersion, Records: records})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.configDir, 0o700); err != nil {
		return err
	}
	path := d.organizePath(key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
