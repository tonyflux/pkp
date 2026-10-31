package services

// AI assistant · Organize: the page turns what the user asked for into a
// plan of changes — rename, move along a folder path below the scope (the
// folders made as needed), move to the trash, put a folder in the trash once
// it is empty — and the user confirms it. This side checks every change
// against the folders as the server has them now and carries out what still
// fits, for the account the plan was made for, whatever is on screen by then:
//
//   - an entry renamed, moved or deleted since the preview is left alone;
//   - a folder and anything below it are never both changed by one plan: the
//     second change would act on a place the first one already moved;
//   - no change may end on a name something else holds where it lands,
//     compared as the page compares (Unicode NFC, any case), and the steps
//     are ordered so that no request ever names a taken one — the server
//     refuses a rename onto a taken name, but silently numbers a moved or
//     restored entry "name(1)";
//   - nothing is ever deleted: the only writes are make folder, rename, move
//     and move to the trash, whatever the plan says.
//
// Moves and trashing run as server tasks that finish seconds after the
// request returns. A run waits for each batch's task and reads the entries
// back before any step that depends on them, and reports where each one
// really is. Every run is recorded (organize_record.go), so undo works from
// the record, also after a restart.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/text/unicode/norm"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// EventOrganizeProgress reports a run of Organize (or of undo) as its
// changes finish.
const EventOrganizeProgress = "organize:progress"

// OrganizeProgress is the payload of EventOrganizeProgress.
type OrganizeProgress struct {
	// Account is the account the run acts for (Account.ID).
	Account string `json:"account"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
}

const (
	// maxPlan is the most changes one plan may carry.
	maxPlan = 5000
	// maxListing bounds one folder a run reads in full: two hundred pages.
	maxListing = 20000
	// moveBatch is the most ids one batch request carries, as many as a
	// list page and a batchGet.
	moveBatch = drive.MaxListLimit
	// listParallel is how many folders a run reads at once.
	listParallel = 4

	opTrash        = "trash"
	opTrashIfEmpty = "trashIfEmpty"
)

var (
	// taskPoll paces the reads of a batch's server task; tests shorten it.
	taskPoll = 400 * time.Millisecond
	// A run waits for a batch's task taskWait plus a second per entry, at
	// most taskWaitCap, then reads the entries back whatever the task says.
	taskWait    = 30 * time.Second
	taskWaitCap = 5 * time.Minute
)

var (
	errPlanTooLarge = &lazyError{func() string { return i18n.F("单个计划不能超过 %d 项", maxPlan) }}
	errOrganizing   = i18n.New("这个账号正在整理，请等这一次完成")
)

// Why a change did not happen, as the receipt shows it.
func probStale() string     { return i18n.T("已被移动或删除，未处理") }
func probRenamed() string   { return i18n.T("已被改名，未处理") }
func probUnknownOp() string { return i18n.T("不支持的操作，未处理") }
func probNotFolder() string { return i18n.T("不是文件夹，未处理") }
func probBadName() string   { return i18n.T("新名称无效，未处理") }
func probBadPath() string   { return i18n.T("目标路径无效，未处理") }
func probNested() string {
	return i18n.T("同一计划不能同时改动文件夹和它里面的项，未处理")
}
func probIntoItself() string { return i18n.T("不能把文件夹移到它自己里面，未处理") }
func probClash() string      { return i18n.T("目标位置已有同名项，未处理") }
func probSwap() string {
	return i18n.T("与其他文件的名称互相占用，未处理；可以分两次整理")
}
func probStopped() string     { return i18n.T("已停止，未处理") }
func probNotEmpty() string    { return i18n.T("执行后不是空文件夹，已保留") }
func probUnconfirmed() string { return i18n.T("云盘没有确认这一步，未处理") }
func fmtFileOnPath(name string) string {
	return i18n.F("「%s」已被同名文件占用，未处理", name)
}
func fmtManyOnPath(name string) string { return i18n.F("这里有多个「%s」，未处理", name) }
func fmtMakeFailed(name, cause string) string {
	return i18n.F("没能新建「%s」：%s", name, cause)
}
func fmtReadFailed(name, cause string) string {
	return i18n.F("读不出「%s」里有什么：%s", name, cause)
}
func fmtNumbered(name string) string {
	return i18n.F("目标名称已被占用，云盘改名为「%s」", name)
}

// OrganizeChange is one confirmed change of a plan.
type OrganizeChange struct {
	FileID string `json:"fileId"`
	Folder bool   `json:"folder"`
	// Name and ParentID are where the preview saw the entry. Found anywhere
	// else, or under another name, it has changed since and is left alone.
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	// Op is "" to move and/or rename, "trash", or "trashIfEmpty" (folders
	// only: to the trash if it is empty once everything else is done).
	// Anything else is refused for that change alone.
	Op string `json:"op"`
	// NewName is the name the entry ends up with; "" keeps Name.
	NewName string `json:"newName"`
	// ToPath names the folders below the request's FolderID the entry ends
	// up in ([] is FolderID itself), each made when there is none. Nil
	// means ToParentID instead, the folder id undo moves an entry back to.
	ToPath     []string `json:"toPath"`
	ToParentID string   `json:"toParentId"`
}

// OrganizeRequest is a confirmed plan.
type OrganizeRequest struct {
	// FolderID is the scope ("" is the root): ToPath starts there.
	FolderID string `json:"folderId"`
	// Task is what the user asked for, and Tokens what the plan cost the
	// model; both only go into the record.
	Task    string           `json:"task"`
	Tokens  int              `json:"tokens"`
	Changes []OrganizeChange `json:"changes"`
}

// Organize carries out a confirmed plan for an account ("" is the account
// on screen), one run per account at a time. Changes that no longer fit
// what the folders hold are reported and skipped; the rest go ahead, each
// reported on its own. The run is recorded before the first write and again
// at the end. It returns an error only when nothing was done: the plan is
// too large, another run is going, the folders could not be read or the
// record could not be written.
func (d *DriveService) Organize(account string, req OrganizeRequest) (OrganizeRecord, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return OrganizeRecord{}, err
	}
	if len(req.Changes) > maxPlan {
		return OrganizeRecord{}, errPlanTooLarge
	}
	ctx, end, err := d.beginOrganize(s.key)
	if err != nil {
		return OrganizeRecord{}, err
	}
	defer end()

	r := d.newRun(ctx, s, req.FolderID, len(req.Changes))
	items := make([]*orgItem, len(req.Changes))
	for i, c := range req.Changes {
		items[i] = newItem(c)
	}
	if err := r.read(items); err != nil {
		return OrganizeRecord{}, err
	}
	live := r.settle(r.nest(r.check(items)))

	rec := OrganizeRecord{
		ID:       newRecordID(),
		At:       time.Now().Format(time.RFC3339),
		FolderID: req.FolderID,
		Scope:    d.organizeScope(s, req.FolderID),
		Task:     req.Task,
		Tokens:   req.Tokens,
	}
	fill := func() {
		rec.Items = recordItems(req.Changes, items)
		rec.Created = slices.Clone(r.created)
		rec.Stopped = r.halted
	}
	if len(items) > 0 {
		r.save = func() error {
			fill()
			return d.keepOrganizeRecord(s.key, rec)
		}
		if err := r.save(); err != nil {
			return OrganizeRecord{}, i18n.Errorf("没能写下整理记录，未执行：%w", err)
		}
	}

	live = r.settle(r.makeFolders(live))
	var moves, trash, empty []*orgItem
	for _, it := range live {
		switch it.op {
		case opTrash:
			trash = append(trash, it)
		case opTrashIfEmpty:
			empty = append(empty, it)
		default:
			moves = append(moves, it)
		}
	}
	r.execute(moves)
	r.trashAll(trash)
	r.trashEmptied(empty, probNotEmpty())

	rec.Finished = true
	r.checkpoint(true)
	fill()
	requestSync(s)
	ok, faults := 0, 0
	for _, it := range rec.Items {
		switch {
		case it.Error != "":
			faults++
		case it.Done:
			ok++
		}
	}
	d.announce(notice{Kind: noticeOrganize, Account: s.key, Done: ok, Faults: faults, Stopped: rec.Stopped})
	return rec, nil
}

// StopOrganize ends an account's run (or undo) after the step in flight:
// what has been done stays done and is reported, the rest is not started.
func (d *DriveService) StopOrganize(account string) {
	if s, err := d.sessionFor(account); err == nil {
		d.organizing.stop(s.key)
	}
}

func (d *DriveService) beginOrganize(key string) (context.Context, func(), error) {
	ctx, end, ok := d.organizing.begin(key)
	if !ok {
		return nil, nil, errOrganizing
	}
	return ctx, end, nil
}

// runs holds the stop of each account's run of one kind in flight (organize,
// delete): one run per account at a time.
type runs struct {
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
}

// begin registers a run for key; ok is false while another is going. end
// must be called when the run is over.
func (r *runs) begin(key string) (ctx context.Context, end func(), ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.cancel[key]; busy {
		return nil, nil, false
	}
	if r.cancel == nil {
		r.cancel = map[string]context.CancelFunc{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel[key] = cancel
	return ctx, func() {
		r.mu.Lock()
		delete(r.cancel, key)
		r.mu.Unlock()
		cancel()
	}, true
}

// stop cancels key's run, if one is going.
func (r *runs) stop(key string) {
	r.mu.Lock()
	cancel := r.cancel[key]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// listAll reads every live entry directly in folderID, one timeout per
// page. A folder of more than limit entries is refused rather than read in
// part.
func (d *DriveService) listAll(s *accountSession, folderID string, limit int) ([]drive.File, error) {
	var out []drive.File
	params := drive.ListFilesParams{ParentID: folderID, Filters: drive.OnlyLive(), Limit: drive.MaxListLimit}
	for {
		list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.FileList, error) {
			return client.ListFiles(ctx, params)
		})
		if err != nil {
			return nil, err
		}
		out = append(out, list.Files...)
		if len(out) > limit {
			return nil, i18n.Errorf("有文件夹超过 %d 项，无法核对，请选择更小的范围", limit)
		}
		if list.NextPageToken == "" {
			return out, nil
		}
		params.PageToken = list.NextPageToken
	}
}

/* ---------------- names ---------------- */

// names is what a run knows each folder holds: per folder, the ids holding
// each name, compared as nameKey compares.
type names map[string]map[string][]string

// nameKey is how two names are compared: the page's rule (NFC, any case),
// stricter than the server, which tells case apart.
func nameKey(name string) string { return norm.NFC.String(strings.ToLower(name)) }

func (n names) holders(folder, name string) []string { return n[folder][nameKey(name)] }

// free says whether name is free in folder for id: nobody else holds it.
func (n names) free(folder, name, id string) bool {
	for _, h := range n.holders(folder, name) {
		if h != id {
			return false
		}
	}
	return true
}

func (n names) add(folder, name, id string) {
	m := n[folder]
	if m == nil {
		m = map[string][]string{}
		n[folder] = m
	}
	k := nameKey(name)
	m[k] = append(m[k], id)
}

func (n names) remove(folder, name, id string) {
	k := nameKey(name)
	ids := n[folder][k]
	if i := slices.Index(ids, id); i >= 0 {
		n[folder][k] = slices.Delete(slices.Clone(ids), i, i+1)
	}
}

func (n names) clone() names {
	out := make(names, len(n))
	for folder, m := range n {
		c := make(map[string][]string, len(m))
		for k, ids := range m {
			c[k] = slices.Clone(ids)
		}
		out[folder] = c
	}
	return out
}

// pathKey identifies a path below the scope as nameKey compares it.
func pathKey(path []string) string {
	keys := make([]string, len(path))
	for i, p := range path {
		keys[i] = nameKey(p)
	}
	return strings.Join(keys, "/")
}

// A folder still to be made stands in under an id no server id looks like,
// one per path: two changes into "Season 1" and "season 1" share it.
func isPlaceholder(id string) bool { return strings.HasPrefix(id, "\x00") }

/* ---------------- one run ---------------- */

// orgItem is one change on its way: where the entry is now and where it
// ends up.
type orgItem struct {
	id       string
	op       string
	isFolder bool
	// name and parent are where the entry is, kept current.
	name, parent string
	// newName and to are where it ends; to is a placeholder until the
	// folder is made.
	newName, to string
	path        []string
	trashed     bool
	restored    bool
	finished    bool
	// problem is why the change did not happen, or why it ended elsewhere
	// than asked; "" once done.
	problem string
}

// newItem reads one change of a request; what no folder could make sense
// of is refused before anything is read.
func newItem(c OrganizeChange) *orgItem {
	it := &orgItem{id: c.FileID, op: c.Op, isFolder: c.Folder, name: c.Name, parent: c.ParentID, newName: c.NewName, to: c.ToParentID, path: c.ToPath}
	switch c.Op {
	case "":
		if it.newName == "" {
			it.newName = c.Name
		}
		switch {
		case invalidName(it.newName):
			it.problem = probBadName()
		case slices.ContainsFunc(c.ToPath, invalidName):
			it.problem = probBadPath()
		}
	case opTrash, opTrashIfEmpty:
		// Trashed in place: the entry holds its name until the end.
		it.newName, it.to, it.path = c.Name, c.ParentID, nil
		if c.Op == opTrashIfEmpty && !c.Folder {
			it.problem = probNotFolder()
		}
	default:
		it.problem = probUnknownOp()
	}
	return it
}

// layer is one folder along a ToPath: an existing folder, a placeholder for
// one to make, or why no change can go through it.
type layer struct {
	id, problem string
	// names are the path's folder names below the scope down to this one.
	names []string
}

// newFolder is a folder a run makes.
type newFolder struct {
	name   string
	parent string // a folder id or another placeholder
	path   []string
	id     string // once made
}

type organizeRun struct {
	d      *DriveService
	s      *accountSession
	ctx    context.Context
	folder string
	// undo words the checks for undo: the place to check is where the
	// earlier run left an entry, and the name to keep free its old one.
	undo bool
	// names and where are the folders as read, kept current as the run
	// changes them; listed are the folders read.
	names  names
	where  map[string]drive.File
	listed map[string]bool
	layers map[string]layer
	made   map[string]*newFolder
	// madeOrder lists placeholders as the paths were walked: parents first.
	madeOrder []string
	created   []OrganizeFolder
	anc       map[string][]string
	done      int
	total     int
	halted    bool
	save      func() error
	savedAt   time.Time
}

func (d *DriveService) newRun(ctx context.Context, s *accountSession, folder string, total int) *organizeRun {
	return &organizeRun{
		d: d, s: s, ctx: ctx, folder: folder, total: total,
		names: names{}, where: map[string]drive.File{}, listed: map[string]bool{},
		layers: map[string]layer{}, made: map[string]*newFolder{}, anc: map[string][]string{},
		created: []OrganizeFolder{},
	}
}

func (r *organizeRun) stopped() bool { return r.ctx.Err() != nil }

// stop leaves an item undone because the run was stopped.
func (r *organizeRun) stop(it *orgItem) {
	r.halted = true
	r.finish(it, probStopped())
}

// finish records why an item stopped where it is ("" for done), and
// reports the run's progress.
func (r *organizeRun) finish(it *orgItem, problem string) {
	if it.finished {
		return
	}
	it.finished, it.problem = true, problem
	r.done++
	if app := application.Get(); app != nil {
		app.Event.Emit(EventOrganizeProgress, OrganizeProgress{Account: r.s.key, Done: r.done, Total: r.total})
	}
}

// checkpoint writes the record as it stands, at most once a second unless
// forced, so that a run cut short by a crash can still be undone.
func (r *organizeRun) checkpoint(force bool) {
	if r.save == nil || (!force && time.Since(r.savedAt) < time.Second) {
		return
	}
	r.savedAt = time.Now()
	if err := r.save(); err != nil {
		r.d.log.Warn("organize: could not write the record", "error", err)
	}
}

// listMany reads the folders not read yet, a few at a time, each in full.
func (r *organizeRun) listMany(folders []string) error {
	var todo []string
	for _, f := range folders {
		if !r.listed[f] && !isPlaceholder(f) && !slices.Contains(todo, f) {
			todo = append(todo, f)
		}
	}
	files := make([][]drive.File, len(todo))
	errs := make([]error, len(todo))
	sem := make(chan struct{}, listParallel)
	var wg sync.WaitGroup
	for i, folder := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			files[i], errs[i] = r.d.listAll(r.s, folder, maxListing)
		}()
	}
	wg.Wait()
	for i, folder := range todo {
		if errs[i] != nil {
			return errs[i]
		}
		r.listed[folder] = true
		if r.names[folder] == nil {
			r.names[folder] = map[string][]string{}
		}
		for _, f := range files[i] {
			f.ParentID = folder
			r.names.add(folder, f.Name, f.ID)
			r.where[f.ID] = f
		}
	}
	return nil
}

func (r *organizeRun) list(folder string) error { return r.listMany([]string{folder}) }

// read lists every folder the changes come from and go to, and walks each
// ToPath from the scope layer by layer: an existing folder of that name is
// read and the walk goes on; a missing one is to be made, and so is the
// rest of the path, which has nothing to read.
func (r *organizeRun) read(items []*orgItem) error {
	var folders []string
	walk := false
	for _, it := range items {
		if it.problem != "" {
			continue
		}
		folders = append(folders, it.parent)
		switch {
		case it.op != "":
		case it.path != nil:
			walk = true
		default:
			folders = append(folders, it.to)
		}
	}
	if walk {
		folders = append(folders, r.folder)
	}
	if err := r.listMany(folders); err != nil {
		return err
	}
	r.layers[""] = layer{id: r.folder, names: []string{}}
	for depth := 0; walk; depth++ {
		walk = false
		var next []string
		for _, it := range items {
			if it.problem != "" || it.op != "" || len(it.path) <= depth {
				continue
			}
			walk = true
			key := pathKey(it.path[:depth+1])
			if _, ok := r.layers[key]; ok {
				continue
			}
			l := r.resolve(r.layers[pathKey(it.path[:depth])], it.path[depth], key)
			r.layers[key] = l
			if l.problem == "" && !isPlaceholder(l.id) {
				next = append(next, l.id)
			}
		}
		if err := r.listMany(next); err != nil {
			return err
		}
	}
	return nil
}

// resolve finds the folder named seg in the layer above, or stands in for
// the one to make. A name held by a file or by several entries blocks every
// change through it: which one the user meant is not for this side to guess.
func (r *organizeRun) resolve(parent layer, seg, key string) layer {
	if parent.problem != "" {
		return parent
	}
	path := append(slices.Clone(parent.names), seg)
	if !isPlaceholder(parent.id) {
		holders := r.names.holders(parent.id, seg)
		switch {
		case len(holders) > 1:
			return layer{problem: fmtManyOnPath(seg)}
		case len(holders) == 1 && !r.where[holders[0]].IsFolder():
			return layer{problem: fmtFileOnPath(seg)}
		case len(holders) == 1:
			path[len(path)-1] = r.where[holders[0]].Name
			return layer{id: holders[0], names: path}
		}
	}
	id := "\x00" + key
	r.made[id] = &newFolder{name: seg, parent: parent.id, path: path}
	r.madeOrder = append(r.madeOrder, id)
	return layer{id: id, names: path}
}

// check reports the changes that no longer fit the folders and returns the
// rest; a change that is already true is done.
func (r *organizeRun) check(items []*orgItem) []*orgItem {
	stale, renamed := probStale(), probRenamed()
	if r.undo {
		stale, renamed = probMovedSince(), probMovedSince()
	}
	var live []*orgItem
	for _, it := range items {
		problem := it.problem
		f, ok := r.where[it.id]
		if ok {
			it.isFolder = f.IsFolder()
		}
		if problem == "" {
			switch {
			case !ok || f.ParentID != it.parent:
				problem = stale
			case f.Name != it.name:
				problem = renamed
			case it.op == opTrashIfEmpty && !f.IsFolder():
				problem = probNotFolder()
			case it.path != nil:
				l := r.layers[pathKey(it.path)]
				problem, it.to = l.problem, l.id
			}
		}
		switch {
		case problem != "":
			r.finish(it, problem)
		case it.op == "" && it.name == it.newName && it.parent == it.to:
			r.finish(it, "")
		default:
			live = append(live, it)
		}
	}
	return live
}

// nest refuses a folder that is moved, renamed or trashed together with
// anything else the plan changes below it — where it is now, or where it
// ends up — both sides. A folder trashed only if empty is exempt: emptying
// it is what the rest of the plan is for, and it goes last.
func (r *organizeRun) nest(items []*orgItem) []*orgItem {
	changed := map[string]*orgItem{}
	for _, it := range items {
		if it.isFolder && it.op != opTrashIfEmpty {
			changed[it.id] = it
		}
	}
	if len(changed) == 0 {
		return items
	}
	refuse := map[*orgItem]string{}
	for _, it := range items {
		chain := r.ancestors(it.parent)
		if it.op == "" {
			chain = append(slices.Clone(chain), r.ancestors(r.real(it.to))...)
		}
		for _, id := range chain {
			switch a := changed[id]; {
			case a == nil:
			case a == it:
				if refuse[it] == "" {
					refuse[it] = probIntoItself()
				}
			default:
				refuse[it], refuse[a] = probNested(), probNested()
			}
		}
	}
	var keep []*orgItem
	for _, it := range items {
		if why := refuse[it]; why != "" {
			r.finish(it, why)
			continue
		}
		keep = append(keep, it)
	}
	return keep
}

// real is the existing folder a placeholder will be made in, at any depth.
func (r *organizeRun) real(id string) string {
	for isPlaceholder(id) {
		id = r.made[id].parent
	}
	return id
}

// ancestors is id and the folders above it: as the listings of this run
// place them, and beyond those as the account's index does. Without an
// index only what the run read is known.
func (r *organizeRun) ancestors(id string) []string {
	if a, ok := r.anc[id]; ok {
		return a
	}
	var out []string
	for cur, hops := id, 0; cur != "" && hops < 64; hops++ {
		if a, ok := r.anc[cur]; ok {
			out = append(out, a...)
			break
		}
		out = append(out, cur)
		if f, ok := r.where[cur]; ok {
			cur = f.ParentID
			continue
		}
		if idx := r.s.index(); idx != nil {
			crumbs, err := idx.store.Path(cur)
			if err == nil && len(crumbs) > 0 && crumbs[len(crumbs)-1].ID == cur {
				for i := len(crumbs) - 2; i >= 0; i-- {
					out = append(out, crumbs[i].ID)
				}
			}
		}
		break
	}
	r.anc[id] = out
	return out
}

// usedFolders are the placeholders the items still need, parents first.
func (r *organizeRun) usedFolders(items []*orgItem) []string {
	used := map[string]bool{}
	for _, it := range items {
		for id := it.to; it.op == "" && isPlaceholder(id) && !used[id]; id = r.made[id].parent {
			used[id] = true
		}
	}
	var out []string
	for _, id := range r.madeOrder {
		if used[id] {
			out = append(out, id)
		}
	}
	return out
}

// settle drops the items whose end would share a name with something else
// in the folder they land in, until none does. Everything counts: entries
// that stay, entries on their way to the trash (they hold their names until
// the end), folders to be made and the other changes. Both sides of a clash
// go, and a folder to make that clashes takes every change through it along.
func (r *organizeRun) settle(items []*orgItem) []*orgItem {
	clash := probClash()
	if r.undo {
		clash = probPlaceTaken()
	}
	for {
		end := r.names.clone()
		used := r.usedFolders(items)
		for _, id := range used {
			f := r.made[id]
			end.add(f.parent, f.name, id)
		}
		for _, it := range items {
			if it.op == "" {
				end.remove(it.parent, it.name, it.id)
				end.add(it.to, it.newName, it.id)
			}
		}
		blocked := map[string]bool{}
		for _, id := range used {
			f := r.made[id]
			blocked[id] = blocked[f.parent] || len(end.holders(f.parent, f.name)) > 1
		}
		var keep []*orgItem
		for _, it := range items {
			if it.op == "" && (blocked[it.to] || len(end.holders(it.to, it.newName)) > 1) {
				r.finish(it, clash)
				continue
			}
			keep = append(keep, it)
		}
		if len(keep) == len(items) {
			return keep
		}
		items = keep
	}
}

// makeFolders makes the folders the items still need, parents first, and
// reads what each holds: one another client made a moment ago is joined,
// not doubled. Items through a folder that could not be made stay where
// they are.
func (r *organizeRun) makeFolders(items []*orgItem) []*orgItem {
	failed := map[string]string{}
	for _, id := range r.usedFolders(items) {
		nf := r.made[id]
		parent := nf.parent
		if isPlaceholder(parent) {
			if failed[parent] != "" {
				failed[id] = failed[parent]
				continue
			}
			parent = r.made[parent].id
		}
		if r.stopped() {
			r.halted = true
			failed[id] = probStopped()
			continue
		}
		f, err := callIn(r.d, r.s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
			return client.EnsureFolder(ctx, parent, nf.name)
		})
		if err != nil {
			failed[id] = fmtMakeFailed(nf.name, cause(err))
			continue
		}
		f.ParentID = parent
		nf.id = f.ID
		if isPlaceholder(nf.parent) {
			nf.path = append(slices.Clone(r.made[nf.parent].path), f.Name)
		} else {
			nf.path[len(nf.path)-1] = f.Name
		}
		r.created = append(r.created, OrganizeFolder{ID: f.ID, Name: f.Name, ParentID: parent, Path: slices.Clone(nf.path)})
		if idx := r.s.index(); idx != nil {
			r.d.indexErr(idx.store.Upsert([]index.Row{toRow(*f)}))
		}
		r.names.add(parent, f.Name, f.ID)
		r.where[f.ID] = *f
		if err := r.list(f.ID); err != nil {
			failed[id] = fmtReadFailed(f.Name, cause(err))
		}
		r.checkpoint(false)
	}
	var keep []*orgItem
	for _, it := range items {
		if it.op == "" && isPlaceholder(it.to) {
			if why := failed[it.to]; why != "" {
				r.finish(it, why)
				continue
			}
			it.to = r.made[it.to].id
		}
		keep = append(keep, it)
	}
	r.checkpoint(true)
	return keep
}

// execute runs the moves and renames in rounds: first the moves whose name
// is free where they go (batched per folder), then the renames whose new
// name is free where the entry is — in its final folder, or before a move
// whose own name is taken there. A round in which nothing can go means the
// remaining items hold each other's names (a swap); those stay as they are.
func (r *organizeRun) execute(items []*orgItem) {
	pending := items
	for len(pending) > 0 {
		if r.stopped() {
			for _, it := range pending {
				r.stop(it)
			}
			return
		}
		moved := r.moveRound(pending)
		renamed := r.renameRound(pending)
		var next []*orgItem
		for _, it := range pending {
			switch {
			case it.finished:
			case it.parent == it.to && it.name == it.newName:
				r.finish(it, "")
			default:
				next = append(next, it)
			}
		}
		if !moved && !renamed {
			for _, it := range next {
				r.finish(it, probSwap())
			}
			return
		}
		pending = next
	}
	r.checkpoint(true)
}

func (r *organizeRun) moveRound(pending []*orgItem) bool {
	batches := map[string][]*orgItem{}
	var order []string
	for _, it := range pending {
		if it.finished || it.parent == it.to || !r.names.free(it.to, it.name, it.id) {
			continue
		}
		if batches[it.to] == nil {
			order = append(order, it.to)
		}
		batches[it.to] = append(batches[it.to], it)
		// Held from now, so a second entry of the same name waits a round.
		r.names.add(it.to, it.name, it.id)
	}
	var sent []sentBatch
	for _, to := range order {
		batch := batches[to]
		for start := 0; start < len(batch); start += moveBatch {
			if r.stopped() {
				for _, it := range batch[start:] {
					r.names.remove(to, it.name, it.id)
				}
				break
			}
			post := func(ctx context.Context, client *drive.Client, ids []string) (string, error) {
				return client.BatchMove(ctx, ids, to)
			}
			sent = append(sent, r.send(batch[start:min(start+moveBatch, len(batch))], to, post, func(it *orgItem, err error) {
				r.names.remove(to, it.name, it.id)
				r.finish(it, cause(err))
			})...)
		}
	}
	got, _ := r.await(sent)
	for _, b := range sent {
		for _, it := range b.items {
			r.moved(it, b.to, got)
		}
	}
	r.checkpoint(false)
	return len(order) > 0
}

// moved takes in where a moved entry really is once its task is over.
func (r *organizeRun) moved(it *orgItem, to string, got map[string]drive.File) {
	f, ok := got[it.id]
	if !ok || f.ParentID != to {
		r.names.remove(to, it.name, it.id)
		r.finish(it, probUnconfirmed())
		return
	}
	r.names.remove(it.parent, it.name, it.id)
	it.parent = to
	r.d.patchIndex(r.s, []string{it.id}, index.Patch{ParentID: &to})
	if f.Name != "" && f.Name != it.name {
		// Someone took the name in the meantime and the server numbered
		// this one instead; it goes no further.
		r.renamedTo(it, f.Name)
		r.finish(it, fmtNumbered(f.Name))
	}
}

// renamedTo takes in a name the server gave an entry.
func (r *organizeRun) renamedTo(it *orgItem, name string) {
	r.names.remove(it.parent, it.name, it.id)
	r.names.add(it.parent, name, it.id)
	it.name = name
	r.d.patchIndex(r.s, []string{it.id}, index.Patch{Name: &name})
}

func (r *organizeRun) renameRound(pending []*orgItem) bool {
	progressed := false
	for _, it := range pending {
		if it.finished || it.name == it.newName {
			continue
		}
		// Moves first while the name is free where the entry goes.
		if it.parent != it.to && r.names.free(it.to, it.name, it.id) {
			continue
		}
		if !r.names.free(it.parent, it.newName, it.id) {
			continue
		}
		if r.stopped() {
			return progressed
		}
		progressed = true
		r.rename(it)
		r.checkpoint(false)
	}
	return progressed
}

func (r *organizeRun) rename(it *orgItem) {
	f, err := callIn(r.d, r.s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
		return client.Rename(ctx, it.id, it.newName)
	})
	if err != nil {
		r.finish(it, cause(err))
		return
	}
	got := f.Name
	if got == "" {
		got = it.newName
	}
	r.renamedTo(it, got)
	if got != it.newName {
		r.finish(it, fmtNumbered(got))
	}
}

// trashAll puts the trash items in the recycle bin, a batch at a time.
func (r *organizeRun) trashAll(items []*orgItem) {
	var sent []sentBatch
	for start := 0; start < len(items); start += moveBatch {
		if r.stopped() {
			for _, it := range items[start:] {
				r.stop(it)
			}
			break
		}
		sent = append(sent, r.send(items[start:min(start+moveBatch, len(items))], "", batchTrash, func(it *orgItem, err error) {
			r.finish(it, cause(err))
		})...)
	}
	got, answered := r.await(sent)
	for _, b := range sent {
		for _, it := range b.items {
			if r.trashed(it, got, answered) {
				r.finish(it, "")
			}
		}
	}
	r.checkpoint(true)
}

func batchTrash(ctx context.Context, client *drive.Client, ids []string) (string, error) {
	return client.BatchTrash(ctx, ids)
}

// trashed takes in whether an entry reached the bin: a read of live entries
// leaves it out once it has.
func (r *organizeRun) trashed(it *orgItem, got map[string]drive.File, answered map[string]bool) bool {
	if f, ok := got[it.id]; !answered[it.id] || (ok && !f.Trashed) {
		r.finish(it, probUnconfirmed())
		return false
	}
	it.trashed = true
	r.names.remove(it.parent, it.name, it.id)
	trashed := true
	r.d.patchIndex(r.s, []string{it.id}, index.Patch{Trashed: &trashed})
	return true
}

// trashEmptied puts each folder in the trash that holds nothing live, read
// right before. Rounds repeat while some go: a folder emptied by trashing
// the one inside it goes in the next round, whatever the order asked.
// The rest are left with notEmpty.
func (r *organizeRun) trashEmptied(items []*orgItem, notEmpty string) {
	pending := items
	for len(pending) > 0 {
		if r.stopped() {
			for _, it := range pending {
				r.stop(it)
			}
			return
		}
		var empty, rest []*orgItem
		for _, it := range pending {
			holds, err := r.d.holdsLive(r.s, it.id)
			switch {
			case err != nil:
				r.finish(it, cause(err))
			case holds:
				rest = append(rest, it)
			default:
				empty = append(empty, it)
			}
		}
		if len(empty) == 0 {
			for _, it := range rest {
				r.finish(it, notEmpty)
			}
			return
		}
		var sent []sentBatch
		for start := 0; start < len(empty); start += moveBatch {
			sent = append(sent, r.send(empty[start:min(start+moveBatch, len(empty))], "", batchTrash, func(it *orgItem, err error) {
				r.finish(it, cause(err))
			})...)
		}
		got, answered := r.await(sent)
		for _, b := range sent {
			for _, it := range b.items {
				if r.trashed(it, got, answered) {
					r.finish(it, "")
				}
			}
		}
		r.checkpoint(true)
		pending = rest
	}
}

/* ---------------- batches ---------------- */

// sentBatch is one batch request the server took, and the task carrying
// it out.
type sentBatch struct {
	items []*orgItem
	to    string
	task  string
}

type postBatch func(ctx context.Context, client *drive.Client, ids []string) (string, error)

func idsOf(items []*orgItem) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	return ids
}

// send posts one batch (sendSplit) and keeps the items with their task.
func (r *organizeRun) send(chunk []*orgItem, to string, post postBatch, refused func(*orgItem, error)) []sentBatch {
	byID := make(map[string]*orgItem, len(chunk))
	for _, it := range chunk {
		byID[it.id] = it
	}
	var out []sentBatch
	for _, t := range r.d.sendSplit(r.s, idsOf(chunk), post, func(id string, err error) { refused(byID[id], err) }) {
		items := make([]*orgItem, len(t.ids))
		for i, id := range t.ids {
			items[i] = byID[id]
		}
		out = append(out, sentBatch{items: items, to: to, task: t.task})
	}
	return out
}

// await waits for the tasks of the batches sent to finish, then reads their
// entries back: got holds the live ones, answered the ids the read covered.
// A task that fails or outlasts the wait is not trusted either way; the
// read says what happened.
func (r *organizeRun) await(sent []sentBatch) (got map[string]drive.File, answered map[string]bool) {
	var ids, tasks []string
	for _, b := range sent {
		ids = append(ids, idsOf(b.items)...)
		tasks = append(tasks, b.task)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	r.d.awaitTasks(r.s, tasks, len(ids))
	return r.d.readBack(r.s, ids)
}

// sentTask is one batch request the server took, and the task carrying it
// out ("" for a request that has none).
type sentTask struct {
	ids  []string
	task string
}

// sendSplit posts one batch for an account. A refused batch is sent again
// entry by entry, so one entry's refusal does not cost the others; refused
// reports each one the server would not take.
func (d *DriveService) sendSplit(s *accountSession, ids []string, post postBatch, refused func(id string, err error)) []sentTask {
	task, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (string, error) {
		return post(ctx, client, ids)
	})
	if err == nil {
		return []sentTask{{ids: ids, task: task}}
	}
	if len(ids) == 1 {
		refused(ids[0], err)
		return nil
	}
	var out []sentTask
	for _, id := range ids {
		out = append(out, d.sendSplit(s, []string{id}, post, refused)...)
	}
	return out
}

// awaitTasks waits for an account's server tasks carrying n entries to
// finish: taskWait plus a second per entry at most, capped at taskWaitCap.
// A task that cannot be read or fails ends its wait; the caller reads the
// entries back either way.
func (d *DriveService) awaitTasks(s *accountSession, tasks []string, n int) {
	deadline := time.Now().Add(min(taskWait+time.Duration(n)*time.Second, taskWaitCap))
	for _, task := range tasks {
		for task != "" && time.Now().Before(deadline) {
			t, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.Task, error) {
				return client.GetTask(ctx, task)
			})
			if err != nil || t.Done() {
				break
			}
			time.Sleep(taskPoll)
		}
	}
}

// readBack reads an account's entries by id, a batchGet at a time: got
// holds what the server returned, answered the ids a read covered. The
// server leaves out trashed entries and destroyed ones alike, so absence
// alone cannot tell the two apart.
func (d *DriveService) readBack(s *accountSession, ids []string) (got map[string]drive.File, answered map[string]bool) {
	got, answered = map[string]drive.File{}, map[string]bool{}
	for start := 0; start < len(ids); start += drive.MaxListLimit {
		chunk := ids[start:min(start+drive.MaxListLimit, len(ids))]
		files, err := callIn(d, s, func(ctx context.Context, client *drive.Client) ([]drive.File, error) {
			return client.BatchGetFiles(ctx, chunk, drive.GetFileParams{})
		})
		if err != nil {
			d.log.Warn("drive: could not read entries back", "error", err)
			continue
		}
		for _, id := range chunk {
			answered[id] = true
		}
		for _, f := range files {
			got[f.ID] = f
		}
	}
	return got, answered
}

// holdsLive says whether a folder holds any live entry directly, read from
// the server right now: one entry is enough to know.
func (d *DriveService) holdsLive(s *accountSession, folderID string) (bool, error) {
	list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.FileList, error) {
		return client.ListFiles(ctx, drive.ListFilesParams{ParentID: folderID, Filters: drive.OnlyLive(), Limit: 1})
	})
	if err != nil {
		return false, err
	}
	return len(list.Files) > 0, nil
}

// invalidName refuses what no folder can hold. The page holds names it
// proposes to a stricter rule; this side also takes back the names entries
// had before (undo), whatever the server once accepted.
func invalidName(name string) bool {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return true
	}
	return strings.ContainsFunc(name, func(c rune) bool { return c == '/' || c < 0x20 || c == 0x7f })
}

// cause is the server's own words for a refusal, without the request line.
func cause(err error) string {
	var api *drive.APIError
	if errors.As(err, &api) {
		if api.Description != "" {
			return api.Description
		}
		if api.Name != "" {
			return api.Name
		}
	}
	return err.Error()
}
