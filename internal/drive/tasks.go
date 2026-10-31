package drive

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Task types reported in Task.Type.
const (
	TaskTypeOffline     = "offline"
	TaskTypeTranscoding = "transcoding"
)

// FolderTypeDownload is the server-managed folder offline tasks land in when
// no explicit parent is given.
const FolderTypeDownload = "DOWNLOAD"

// uploadTypeURL tells the create endpoint that the server should fetch the
// bytes itself instead of accepting an upload.
const uploadTypeURL = "UPLOAD_TYPE_URL"

// Task is one server-side job, e.g. an offline download of a torrent.
type Task struct {
	Kind        string            `json:"kind"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	UserID      string            `json:"user_id"`
	Statuses    []TaskStatus      `json:"statuses"`
	StatusSize  int               `json:"status_size"`
	Params      map[string]string `json:"params"`
	FileID      string            `json:"file_id"`
	FileName    string            `json:"file_name"`
	FileSize    Int64             `json:"file_size"`
	Message     string            `json:"message"`
	CreatedTime string            `json:"created_time"`
	UpdatedTime string            `json:"updated_time"`
	ThirdTaskID string            `json:"third_task_id"`
	Phase       string            `json:"phase"`
	Progress    float64           `json:"progress"`
	IconLink    string            `json:"icon_link"`
	// ReferenceResource is the file the task fills; its parent is where the
	// result lands. Nil until the server has created the file.
	ReferenceResource *TaskReference `json:"reference_resource"`
}

// TaskReference is the part of a task's target file the transfer list uses.
type TaskReference struct {
	ParentID string `json:"parent_id"`
}

// Done reports whether the task reached a terminal phase.
func (t Task) Done() bool { return t.Phase == PhaseComplete || t.Phase == PhaseError }

// TaskStatus is one sub-item of a task, e.g. one file inside a torrent.
type TaskStatus struct {
	Kind        string            `json:"kind"`
	ID          string            `json:"id"`
	FileID      string            `json:"file_id"`
	Name        string            `json:"name"`
	IconLink    string            `json:"icon_link"`
	Progress    float64           `json:"progress"`
	FileName    string            `json:"file_name"`
	FileSize    Int64             `json:"file_size"`
	Phase       string            `json:"phase"`
	Message     string            `json:"message"`
	CreatedTime string            `json:"created_time"`
	UpdatedTime string            `json:"updated_time"`
	Params      map[string]string `json:"params"`
}

// TaskList is one page of ListTasks.
type TaskList struct {
	Tasks         []Task `json:"tasks"`
	NextPageToken string `json:"next_page_token"`
	// ExpiresIn is how long the server considers this snapshot fresh, in
	// seconds; use it to pace polling instead of guessing an interval.
	ExpiresIn int `json:"expires_in"`
}

// MaxTaskListLimit is the largest page /drive/v1/tasks serves: it answers a
// bigger limit with 500 tasks and a next_page_token.
const MaxTaskListLimit = 500

// ListTasksParams selects a page of tasks.
type ListTasksParams struct {
	// Type filters by task type; empty returns every type.
	Type      string
	PageToken string
	// Limit is clamped to MaxTaskListLimit; zero leaves it to the server.
	Limit   int
	Filters Filters
}

// ListTasks returns one page of transfer tasks.
func (c *Client) ListTasks(ctx context.Context, params ListTasksParams) (*TaskList, error) {
	query := url.Values{}
	if params.Type != "" {
		query.Set("type", params.Type)
	}
	if params.PageToken != "" {
		query.Set("page_token", params.PageToken)
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.Itoa(min(params.Limit, MaxTaskListLimit)))
	}
	filters, err := params.Filters.encode()
	if err != nil {
		return nil, err
	}
	if filters != "" {
		query.Set("filters", filters)
	}

	var out TaskList
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/tasks", query: query}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTask reads one task by id.
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	if taskID == "" {
		return nil, errors.New("drive: task id is required")
	}
	var out Task
	path := "/drive/v1/tasks/" + url.PathEscape(taskID)
	if err := c.do(ctx, request{method: http.MethodGet, path: path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OfflineTaskParams describes a server-side download.
type OfflineTaskParams struct {
	// URL is a magnet link, an http(s) URL or a torrent reference.
	URL string
	// ParentID is the destination folder; empty puts the result in the
	// server-managed download folder.
	ParentID string
	// Name overrides the destination name.
	Name string
	// Files selects a subset of a multi-file source by index.
	Files []string
	// NeedDedup asks the server to skip content already in the drive.
	NeedDedup bool
}

// OfflineTask is the outcome of creating a server-side download: the task to
// poll, and the placeholder file it will fill.
type OfflineTask struct {
	Task Task `json:"task"`
	File File `json:"file"`
}

// CreateOfflineTask asks the server to fetch a URL into the drive.
func (c *Client) CreateOfflineTask(ctx context.Context, params OfflineTaskParams) (*OfflineTask, error) {
	if strings.TrimSpace(params.URL) == "" {
		return nil, errors.New("drive: offline task url is required")
	}

	body := map[string]any{
		"kind":        KindFile,
		"upload_type": uploadTypeURL,
		"url":         map[string]any{"url": params.URL},
	}
	if len(params.Files) > 0 {
		body["url"] = map[string]any{"url": params.URL, "files": params.Files}
	}
	if params.ParentID != "" {
		body["parent_id"] = params.ParentID
	} else {
		body["folder_type"] = FolderTypeDownload
	}
	if params.Name != "" {
		body["name"] = params.Name
	}
	if params.NeedDedup {
		body["need_dedup"] = true
	}

	var out OfflineTask
	if err := c.do(ctx, request{method: http.MethodPost, path: "/drive/v1/files", body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTasks removes tasks. deleteFiles also discards whatever they already
// downloaded, which is why it is an explicit argument rather than a default.
func (c *Client) DeleteTasks(ctx context.Context, taskIDs []string, deleteFiles bool) error {
	if len(taskIDs) == 0 {
		return errors.New("drive: at least one task id is required")
	}
	query := url.Values{}
	for _, id := range taskIDs {
		query.Add("task_ids", id)
	}
	query.Set("delete_files", strconv.FormatBool(deleteFiles))
	return c.do(ctx, request{method: http.MethodDelete, path: "/drive/v1/tasks", query: query}, nil)
}
