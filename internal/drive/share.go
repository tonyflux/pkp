package drive

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// Share status values the server reports for a share the account created.
const (
	ShareStatusOK      = "OK"
	ShareStatusExpired = "EXPIRED"
	ShareStatusDeleted = "DELETED"
)

// Pass-code options for CreateShare.
const (
	PassCodeRequired    = "REQUIRED"
	PassCodeNotRequired = "NOT_REQUIRED"
)

// ShareUser is the owner block the server attaches to a share.
type ShareUser struct {
	UserID string `json:"user_id"`
	Name   string `json:"nickname"`
}

// Share is one row of the account's share list (ShareMgrData).
type Share struct {
	ShareID         string            `json:"share_id"`
	ShareStatus     string            `json:"share_status"`
	ShareStatusText string            `json:"share_status_text"`
	Title           string            `json:"title"`
	IconLink        string            `json:"icon_link"`
	ThumbnailLink   string            `json:"thumbnail_link"`
	PassCode        string            `json:"pass_code"`
	FileNum         Int64             `json:"file_num"`
	RestoreLimit    Int64             `json:"restore_limit"`
	ExpirationDays  Int64             `json:"expiration_days"`
	ExpirationAt    string            `json:"expiration_at"`
	RestoreCount    Int64             `json:"restore_count"`
	ExpirationLeft  Int64             `json:"expiration_left"`
	ViewCount       Int64             `json:"view_count"`
	CreateTime      string            `json:"create_time"`
	UserInfo        ShareUser         `json:"user_info"`
	ShareURL        string            `json:"share_url"`
	FileID          string            `json:"file_id"`
	FileKind        string            `json:"file_kind"`
	FileSize        Int64             `json:"file_size"`
	ShareTo         string            `json:"share_to"`
	Params          map[string]string `json:"params"`
	AllowFollow     bool              `json:"allow_follow"`
}

// ShareList is one page of ListShares.
type ShareList struct {
	Data          []Share `json:"data"`
	NextPageToken string  `json:"next_page_token"`
}

// ListSharesParams pages the account's shares.
type ListSharesParams struct {
	PageToken     string
	Limit         int
	ThumbnailSize ThumbnailSize
}

// ListShares returns one page of the shares this account created.
func (c *Client) ListShares(ctx context.Context, params ListSharesParams) (*ShareList, error) {
	query := url.Values{}
	if params.PageToken != "" {
		query.Set("page_token", params.PageToken)
	}
	limit := params.Limit
	if limit <= 0 || limit > MaxListLimit {
		limit = MaxListLimit
	}
	query.Set("limit", strconv.Itoa(limit))
	if params.ThumbnailSize != "" {
		query.Set("thumbnail_size", string(params.ThumbnailSize))
	}
	var out ShareList
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/share/list", query: query}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateShareParams configures a new share link.
type CreateShareParams struct {
	FileIDs []string
	// ExpirationDays is the lifetime in days; 0 or negative means never.
	// ExpirationAt, an RFC3339 instant, takes precedence when set.
	ExpirationDays int
	ExpirationAt   string
	// PassCode asks the server to mint an access code; CustomPassCode sets
	// one instead when non-empty.
	PassCode       bool
	CustomPassCode string
	Title          string
}

// CreatedShare is what the server returns for a new share.
type CreatedShare struct {
	ShareID   string `json:"share_id"`
	ShareURL  string `json:"share_url"`
	PassCode  string `json:"pass_code"`
	ShareText string `json:"share_text"`
}

// CreateShare publishes a share link for the given entries. The body
// follows what the official web client sends: share_to names the link
// kind, which must agree with the pass-code option; expiration_days is a
// number with -1 for never, replaced by expiration_at for a custom date.
func (c *Client) CreateShare(ctx context.Context, params CreateShareParams) (*CreatedShare, error) {
	if len(params.FileIDs) == 0 {
		return nil, errors.New("drive: at least one file id is required")
	}
	passCode, shareTo := PassCodeNotRequired, "publiclink"
	if params.PassCode || params.CustomPassCode != "" {
		passCode, shareTo = PassCodeRequired, "encryptedlink"
	}
	body := map[string]any{
		"file_ids":         params.FileIDs,
		"share_to":         shareTo,
		"pass_code_option": passCode,
	}
	switch {
	case params.ExpirationAt != "":
		body["expiration_at"] = params.ExpirationAt
	case params.ExpirationDays > 0:
		body["expiration_days"] = params.ExpirationDays
	default:
		body["expiration_days"] = -1
	}
	if params.CustomPassCode != "" {
		body["custom_pass_code"] = params.CustomPassCode
	}
	if params.Title != "" {
		body["title"] = params.Title
	}
	var out CreatedShare
	if err := c.do(ctx, request{method: http.MethodPost, path: "/drive/v1/share", body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteShares cancels share links; their URLs stop working.
func (c *Client) DeleteShares(ctx context.Context, shareIDs []string) error {
	if len(shareIDs) == 0 {
		return errors.New("drive: at least one share id is required")
	}
	return c.do(ctx, request{method: http.MethodPost, path: "/drive/v1/share:batchDelete", body: map[string]any{"ids": shareIDs}}, nil)
}
