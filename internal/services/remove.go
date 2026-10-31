package services

// Deletion: one channel for every way an entry leaves
// or comes back — Move to Trash, Delete Permanently, Restore, Empty Trash, and the Cleanup page's
// duplicates and empty folders. Each run is for the account it was asked
// for, one per account at a time, and goes the same way:
//
//   - the entries are read from the server first; one gone, renamed or moved
//     since the index (what the page showed) saw it is reported with the
//     reason and never sent;
//   - requests carry at most 100 ids; trashing and restoring are server
//     tasks, so each batch's task is waited for and its entries read back;
//     a permanent delete has no task and is confirmed by the entries being
//     neither live (batchGet) nor in the recycle bin (the bin listing):
//     batchGet leaves out trashed and destroyed entries alike;
//   - every entry ends removed or failed with a reason, except the batches
//     StopRemove kept from being sent;
//   - the index follows what the server confirmed; a permanent delete also
//     wakes the sync, because the server keeps listing destroyed entries as
//     trashed in the whole-tree feed and only a recycle-bin relist drops them.

import (
	"context"
	"errors"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// EventRemoveProgress reports a remove run as its entries settle.
const EventRemoveProgress = "drive:remove"

// RemoveProgress is the payload of EventRemoveProgress.
type RemoveProgress struct {
	Account string `json:"account"`
	Done    int    `json:"done"` // items settled so far (ok or failed)
	Total   int    `json:"total"`
	// Phase is "check" (re-confirming empty folders), "trash", "delete" or
	// "restore".
	Phase string `json:"phase"`
}

// RemoveResult is one entry a run could not remove.
type RemoveResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Error is the reason in Chinese; a server refusal is the server's own
	// words (cause).
	Error string `json:"error"`
}

// RemoveReport is how a run ended.
type RemoveReport struct {
	// Removed are the ids confirmed done: trashed, gone or restored.
	Removed []string       `json:"removed"`
	Failed  []RemoveResult `json:"failed"`
	// Freed is the bytes a permanent delete or Empty Trash destroyed.
	Freed int64 `json:"freed"`
	// Stopped says StopRemove cut the run short: the batches not sent by
	// then are neither removed nor failed.
	Stopped bool `json:"stopped"`
}

// TrashSummary is what Empty Trash is about to destroy, from the index.
type TrashSummary struct {
	Count int   `json:"count"` // top-level trashed entries, as the bin lists them
	Size  int64 `json:"size"`  // bytes of every trashed file
	Ready bool  `json:"ready"` // false: the index cannot say yet
}

const (
	// maxRemove is the most ids one call takes (same cap as Organize).
	maxRemove = maxPlan
	// maxEmptyWalk bounds the folders read to confirm one folder empty.
	maxEmptyWalk = 1000

	phaseCheck   = "check"
	phaseTrash   = "trash"
	phaseDelete  = "delete"
	phaseRestore = "restore"
)

var (
	errRemoveTooMany = &lazyError{func() string { return i18n.F("一次最多处理 %d 项，请缩小范围", maxRemove) }}
	errRemoving      = i18n.New("这个账号还有删除操作在进行，请等这一次完成")
)

// Why an entry was not removed, as the receipt shows it.
func whyGone() string     { return i18n.T("云盘里已经没有这一项，未处理") }
func whyMoved() string    { return i18n.T("已被移到别的文件夹，未处理") }
func whySpecial() string  { return i18n.T("特殊文件夹，未处理") }
func whyNotEmpty() string { return i18n.T("已经不是空的，未处理") }
func whyTooMany() string {
	return i18n.T("里面的文件夹太多，没能确认是空的，未处理")
}

// RemoveFiles moves entries of an account ("" is the account on screen) to
// the recycle bin, or with permanent destroys them, live or trashed. It
// returns an error only when nothing was done.
func (d *DriveService) RemoveFiles(account string, fileIDs []string, permanent bool) (RemoveReport, error) {
	phase := phaseTrash
	if permanent {
		phase = phaseDelete
	}
	return d.remove(account, fileIDs, phase, nil)
}

// RestoreFiles takes entries of an account out of the recycle bin. An entry
// already live counts as restored.
func (d *DriveService) RestoreFiles(account string, fileIDs []string) (RemoveReport, error) {
	return d.remove(account, fileIDs, phaseRestore, nil)
}

// StopRemove ends an account's remove run after the batch in flight.
func (d *DriveService) StopRemove(account string) {
	if s, err := d.sessionFor(account); err == nil {
		d.removing.stop(s.key)
	}
}

// TrashSummary counts the recycle bin of an account from its index, for
// the Empty Trash confirmation.
func (d *DriveService) TrashSummary(account string) (TrashSummary, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return TrashSummary{}, err
	}
	idx := s.index()
	if idx == nil || !idx.syncer.Ready() {
		return TrashSummary{}, nil
	}
	count, size, err := idx.store.TrashSummary()
	if err != nil {
		return TrashSummary{}, err
	}
	return TrashSummary{Count: count, Size: size, Ready: true}, nil
}

// EmptyTrash destroys everything in an account's recycle bin, as the server
// lists it now, a batch at a time, and reports each entry.
func (d *DriveService) EmptyTrash(account string) (RemoveReport, error) {
	r, end, err := d.beginRemove(account, phaseDelete)
	if err != nil {
		return RemoveReport{}, err
	}
	defer end()
	bin, err := r.d.binAll(r.s)
	if err != nil {
		return RemoveReport{}, err
	}
	ids := make([]string, len(bin))
	for i, f := range bin {
		ids[i] = f.ID
		r.files[f.ID] = f
	}
	r.total = len(ids)
	r.progress()
	r.destroy(ids)
	return r.report, nil
}

/* ---------------- one run ---------------- */

type removeRun struct {
	d     *DriveService
	s     *accountSession
	ctx   context.Context
	phase string
	total int
	// files are the entries as read before the run, by id; rows what the
	// index held for them.
	files map[string]drive.File
	rows  map[string]index.Row
	// confirming are permanent deletes sent and not live any more, still
	// to be looked for in the recycle bin.
	confirming []string
	report     RemoveReport
}

// beginRemove starts a run for account, refusing a second one. end drops
// the account's duplicate scan: what it listed may be gone now.
func (d *DriveService) beginRemove(account, phase string) (*removeRun, func(), error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, nil, err
	}
	ctx, end, ok := d.removing.begin(s.key)
	if !ok {
		return nil, nil, errRemoving
	}
	r := &removeRun{
		d: d, s: s, ctx: ctx, phase: phase,
		files: map[string]drive.File{}, rows: map[string]index.Row{},
		report: RemoveReport{Removed: []string{}, Failed: []RemoveResult{}},
	}
	return r, func() {
		d.dropDups(s.key)
		end()
		r.announce()
	}, nil
}

// announce tells System Notifications how a run that did something ended.
func (r *removeRun) announce() {
	done, faults := len(r.report.Removed), len(r.report.Failed)
	if done+faults == 0 {
		return
	}
	verb := "移到回收站" // i18n-ignore: notice.Verb is read by notify.go, which words it
	switch r.phase {
	case phaseDelete:
		verb = "彻底删除" // i18n-ignore
	case phaseRestore:
		verb = "还原" // i18n-ignore
	}
	r.d.announce(notice{Kind: noticeRemove, Account: r.s.key, Verb: verb, Done: done, Faults: faults, Failed: done == 0, Stopped: r.report.Stopped})
}

// remove is the run behind RemoveFiles, RestoreFiles and RemoveDuplicates;
// guard, when set, may refuse the whole request before anything is read.
func (d *DriveService) remove(account string, fileIDs []string, phase string, guard func(*accountSession, []string) error) (RemoveReport, error) {
	ids, err := cleanIDs(fileIDs)
	if err != nil {
		return RemoveReport{}, err
	}
	r, end, err := d.beginRemove(account, phase)
	if err != nil {
		return RemoveReport{}, err
	}
	defer end()
	if guard != nil {
		if err := guard(r.s, ids); err != nil {
			return RemoveReport{}, err
		}
	}
	r.total = len(ids)
	live, binned, err := r.locate(ids)
	if err != nil {
		return RemoveReport{}, err
	}
	var send []string
	for _, id := range ids {
		_, isLive := live[id]
		_, inBin := binned[id]
		switch {
		case !isLive && !inBin:
			r.fail(id, whyGone())
		case phase == phaseTrash && inBin:
			r.trashedOK(id) // already where it was meant to go
		case phase == phaseRestore && isLive:
			r.restoredOK(id, live[id])
		default:
			if why := r.changed(id); why != "" {
				r.fail(id, why)
				continue
			}
			send = append(send, id)
		}
	}
	r.progress()
	switch phase {
	case phaseTrash:
		r.trash(send)
	case phaseRestore:
		r.restore(send)
	case phaseDelete:
		r.destroy(send)
	}
	if len(live)+len(binned) < len(ids) {
		requestSync(r.s) // the index still lists what the server no longer has
	}
	return r.report, nil
}

// cleanIDs drops blanks and repeats and enforces maxRemove.
func cleanIDs(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > maxRemove {
		return nil, errRemoveTooMany
	}
	return out, nil
}

// locate reads ids from the server: live is what batchGet returns, binned
// what the recycle bin lists; anything in neither is gone. The bin is read
// only when batchGet left some out. A failed read ends the run before
// anything is sent. The index rows of ids are kept as what the page saw.
func (r *removeRun) locate(ids []string) (live, binned map[string]drive.File, err error) {
	live, binned = map[string]drive.File{}, map[string]drive.File{}
	for start := 0; start < len(ids); start += drive.MaxListLimit {
		chunk := ids[start:min(start+drive.MaxListLimit, len(ids))]
		files, err := callIn(r.d, r.s, func(ctx context.Context, client *drive.Client) ([]drive.File, error) {
			return client.BatchGetFiles(ctx, chunk, drive.GetFileParams{})
		})
		if err != nil {
			return nil, nil, i18n.Errorf("没能读到这些文件的最新状态：%w", err)
		}
		for _, f := range files {
			if f.Trashed {
				binned[f.ID] = f
			} else {
				live[f.ID] = f
			}
		}
	}
	missing := false
	for _, id := range ids {
		if _, ok := live[id]; !ok {
			missing = true
			break
		}
	}
	if missing {
		bin, err := r.d.binAll(r.s)
		if err != nil {
			return nil, nil, i18n.Errorf("没能读到回收站：%w", err)
		}
		wanted := make(map[string]bool, len(ids))
		for _, id := range ids {
			wanted[id] = true
		}
		for _, f := range bin {
			if wanted[f.ID] {
				binned[f.ID] = f
			}
		}
	}
	for id, f := range live {
		r.files[id] = f
	}
	for id, f := range binned {
		r.files[id] = f
	}
	if idx := r.s.index(); idx != nil {
		rows, err := idx.store.Rows(ids)
		if err != nil {
			r.d.log.Warn("remove: index unreadable, entries not compared", "error", err)
		}
		for _, row := range rows {
			r.rows[row.ID] = row
		}
	}
	return live, binned, nil
}

// changed says why an entry is not what the page showed: renamed or moved
// since the index saw it; "" when it is, or the index never knew it.
func (r *removeRun) changed(id string) string {
	row, ok := r.rows[id]
	if !ok {
		return ""
	}
	f := r.files[id]
	switch {
	case row.Name != f.Name:
		return probRenamed()
	case row.ParentID != f.ParentID:
		return whyMoved()
	}
	return ""
}

// binAll lists an account's recycle bin, every page: the entries trashed
// in their own right, which is what the Trash view shows.
func (d *DriveService) binAll(s *accountSession) ([]drive.File, error) {
	var out []drive.File
	params := drive.ListFilesParams{ParentID: drive.WholeTree, Filters: drive.NewFilters().Eq("trashed", true)}
	for {
		list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.FileList, error) {
			return client.ListFiles(ctx, params)
		})
		if err != nil {
			return nil, err
		}
		out = append(out, list.Files...)
		if list.NextPageToken == "" {
			return out, nil
		}
		params.PageToken = list.NextPageToken
	}
}

func (r *removeRun) name(id string) string {
	if f, ok := r.files[id]; ok && f.Name != "" {
		return f.Name
	}
	return r.rows[id].Name
}

func (r *removeRun) fail(id, why string) {
	r.report.Failed = append(r.report.Failed, RemoveResult{ID: id, Name: r.name(id), Error: why})
}

func (r *removeRun) ok(id string) { r.report.Removed = append(r.report.Removed, id) }

// trashedOK records an entry confirmed in the bin; the index follows,
// its subtree with it.
func (r *removeRun) trashedOK(id string) {
	r.ok(id)
	r.d.patchIndex(r.s, []string{id}, index.Patch{Trashed: new(true)})
}

// restoredOK records an entry confirmed live again, where and as the
// server put it back (a taken name comes back numbered).
func (r *removeRun) restoredOK(id string, f drive.File) {
	r.ok(id)
	p := index.Patch{Trashed: new(false), ParentID: new(f.ParentID)}
	if f.Name != "" {
		p.Name = &f.Name
	}
	r.d.patchIndex(r.s, []string{id}, p)
}

func (r *removeRun) progress() {
	if app := application.Get(); app != nil {
		done := len(r.report.Removed) + len(r.report.Failed) + len(r.confirming)
		app.Event.Emit(EventRemoveProgress, RemoveProgress{Account: r.s.key, Done: done, Total: r.total, Phase: r.phase})
	}
}

// batches sends ids moveBatch at a time through post, waits for each
// batch's task and hands what the server took to settle. A stop is
// honoured between batches.
func (r *removeRun) batches(ids []string, post postBatch, settle func(sent []string)) {
	for start := 0; start < len(ids); start += moveBatch {
		if r.ctx.Err() != nil {
			r.report.Stopped = true
			return
		}
		batch := ids[start:min(start+moveBatch, len(ids))]
		var sent, tasks []string
		for _, t := range r.d.sendSplit(r.s, batch, post, func(id string, err error) { r.fail(id, cause(err)) }) {
			sent = append(sent, t.ids...)
			tasks = append(tasks, t.task)
		}
		r.d.awaitTasks(r.s, tasks, len(sent))
		settle(sent)
		r.progress()
	}
}

// trash moves live entries to the bin. A trashed entry is left out of a
// batchGet, so one the read covered and did not return is in the bin.
func (r *removeRun) trash(ids []string) {
	r.batches(ids, batchTrash, func(sent []string) {
		got, answered := r.d.readBack(r.s, sent)
		for _, id := range sent {
			if f, ok := got[id]; !answered[id] || (ok && !f.Trashed) {
				r.fail(id, probUnconfirmed())
				continue
			}
			r.trashedOK(id)
		}
	})
}

// restore takes entries out of the bin; batchGet returns them once live.
func (r *removeRun) restore(ids []string) {
	r.batches(ids, batchUntrash, func(sent []string) {
		got, _ := r.d.readBack(r.s, sent)
		for _, id := range sent {
			if f, ok := got[id]; ok && !f.Trashed {
				r.restoredOK(id, f)
				continue
			}
			r.fail(id, probUnconfirmed())
		}
	})
}

func batchDelete(ctx context.Context, client *drive.Client, ids []string) (string, error) {
	return "", client.Delete(ctx, ids)
}

// destroy deletes entries for good. What batchGet still returns live
// failed; the rest is gone once the recycle bin no longer lists it either,
// read once after the last batch. The index drops each confirmed entry with
// its subtree, and a sync is asked for (see the top of this file).
func (r *removeRun) destroy(ids []string) {
	r.batches(ids, batchDelete, func(sent []string) {
		got, answered := r.d.readBack(r.s, sent)
		for _, id := range sent {
			if f, ok := got[id]; !answered[id] || (ok && !f.Trashed) {
				r.fail(id, probUnconfirmed())
				continue
			}
			r.confirming = append(r.confirming, id)
		}
	})
	if len(r.confirming) == 0 {
		return
	}
	pending := r.confirming
	r.confirming = nil
	bin, err := r.d.binAll(r.s)
	if err != nil {
		r.d.log.Warn("remove: recycle bin unreadable, deletes unconfirmed", "error", err)
		for _, id := range pending {
			r.fail(id, probUnconfirmed())
		}
		r.progress()
		return
	}
	still := make(map[string]bool, len(bin))
	for _, f := range bin {
		still[f.ID] = true
	}
	var gone []string
	for _, id := range pending {
		if still[id] {
			r.fail(id, probUnconfirmed())
			continue
		}
		gone = append(gone, id)
	}
	r.report.Freed += r.freedBy(gone)
	for _, id := range gone {
		r.ok(id)
	}
	if idx := r.s.index(); idx != nil && len(gone) > 0 {
		_, err := idx.store.DeleteTree(gone)
		r.d.indexErr(err)
	}
	requestSync(r.s)
	r.progress()
}

// freedBy is the bytes destroying ids released: a file's own size, and
// for a folder whatever the index holds below it.
func (r *removeRun) freedBy(ids []string) int64 {
	var total int64
	var folders []string
	for _, id := range ids {
		f := r.files[id]
		if f.IsFolder() {
			folders = append(folders, id)
			continue
		}
		total += int64(f.Size)
	}
	if idx := r.s.index(); idx != nil && len(folders) > 0 {
		n, err := idx.store.TreeSize(folders)
		if err != nil {
			r.d.log.Warn("remove: freed bytes of folders unknown", "error", err)
		}
		total += n
	}
	return total
}

/* ---------------- empty folders ---------------- */

// RemoveEmptyFolders moves folders of an account to the recycle bin once
// the server confirms, right before, that nothing live is anywhere inside
// them but empty folders; the rest are reported 「no longer empty, not processed」.
// Folders are only ever trashed, never deleted for good.
func (d *DriveService) RemoveEmptyFolders(account string, folderIDs []string) (RemoveReport, error) {
	ids, err := cleanIDs(folderIDs)
	if err != nil {
		return RemoveReport{}, err
	}
	r, end, err := d.beginRemove(account, phaseCheck)
	if err != nil {
		return RemoveReport{}, err
	}
	defer end()
	r.total = len(ids)
	live, binned, err := r.locate(ids)
	if err != nil {
		return RemoveReport{}, err
	}
	var check []string
	for _, id := range ids {
		f, isLive := live[id]
		_, inBin := binned[id]
		switch {
		case inBin:
			r.trashedOK(id)
		case !isLive:
			r.fail(id, whyGone())
		case !f.IsFolder():
			r.fail(id, probNotFolder())
		case f.FolderType != "NORMAL":
			r.fail(id, whySpecial())
		default:
			if why := r.changed(id); why != "" {
				r.fail(id, why)
				continue
			}
			check = append(check, id)
		}
	}
	r.progress()
	var empty []string
	for _, id := range check {
		if r.ctx.Err() != nil {
			r.report.Stopped = true
			return r.report, nil
		}
		ok, err := r.d.emptyBelow(r.s, id)
		switch {
		case errors.Is(err, errTooManyFolders):
			r.fail(id, whyTooMany())
		case err != nil:
			r.fail(id, cause(err))
		case !ok:
			r.fail(id, whyNotEmpty())
		default:
			empty = append(empty, id)
			r.confirming = append(r.confirming, id) // counted as settled while checking
		}
		r.progress()
	}
	r.confirming = nil
	r.phase = phaseTrash
	r.trash(empty)
	return r.report, nil
}

var errTooManyFolders = errors.New("too many folders to confirm empty")

// emptyBelow says whether nothing live but empty folders is anywhere under
// folderID, read from the server now. Most such folders hold nothing at
// all, which one request of one entry answers; a shell of empty folders is
// walked folder by folder, stopping at the first file.
func (d *DriveService) emptyBelow(s *accountSession, folderID string) (bool, error) {
	holds, err := d.holdsLive(s, folderID)
	if err != nil || !holds {
		return !holds, err
	}
	queue := []string{folderID}
	for walked := 0; len(queue) > 0; walked++ {
		if walked >= maxEmptyWalk {
			return false, errTooManyFolders
		}
		entries, err := d.listAll(s, queue[0], maxListing)
		if err != nil {
			return false, err
		}
		queue = queue[1:]
		if slices.ContainsFunc(entries, func(f drive.File) bool { return !f.IsFolder() }) {
			return false, nil
		}
		for _, f := range entries {
			queue = append(queue, f.ID)
		}
	}
	return true, nil
}
