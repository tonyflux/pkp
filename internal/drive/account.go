package drive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// TransferUsage is one metered axis of a transfer allowance, in bytes. A zero
// Total means the axis carries no cap this period.
type TransferUsage struct {
	Total Int64 `json:"total_assets"`
	Used  Int64 `json:"assets"`
}

// TransferAxes are what a transfer allowance meters: cloud downloads (offline
// tasks), downstream traffic, uploads, and the daily downstream cap that only
// accounts without a plan carry.
type TransferAxes struct {
	CloudDownload   TransferUsage `json:"offline"`
	Downstream      TransferUsage `json:"download"`
	Upload          TransferUsage `json:"upload"`
	DailyDownstream TransferUsage `json:"download_daily"`
}

// SessionTransfer is what one session of a connected client drew this month.
// For WebDAV the session is a credential, named by its username.
type SessionTransfer struct {
	SessionID string `json:"session_id"`
	TransferAxes
}

// ClientTransfer is what one connected client drew this month, in total and
// per session.
type ClientTransfer struct {
	ClientID string `json:"client_id"`
	TransferAxes
	Sessions []SessionTransfer `json:"sessions"`
}

// TransferQuota is the account's transfer allowance for the calendar month:
// the plan's base allowance, purchased extras, the share connected apps may
// draw on, and what each connected client drew. Only clients that drew
// something are listed.
type TransferQuota struct {
	Base struct {
		TransferAxes
		VIPStatus  string `json:"vip_status"`
		ExpireTime string `json:"expire_time"`
	} `json:"base"`
	Extra   TransferAxes     `json:"transfer"`
	Apps    TransferAxes     `json:"apps_summary"`
	Clients []ClientTransfer `json:"clients"`
}

// TransferQuota reads this month's transfer allowance and usage. limit=1 keeps
// the purchase history the same endpoint pages through down to one entry.
func (c *Client) TransferQuota(ctx context.Context) (*TransferQuota, error) {
	var out TransferQuota
	query := url.Values{"type": {"transfer"}, "limit": {"1"}}
	if err := c.do(ctx, request{method: http.MethodGet, path: "/vip/v1/quantity/list", query: query}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VIPItem is one membership an account holds or held: Global Premium or
// Regional Premium. Status is "ok" while it runs; SurplusDay goes negative
// once it has ended.
type VIPItem struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Expire      string `json:"expire"`
	SurplusDay  int    `json:"surplus_day"`
}

// VIPInfo is the account's membership. Status "ok" means one of the items
// is running and Expire is when the last running one ends. Restricted is
// set when the server limits the membership where the account is used, and
// says why in Content.Text.
type VIPInfo struct {
	Status     string    `json:"status"`
	Type       string    `json:"type"`
	Expire     string    `json:"expire"`
	Items      []VIPItem `json:"vipItem"`
	Restricted struct {
		Result  bool `json:"result"`
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"restricted"`
}

// VIPInfo reads the account's membership.
func (c *Client) VIPInfo(ctx context.Context) (*VIPInfo, error) {
	var out struct {
		Data VIPInfo `json:"data"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, path: "/vip/v1/vip/info"}, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// AcceptInvite asks the server to bind the account to the inviter behind
// code (POST /vip/v1/activity/invite).
func (c *Client) AcceptInvite(ctx context.Context, code string) error {
	body := map[string]any{"apk_extra": map[string]string{"invite_code": code}}
	return c.do(ctx, request{method: http.MethodPost, path: "/vip/v1/activity/invite", body: body}, nil)
}

// UserSettings reads account preferences that every PikPak client shares.
// Values come back as text — "true"/"false" for switches, a mode name
// otherwise — and items the server does not know are absent.
func (c *Client) UserSettings(ctx context.Context, items ...string) (map[string]string, error) {
	query := url.Values{}
	for _, item := range items {
		query.Add("items", item)
	}
	var out struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, path: "/user/v1/settings", query: query}, &out); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(out.Settings))
	for item, raw := range out.Settings {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			// A bare true or 10: the text is the value.
			text = string(raw)
		}
		values[item] = text
	}
	return values, nil
}

// SetUserSetting writes one account preference. The server keeps every value
// as text, so switches are "true" or "false".
func (c *Client) SetUserSetting(ctx context.Context, item, value string) error {
	body := map[string]string{"item": item, "value": value}
	return c.do(ctx, request{method: http.MethodPost, path: "/user/v1/settings", body: body}, nil)
}
