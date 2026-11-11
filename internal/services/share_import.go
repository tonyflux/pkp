package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

var (
	errShareInput       = i18n.New("分享链接或文件选择无效")
	errShareUnavailable = i18n.New("分享已失效或无法访问，请重新检查链接和提取码")
	errSharePreview     = i18n.New("此分享文件不支持预览")
	errShareReceipt     = i18n.New("无法确认转存结果，请检查目标文件夹，勿重复提交")
	errShareMapping     = i18n.New("转存结果与所选文件或目标文件夹不一致")
	errShareTask        = i18n.New("未找到此账号的分享转存任务")
	errShareFailed      = i18n.New("分享转存失败")
	errShareCapacity    = i18n.New("待确认的转存任务过多，请先检查目标文件夹")
)

type ShareEntry struct {
	Entry
	Width  int `json:"width"`
	Height int `json:"height"`
}

type ShareImport struct {
	ShareID       string       `json:"shareId"`
	URL           string       `json:"url"`
	Title         string       `json:"title"`
	Owner         string       `json:"owner"`
	Status        string       `json:"status"`
	StatusText    string       `json:"statusText"`
	FileCount     int64        `json:"fileCount"`
	Entries       []ShareEntry `json:"entries"`
	NextPageToken string       `json:"nextPageToken"`
	PassCodeToken string       `json:"passCodeToken"`
}

type ShareFilePage struct {
	Entries       []ShareEntry `json:"entries"`
	NextPageToken string       `json:"nextPageToken"`
	PassCodeToken string       `json:"passCodeToken"`
}

type SharePreview struct {
	URL      string       `json:"url"`
	Kind     string       `json:"kind"`
	MimeType string       `json:"mimeType"`
	Name     string       `json:"name"`
	Text     *TextPreview `json:"text"`
}

type RestoreShareRequest struct {
	ShareID       string   `json:"shareId"`
	PassCodeToken string   `json:"passCodeToken"`
	ParentID      string   `json:"parentId"`
	FileIDs       []string `json:"fileIds"`
}

type ShareRestoreResult struct {
	Status   string `json:"status"`
	TaskID   string `json:"taskId"`
	ParentID string `json:"parentId"`
	// Pending/error IDs are source IDs; complete IDs are restored IDs in selection order.
	FileIDs []string `json:"fileIds"`
	Message string   `json:"message"`
}

type shareScope struct{ account, id string }
type shareAccess struct{ passCode, token string }
type trackedShareRestore struct {
	request  RestoreShareRequest
	result   ShareRestoreResult
	mapping  []string
	verified int
}

// Receipts are process-owned: a caller cannot poll an unrelated account task
// or change a pending restore's destination after switching the visible account.
type shareImportState struct {
	mu       sync.Mutex
	access   map[shareScope]shareAccess
	restores map[string]*trackedShareRestore
	tasks    map[shareScope]*trackedShareRestore
}

func (d *DriveService) shareAccount(account string) (*accountSession, error) {
	if account == "" {
		return nil, errNoSuchAccount
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	if _, err := s.api(); err != nil {
		return nil, err
	}
	return s, nil
}

// InspectShare performs only anonymous reads; selecting a receiver never changes
// the app's active account and no account credentials enter this response.
func (d *DriveService) InspectShare(account, rawURL, passCode string) (ShareImport, error) {
	s, err := d.shareAccount(account)
	if err != nil {
		return ShareImport{}, err
	}
	id, canonical, linkedCode, err := drive.ParseShareURL(rawURL)
	if err != nil || len(passCode) > 128 || strings.IndexFunc(passCode, unicode.IsControl) >= 0 {
		return ShareImport{}, errShareInput
	}
	if passCode == "" {
		passCode = linkedCode
	}
	if passCode != "" {
		canonical = "https://mypikpak.com/s/" + id + "?" + url.Values{"pass_code": {passCode}}.Encode()
	}
	page, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.SharedFiles, error) {
		return client.InspectShare(ctx, drive.InspectShareParams{ShareID: id, PassCode: passCode})
	})
	if err != nil {
		return ShareImport{}, err
	}
	if page.ShareID != "" && page.ShareID != id {
		return ShareImport{}, errShareInput
	}
	out := ShareImport{ShareID: id, URL: canonical, Title: page.Title, Owner: page.UserInfo.Name, Status: page.Status, StatusText: page.StatusText, FileCount: int64(page.FileNum), Entries: []ShareEntry{}}
	if out.Status == drive.ShareStatusOK {
		out.Entries = shareEntries(page.Files)
		out.NextPageToken = page.NextPageToken
		out.PassCodeToken = page.PassCodeToken
	}
	d.shareImports.mu.Lock()
	if d.shareImports.access == nil {
		d.shareImports.access = make(map[shareScope]shareAccess)
	}
	for key := range d.shareImports.access {
		if key.account == account || d.byKey(key.account) == nil {
			delete(d.shareImports.access, key)
		}
	}
	key := shareScope{account, id}
	if out.Status == drive.ShareStatusOK {
		d.shareImports.access[key] = shareAccess{passCode: passCode, token: page.PassCodeToken}
	}
	d.shareImports.mu.Unlock()
	return out, nil
}

// ListShareFiles uses the header endpoint for root pagination: detail pagination
// alone loses the original pass code on protected shares.
func (d *DriveService) ListShareFiles(account, shareID, passCodeToken, parentID, pageToken string) (ShareFilePage, error) {
	s, err := d.shareAccount(account)
	if err != nil {
		return ShareFilePage{}, err
	}
	d.shareImports.mu.Lock()
	access := d.shareImports.access[shareScope{account, shareID}]
	d.shareImports.mu.Unlock()
	if passCodeToken == "" {
		passCodeToken = access.token
	}
	page, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.SharedFiles, error) {
		if parentID == "" {
			return client.InspectShare(ctx, drive.InspectShareParams{ShareID: shareID, PassCode: access.passCode, PassCodeToken: passCodeToken, PageToken: pageToken})
		}
		return client.ListSharedFiles(ctx, drive.ListSharedFilesParams{ShareID: shareID, PassCodeToken: passCodeToken, ParentID: parentID, PageToken: pageToken})
	})
	if err != nil {
		return ShareFilePage{}, err
	}
	if page.Status != drive.ShareStatusOK || (page.ShareID != "" && page.ShareID != shareID) {
		return ShareFilePage{}, errShareUnavailable
	}
	token := page.PassCodeToken
	if token == "" {
		token = passCodeToken
	}
	return ShareFilePage{Entries: shareEntries(page.Files), NextPageToken: page.NextPageToken, PassCodeToken: token}, nil
}

func shareEntries(files []drive.File) []ShareEntry {
	entries := make([]ShareEntry, 0, len(files))
	for _, file := range files {
		if !drive.ValidShareID(file.ID) || file.Trashed {
			continue
		}
		entry := ShareEntry{Entry: toEntry(file)}
		for _, media := range file.Medias {
			if media.Video.Width > entry.Width {
				entry.Width, entry.Height = media.Video.Width, media.Video.Height
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func sharedFileReady(file drive.File) bool {
	return !file.Trashed && (file.Phase == "" || file.Phase == drive.PhaseComplete)
}

func (d *DriveService) PreviewShareFile(ctx context.Context, account, shareID, passCodeToken, fileID string) (SharePreview, error) {
	s, err := d.shareAccount(account)
	if err != nil {
		return SharePreview{}, err
	}
	client, err := s.api()
	if err != nil {
		return SharePreview{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()
	response, err := client.GetSharedFile(ctx, shareID, passCodeToken, fileID)
	if err = d.classify(s, client, err); err != nil {
		return SharePreview{}, err
	}
	if response.Status != "" && response.Status != drive.ShareStatusOK {
		return SharePreview{}, errShareUnavailable
	}
	file := response.FileInfo
	if file.ID != fileID || !sharedFileReady(file) || file.IsFolder() {
		return SharePreview{}, errSharePreview
	}
	kind := previewOf(file)
	// A real MIME outranks a misleading extension (e.g. text named movie.mp4).
	if isPlayable(file) && !strings.HasPrefix(file.MimeType, "text/") && !strings.HasPrefix(file.MimeType, "image/") && file.MimeType != "application/pdf" {
		kind = categoryOf(file)
	}
	if kind != "video" && kind != "audio" && kind != "image" && kind != "pdf" && kind != "text" {
		return SharePreview{}, errSharePreview
	}
	link := file.ContentLink()
	if kind == "video" || kind == "audio" {
		fallback := ""
		for _, media := range file.Medias {
			if media.Link.URL == "" || media.NeedMoreQuota {
				continue
			}
			mediaURL, err := url.Parse(media.Link.URL)
			if err != nil {
				continue
			}
			path := strings.ToLower(mediaURL.Path)
			if strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".mpd") || strings.HasSuffix(path, ".ts") {
				continue
			}
			// The range proxy does not rewrite manifest segment URLs.
			if media.IsOrigin {
				link = media.Link.URL
				break
			}
			if fallback == "" {
				fallback = media.Link.URL
			}
		}
		if link == "" {
			link = fallback
		}
	}
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return SharePreview{}, errNoDownloadLink
	}
	if d.player == nil {
		return SharePreview{}, errNoPlayer
	}
	contentType := file.MimeType
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = mime.TypeByExtension("." + extensionOf(file))
	}
	if kind == "pdf" {
		contentType = "application/pdf"
	} else if kind == "text" {
		contentType = "text/plain; charset=utf-8"
	}
	owner := d.previewOwner(ctx)
	local, err := d.player.openPreview(owner, link, d.mediaHeaders(), contentType)
	if err != nil {
		return SharePreview{}, err
	}
	out := SharePreview{URL: local, Kind: kind, MimeType: contentType, Name: file.Name}
	if kind == "text" {
		text, err := readShareText(ctx, local)
		if err != nil {
			d.player.closePreview(owner, local)
			return SharePreview{}, err
		}
		out.Text = &text
	}
	if err := ctx.Err(); err != nil {
		d.player.closePreview(owner, local)
		return SharePreview{}, err
	}
	return out, nil
}

func readShareText(ctx context.Context, local string) (TextPreview, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, local, nil)
	if err != nil {
		return TextPreview{}, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", textPreviewLimit))
	response, err := netproxy.Client().Do(req)
	if err != nil {
		return TextPreview{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return TextPreview{}, fmt.Errorf("drive: fetch shared text: upstream status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, textPreviewLimit+1))
	if err != nil {
		return TextPreview{}, err
	}
	truncated := len(raw) > textPreviewLimit
	if truncated {
		raw = raw[:textPreviewLimit]
	}
	text, encoding := decodeText(raw)
	return TextPreview{Text: text, Truncated: truncated, Encoding: encoding}, nil
}

func validRestoreShareRequest(req RestoreShareRequest) bool {
	if !drive.ValidShareID(req.ShareID) || (req.ParentID != "" && !drive.ValidShareID(req.ParentID)) || len(req.PassCodeToken) > 8192 || strings.IndexFunc(req.PassCodeToken, unicode.IsControl) >= 0 || len(req.FileIDs) == 0 || len(req.FileIDs) > 1000 {
		return false
	}
	seen := make(map[string]bool, len(req.FileIDs))
	for _, id := range req.FileIDs {
		if !drive.ValidShareID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func shareRestoreKey(account string, req RestoreShareRequest) string {
	ids := append([]string(nil), req.FileIDs...)
	sort.Strings(ids)
	return account + "/" + req.ShareID + "/" + req.ParentID + "/" + strings.Join(ids, ",")
}

// RestoreShare is the only share-import mutation, called after confirmation.
// Retain every possibly accepted write independently of its presentation status.
func (d *DriveService) RestoreShare(account string, req RestoreShareRequest) (ShareRestoreResult, error) {
	s, err := d.shareAccount(account)
	if err != nil {
		return ShareRestoreResult{}, err
	}
	if !validRestoreShareRequest(req) {
		return ShareRestoreResult{}, errShareInput
	}
	req.FileIDs = append([]string(nil), req.FileIDs...)
	key := shareRestoreKey(account, req)
	state := &d.shareImports
	state.mu.Lock()
	if existing := state.restores[key]; existing != nil {
		out := existing.result
		state.mu.Unlock()
		return out, nil
	}
	// Only verified completion makes eviction safe; presentation errors can
	// still describe accepted or uncertain writes and must never permit replay.
	if len(state.restores) >= 64 {
		for oldKey, old := range state.restores {
			if old.result.Status != "complete" {
				continue
			}
			delete(state.restores, oldKey)
			for taskKey, task := range state.tasks {
				if task == old {
					delete(state.tasks, taskKey)
				}
			}
			break
		}
		if len(state.restores) >= 64 {
			state.mu.Unlock()
			return ShareRestoreResult{}, errShareCapacity
		}
	}
	tracked := &trackedShareRestore{request: req, result: ShareRestoreResult{Status: "pending", ParentID: req.ParentID, FileIDs: req.FileIDs, Message: errShareReceipt.Error()}}
	if state.restores == nil {
		state.restores = make(map[string]*trackedShareRestore)
	}
	state.restores[key] = tracked
	state.mu.Unlock()
	forget := func() {
		state.mu.Lock()
		delete(state.restores, key)
		state.mu.Unlock()
	}
	// Validate live membership and the destination before sending any mutation.
	if req.ParentID != "" {
		err = d.doIn(context.Background(), s, func(ctx context.Context, client *drive.Client) error {
			folder, err := client.GetFile(ctx, req.ParentID, drive.GetFileParams{})
			if err != nil {
				return err
			}
			if folder.ID != req.ParentID || !folder.IsFolder() || !sharedFileReady(*folder) {
				return errDestinationGone
			}
			return nil
		})
	}
	if err == nil {
		for _, id := range req.FileIDs {
			err = d.doIn(context.Background(), s, func(ctx context.Context, client *drive.Client) error {
				file, err := client.GetSharedFile(ctx, req.ShareID, req.PassCodeToken, id)
				if err != nil {
					return err
				}
				if file.Status != "" && file.Status != drive.ShareStatusOK {
					return errShareUnavailable
				}
				if file.FileInfo.ID != id || !sharedFileReady(file.FileInfo) {
					return errShareInput
				}
				return nil
			})
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		forget()
		return ShareRestoreResult{}, err
	}
	receipt, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.ShareRestoreReceipt, error) {
		return client.RestoreShare(ctx, drive.RestoreShareParams{ShareID: req.ShareID, PassCodeToken: req.PassCodeToken, ParentID: req.ParentID, FileIDs: req.FileIDs})
	})
	if err != nil {
		var apiErr *drive.APIError
		if errors.As(err, &apiErr) && !apiErr.Retryable() {
			forget()
			return ShareRestoreResult{}, err
		}
		requestSync(s)
		return tracked.result, nil
	}
	out := tracked.result
	if receipt.ShareStatus != "" && receipt.ShareStatus != drive.ShareStatusOK {
		out.Status, out.Message = "error", errShareUnavailable.Error()
	} else {
		requestSync(s)
		out.TaskID = receipt.TaskID
		if out.TaskID != "" && !drive.ValidShareID(out.TaskID) {
			out.TaskID = ""
		}
		switch {
		case receipt.ParentID != req.ParentID:
			out.Status, out.Message = "error", errShareMapping.Error()
		case receipt.Status == drive.ShareRestoreComplete:
			out = d.completeShareRestore(s, tracked, out, receipt.Params["trace_file_ids"])
		case receipt.Status == drive.ShareRestoreStart:
			if out.TaskID != "" {
				out.Message = ""
			}
		default:
			out.Status, out.Message = "error", errShareReceipt.Error()
		}
	}
	state.mu.Lock()
	tracked.result = out
	if out.TaskID != "" {
		if state.tasks == nil {
			state.tasks = make(map[shareScope]*trackedShareRestore)
		}
		taskKey := shareScope{account, out.TaskID}
		if previous := state.tasks[taskKey]; previous != nil && previous != tracked {
			out.Status, out.Message = "error", errShareMapping.Error()
			tracked.result = out
		} else {
			state.tasks[taskKey] = tracked
		}
	}
	state.mu.Unlock()
	return out, nil
}

// ShareRestoreStatus accepts only a task returned by this account's restore.
func (d *DriveService) ShareRestoreStatus(account, taskID string) (ShareRestoreResult, error) {
	s, err := d.shareAccount(account)
	if err != nil {
		return ShareRestoreResult{}, err
	}
	if !drive.ValidShareID(taskID) {
		return ShareRestoreResult{}, errShareTask
	}
	state := &d.shareImports
	state.mu.Lock()
	tracked := state.tasks[shareScope{account, taskID}]
	if tracked == nil {
		state.mu.Unlock()
		return ShareRestoreResult{}, errShareTask
	}
	out, req := tracked.result, tracked.request
	state.mu.Unlock()
	if out.Status != "pending" {
		return out, nil
	}
	task, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.Task, error) {
		return client.GetTask(ctx, taskID)
	})
	if err != nil {
		return ShareRestoreResult{}, err
	}
	switch {
	case task.ID != taskID || (task.ReferenceResource != nil && task.ReferenceResource.ParentID != req.ParentID):
		out.Status, out.Message = "error", errShareMapping.Error()
	case task.Phase == drive.PhaseComplete:
		out = d.completeShareRestore(s, tracked, out, task.Params["trace_file_ids"])
	case task.Phase == drive.PhaseError:
		out.Status, out.Message = "error", errShareFailed.Error()
		if detail := task.Params["error_detail"]; detail != "" {
			out.Message += ": " + detail
		} else if task.Message != "" {
			out.Message += ": " + task.Message
		}
	case task.Phase == drive.PhasePending || task.Phase == drive.PhaseRunning:
		out.Message = ""
	default:
		out.Message = errShareReceipt.Error()
	}
	state.mu.Lock()
	if tracked.result.Status == "pending" {
		tracked.result = out
	} else {
		out = tracked.result
	}
	state.mu.Unlock()
	if out.Status == "complete" {
		requestSync(s)
	}
	return out, nil
}

func (d *DriveService) completeShareRestore(s *accountSession, tracked *trackedShareRestore, out ShareRestoreResult, trace string) ShareRestoreResult {
	req := tracked.request
	ids, err := restoredShareIDs(trace, req.FileIDs)
	if err != nil {
		out.Status, out.Message = "error", errShareMapping.Error()
		return out
	}
	d.shareImports.mu.Lock()
	if tracked.mapping == nil {
		tracked.mapping = ids
	} else if !slices.Equal(tracked.mapping, ids) {
		d.shareImports.mu.Unlock()
		out.Status, out.Message = "error", errShareMapping.Error()
		return out
	}
	verified := tracked.verified
	d.shareImports.mu.Unlock()
	for i := verified; i < len(ids); i++ {
		id := ids[i]
		err = d.doIn(context.Background(), s, func(ctx context.Context, client *drive.Client) error {
			file, err := client.GetFile(ctx, id, drive.GetFileParams{})
			if err != nil {
				return err
			}
			if file.ID != id || file.ParentID != req.ParentID || file.Trashed {
				return errShareMapping
			}
			d.shareImports.mu.Lock()
			if tracked.verified == i {
				tracked.verified++
			}
			d.shareImports.mu.Unlock()
			return nil
		})
		if err != nil {
			if !drive.IsNotFound(err) {
				var apiErr *drive.APIError
				if errors.As(err, &apiErr) && !apiErr.Retryable() || errors.Is(err, errShareMapping) {
					out.Status = "error"
				}
			}
			out.Message = err.Error()
			return out
		}
	}
	out.Status, out.Message, out.FileIDs = "complete", "", ids
	return out
}

// Parse the authoritative one-to-one mapping, rejecting duplicate JSON keys,
// extra sources, empty targets and aliases before claiming success.
func restoredShareIDs(trace string, selected []string) ([]string, error) {
	decoder := json.NewDecoder(strings.NewReader(trace))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errShareMapping
	}
	mapping := make(map[string]string, len(selected))
	targets := make(map[string]bool, len(selected))
	for decoder.More() {
		token, err := decoder.Token()
		source, ok := token.(string)
		var target string
		if err != nil || !ok || mapping[source] != "" || decoder.Decode(&target) != nil || !drive.ValidShareID(source) || !drive.ValidShareID(target) || targets[target] {
			return nil, errShareMapping
		}
		mapping[source], targets[target] = target, true
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || len(mapping) != len(selected) {
		return nil, errShareMapping
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errShareMapping
	}
	ids := make([]string, 0, len(selected))
	for _, source := range selected {
		if mapping[source] == "" {
			return nil, errShareMapping
		}
		ids = append(ids, mapping[source])
	}
	return ids, nil
}
