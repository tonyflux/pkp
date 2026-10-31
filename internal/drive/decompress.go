package drive

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Archive status values handled by the client from the response's status
// field. Business outcomes may accompany HTTP 200; request errors are
// handled separately as APIError.
const (
	ArchiveOK              = "OK"
	ArchivePasswordEmpty   = "PASS_WORD_EMPTY"
	ArchivePasswordWrong   = "PASS_WORD_ERROR"
	ArchiveRunningTask     = "RUNNING_TASK"
	ArchiveListTimeout     = "FILE_LIST_TIMEOUT"
	ArchiveInvalidFormat   = "INVALID_FILE_FORMAT"
	ArchiveNeedMoreQuota   = "NEED_MORE_QUOTA"
	ArchiveTooManySubfiles = "TOO_MANY_SUBFILES"
	ArchiveTooBig          = "ARCHIVE_TOO_BIG"
)

// ArchiveListParams asks for one level of an archive kept in the drive.
type ArchiveListParams struct {
	FileID string
	// GCID is the archive's content id (File.Hash); the service requires it
	// beside the file id.
	GCID string
	// Password is sent as typed; "" for an archive that needs none.
	Password string
	// Path is a folder inside the archive as an earlier listing gave it
	// (ArchiveEntry.Path, ending in "/"); "" is the top level.
	Path string
}

// ArchiveListing is one level of an archive. Files is empty unless Status
// is ArchiveOK.
type ArchiveListing struct {
	Status string `json:"status"`
	// StatusText is the service's own words, set for a few statuses only.
	StatusText string `json:"status_text"`
	// TaskID is the extraction already running for this archive
	// (Status ArchiveRunningTask).
	TaskID      string         `json:"task_id"`
	CurrentPath string         `json:"current_path"`
	Title       string         `json:"title"`
	FileSize    Int64          `json:"file_size"`
	GCID        string         `json:"gcid"`
	Files       []ArchiveEntry `json:"files"`
}

// ArchiveEntry is a file or folder inside an archive. Folders are made up
// from the members' paths: they carry no index, size or child count.
type ArchiveEntry struct {
	Index    int    `json:"index"`
	Name     string `json:"filename"`
	Size     Int64  `json:"filesize"`
	MimeType string `json:"mime_type"`
	// GCID is set for a member the cloud already holds.
	GCID string `json:"gcid"`
	Kind string `json:"kind"`
	// Path is the full path inside the archive, without a leading slash;
	// a folder's ends in "/".
	Path string `json:"path"`
}

// ListArchive reads one level of an archive's contents.
func (c *Client) ListArchive(ctx context.Context, params ArchiveListParams) (*ArchiveListing, error) {
	if params.FileID == "" || params.GCID == "" {
		return nil, errors.New("drive: listing an archive needs its file id and gcid")
	}
	body := map[string]any{"file_id": params.FileID, "gcid": params.GCID, "password": params.Password, "path": params.Path}
	var out ArchiveListing
	if err := c.do(ctx, request{method: http.MethodPost, path: "/decompress/v1/list", body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ArchivePick is one member to extract: a file by Index, a folder (and
// everything under it) by Path, both as the listing gave them.
type ArchivePick struct {
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`
}

// ExtractParams starts extracting an archive into a drive folder. The
// service makes a folder named after the archive inside ParentID and puts
// the picked members under it, keeping their inner folders.
type ExtractParams struct {
	FileID   string
	GCID     string
	Password string
	// ParentID is the destination folder; "" is the drive's root.
	ParentID string
	// ParentPath names the destination for the task's record only
	// (["Movies & TV", "Collections"]); the service never resolves it.
	ParentPath []string
	// Picks are the members to extract; empty extracts everything.
	Picks []ArchivePick
}

// ExtractResult is the service's answer to an extraction. TaskID is a
// drive task (type "decompress") when Status is ArchiveOK, or the one
// already running when it is ArchiveRunningTask.
type ExtractResult struct {
	Status     string `json:"status"`
	StatusText string `json:"status_text"`
	TaskID     string `json:"task_id"`
	// FilesNum is how many archive members the task extracts, folder
	// entries included (a zip of 2 folders answered 6, probed 2026-10-17).
	FilesNum int `json:"files_num"`
	// RedirectLink explains how to get the right to extract
	// (ArchiveNeedMoreQuota).
	RedirectLink string `json:"redirect_link"`
}

// Extract submits an extraction task.
func (c *Client) Extract(ctx context.Context, params ExtractParams) (*ExtractResult, error) {
	if params.FileID == "" || params.GCID == "" {
		return nil, errors.New("drive: extracting an archive needs its file id and gcid")
	}
	picks := params.Picks
	if picks == nil {
		picks = []ArchivePick{}
	}
	parentPath := params.ParentPath
	if parentPath == nil {
		parentPath = []string{}
	}
	body := map[string]any{
		"file_id":          params.FileID,
		"gcid":             params.GCID,
		"password":         params.Password,
		"default_parent":   false,
		"parent_id":        params.ParentID,
		"parent_full_path": parentPath,
		"files":            picks,
	}
	var out ExtractResult
	if err := c.do(ctx, request{method: http.MethodPost, path: "/decompress/v1/decompress", body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExtractProgress is the state of an extraction task.
type ExtractProgress struct {
	// Progress is a percentage, 0–100.
	Progress int `json:"progress"`
	// ExpiresIn is how many seconds to wait before asking again; 999 once
	// the task has ended.
	ExpiresIn int `json:"expires_in"`
	// Phase is one of the PHASE_TYPE_* values.
	Phase string `json:"phase"`
	// FileID is set once the task completes: the folder it made, or the
	// only file it extracted.
	FileID            string            `json:"file_id"`
	FileName          string            `json:"file_name"`
	FileSize          Int64             `json:"file_size"`
	TaskSize          Int64             `json:"task_size"`
	TaskSizeCompleted Int64             `json:"task_size_completed"`
	DecompressPath    []string          `json:"decompress_path"`
	Params            map[string]string `json:"params"`
}

// ExtractProgressOf reads an extraction task's state.
func (c *Client) ExtractProgressOf(ctx context.Context, taskID string) (*ExtractProgress, error) {
	if taskID == "" {
		return nil, errors.New("drive: task id is required")
	}
	var out ExtractProgress
	query := url.Values{"task_id": {taskID}}
	if err := c.do(ctx, request{method: http.MethodGet, path: "/decompress/v1/progress", query: query}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
