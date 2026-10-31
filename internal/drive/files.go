package drive

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Object kinds returned in File.Kind.
const (
	KindFile   = "drive#file"
	KindFolder = "drive#folder"
)

// Phase values shared by files and tasks.
const (
	PhasePending  = "PHASE_TYPE_PENDING"
	PhaseRunning  = "PHASE_TYPE_RUNNING"
	PhaseError    = "PHASE_TYPE_ERROR"
	PhaseComplete = "PHASE_TYPE_COMPLETE"
	PhasePaused   = "PHASE_TYPE_PAUSED"
)

// Usage tells the server what the link will be used for. It changes which
// media variants come back and which rate limit applies.
type Usage string

const (
	UsageAll   Usage = "ALL"
	UsageFetch Usage = "FETCH" // plain download
	UsagePlay  Usage = "PLAY"  // streaming
	UsageCache Usage = "CACHE" // client-side pre-caching
)

// ThumbnailSize selects the thumbnail variant on list and get.
type ThumbnailSize string

const (
	ThumbnailDefault ThumbnailSize = "SIZE_DEFAULT"
	ThumbnailSmall   ThumbnailSize = "SIZE_SMALL"
	ThumbnailMedium  ThumbnailSize = "SIZE_MEDIUM"
	ThumbnailLarge   ThumbnailSize = "SIZE_LARGE"
)

// Order values for ListFilesParams.Order. The server ignores ordering when
// ParentID is "*" (whole-tree sync mode) and sorts by its own key instead.
const (
	OrderDefault         = "DEFAULT_ORDER"
	OrderTypeDesc        = "TYPE_DESC"
	OrderModifyTimeAsc   = "MODIFY_TIME_ASC"
	OrderModifyTimeDesc  = "MODIFY_TIME_DESC"
	OrderSizeAsc         = "SIZE_ASC"
	OrderSizeDesc        = "SIZE_DESC"
	OrderCreatedTimeAsc  = "CREATED_TIME_ASC"
	OrderCreatedTimeDesc = "CREATED_TIME_DESC"
	OrderNameAsc         = "NAME_ASC"
	OrderNameDesc        = "NAME_DESC"
)

// MaxListLimit is the largest page the server honours.
const MaxListLimit = 100

// Int64 decodes a 64-bit integer that the API may send either as a JSON
// number or, because the gateway renders proto int64 as a string, quoted.
type Int64 int64

func (v *Int64) UnmarshalJSON(data []byte) error {
	text := string(bytes.TrimSpace(data))
	if text == "null" || text == `""` || text == "" {
		*v = 0
		return nil
	}
	text = strings.Trim(text, `"`)
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("drive: %q is not an integer: %w", text, err)
	}
	*v = Int64(parsed)
	return nil
}

func (v Int64) MarshalJSON() ([]byte, error) {
	return strconv.AppendInt(nil, int64(v), 10), nil
}

// Link is a time-limited URL to the bytes of a file or media variant.
type Link struct {
	URL string `json:"url"`
	// Token is the acceleration token embedded in some links.
	Token string `json:"token"`
	// Expire is RFC3339 with milliseconds. Refresh the link before it passes.
	Expire string `json:"expire"`
	// Type distinguishes WAN from LAN endpoints.
	Type string `json:"type"`
}

// Video describes the picture of a media variant.
type Video struct {
	Height    int   `json:"height"`
	Width     int   `json:"width"`
	Duration  Int64 `json:"duration"`
	BitRate   Int64 `json:"bit_rate"`
	FrameRate int   `json:"frame_rate"`
}

// Media is one playable rendition: the original file or a transcode.
type Media struct {
	MediaID        string   `json:"media_id"`
	MediaName      string   `json:"media_name"`
	Video          Video    `json:"video"`
	Link           Link     `json:"link"`
	NeedMoreQuota  bool     `json:"need_more_quota"`
	VIPTypes       []string `json:"vip_types"`
	IsDefault      bool     `json:"is_default"`
	Priority       int      `json:"priority"`
	IsOrigin       bool     `json:"is_origin"`
	ResolutionName string   `json:"resolution_name"`
	IsVisible      bool     `json:"is_visible"`
	Category       string   `json:"category"`
}

// File is a file or folder. Only the fields this client uses are modelled;
// the API returns more.
type File struct {
	Kind           string            `json:"kind"`
	ID             string            `json:"id"`
	ParentID       string            `json:"parent_id"`
	Name           string            `json:"name"`
	UserID         string            `json:"user_id"`
	Size           Int64             `json:"size"`
	FileExtension  string            `json:"file_extension"`
	MimeType       string            `json:"mime_type"`
	Starred        bool              `json:"-"` // from the STAR tag, see UnmarshalJSON
	Trashed        bool              `json:"trashed"`
	DeleteTime     string            `json:"delete_time"`
	CreatedTime    string            `json:"created_time"`
	ModifiedTime   string            `json:"modified_time"`
	IconLink       string            `json:"icon_link"`
	ThumbnailLink  string            `json:"thumbnail_link"`
	MD5Checksum    string            `json:"md5_checksum"`
	Hash           string            `json:"hash"`
	WebContentLink string            `json:"web_content_link"`
	Links          map[string]Link   `json:"links"`
	Medias         []Media           `json:"medias"`
	Phase          string            `json:"phase"`
	FolderType     string            `json:"folder_type"`
	Params         map[string]string `json:"params"`
}

// IsFolder reports whether the entry can be descended into.
func (f File) IsFolder() bool { return f.Kind == KindFolder }

// starTag is the system tag the server attaches to a starred entry. It is
// also what the `system_tag` list filter matches.
const starTag = "STAR"

// UnmarshalJSON decodes the modelled fields and derives Starred from tags.
// The server's own `starred` boolean stays false on starred entries.
func (f *File) UnmarshalJSON(data []byte) error {
	type plain File
	var raw struct {
		plain
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*f = File(raw.plain)
	for _, tag := range raw.Tags {
		if tag.Name == starTag {
			f.Starred = true
			break
		}
	}
	return nil
}

// ContentLink picks the best download URL: the typed octet-stream link when
// present, otherwise the flat web_content_link.
func (f File) ContentLink() string {
	if link, ok := f.Links["application/octet-stream"]; ok && link.URL != "" {
		return link.URL
	}
	return f.WebContentLink
}

// FileList is one page of ListFiles.
type FileList struct {
	Kind          string `json:"kind"`
	Files         []File `json:"files"`
	NextPageToken string `json:"next_page_token"`
	// Version encodes last_destroy_time; DestroyTime decodes it. It moves
	// some time after a batchDelete, not at once, so the index package
	// treats a change as one of several triggers to relist the recycle
	// bin. The server ignores any version sent back to it.
	Version string `json:"version"`
	// SyncTime is the server-side sort key of the last entry on the page;
	// on the last page of a whole-tree walk it is the drive's high-water
	// mark, and SyncCursor turns it into a page token that resumes there.
	SyncTime string `json:"sync_time"`
}

// WholeTree is the parent id that makes ListFiles walk every folder.
const WholeTree = "*"

// syncCursor is the decoded shape of a whole-tree next_page_token.
type syncCursor struct {
	Kind string `json:"kind"`
	Data struct {
		ModifiedTime string `json:"modified_time"`
		ID           string `json:"id"`
	} `json:"data"`
}

// SyncCursor builds the page token that resumes a whole-tree walk at
// syncTime (inclusive). It is the same shape the server emits in
// next_page_token, so a stored sync_time can stand in for one. Empty
// syncTime yields an empty token, which starts the walk from the beginning.
func SyncCursor(syncTime string) string {
	if syncTime == "" {
		return ""
	}
	var cursor syncCursor
	cursor.Kind = "modified_time_asc"
	cursor.Data.ModifiedTime = syncTime
	data, _ := json.Marshal(cursor)
	return "2." + base64.RawURLEncoding.EncodeToString(data)
}

// CursorTime is the sort key a whole-tree page token resumes at: the
// modified_time inside the shape SyncCursor builds and the server emits
// ("<n>.<base64 JSON>", n counting up page by page: 2., 3., 4. …). ""
// when the token is not in that shape.
func CursorTime(token string) string {
	n, data, ok := strings.Cut(token, ".")
	if !ok || n == "" || strings.Trim(n, "0123456789") != "" {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return ""
	}
	var cursor syncCursor
	if json.Unmarshal(raw, &cursor) != nil {
		return ""
	}
	return cursor.Data.ModifiedTime
}

// DestroyTime decodes the last_destroy_time carried by FileList.Version.
// Returns "" when the version is empty or not in the known shape.
func DestroyTime(version string) string {
	if version == "" {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(version)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(version); err != nil {
			return ""
		}
	}
	var decoded struct {
		LastDestroyTime string `json:"last_destroy_time"`
	}
	if json.Unmarshal(raw, &decoded) != nil {
		return ""
	}
	return decoded.LastDestroyTime
}

// Filters is the server-side query passed as the `filters` parameter. It
// serialises to {"field":{"op":value}}.
type Filters map[string]map[string]any

// NewFilters returns an empty filter set.
func NewFilters() Filters { return Filters{} }

// Set adds one condition, e.g. Set("trashed", "eq", false).
func (f Filters) Set(field, op string, value any) Filters {
	if f[field] == nil {
		f[field] = map[string]any{}
	}
	f[field][op] = value
	return f
}

// Eq is the common case of Set(field, "eq", value).
func (f Filters) Eq(field string, value any) Filters { return f.Set(field, "eq", value) }

func (f Filters) encode() (string, error) {
	if len(f) == 0 {
		return "", nil
	}
	data, err := json.Marshal(map[string]map[string]any(f))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// OnlyLive filters out trashed entries, which the API otherwise includes.
func OnlyLive() Filters { return NewFilters().Eq("trashed", false) }

// Starred narrows a listing to starred entries. Filtering on the `starred`
// field matches nothing; the server keys stars on the system tag.
func (f Filters) Starred() Filters { return f.Set("system_tag", "in", starTag) }

// ListFilesParams selects a page of a folder, or of the whole tree.
type ListFilesParams struct {
	// ParentID is the folder id. Empty means the drive root; WholeTree walks
	// every folder in server sort-key order, ignoring Limit and Order.
	ParentID string
	// PageToken continues a previous page, or resumes a whole-tree walk from
	// SyncCursor. An unknown token is not an error: the server silently
	// starts over from the beginning.
	PageToken string
	// Limit is clamped to MaxListLimit.
	Limit int
	// Filters narrows the result server-side.
	Filters Filters
	// Order is one of the Order* constants; ignored in whole-tree mode.
	Order string
	// ThumbnailSize selects the thumbnail variant. Empty means none.
	ThumbnailSize ThumbnailSize
	// WithAudit asks for audit state alongside each entry.
	WithAudit bool
}

// ListFiles returns one page of entries.
func (c *Client) ListFiles(ctx context.Context, params ListFilesParams) (*FileList, error) {
	query := url.Values{}
	query.Set("parent_id", params.ParentID)
	if params.PageToken != "" {
		query.Set("page_token", params.PageToken)
	}

	limit := params.Limit
	if limit <= 0 || limit > MaxListLimit {
		limit = MaxListLimit
	}
	query.Set("limit", strconv.Itoa(limit))

	filters, err := params.Filters.encode()
	if err != nil {
		return nil, err
	}
	if filters != "" {
		query.Set("filters", filters)
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	if params.ThumbnailSize != "" {
		query.Set("thumbnail_size", string(params.ThumbnailSize))
	}
	if params.WithAudit {
		query.Set("with_audit", "true")
	}

	var out FileList
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/files", query: query}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetFileParams controls what a single-file read returns.
type GetFileParams struct {
	Usage         Usage
	ThumbnailSize ThumbnailSize
}

// GetFile reads one entry. Pass UsageFetch or UsagePlay to have the server
// mint download links.
func (c *Client) GetFile(ctx context.Context, id string, params GetFileParams) (*File, error) {
	if id == "" {
		return nil, errors.New("drive: file id is required")
	}
	query := url.Values{}
	if params.Usage != "" {
		query.Set("usage", string(params.Usage))
	}
	if params.ThumbnailSize != "" {
		query.Set("thumbnail_size", string(params.ThumbnailSize))
	}

	var out File
	path := "/drive/v1/files/" + url.PathEscape(id)
	if err := c.do(ctx, request{method: http.MethodGet, path: path, query: query}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BatchGetFiles reads several entries in one round trip. The server caps the
// request at 100 ids.
func (c *Client) BatchGetFiles(ctx context.Context, ids []string, params GetFileParams) ([]File, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query := url.Values{}
	for _, id := range ids {
		query.Add("ids", id)
	}
	if params.Usage != "" {
		query.Set("usage", string(params.Usage))
	}
	if params.ThumbnailSize != "" {
		query.Set("thumbnail_size", string(params.ThumbnailSize))
	}

	var out FileList
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/files:batchGet", query: query}, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// CreateFolder makes a folder under parentID (empty for the root).
func (c *Client) CreateFolder(ctx context.Context, parentID, name string) (*File, error) {
	if name == "" {
		return nil, errors.New("drive: folder name is required")
	}
	body := map[string]any{"kind": KindFolder, "name": name}
	if parentID != "" {
		body["parent_id"] = parentID
	}

	var out struct {
		File File `json:"file"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, path: "/drive/v1/files", body: body}, &out); err != nil {
		return nil, err
	}
	if out.File.ID == "" {
		return nil, errors.New("drive: folder create returned no id")
	}
	return &out.File, nil
}

// EnsureFolder is CreateFolder that returns the existing folder when one of
// that name is already under parentID, instead of making a numbered copy.
// Folder uploads use it so re-uploading a tree merges into the first one.
func (c *Client) EnsureFolder(ctx context.Context, parentID, name string) (*File, error) {
	if name == "" {
		return nil, errors.New("drive: folder name is required")
	}
	body := map[string]any{"kind": KindFolder, "name": name, "ignore_duplicated_name": true}
	if parentID != "" {
		body["parent_id"] = parentID
	}
	var out struct {
		File File `json:"file"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, path: "/drive/v1/files", body: body}, &out); err != nil {
		return nil, err
	}
	if out.File.ID == "" {
		return nil, errors.New("drive: folder create returned no id")
	}
	return &out.File, nil
}

// Rename changes the display name of one entry.
func (c *Client) Rename(ctx context.Context, id, name string) (*File, error) {
	if id == "" || name == "" {
		return nil, errors.New("drive: id and name are required")
	}
	var out File
	path := "/drive/v1/files/" + url.PathEscape(id)
	if err := c.do(ctx, request{method: http.MethodPatch, path: path, body: map[string]any{"name": name}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Trash moves entries to the recycle bin, where they stay recoverable.
func (c *Client) Trash(ctx context.Context, ids []string) error {
	_, err := c.BatchTrash(ctx, ids)
	return err
}

// BatchTrash is Trash that returns the server task carrying it out: the
// entries reach the bin over the next seconds, not when the call returns.
func (c *Client) BatchTrash(ctx context.Context, ids []string) (string, error) {
	return c.batchTask(ctx, "/drive/v1/files:batchTrash", ids, nil)
}

// Untrash restores entries from the recycle bin.
func (c *Client) Untrash(ctx context.Context, ids []string) error {
	_, err := c.BatchUntrash(ctx, ids)
	return err
}

// BatchUntrash is Untrash that returns the server task carrying it out. An
// entry whose name is taken in its old folder by then comes back renamed
// ("name(1)").
func (c *Client) BatchUntrash(ctx context.Context, ids []string) (string, error) {
	return c.batchTask(ctx, "/drive/v1/files:batchUntrash", ids, nil)
}

// Delete removes entries permanently.
func (c *Client) Delete(ctx context.Context, ids []string) error {
	return c.batchIDs(ctx, "/drive/v1/files:batchDelete", ids, nil)
}

// Star marks entries as favourites.
func (c *Client) Star(ctx context.Context, ids []string) error {
	return c.batchIDs(ctx, "/drive/v1/files:star", ids, nil)
}

// Unstar clears the favourite mark.
func (c *Client) Unstar(ctx context.Context, ids []string) error {
	return c.batchIDs(ctx, "/drive/v1/files:unstar", ids, nil)
}

// Move relocates entries under parentID.
func (c *Client) Move(ctx context.Context, ids []string, parentID string) error {
	_, err := c.BatchMove(ctx, ids, parentID)
	return err
}

// BatchMove is Move that returns the server task carrying it out: entries
// change parent one by one over the next seconds. An entry whose name is
// taken where it lands is moved anyway and renamed ("name(1)").
func (c *Client) BatchMove(ctx context.Context, ids []string, parentID string) (string, error) {
	return c.batchTask(ctx, "/drive/v1/files:batchMove", ids, map[string]any{"parent_id": parentID})
}

// Copy duplicates entries under parentID.
func (c *Client) Copy(ctx context.Context, ids []string, parentID string) error {
	return c.batchIDs(ctx, "/drive/v1/files:batchCopy", ids, map[string]any{"parent_id": parentID})
}

func (c *Client) batchIDs(ctx context.Context, path string, ids []string, to map[string]any) error {
	if len(ids) == 0 {
		return errors.New("drive: at least one id is required")
	}
	body := map[string]any{"ids": ids}
	if to != nil {
		body["to"] = to
	}
	return c.do(ctx, request{method: http.MethodPost, path: path, body: body}, nil)
}

// batchTask is batchIDs for the calls the server answers with a task id
// and completes in the background (GetTask follows it).
func (c *Client) batchTask(ctx context.Context, path string, ids []string, to map[string]any) (string, error) {
	if len(ids) == 0 {
		return "", errors.New("drive: at least one id is required")
	}
	body := map[string]any{"ids": ids}
	if to != nil {
		body["to"] = to
	}
	var out struct {
		TaskID string `json:"task_id"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, path: path, body: body}, &out); err != nil {
		return "", err
	}
	return out.TaskID, nil
}

// Quota is one metered storage or playback budget.
type Quota struct {
	Kind           string `json:"kind"`
	Limit          Int64  `json:"limit"`
	Usage          Int64  `json:"usage"`
	UsageInTrash   Int64  `json:"usage_in_trash"`
	PlayTimesLimit Int64  `json:"play_times_limit"`
	PlayTimesUsage Int64  `json:"play_times_usage"`
	IsUnlimited    bool   `json:"is_unlimited"`
}

// About is the account's storage picture.
type About struct {
	Kind      string           `json:"kind"`
	Quota     Quota            `json:"quota"`
	ExpiresAt string           `json:"expires_at"`
	Quotas    map[string]Quota `json:"quotas"`
}

// About reads the account quota.
func (c *Client) About(ctx context.Context) (*About, error) {
	var out About
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/about"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
