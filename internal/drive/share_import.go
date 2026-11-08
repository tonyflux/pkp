package drive

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ParseShareURL accepts only PikPak's HTTPS share links, never a redirect or
// user-controlled API host. Subpaths are discarded; an access code is retained.
func ParseShareURL(raw string) (shareID, canonicalURL, passCode string, err error) {
	invalid := errors.New("drive: invalid PikPak share URL")
	if len(raw) == 0 || len(raw) > 8192 || strings.ContainsAny(raw, "\\") || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || invalidShareText(raw) {
		return "", "", "", invalid
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.RawPath != "" {
		return "", "", "", invalid
	}
	host := strings.ToLower(u.Host)
	if host != "mypikpak.com" && host != "www.mypikpak.com" {
		return "", "", "", invalid
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "" || parts[1] != "s" {
		return "", "", "", invalid
	}
	for _, part := range parts[2:] {
		if !ValidShareID(part) {
			return "", "", "", invalid
		}
	}
	query, parseErr := url.ParseQuery(u.RawQuery)
	if parseErr != nil {
		return "", "", "", invalid
	}
	for key, values := range query {
		if invalidShareText(key) {
			return "", "", "", invalid
		}
		for _, value := range values {
			if invalidShareText(value) {
				return "", "", "", invalid
			}
		}
	}
	codes, passwords := query["pass_code"], query["password"]
	if len(codes) > 1 || len(passwords) > 1 || (len(codes) == 1 && len(passwords) == 1 && codes[0] != passwords[0]) {
		return "", "", "", invalid
	}
	passCode = query.Get("pass_code")
	if len(codes) == 0 {
		passCode = query.Get("password")
	}
	if len(passCode) > 128 {
		return "", "", "", invalid
	}
	shareID = parts[2]
	canonicalURL = "https://mypikpak.com/s/" + shareID
	if passCode != "" {
		canonicalURL += "?" + url.Values{"pass_code": {passCode}}.Encode()
	}
	return shareID, canonicalURL, passCode, nil
}

// ValidShareID validates opaque IDs before they enter paths, trace lists or API
// queries. The same alphabet is used by share, file and restore task IDs.
func ValidShareID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func invalidShareText(value string) bool {
	return !utf8.ValidString(value) || strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
	}) >= 0
}

func validShareCapability(value string) bool {
	return len(value) <= 8192 && !invalidShareText(value)
}

const (
	SharePassCodeEmpty   = "PASS_CODE_EMPTY"
	SharePassCodeError   = "PASS_CODE_ERROR"
	ShareRestoreComplete = "RESTORE_COMPLETE"
	ShareRestoreStart    = "RESTORE_START"
	ShareRestoreUnknown  = "RESTORE_UNKNOWN"
)

// SharedFiles is one anonymous page; its token is a share capability, not an
// account credential. The header endpoint also supplies title and owner.
type SharedFiles struct {
	ShareID       string    `json:"share_id"`
	Status        string    `json:"share_status"`
	StatusText    string    `json:"share_status_text"`
	Title         string    `json:"title"`
	UserInfo      ShareUser `json:"user_info"`
	FileNum       Int64     `json:"file_num"`
	Files         []File    `json:"files"`
	NextPageToken string    `json:"next_page_token"`
	PassCodeToken string    `json:"pass_code_token"`
}

type InspectShareParams struct {
	ShareID       string
	PassCode      string
	PassCodeToken string
	PageToken     string
}

// InspectShare reads a share header and its root page without account auth.
// The root endpoint accepts the pass-code capability when continuing a page.
func (c *Client) InspectShare(ctx context.Context, params InspectShareParams) (*SharedFiles, error) {
	if !ValidShareID(params.ShareID) || len(params.PassCode) > 128 || invalidShareText(params.PassCode) || !validShareCapability(params.PassCodeToken) || !validShareCapability(params.PageToken) {
		return nil, errors.New("drive: invalid share request")
	}
	query := sharePageQuery(params.ShareID, params.PassCodeToken, params.PageToken)
	if params.PassCode != "" {
		query.Set("pass_code", params.PassCode)
	}
	var out SharedFiles
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/share", query: query, anonymous: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type ListSharedFilesParams struct {
	ShareID       string
	PassCodeToken string
	ParentID      string
	PageToken     string
}

// ListSharedFiles reads a folder (or the root) with the share capability.
func (c *Client) ListSharedFiles(ctx context.Context, params ListSharedFilesParams) (*SharedFiles, error) {
	if !ValidShareID(params.ShareID) || (params.ParentID != "" && !ValidShareID(params.ParentID)) || !validShareCapability(params.PassCodeToken) || !validShareCapability(params.PageToken) {
		return nil, errors.New("drive: invalid share folder request")
	}
	query := sharePageQuery(params.ShareID, params.PassCodeToken, params.PageToken)
	query.Set("parent_id", params.ParentID)
	var out SharedFiles
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/share/detail", query: query, anonymous: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func sharePageQuery(shareID, token, pageToken string) url.Values {
	query := url.Values{"share_id": {shareID}, "limit": {"100"}, "thumbnail_size": {string(ThumbnailLarge)}}
	if token != "" {
		query.Set("pass_code_token", token)
	}
	if pageToken != "" {
		query.Set("page_token", pageToken)
	}
	return query
}

// SharedFile is the anonymous file-info response, including expiry/code gates.
type SharedFile struct {
	Status     string `json:"share_status"`
	StatusText string `json:"share_status_text"`
	FileInfo   File   `json:"file_info"`
}

func (c *Client) GetSharedFile(ctx context.Context, shareID, passCodeToken, fileID string) (*SharedFile, error) {
	if !ValidShareID(shareID) || !ValidShareID(fileID) || !validShareCapability(passCodeToken) {
		return nil, errors.New("drive: invalid shared file request")
	}
	query := url.Values{"share_id": {shareID}, "file_id": {fileID}, "pass_code_token": {passCodeToken}}
	var out SharedFile
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/share/file_info", query: query, anonymous: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RestoreShareParams always names an explicit account-owned destination; an
// empty ParentID means its root, not a server-chosen download folder.
type RestoreShareParams struct {
	ShareID       string
	PassCodeToken string
	ParentID      string
	FileIDs       []string
}

type ShareRestoreReceipt struct {
	Status      string            `json:"restore_status"`
	ShareStatus string            `json:"share_status"`
	StatusText  string            `json:"share_status_text"`
	ParentID    string            `json:"file_id"`
	TaskID      string            `json:"restore_task_id"`
	Params      map[string]string `json:"params"`
}

// RestoreShare is a single authenticated mutation. No folder is created first.
func (c *Client) RestoreShare(ctx context.Context, params RestoreShareParams) (*ShareRestoreReceipt, error) {
	if !ValidShareID(params.ShareID) || (params.ParentID != "" && !ValidShareID(params.ParentID)) || !validShareCapability(params.PassCodeToken) || len(params.FileIDs) == 0 || len(params.FileIDs) > 1000 {
		return nil, errors.New("drive: invalid share restore request")
	}
	seen := make(map[string]bool, len(params.FileIDs))
	for _, id := range params.FileIDs {
		if !ValidShareID(id) || seen[id] {
			return nil, errors.New("drive: invalid or duplicate selected share file")
		}
		seen[id] = true
	}
	body := map[string]any{
		"parent_id":         params.ParentID,
		"share_id":          params.ShareID,
		"pass_code_token":   params.PassCodeToken,
		"file_ids":          params.FileIDs,
		"ancestor_ids":      []string{},
		"specify_parent_id": true,
		"params":            map[string]string{"trace_file_ids": strings.Join(params.FileIDs, ",")},
	}
	var out ShareRestoreReceipt
	if err := c.do(ctx, request{method: http.MethodPost, path: "/drive/v1/share/restore", body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
