package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strconv"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
	"github.com/tonyflux/pkp/internal/torrent"
	"github.com/tonyflux/pkp/internal/upload"
)

// AssistantResource deliberately contains metadata only, never a transfer's
// source URL, credentials, error detail, hash or local destination.
type AssistantResource struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Folder       bool    `json:"folder"`
	ParentID     string  `json:"parentId"`
	Size         int64   `json:"size"`
	ModifiedTime string  `json:"modifiedTime"`
	State        string  `json:"state"`
	Progress     float64 `json:"progress"`
}

type AssistantResourcePage struct {
	Items         []AssistantResource `json:"items"`
	NextPageToken string              `json:"nextPageToken"`
}

type AssistantOperationRequest struct {
	Action   string   `json:"action"`
	IDs      []string `json:"ids"`
	ParentID string   `json:"parentId"`
	Name     string   `json:"name"`
}

type AssistantOperationItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

type AssistantOperationResult struct {
	Results []AssistantOperationItem `json:"results"`
}

var (
	errAssistantRequest      = i18n.New("助手操作参数无效，未处理")
	errAssistantAction       = i18n.New("助手不支持此操作，未处理")
	errAssistantTarget       = i18n.New("目标已失效或不属于此类操作，未处理")
	errAssistantState        = i18n.New("目标状态已改变，不适用此操作，未处理")
	errAssistantRecursive    = i18n.New("不能把文件夹复制到自身或其子文件夹")
	errAssistantRetryHash    = i18n.New("此云端任务没有可用的种子哈希，不能重试")
	errAssistantFailure      = i18n.New("操作未能完成，敏感错误详情已隐藏")
	errAssistantPageTooLarge = i18n.New("云盘返回的结果页过大，无法安全分页，未返回不完整结果")
	assistantPrivateMetadata = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|magnet:\?)[^\s]+|(?:[a-z]:[\\/]|~[\\/]|/)[^\s]+|authorization\s*[:=]\s*[^\r\n]+|(?:password|pass_code|access_token|refresh_token|access_key_secret)\s*[:=]\s*[^\s]+|[a-f0-9]{40}`)
)

func assistantName(name string) string {
	return assistantPrivateMetadata.ReplaceAllString(name, i18n.T("已隐藏敏感信息"))
}

// Server and filesystem errors may echo signed URLs, credentials or paths.
// Keep authentication semantics, but never send their free-form details to AI.
func assistantFailure(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{drive.ErrNoCredentials, drive.ErrPasswordRequired, errNoSuchAccount, errAssistantRequest, errAssistantAction, errAssistantTarget, errAssistantState, errAssistantRecursive, errAssistantRetryHash, errAssistantFailure, errRemoving} {
		if errors.Is(err, known) {
			return known
		}
	}
	var api *drive.APIError
	if errors.As(err, &api) {
		return i18n.Errorf("云盘拒绝了操作（状态码 %d），敏感错误详情已隐藏", api.Status)
	}
	return errAssistantFailure
}

func assistantID(id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

const (
	assistantResourceLimit = 40
	assistantCursorLimit   = 256 * 1024
)

// The continuation is bound to account and catalog. Local sequence numbers,
// unlike slice offsets, do not skip records when earlier jobs are removed.
type assistantResourceCursor struct {
	Account  string              `json:"account"`
	Kind     string              `json:"kind"`
	Stage    string              `json:"stage"`
	After    int                 `json:"after"`
	Token    string              `json:"token"`
	Snapshot []AssistantResource `json:"snapshot,omitempty"`
}

func (c assistantResourceCursor) encode() string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}

func assistantCursor(account, kind, token string) (assistantResourceCursor, error) {
	c := assistantResourceCursor{Account: account, Kind: kind, Stage: "cloud"}
	if kind == "transfers" {
		c.Stage = "upload"
	}
	if token == "" {
		return c, nil
	}
	if len(token) > assistantCursorLimit {
		return c, errAssistantRequest
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || json.Unmarshal(data, &c) != nil || c.Account != account || c.Kind != kind || c.After < 0 ||
		(c.Stage != "cloud" && (kind != "transfers" || (c.Stage != "upload" && c.Stage != "download"))) ||
		(c.Stage != "cloud" && (c.Token != "" || len(c.Snapshot) != 0)) ||
		(c.Stage == "cloud" && c.After != 0) {
		return c, errAssistantRequest
	}
	return c, nil
}

// Oversized API pages travel as redacted immutable metadata inside the bounded
// opaque cursor, never as offsets into a remote listing changed by a restore.
func assistantFinishPage(out AssistantResourcePage, c assistantResourceCursor) (AssistantResourcePage, error) {
	if len(out.Items) > assistantResourceLimit {
		c.Snapshot = out.Items[assistantResourceLimit:]
		out.Items = out.Items[:assistantResourceLimit]
	}
	if len(c.Snapshot) != 0 || c.Token != "" {
		token := c.encode()
		if len(token) > assistantCursorLimit {
			return AssistantResourcePage{Items: []AssistantResource{}}, errAssistantPageTooLarge
		}
		out.NextPageToken = token
	}
	return out, nil
}

func assistantSequence(id string) int {
	if len(id) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(id[1:])
	return n
}

// AssistantResources reads only the named account. Every local record is
// reachable before cloud pages, without repeating locals on each cloud page.
func (d *DriveService) AssistantResources(account, kind, pageToken string) (AssistantResourcePage, error) {
	out := AssistantResourcePage{Items: []AssistantResource{}}
	if (kind != "trash" && kind != "shares" && kind != "transfers") || !assistantID(account) {
		return out, errAssistantRequest
	}
	cursor, err := assistantCursor(account, kind, pageToken)
	if err != nil {
		return out, err
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return out, assistantFailure(err)
	}
	if len(cursor.Snapshot) != 0 {
		end := min(len(cursor.Snapshot), assistantResourceLimit)
		out.Items = cursor.Snapshot[:end]
		cursor.Snapshot = cursor.Snapshot[end:]
		return assistantFinishPage(out, cursor)
	}
	if kind == "transfers" {
		if cursor.Stage == "upload" {
			if q := s.queue(); q != nil {
				for _, task := range q.List() {
					if assistantSequence(task.ID) <= cursor.After {
						continue
					}
					out.Items = append(out.Items, AssistantResource{ID: task.ID, Name: assistantName(task.Name), Kind: "upload", ParentID: task.ParentID, Size: task.Size, ModifiedTime: task.UpdatedAt, State: assistantUploadState(task.State), Progress: assistantProgress(task.Done, task.Size)})
					cursor.After = assistantSequence(task.ID)
					if len(out.Items) == assistantResourceLimit {
						out.NextPageToken = cursor.encode()
						return out, nil
					}
				}
			}
			cursor.Stage, cursor.After = "download", 0
		}
		if cursor.Stage == "download" {
			if q := s.downloadQueue(); q != nil {
				for _, task := range q.List() {
					if assistantSequence(task.ID) <= cursor.After {
						continue
					}
					out.Items = append(out.Items, AssistantResource{ID: task.ID, Name: assistantName(task.Name), Kind: "download", Size: task.Size, ModifiedTime: task.UpdatedAt, State: assistantDownloadState(task.State), Progress: assistantProgress(task.Done, task.Size)})
					cursor.After = assistantSequence(task.ID)
					if len(out.Items) == assistantResourceLimit {
						out.NextPageToken = cursor.encode()
						return out, nil
					}
				}
			}
			cursor.Stage, cursor.After = "cloud", 0
		}
		if len(out.Items) != 0 {
			out.NextPageToken = cursor.encode()
			return out, nil
		}
	}
	switch kind {
	case "trash":
		list, err := callIn(d, s, func(ctx context.Context, c *drive.Client) (*drive.FileList, error) {
			return c.ListFiles(ctx, drive.ListFilesParams{ParentID: drive.WholeTree, Filters: drive.NewFilters().Eq("trashed", true), Limit: assistantResourceLimit, PageToken: cursor.Token})
		})
		if err != nil {
			return out, assistantFailure(err)
		}
		for _, f := range list.Files {
			kind := "trash_file"
			if f.IsFolder() {
				kind = "trash_folder"
			}
			out.Items = append(out.Items, AssistantResource{ID: f.ID, Name: assistantName(f.Name), Kind: kind, Folder: f.IsFolder(), ParentID: f.ParentID, Size: int64(f.Size), ModifiedTime: f.ModifiedTime})
		}
		cursor.Token = list.NextPageToken
	case "shares":
		list, err := callIn(d, s, func(ctx context.Context, c *drive.Client) (*drive.ShareList, error) {
			return c.ListShares(ctx, drive.ListSharesParams{Limit: assistantResourceLimit, PageToken: cursor.Token})
		})
		if err != nil {
			return out, assistantFailure(err)
		}
		for _, share := range list.Data {
			row := toShareSummary(share)
			out.Items = append(out.Items, AssistantResource{ID: row.ID, Name: assistantName(row.Title), Kind: "share", Folder: row.Folder, Size: row.FileSize, ModifiedTime: row.CreatedAt, State: row.Status})
		}
		cursor.Token = list.NextPageToken
	case "transfers":
		list, err := callIn(d, s, func(ctx context.Context, c *drive.Client) (*drive.TaskList, error) {
			return c.ListTasks(ctx, drive.ListTasksParams{Type: drive.TaskTypeOffline, Limit: assistantResourceLimit, PageToken: cursor.Token})
		})
		if err != nil {
			return out, assistantFailure(err)
		}
		for _, task := range list.Tasks {
			parent := ""
			if task.ReferenceResource != nil {
				parent = task.ReferenceResource.ParentID
			}
			out.Items = append(out.Items, AssistantResource{ID: task.ID, Name: assistantName(task.Name), Kind: "task", ParentID: parent, Size: int64(task.FileSize), ModifiedTime: task.UpdatedTime, State: assistantTaskState(task.Phase), Progress: task.Progress})
		}
		cursor.Token = list.NextPageToken
	}
	return assistantFinishPage(out, cursor)
}

func assistantProgress(done, size int64) float64 {
	if size <= 0 {
		return 0
	}
	return min(100, max(0, float64(done)/float64(size)*100))
}

func assistantTaskState(state string) string {
	switch state {
	case drive.PhasePending, drive.PhaseRunning, drive.PhasePaused, drive.PhaseComplete, drive.PhaseError:
		return state
	}
	return ""
}

func assistantUploadState(state string) string {
	switch state {
	case upload.StateQueued, upload.StateUploading, upload.StateDone, upload.StateFailed, upload.StateCancelled, upload.StateInterrupted:
		return state
	}
	return ""
}

func assistantDownloadState(state string) string {
	switch state {
	case download.StateQueued, download.StateDownloading, download.StateDone, download.StatePaused, download.StateFailed, download.StateCancelled, download.StateInterrupted:
		return state
	}
	return ""
}

func assistantRequest(req AssistantOperationRequest) error {
	// This allowlist runs before account lookup, credentials or any request.
	switch req.Action {
	case "create_folder", "copy", "star", "unstar", "restore", "pause_download", "resume_download", "restart_download", "cancel_download", "remove_download", "retry_upload", "cancel_upload", "remove_upload", "retry_cloud_task", "remove_cloud_task", "cancel_share":
	default:
		return errAssistantAction
	}
	if req.ParentID != "" && !assistantID(req.ParentID) {
		return errAssistantRequest
	}
	if req.Action == "create_folder" {
		if len(req.IDs) != 0 || invalidName(req.Name) {
			return errAssistantRequest
		}
		return nil
	}
	if req.Name != "" || (req.Action != "copy" && req.ParentID != "") || len(req.IDs) == 0 || len(req.IDs) > maxRemove {
		return errAssistantRequest
	}
	seen := make(map[string]bool, len(req.IDs))
	for _, id := range req.IDs {
		if !assistantID(id) || seen[id] {
			return errAssistantRequest
		}
		seen[id] = true
	}
	return nil
}

func (d *DriveService) assistantLiveFile(s *accountSession, id string) (*drive.File, error) {
	f, err := callIn(d, s, func(ctx context.Context, c *drive.Client) (*drive.File, error) {
		return c.GetFile(ctx, id, drive.GetFileParams{})
	})
	if err != nil {
		return nil, assistantFailure(err)
	}
	if f.ID != id || f.Trashed || (f.Kind != drive.KindFile && f.Kind != drive.KindFolder) {
		return nil, errAssistantTarget
	}
	return f, nil
}

// Walk the actual destination, not the local index: every ancestor must still
// be live, and no copied folder may be an ancestor of its own destination.
func (d *DriveService) assistantDestination(s *accountSession, parent string, sources map[string]drive.File) error {
	seen := map[string]bool{}
	for parent != "" {
		if seen[parent] {
			return errAssistantTarget
		}
		seen[parent] = true
		if f, ok := sources[parent]; ok && f.IsFolder() {
			return errAssistantRecursive
		}
		f, err := d.assistantLiveFile(s, parent)
		if err != nil {
			return err
		}
		if !f.IsFolder() {
			return errAssistantTarget
		}
		parent = f.ParentID
	}
	return nil
}

// AssistantOperate is intentionally separate from manual UI RPCs: no method
// names, permanent deletes or trash-emptying flag can cross this boundary.
// Validate the entire batch before the first write, then report each outcome.
func (d *DriveService) AssistantOperate(account string, req AssistantOperationRequest) (AssistantOperationResult, error) {
	out := AssistantOperationResult{Results: []AssistantOperationItem{}}
	if err := assistantRequest(req); err != nil {
		return out, err
	}
	if !assistantID(account) {
		return out, errAssistantRequest
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return out, assistantFailure(err)
	}
	if _, err := s.api(); err != nil {
		return out, assistantFailure(err)
	}
	files := map[string]drive.File{}
	tasks := map[string]drive.Task{}
	names := make(map[string]string, len(req.IDs))
	var downloads *download.Queue
	var uploads *upload.Queue
	userID := func() string {
		s.mu.RLock()
		tokens, rec := s.tokens, s.rec
		s.mu.RUnlock()
		if tokens != nil {
			if tok, err := tokens.Current(); err == nil && tok.UserID != "" {
				return tok.UserID
			}
		}
		return rec.UserID
	}
	switch req.Action {
	case "create_folder", "copy", "star", "unstar", "restore":
		for _, id := range req.IDs {
			var f *drive.File
			if req.Action == "restore" {
				f, err = callIn(d, s, func(ctx context.Context, c *drive.Client) (*drive.File, error) {
					return recycledFile(ctx, c, id)
				})
				if err == nil && (!f.Trashed || f.ID != id || (f.Kind != drive.KindFile && f.Kind != drive.KindFolder)) {
					err = errAssistantTarget
				}
			} else {
				f, err = d.assistantLiveFile(s, id)
			}
			if err != nil {
				return out, assistantFailure(err)
			}
			files[id], names[id] = *f, assistantName(f.Name)
		}
		if req.Action == "copy" || req.Action == "create_folder" {
			if err := d.assistantDestination(s, req.ParentID, files); err != nil {
				return out, err
			}
		}
	case "pause_download", "resume_download", "restart_download", "cancel_download", "remove_download":
		downloads, err = s.liveDownloads()
		if err != nil {
			return out, assistantFailure(err)
		}
		rows := map[string]download.Task{}
		for _, task := range downloads.List() {
			rows[task.ID] = task
		}
		for _, id := range req.IDs {
			task, ok := rows[id]
			if !ok {
				return out, errAssistantTarget
			}
			valid := assistantDownloadState(task.State) != ""
			switch req.Action {
			case "pause_download":
				valid = task.Active()
			case "resume_download", "restart_download":
				valid = task.State == download.StatePaused || task.State == download.StateFailed || task.State == download.StateCancelled || task.State == download.StateInterrupted
			case "cancel_download":
				valid = valid && task.State != download.StateDone && task.State != download.StateCancelled
			}
			if !valid {
				return out, errAssistantState
			}
			names[id] = assistantName(task.Name)
		}
	case "retry_upload", "cancel_upload", "remove_upload":
		uploads, err = s.liveQueue()
		if err != nil {
			return out, assistantFailure(err)
		}
		rows := map[string]upload.Task{}
		for _, task := range uploads.List() {
			rows[task.ID] = task
		}
		for _, id := range req.IDs {
			task, ok := rows[id]
			if !ok {
				return out, errAssistantTarget
			}
			valid := assistantUploadState(task.State) != ""
			switch req.Action {
			case "cancel_upload":
				valid = task.Active()
			case "retry_upload":
				valid = task.State == upload.StateFailed || task.State == upload.StateCancelled || task.State == upload.StateInterrupted
				if valid {
					info, statErr := os.Stat(task.Path)
					if statErr != nil || !info.Mode().IsRegular() {
						return out, errAssistantTarget
					}
					if err := d.assistantDestination(s, task.ParentID, nil); err != nil {
						return out, err
					}
				}
			}
			if !valid {
				return out, errAssistantState
			}
			names[id] = assistantName(task.Name)
		}
	case "retry_cloud_task", "remove_cloud_task":
		for _, id := range req.IDs {
			task, err := callIn(d, s, func(ctx context.Context, c *drive.Client) (*drive.Task, error) { return c.GetTask(ctx, id) })
			if err != nil {
				return out, assistantFailure(err)
			}
			if task.ID != id || task.Type != drive.TaskTypeOffline || (task.UserID != "" && task.UserID != userID()) {
				return out, errAssistantTarget
			}
			if assistantTaskState(task.Phase) == "" {
				return out, errAssistantState
			}
			if req.Action == "retry_cloud_task" {
				if task.Phase != drive.PhaseError {
					return out, errAssistantState
				}
				if torrent.BTIH(task.Params["url"]) == "" {
					return out, errAssistantRetryHash
				}
				if task.ReferenceResource != nil {
					if err := d.assistantDestination(s, task.ReferenceResource.ParentID, nil); err != nil {
						return out, err
					}
				}
			}
			tasks[id], names[id] = *task, assistantName(task.Name)
		}
	case "cancel_share":
		wanted := make(map[string]bool, len(req.IDs))
		for _, id := range req.IDs {
			wanted[id] = true
		}
		seen := map[string]bool{}
		for token := ""; ; {
			list, err := callIn(d, s, func(ctx context.Context, c *drive.Client) (*drive.ShareList, error) {
				return c.ListShares(ctx, drive.ListSharesParams{Limit: drive.MaxListLimit, PageToken: token})
			})
			if err != nil {
				return out, assistantFailure(err)
			}
			for _, share := range list.Data {
				if !wanted[share.ShareID] {
					continue
				}
				if (share.UserInfo.UserID != "" && share.UserInfo.UserID != userID()) || share.ShareStatus == drive.ShareStatusDeleted {
					return out, errAssistantTarget
				}
				names[share.ShareID] = assistantName(toShareSummary(share).Title)
				delete(wanted, share.ShareID)
			}
			if len(wanted) == 0 {
				break
			}
			if list.NextPageToken == "" {
				return out, errAssistantTarget
			}
			if seen[list.NextPageToken] {
				return out, errAssistantFailure
			}
			seen[list.NextPageToken] = true
			token = list.NextPageToken
		}
	}
	if req.Action == "create_folder" {
		f, err := callIn(d, s, func(ctx context.Context, c *drive.Client) (*drive.File, error) {
			return c.CreateFolder(ctx, req.ParentID, req.Name)
		})
		item := AssistantOperationItem{Name: assistantName(req.Name)}
		if err != nil {
			item.Error = assistantFailure(err).Error()
		} else if f.ID == "" || !f.IsFolder() {
			item.Error = probUnconfirmed()
		} else {
			item.ID, item.Name = f.ID, assistantName(f.Name)
			if idx := s.index(); idx != nil {
				d.indexErr(idx.store.Upsert([]index.Row{toRow(*f)}))
			}
		}
		out.Results = append(out.Results, item)
		return out, nil
	}
	for _, id := range req.IDs {
		item := AssistantOperationItem{ID: id, Name: names[id]}
		err := d.assistantApply(s, req, id, tasks[id], downloads, uploads)
		if err != nil {
			item.Error = assistantFailure(err).Error()
		}
		out.Results = append(out.Results, item)
	}
	return out, nil
}

func (d *DriveService) assistantApply(s *accountSession, req AssistantOperationRequest, id string, task drive.Task, downloads *download.Queue, uploads *upload.Queue) error {
	switch req.Action {
	case "pause_download":
		return downloads.Pause(id)
	case "resume_download":
		return downloads.Resume(id)
	case "restart_download":
		return downloads.Restart(id)
	case "cancel_download":
		return downloads.Cancel(id)
	case "remove_download":
		return downloads.Remove(id)
	case "retry_upload":
		return uploads.Retry(id)
	case "cancel_upload":
		return uploads.Cancel(id)
	case "remove_upload":
		return uploads.Remove(id)
	case "restore":
		// Reuse restore's task wait and read-back, never its delete phases.
		r, end, err := d.beginRemove(s.key, phaseRestore)
		if err != nil {
			return err
		}
		defer end()
		r.total = 1
		r.restore([]string{id})
		if len(r.report.Removed) != 1 {
			return errAssistantFailure
		}
		return nil
	}
	err := d.doIn(context.Background(), s, func(ctx context.Context, c *drive.Client) error {
		switch req.Action {
		case "copy":
			return c.Copy(ctx, []string{id}, req.ParentID)
		case "star":
			return c.Star(ctx, []string{id})
		case "unstar":
			return c.Unstar(ctx, []string{id})
		case "remove_cloud_task":
			return c.DeleteTasks(ctx, []string{id}, false)
		case "retry_cloud_task":
			parent := ""
			if task.ReferenceResource != nil {
				parent = task.ReferenceResource.ParentID
			}
			created, err := c.CreateOfflineTask(ctx, drive.OfflineTaskParams{URL: "magnet:?xt=urn:btih:" + torrent.BTIH(task.Params["url"]), ParentID: parent})
			if err == nil && (created.Task.ID == "" || created.Task.Phase == drive.PhaseError) {
				return errAssistantFailure
			}
			return err
		case "cancel_share":
			return c.DeleteShares(ctx, []string{id})
		}
		return errAssistantAction
	})
	if err == nil {
		switch req.Action {
		case "star", "unstar":
			starred := req.Action == "star"
			d.patchIndex(s, []string{id}, index.Patch{Starred: &starred})
		case "copy", "retry_cloud_task", "remove_cloud_task":
			requestSync(s)
			if req.Action == "retry_cloud_task" {
				kickInbox(s)
			}
		}
	}
	return err
}
