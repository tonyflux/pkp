package services

import (
	"context"
	"strings"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
)

// ShareSummary is one row of my shares, flattened for display.
type ShareSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	FileID    string `json:"fileId"`
	Folder    bool   `json:"folder"`
	FileSize  int64  `json:"fileSize"`
	FileNum   int64  `json:"fileNum"`
	// Status is "active", "expired", "canceled" or "blocked"; StatusText is
	// the server's own wording when it sent one.
	Status     string `json:"status"`
	StatusText string `json:"statusText"`
	Views      int64  `json:"views"`
	Saves      int64  `json:"saves"`
	CreatedAt  string `json:"createdAt"`
	// ExpiresAt is RFC3339, empty for a share that never expires; DaysLeft
	// is -1 in that case.
	ExpiresAt string `json:"expiresAt"`
	DaysLeft  int64  `json:"daysLeft"`
	URL       string `json:"url"`
	PassCode  string `json:"passCode"`
}

// SharePage is one page of ListShares.
type SharePage struct {
	Shares        []ShareSummary `json:"shares"`
	NextPageToken string         `json:"nextPageToken"`
}

// ShareRequest configures CreateShare.
type ShareRequest struct {
	FileIDs []string `json:"fileIds"`
	// ExpirationDays: 0 means never; ExpiresAt (RFC3339) wins when set.
	ExpirationDays int    `json:"expirationDays"`
	ExpiresAt      string `json:"expiresAt"`
	PassCode       bool   `json:"passCode"`
	// CustomPassCode sets the access code. Empty lets the server mint one
	// when PassCode is set.
	CustomPassCode string `json:"customPassCode"`
}

// CreatedShare is the result of CreateShare: the link, and the code when one
// was requested.
type CreatedShare struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	PassCode string `json:"passCode"`
}

// ListShares returns one page of the shares this account created.
func (d *DriveService) ListShares(pageToken string) (SharePage, error) {
	list, err := call(d, func(ctx context.Context, client *drive.Client) (*drive.ShareList, error) {
		return client.ListShares(ctx, drive.ListSharesParams{PageToken: pageToken, Limit: drive.MaxListLimit, ThumbnailSize: drive.ThumbnailMedium})
	})
	if err != nil {
		return SharePage{}, err
	}
	page := SharePage{Shares: make([]ShareSummary, 0, len(list.Data)), NextPageToken: list.NextPageToken}
	for _, s := range list.Data {
		page.Shares = append(page.Shares, toShareSummary(s))
	}
	return page, nil
}

// CreateShare publishes a link for the given entries.
func (d *DriveService) CreateShare(req ShareRequest) (CreatedShare, error) {
	if len(req.FileIDs) == 0 {
		return CreatedShare{}, i18n.New("没有选择要分享的文件")
	}
	code, err := sharePassCode(req.CustomPassCode)
	if err != nil {
		return CreatedShare{}, err
	}
	created, err := call(d, func(ctx context.Context, client *drive.Client) (*drive.CreatedShare, error) {
		return client.CreateShare(ctx, drive.CreateShareParams{
			FileIDs:        req.FileIDs,
			ExpirationDays: req.ExpirationDays,
			ExpirationAt:   req.ExpiresAt,
			PassCode:       req.PassCode,
			CustomPassCode: code,
		})
	})
	if err != nil {
		return CreatedShare{}, err
	}
	return CreatedShare{ID: created.ShareID, URL: created.ShareURL, PassCode: created.PassCode}, nil
}

// sharePassCode trims a caller-supplied access code. Empty means the server
// mints one. A set code must be 4 letters or digits: every code this client
// has seen the server return is that shape.
func sharePassCode(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if code == "" {
		return "", nil
	}
	if len(code) != 4 || strings.IndexFunc(code, func(r rune) bool {
		return r < '0' || (r > '9' && r < 'A') || (r > 'Z' && r < 'a') || r > 'z'
	}) >= 0 {
		return "", i18n.New("提取码须为 4 位字母或数字")
	}
	return code, nil
}

// CancelShares stops share links; their URLs stop working at once.
func (d *DriveService) CancelShares(shareIDs []string) error {
	return d.do(func(ctx context.Context, client *drive.Client) error {
		return client.DeleteShares(ctx, shareIDs)
	})
}

func toShareSummary(s drive.Share) ShareSummary {
	out := ShareSummary{
		ID:         s.ShareID,
		Title:      s.Title,
		Thumbnail:  s.ThumbnailLink,
		FileID:     s.FileID,
		Folder:     s.FileKind == drive.KindFolder,
		FileSize:   int64(s.FileSize),
		FileNum:    int64(s.FileNum),
		StatusText: s.ShareStatusText,
		Views:      int64(s.ViewCount),
		Saves:      int64(s.RestoreCount),
		CreatedAt:  s.CreateTime,
		DaysLeft:   -1,
		URL:        s.ShareURL,
		PassCode:   s.PassCode,
	}
	if out.Title == "" {
		out.Title = s.Params["name"]
	}
	switch s.ShareStatus {
	case drive.ShareStatusOK:
		out.Status = "active"
	case drive.ShareStatusExpired:
		out.Status = "expired"
	case drive.ShareStatusDeleted:
		out.Status = "canceled"
	default:
		out.Status = "blocked"
	}
	// The server sends "-1" for a share that never expires.
	if s.ExpirationAt != "" && s.ExpirationAt != "-1" {
		out.ExpiresAt = s.ExpirationAt
		out.DaysLeft = int64(s.ExpirationLeft)
	}
	return out
}

/* ---------------- connections ---------------- */

// WebDAVClient is one credential issued for a WebDAV client. Password is
// what the client types; the UI masks it until the user asks to see it.
type WebDAVClient struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	CreatedAt      string `json:"createdAt"`
	LastActiveAt   string `json:"lastActiveAt"`
	LastActiveIP   string `json:"lastActiveIp"`
	ReadOnly       bool   `json:"readOnly"`
	MediaOptimized bool   `json:"mediaOptimized"`
	MaxEntries     int    `json:"maxEntries"`
}

// WebDAVInfo is the account's WebDAV state. Endpoint is where clients mount
// the drive; LegacyEndpoint is the older host, for clients that cannot reach
// the unified one.
type WebDAVInfo struct {
	Enabled        bool           `json:"enabled"`
	Premium        bool           `json:"premium"`
	Endpoint       string         `json:"endpoint"`
	LegacyEndpoint string         `json:"legacyEndpoint"`
	Clients        []WebDAVClient `json:"clients"`
}

// WebDAVClientOptions are the per-client settings UpdateWebDAVClient writes.
type WebDAVClientOptions struct {
	Name           string `json:"name"`
	ReadOnly       bool   `json:"readOnly"`
	MediaOptimized bool   `json:"mediaOptimized"`
	MaxEntries     int    `json:"maxEntries"`
}

// WebDAV reads the WebDAV status and issued clients.
func (d *DriveService) WebDAV() (WebDAVInfo, error) {
	status, err := call(d, func(ctx context.Context, client *drive.Client) (*drive.WebDAVStatus, error) {
		return client.ListWebDAV(ctx)
	})
	if err != nil {
		return WebDAVInfo{}, err
	}
	info := WebDAVInfo{
		Enabled:        status.Enabled,
		Premium:        status.IsPremium,
		Endpoint:       drive.WebDAVEndpoint,
		LegacyEndpoint: drive.WebDAVLegacyEndpoint,
		Clients:        make([]WebDAVClient, 0, len(status.Applications)),
	}
	for _, a := range status.Applications {
		info.Clients = append(info.Clients, toWebDAVClient(a))
	}
	return info, nil
}

// SetWebDAVEnabled switches the WebDAV endpoint for the whole account.
func (d *DriveService) SetWebDAVEnabled(enabled bool) error {
	return d.do(func(ctx context.Context, client *drive.Client) error {
		return client.SetWebDAVEnabled(ctx, enabled)
	})
}

// AddWebDAVClient issues a new credential named for the client it is for.
func (d *DriveService) AddWebDAVClient(name string) (WebDAVClient, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return WebDAVClient{}, i18n.New("请填写客户端名称")
	}
	app, err := call(d, func(ctx context.Context, client *drive.Client) (*drive.WebDAVApplication, error) {
		return client.CreateWebDAVApplication(ctx, name)
	})
	if err != nil {
		return WebDAVClient{}, err
	}
	return toWebDAVClient(*app), nil
}

// UpdateWebDAVClient changes a credential's name and options.
func (d *DriveService) UpdateWebDAVClient(username, password string, opts WebDAVClientOptions) error {
	return d.do(func(ctx context.Context, client *drive.Client) error {
		return client.UpdateWebDAVApplication(ctx, username, password, drive.WebDAVOptions{
			ApplicationName:       strings.TrimSpace(opts.Name),
			MaxEntriesInResponse:  opts.MaxEntries,
			OptimizedForMediaPlay: opts.MediaOptimized,
			ReadOnly:              opts.ReadOnly,
		})
	})
}

// RenewWebDAVPassword replaces a credential's password and returns the new one.
func (d *DriveService) RenewWebDAVPassword(username, password string) (WebDAVClient, error) {
	app, err := call(d, func(ctx context.Context, client *drive.Client) (*drive.WebDAVApplication, error) {
		return client.RenewWebDAVPassword(ctx, username, password)
	})
	if err != nil {
		return WebDAVClient{}, err
	}
	return toWebDAVClient(*app), nil
}

// RemoveWebDAVClient revokes a credential.
func (d *DriveService) RemoveWebDAVClient(username, password string) error {
	return d.do(func(ctx context.Context, client *drive.Client) error {
		return client.DeleteWebDAVApplication(ctx, username, password)
	})
}

func toWebDAVClient(a drive.WebDAVApplication) WebDAVClient {
	return WebDAVClient{
		ID:             int64(a.ID),
		Name:           a.ApplicationName,
		Username:       a.Username,
		Password:       a.Password,
		CreatedAt:      a.CreatedAt,
		LastActiveAt:   a.LastActiveAt,
		LastActiveIP:   a.LastActiveIP,
		ReadOnly:       a.ReadOnly != 0,
		MediaOptimized: a.OptimizedForMediaPlay != 0,
		MaxEntries:     int(a.MaxEntriesInResponse),
	}
}

// AppSession is one authorised session of a connected app.
type AppSession struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	UserAgent    string `json:"userAgent"`
	Scopes       string `json:"scopes"`
	CreatedAt    string `json:"createdAt"`
	LastActiveAt string `json:"lastActiveAt"`
	LastActiveIP string `json:"lastActiveIp"`
	ExpiresAt    string `json:"expiresAt"`
	Revoked      bool   `json:"revoked"`
}

// ConnectedApp groups the sessions of one client: "mcp", "pat", "cli",
// "webdav" or "third_party".
type ConnectedApp struct {
	Type     string       `json:"type"`
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Icon     string       `json:"icon"`
	Sessions []AppSession `json:"sessions"`
}

// ConnectedApps lists every client that holds a session on the account.
func (d *DriveService) ConnectedApps() ([]ConnectedApp, error) {
	clients, err := call(d, func(ctx context.Context, client *drive.Client) ([]drive.ConnectedClient, error) {
		return client.ListClients(ctx)
	})
	if err != nil {
		return nil, err
	}
	out := make([]ConnectedApp, 0, len(clients))
	for _, c := range clients {
		app := ConnectedApp{Type: appType(c.ClientType), ID: c.ClientID, Name: c.ClientName, Icon: c.Icon, Sessions: make([]AppSession, 0, len(c.Sessions))}
		for _, s := range c.Sessions {
			app.Sessions = append(app.Sessions, AppSession{
				ID:           s.SessionID,
				Label:        s.LabelName,
				UserAgent:    s.UserAgent,
				Scopes:       s.Scopes,
				CreatedAt:    s.CreatedAt,
				LastActiveAt: s.LastActiveAt,
				LastActiveIP: s.LastActiveIP,
				ExpiresAt:    s.ExpiresAt,
				Revoked:      s.Revoked,
			})
		}
		out = append(out, app)
	}
	return out, nil
}

// Profile fetches the display name and avatar of the account on screen, and
// keeps them with the account for the switcher.
func (d *DriveService) Profile() (*drive.Profile, error) {
	s, err := d.active()
	if err != nil {
		return nil, err
	}
	p, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.Profile, error) {
		return client.Me(ctx)
	})
	if err != nil {
		return nil, err
	}
	d.noteProfile(s, p)
	return p, nil
}

// RevokeSessions ends sessions of connected apps: they lose access to the
// account at once and have to be connected again from the app. The server
// removes what it can; whatever it kept is reported as one error.
func (d *DriveService) RevokeSessions(sessionIDs []string) error {
	failures, err := call(d, func(ctx context.Context, client *drive.Client) ([]drive.RevokeFailure, error) {
		return client.RevokeSessions(ctx, sessionIDs)
	})
	if err != nil {
		return err
	}
	if len(failures) > 0 {
		d.log.Warn("drive: sessions not removed", "failed", len(failures), "of", len(sessionIDs), "first", failures[0].Error)
		return i18n.Errorf("有 %d 个会话（共 %d 个）没有移除", len(failures), len(sessionIDs))
	}
	return nil
}

// AccessTokenOptions describe a personal access token to issue.
type AccessTokenOptions struct {
	Label string `json:"label"`
	// Scopes are the server's scope names, e.g. "files:read" or "share".
	Scopes []string `json:"scopes"`
	// Lifetime is in seconds.
	Lifetime int64 `json:"lifetime"`
}

// AccessToken is a freshly issued personal access token. Token is shown this
// once: the server lists only the session afterwards.
type AccessToken struct {
	Token     string `json:"token"`
	Scopes    string `json:"scopes"`
	ExpiresAt string `json:"expiresAt"`
}

// CreateAccessToken issues a personal access token for a CLI, a script or an
// MCP client that cannot sign in through the browser.
func (d *DriveService) CreateAccessToken(opts AccessTokenOptions) (AccessToken, error) {
	label := strings.TrimSpace(opts.Label)
	if label == "" {
		return AccessToken{}, i18n.New("请填写令牌名称")
	}
	tok, err := call(d, func(ctx context.Context, client *drive.Client) (*drive.PersonalAccessToken, error) {
		return client.CreatePersonalAccessToken(ctx, label, opts.Scopes, time.Duration(opts.Lifetime)*time.Second)
	})
	if err != nil {
		return AccessToken{}, err
	}
	return toAccessToken(tok, strings.Join(opts.Scopes, " ")), nil
}

// RegenerateAccessToken replaces the token of a personal-token session with a
// new one; everything still using the old token stops working. A zero
// lifetime keeps the old token's.
func (d *DriveService) RegenerateAccessToken(sessionID string, lifetime int64) (AccessToken, error) {
	tok, err := call(d, func(ctx context.Context, client *drive.Client) (*drive.PersonalAccessToken, error) {
		return client.RegeneratePersonalAccessToken(ctx, sessionID, time.Duration(lifetime)*time.Second)
	})
	if err != nil {
		return AccessToken{}, err
	}
	return toAccessToken(tok, ""), nil
}

// toAccessToken turns the lifetime the server counts from now into a
// deadline. requested stands in for the scopes when the answer names none.
func toAccessToken(tok *drive.PersonalAccessToken, requested string) AccessToken {
	out := AccessToken{Token: tok.AccessToken, Scopes: tok.Scope}
	if out.Scopes == "" {
		out.Scopes = requested
	}
	if tok.ExpiresIn > 0 {
		out.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return out
}

func appType(clientType string) string {
	switch clientType {
	case "mcp", "cli", "webdav":
		return clientType
	case "personal_access_token", "pat":
		return "pat"
	}
	return "third_party"
}
