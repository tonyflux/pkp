package drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// The WebDAV mount points clients connect to with an issued credential. They
// are not the management host (Config.DAVBaseURL): that one only issues and
// edits credentials. The legacy host keeps serving clients set up before the
// unified one existed.
const (
	WebDAVEndpoint       = "https://dav.pikpak.ai"
	WebDAVLegacyEndpoint = "https://dav.mypikpak.com"
)

// WebDAVApplication is one WebDAV client credential the account issued.
// The password is returned by the management API; callers decide how much
// of it to show.
type WebDAVApplication struct {
	ID                    Int64  `json:"id"`
	UserID                string `json:"user_id"`
	Username              string `json:"username"`
	Password              string `json:"password"`
	ApplicationName       string `json:"application_name"`
	CreatedAt             string `json:"created_at"`
	LastActiveAt          string `json:"last_active_at"`
	LastActiveIP          string `json:"last_active_ip"`
	MaxEntriesInResponse  Int64  `json:"max_entries_in_response"`
	OptimizedForMediaPlay Int64  `json:"optimized_for_media_play"`
	ReadOnly              Int64  `json:"read_only"`
}

// WebDAVStatus is the account's WebDAV picture: whether the feature is on,
// whether the plan allows it, and the issued clients.
type WebDAVStatus struct {
	Enabled      bool                `json:"webdav_enable"`
	IsPremium    bool                `json:"is_premium"`
	Applications []WebDAVApplication `json:"applications"`
}

// ListWebDAV reads the WebDAV status and clients.
func (c *Client) ListWebDAV(ctx context.Context) (*WebDAVStatus, error) {
	var out WebDAVStatus
	if err := c.do(ctx, request{method: http.MethodGet, path: "/webdav/v1/applications", base: c.dav}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetWebDAVEnabled turns the WebDAV endpoint on or off for the account.
func (c *Client) SetWebDAVEnabled(ctx context.Context, enabled bool) error {
	return c.do(ctx, request{method: http.MethodPost, path: "/webdav/v1/toggle-enable", base: c.dav, body: map[string]any{"enable": enabled}}, nil)
}

// CreateWebDAVApplication issues a new client credential.
func (c *Client) CreateWebDAVApplication(ctx context.Context, name string) (*WebDAVApplication, error) {
	if name == "" {
		return nil, errors.New("drive: application name is required")
	}
	var out WebDAVApplication
	if err := c.do(ctx, request{method: http.MethodPost, path: "/webdav/v1/application", base: c.dav, body: map[string]any{"application_name": name}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WebDAVOptions are the per-client settings UpdateWebDAVApplication writes.
type WebDAVOptions struct {
	ApplicationName       string
	MaxEntriesInResponse  int
	OptimizedForMediaPlay bool
	ReadOnly              bool
}

// UpdateWebDAVApplication changes a client's name and options. The server
// identifies the client by its username and password pair. The flags are
// booleans here even though the listing reports them as 0/1.
func (c *Client) UpdateWebDAVApplication(ctx context.Context, username, password string, opts WebDAVOptions) error {
	if username == "" || password == "" {
		return errors.New("drive: username and password are required")
	}
	body := map[string]any{
		"username": username,
		"password": password,
		"modify_props": map[string]any{
			"application_name":         opts.ApplicationName,
			"max_entries_in_response":  opts.MaxEntriesInResponse,
			"optimized_for_media_play": opts.OptimizedForMediaPlay,
			"read_only":                opts.ReadOnly,
		},
	}
	return c.do(ctx, request{method: http.MethodPatch, path: "/webdav/v1/application", base: c.dav, body: body}, nil)
}

// RenewWebDAVPassword replaces a client's password; clients still using the
// old one stop connecting.
func (c *Client) RenewWebDAVPassword(ctx context.Context, username, password string) (*WebDAVApplication, error) {
	if username == "" || password == "" {
		return nil, errors.New("drive: username and password are required")
	}
	var out WebDAVApplication
	if err := c.do(ctx, request{method: http.MethodPost, path: "/webdav/v1/renew-password", base: c.dav, body: map[string]any{"username": username, "password": password}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteWebDAVApplication revokes a client credential.
func (c *Client) DeleteWebDAVApplication(ctx context.Context, username, password string) error {
	if username == "" || password == "" {
		return errors.New("drive: username and password are required")
	}
	return c.do(ctx, request{method: http.MethodDelete, path: "/webdav/v1/application", base: c.dav, body: map[string]any{"username": username, "password": password}}, nil)
}

// ClientSession is one authorised session of a connected client: an MCP
// connection, a personal access token, a CLI login or a third-party app.
type ClientSession struct {
	SessionID    string         `json:"session_id"`
	JTI          string         `json:"jti"`
	DeviceID     string         `json:"device_id"`
	UserAgent    string         `json:"user_agent"`
	LabelName    string         `json:"label_name"`
	Scopes       string         `json:"scopes"`
	CreatedAt    string         `json:"created_at"`
	LastActiveAt string         `json:"last_active_at"`
	LastActiveIP string         `json:"last_active_ip"`
	ExpiresAt    string         `json:"expires_at"`
	RevokedAt    string         `json:"revoked_at"`
	Revoked      bool           `json:"revoked"`
	Status       string         `json:"status"`
	Ext          map[string]any `json:"ext"`
}

// ConnectedClient groups the sessions of one client type.
type ConnectedClient struct {
	ClientType string          `json:"client_type"`
	ClientID   string          `json:"client_id"`
	ClientName string          `json:"client_name"`
	Icon       string          `json:"icon"`
	Status     string          `json:"status"`
	Disabled   bool            `json:"disabled"`
	Suspended  bool            `json:"suspended"`
	Sessions   []ClientSession `json:"sessions"`
}

// ListClients reads every connected client and its sessions.
func (c *Client) ListClients(ctx context.Context) ([]ConnectedClient, error) {
	var out struct {
		Clients []ConnectedClient `json:"clients"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, path: "/session/v1/clients"}, &out); err != nil {
		return nil, err
	}
	return out.Clients, nil
}

// maxRevokeBatch is the most sessions one revocation request may name.
const maxRevokeBatch = 50

// RevokeFailure is a session the server did not revoke, and why.
type RevokeFailure struct {
	SessionID string `json:"session_id"`
	Error     string `json:"error"`
}

// RevokeSessions ends sessions of connected clients — an MCP connection, a
// personal access token, a CLI login or a third-party app — so they stop
// reaching the account at once. The server removes what it can and reports
// the rest, so a partial success returns its failures with a nil error.
func (c *Client) RevokeSessions(ctx context.Context, sessionIDs []string) ([]RevokeFailure, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	if len(sessionIDs) > maxRevokeBatch {
		return nil, fmt.Errorf("drive: at most %d sessions per revocation", maxRevokeBatch)
	}
	var out struct {
		Failures []RevokeFailure `json:"failures"`
	}
	body := map[string]any{"session_ids": sessionIDs}
	if err := c.do(ctx, request{method: http.MethodPost, path: "/session/v1/clients:batchDelete", body: body}, &out); err != nil {
		return nil, err
	}
	return out.Failures, nil
}

// PersonalAccessToken is a long-lived token as it is issued. The token itself
// comes back this once; afterwards only its session is listed.
type PersonalAccessToken struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   Int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

// CreatePersonalAccessToken issues a long-lived token for scripts, CLIs and
// unattended automation. The scopes must lie within what the server allows a
// personal token, and the lifetime within its ceiling.
func (c *Client) CreatePersonalAccessToken(ctx context.Context, label string, scopes []string, lifetime time.Duration) (*PersonalAccessToken, error) {
	if label == "" {
		return nil, errors.New("drive: a token label is required")
	}
	if len(scopes) == 0 {
		return nil, errors.New("drive: a token needs at least one scope")
	}
	body := map[string]any{"label": label, "scopes": scopes, "expires_in": seconds(lifetime)}
	return c.issuePersonalAccessToken(ctx, "/v1/user/pat", body)
}

// RegeneratePersonalAccessToken issues a new token in place of the one a
// personal-token session holds; the old token stops working at once. A zero
// lifetime keeps the old token's.
func (c *Client) RegeneratePersonalAccessToken(ctx context.Context, sessionID string, lifetime time.Duration) (*PersonalAccessToken, error) {
	if sessionID == "" {
		return nil, errors.New("drive: a session id is required")
	}
	body := map[string]any{"session_id": sessionID}
	if lifetime > 0 {
		body["expires_in"] = seconds(lifetime)
	}
	return c.issuePersonalAccessToken(ctx, "/v1/user/pat/regenerate", body)
}

func (c *Client) issuePersonalAccessToken(ctx context.Context, path string, body map[string]any) (*PersonalAccessToken, error) {
	var out PersonalAccessToken
	if err := c.do(ctx, request{method: http.MethodPost, path: path, base: c.auth, body: body}, &out); err != nil {
		return nil, err
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("drive: POST %s issued no token", path)
	}
	return &out, nil
}

// seconds renders a lifetime the way the account service takes it: whole
// seconds, as a decimal string.
func seconds(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10)
}

// Profile is the account's public identity as the auth host reports it.
type Profile struct {
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// Me reads the signed-in account's display name and avatar URL.
func (c *Client) Me(ctx context.Context) (*Profile, error) {
	var out Profile
	if err := c.do(ctx, request{method: http.MethodGet, path: "/v1/user/me", base: c.auth}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
