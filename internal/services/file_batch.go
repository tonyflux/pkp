package services

import (
	"context"
	"errors"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

const EventFileBatchProgress = "drive:batch"

// BatchFileItem is the source snapshot the user confirmed, not a fresh index row.
type BatchFileItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
}

type FileBatchRequest struct {
	ID       string          `json:"id"`
	Account  string          `json:"account"`
	Action   string          `json:"action"`
	Items    []BatchFileItem `json:"items"`
	ParentID string          `json:"parentId"`
}

type FileBatchResult struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error"`
	Entry *Entry `json:"entry,omitempty"`
}

type FileBatchReport struct {
	Items   []FileBatchResult `json:"items"`
	Stopped bool              `json:"stopped"`
}

type FileBatchProgress struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Phase   string `json:"phase"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
}

// TrashSnapshot captures only entries already in the recycle bin, so a queued
// empty-trash task never grows to include entries trashed after confirmation.
func (d *DriveService) TrashSnapshot(account string) ([]Entry, error) {
	if account == "" {
		return nil, i18n.New("文件任务缺少账号或来源快照")
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	files, err := d.binAll(s)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(files))
	for i, f := range files {
		entries[i] = toEntry(f)
	}
	return entries, nil
}

// RunFileBatch serializes writes for the explicitly captured account. Only
// known refusals are safe to retry; accepted or ambiguous writes are read back.
func (d *DriveService) RunFileBatch(req FileBatchRequest) (FileBatchReport, error) {
	if req.ID == "" || req.Account == "" || len(req.Items) == 0 {
		return FileBatchReport{}, i18n.New("文件任务缺少账号或来源快照")
	}
	switch req.Action {
	case "trash", "delete", "empty", "restore", "move", "copy", "star", "unstar":
	default:
		return FileBatchReport{}, errors.New(probUnknownOp())
	}
	if req.Action != "empty" && len(req.Items) > maxRemove {
		return FileBatchReport{}, errRemoveTooMany
	}
	ids := make([]string, 0, len(req.Items))
	positions := make(map[string]int, len(req.Items))
	report := FileBatchReport{Items: make([]FileBatchResult, len(req.Items))}
	for i, it := range req.Items {
		if _, exists := positions[it.ID]; exists || it.ID == "" || it.Name == "" {
			return FileBatchReport{}, i18n.New("文件任务缺少账号或来源快照")
		}
		positions[it.ID] = i
		ids = append(ids, it.ID)
		report.Items[i] = FileBatchResult{ID: it.ID, Name: it.Name, State: "unprocessed", Error: probStopped()}
	}
	r, end, err := d.beginRemove(req.Account, req.Action)
	if err != nil {
		return FileBatchReport{}, err
	}
	defer end()
	if req.Action == "empty" {
		r.phase = phaseDelete
	}
	b := &fileBatchRun{r: r, req: req, report: report, positions: positions, phase: "submitting"}
	r.batch, r.total = b, len(ids)
	b.progress("submitting")
	switch req.Action {
	case "trash":
		r.trash(ids)
	case "delete", "empty":
		r.destroy(ids)
	case "restore":
		r.restore(ids)
	default:
		b.batches(ids, func(ctx context.Context, client *drive.Client, ids []string) (string, error) {
			switch req.Action {
			case "move":
				return client.BatchMove(ctx, ids, req.ParentID)
			case "copy":
				return client.BatchCopy(ctx, ids, req.ParentID)
			case "star":
				return "", client.Star(ctx, ids)
			default:
				return "", client.Unstar(ctx, ids)
			}
		}, b.settle)
	}
	b.report.Stopped = r.report.Stopped
	b.progress("verifying")
	return b.report, nil
}

type fileBatchRun struct {
	r          *removeRun
	req        FileBatchRequest
	report     FileBatchReport
	positions  map[string]int
	phase      string
	taskStates map[string]string // most recent batch: source id -> terminal task phase
}

func (b *fileBatchRun) result(id, state, problem string, entry *Entry) {
	i := b.positions[id]
	b.report.Items[i].State, b.report.Items[i].Error, b.report.Items[i].Entry = state, problem, entry
}

func (b *fileBatchRun) progress(phase string) {
	b.phase = phase
	done := 0
	for _, it := range b.report.Items {
		if it.State != "unprocessed" {
			done++
		}
	}
	if app := application.Get(); app != nil {
		app.Event.Emit(EventFileBatchProgress, FileBatchProgress{ID: b.req.ID, Account: b.r.s.key, Phase: phase, Done: done, Total: len(b.report.Items)})
	}
}

// knownWriteRefusal excludes timeouts: a gateway may time out after accepting
// the mutation. Transport errors and 5xx must never trigger a split/replay.
func knownWriteRefusal(err error) bool {
	var api *drive.APIError
	return errors.As(err, &api) && api.Status >= 400 && api.Status < 500 && api.Status != http.StatusRequestTimeout
}

// destination reads the live parent chain, rather than trusting a stale index
// when deciding whether moving/copying a folder would put it inside itself.
func (b *fileBatchRun) destination() (map[string]bool, error) {
	ancestors := map[string]bool{}
	for id := b.req.ParentID; id != ""; {
		if ancestors[id] || len(ancestors) >= maxEmptyWalk {
			return nil, errors.New(probBadPath())
		}
		ancestors[id] = true
		files, err := callIn(b.r.d, b.r.s, func(ctx context.Context, client *drive.Client) ([]drive.File, error) {
			return client.BatchGetFiles(ctx, []string{id}, drive.GetFileParams{})
		})
		if err != nil {
			return nil, err
		}
		if len(files) != 1 || files[0].ID != id || files[0].Trashed || !files[0].IsFolder() {
			return nil, errors.New(probNotFolder())
		}
		id = files[0].ParentID
	}
	return ancestors, nil
}

// prepare runs immediately before each batch, and again before a safe split.
// Every refusal here is known not to have written anything.
func (b *fileBatchRun) prepare(ids []string) []string {
	r := b.r
	live, binned, err := r.locate(ids)
	var ancestors map[string]bool
	if err == nil && (b.req.Action == "move" || b.req.Action == "copy") {
		ancestors, err = b.destination()
	}
	if err != nil {
		for _, id := range ids {
			b.result(id, "failed", cause(err), nil)
		}
		return nil
	}
	var send []string
	for _, id := range ids {
		it := b.req.Items[b.positions[id]]
		r.rows[id] = index.Row{ID: id, Name: it.Name, ParentID: it.ParentID}
		f, exists := r.files[id]
		_, isLive := live[id]
		_, inBin := binned[id]
		problem := ""
		switch {
		case !exists || (!isLive && !inBin):
			problem = whyGone()
		case r.changed(id) != "":
			problem = r.changed(id)
		case f.IsFolder() && f.FolderType != "" && f.FolderType != "NORMAL" && (b.req.Action == "trash" || b.req.Action == "delete" || b.req.Action == "empty" || b.req.Action == "move"):
			problem = whySpecial()
		case b.req.Action == "empty" && !inBin:
			problem = probStale()
		case b.req.Action != "restore" && b.req.Action != "delete" && b.req.Action != "empty" && b.req.Action != "trash" && !isLive:
			problem = probStale()
		case ancestors[id]:
			problem = probIntoItself()
		case (b.req.Action == "move" || b.req.Action == "copy") && f.ParentID == b.req.ParentID:
			problem = i18n.T("已经在目标文件夹，未处理")
		}
		if problem != "" {
			b.result(id, "failed", problem, nil)
			continue
		}
		if b.req.Action == "trash" && inBin {
			r.trashedOK(id)
		} else if b.req.Action == "restore" && isLive {
			r.restoredOK(id, f)
		} else if (b.req.Action == "star" || b.req.Action == "unstar") && f.Starred == (b.req.Action == "star") {
			entry := toEntry(f)
			b.result(id, "done", "", &entry)
			r.d.patchIndex(r.s, []string{id}, index.Patch{Starred: &f.Starred})
		} else {
			send = append(send, id)
		}
	}
	return send
}

func (b *fileBatchRun) send(ids []string, post postBatch) []sentTask {
	if b.r.ctx.Err() != nil {
		b.r.report.Stopped = true
		return nil
	}
	ids = b.prepare(ids)
	if len(ids) == 0 {
		return nil
	}
	if b.r.ctx.Err() != nil {
		b.r.report.Stopped = true
		return nil
	}
	b.progress("submitting")
	task, err := callIn(b.r.d, b.r.s, func(ctx context.Context, client *drive.Client) (string, error) {
		return post(ctx, client, ids)
	})
	if err == nil {
		return []sentTask{{ids: ids, task: task}}
	}
	if !knownWriteRefusal(err) {
		// It may still complete on the server. Verification can upgrade an
		// unknown receipt to done, but no second write is sent.
		return []sentTask{{ids: ids}}
	}
	if len(ids) == 1 {
		b.result(ids[0], "failed", cause(err), nil)
		return nil
	}
	var sent []sentTask
	for _, id := range ids {
		sent = append(sent, b.send([]string{id}, post)...)
	}
	return sent
}

func (b *fileBatchRun) batches(ids []string, post postBatch, settle func([]string)) {
	for start := 0; start < len(ids); start += moveBatch {
		if b.r.ctx.Err() != nil {
			b.r.report.Stopped = true
			break
		}
		sent := b.send(ids[start:min(start+moveBatch, len(ids))], post)
		var accepted, tasks []string
		for _, batch := range sent {
			accepted = append(accepted, batch.ids...)
			tasks = append(tasks, batch.task)
		}
		if len(accepted) > 0 {
			b.progress("processing")
			states := b.r.d.awaitTasks(b.r.s, tasks, len(accepted))
			b.taskStates = map[string]string{}
			for _, batch := range sent {
				for _, id := range batch.ids {
					b.taskStates[id] = states[batch.task]
				}
			}
			b.progress("verifying")
			settle(accepted)
		}
		b.progress(b.phase)
	}
}

func (b *fileBatchRun) settle(ids []string) {
	r := b.r
	if b.req.Action == "copy" {
		for _, id := range ids {
			if b.taskStates[id] == drive.PhaseComplete {
				b.result(id, "done", "", nil)
			} else {
				b.result(id, "unknown", i18n.T("云盘尚未确认结果，请检查后再操作"), nil)
			}
		}
		requestSync(r.s)
		return
	}
	got, answered := r.d.readBack(r.s, ids)
	for _, id := range ids {
		f, ok := got[id]
		if !answered[id] || !ok || f.Trashed || (b.req.Action == "move" && f.ParentID != b.req.ParentID) || (b.req.Action != "move" && f.Starred != (b.req.Action == "star")) {
			b.result(id, "unknown", i18n.T("云盘尚未确认结果，请检查后再操作"), nil)
			continue
		}
		entry := toEntry(f)
		b.result(id, "done", "", &entry)
		if b.req.Action == "move" {
			r.d.patchIndex(r.s, []string{id}, index.Patch{ParentID: &f.ParentID, Name: &f.Name})
		} else {
			r.d.patchIndex(r.s, []string{id}, index.Patch{Starred: &f.Starred})
		}
	}
}
