package services

import (
	"context"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
)

/* ---------------- transfer allowance ---------------- */

// Usage is one metered axis of the transfer allowance, in bytes. A zero Limit
// means the axis carries no cap this period.
type Usage struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

// UsageAxes is what the transfer allowance meters.
type UsageAxes struct {
	CloudDownload   Usage `json:"cloudDownload"`
	Downstream      Usage `json:"downstream"`
	Upload          Usage `json:"upload"`
	DailyDownstream Usage `json:"dailyDownstream"`
}

// TransferQuota is this calendar month's transfer allowance and what was
// drawn from it.
type TransferQuota struct {
	Base  UsageAxes `json:"base"`
	Extra UsageAxes `json:"extra"`
	// Apps is the share of the allowance connected apps draw on together.
	Apps UsageAxes `json:"apps"`
	// Clients and Sessions hold what each connected client, and each of its
	// sessions, drew this month, keyed by client id and by session id — for
	// WebDAV, a credential's username. Whatever drew nothing is absent.
	Clients  map[string]UsageAxes `json:"clients"`
	Sessions map[string]UsageAxes `json:"sessions"`
}

// TransferQuota reads this month's transfer allowance and usage.
func (d *DriveService) TransferQuota() (TransferQuota, error) {
	q, err := call(d, func(ctx context.Context, client *drive.Client) (*drive.TransferQuota, error) {
		return client.TransferQuota(ctx)
	})
	if err != nil {
		return TransferQuota{}, err
	}
	out := TransferQuota{
		Base:     toUsageAxes(q.Base.TransferAxes),
		Extra:    toUsageAxes(q.Extra),
		Apps:     toUsageAxes(q.Apps),
		Clients:  make(map[string]UsageAxes, len(q.Clients)),
		Sessions: map[string]UsageAxes{},
	}
	for _, c := range q.Clients {
		out.Clients[c.ClientID] = toUsageAxes(c.TransferAxes)
		for _, s := range c.Sessions {
			out.Sessions[s.SessionID] = toUsageAxes(s.TransferAxes)
		}
	}
	return out, nil
}

func toUsageAxes(a drive.TransferAxes) UsageAxes {
	usage := func(u drive.TransferUsage) Usage { return Usage{Used: int64(u.Used), Limit: int64(u.Total)} }
	return UsageAxes{
		CloudDownload:   usage(a.CloudDownload),
		Downstream:      usage(a.Downstream),
		Upload:          usage(a.Upload),
		DailyDownstream: usage(a.DailyDownstream),
	}
}

/* ---------------- membership ---------------- */

// MembershipItem is one membership an account holds or held.
type MembershipItem struct {
	// Kind is "global" or "regional" (Global / Regional Premium); Name is
	// the server's own description, for a kind the page does not know.
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
	// Expire is when it ends or ended (RFC 3339); DaysLeft is the server's
	// count of days left, negative once it has ended.
	Expire   string `json:"expire"`
	DaysLeft int    `json:"daysLeft"`
}

// Membership is an account's PikPak membership.
type Membership struct {
	// Active is whether any membership is running; Expire is when the last
	// running one ends.
	Active bool             `json:"active"`
	Expire string           `json:"expire"`
	Items  []MembershipItem `json:"items"`
	// Notice is the server's word when it limits the membership where the
	// account is used; "" otherwise.
	Notice string `json:"notice"`
}

// Membership reads an account's membership ("" is the account on screen).
func (d *DriveService) Membership(account string) (Membership, error) {
	return d.membership(context.Background(), account)
}

func (d *DriveService) membership(ctx context.Context, account string) (Membership, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return Membership{}, err
	}
	var info *drive.VIPInfo
	err = d.doIn(ctx, s, func(ctx context.Context, client *drive.Client) error {
		var err error
		info, err = client.VIPInfo(ctx)
		return err
	})
	if err != nil {
		return Membership{}, err
	}
	out := Membership{Active: info.Status == "ok", Items: make([]MembershipItem, 0, len(info.Items))}
	if out.Active {
		out.Expire = info.Expire
	}
	if info.Restricted.Result {
		out.Notice = info.Restricted.Content.Text
	}
	for _, it := range info.Items {
		out.Items = append(out.Items, MembershipItem{
			Kind:     it.Type,
			Name:     it.Description,
			Active:   it.Status == "ok",
			Expire:   it.Expire,
			DaysLeft: it.SurplusDay,
		})
	}
	return out, nil
}

/* ---------------- transfer preferences ---------------- */

// TransferPreferences shape how cloud downloads land in the drive. They are
// stored with the account, so every PikPak client follows them.
type TransferPreferences struct {
	// SeparateAppFolders saves files added from another app (Telegram, …)
	// into a folder named after that app.
	SeparateAppFolders bool `json:"separateAppFolders"`
	// SmartFilter leaves out the files of a BT or magnet task that look like
	// advertising.
	SmartFilter bool `json:"smartFilter"`
	// AutoParseTorrent starts the files a torrent lists as soon as the
	// torrent file is added, instead of waiting for a selection.
	AutoParseTorrent bool `json:"autoParseTorrent"`
}

// transferSettings maps each TransferPreferences field, by its JSON name, to
// the account setting behind it and the values that setting stores for on
// and off. The filter is a mode rather than a switch on the server.
var transferSettings = map[string]struct{ item, on, off string }{
	"separateAppFolders": {"defaultFolderBasedOnPlatform", "true", "false"},
	"smartFilter":        {"filterFilesWhenCreate", "smart", "close"},
	"autoParseTorrent":   {"autoParseTorrent", "true", "false"},
}

// TransferPreferences reads the account's cloud-download preferences. An
// item the server leaves out is at the server's default: everything off but
// the filter.
func (d *DriveService) TransferPreferences() (TransferPreferences, error) {
	items := make([]string, 0, len(transferSettings))
	for _, s := range transferSettings {
		items = append(items, s.item)
	}
	values, err := call(d, func(ctx context.Context, client *drive.Client) (map[string]string, error) {
		return client.UserSettings(ctx, items...)
	})
	if err != nil {
		return TransferPreferences{}, err
	}
	is := func(name string) bool {
		s := transferSettings[name]
		if v, ok := values[s.item]; ok {
			return v == s.on
		}
		return name == "smartFilter"
	}
	return TransferPreferences{
		SeparateAppFolders: is("separateAppFolders"),
		SmartFilter:        is("smartFilter"),
		AutoParseTorrent:   is("autoParseTorrent"),
	}, nil
}

// SetTransferPreference switches one preference, named as its
// TransferPreferences JSON field.
func (d *DriveService) SetTransferPreference(name string, on bool) error {
	s, ok := transferSettings[name]
	if !ok {
		return i18n.Errorf("不支持的传输设置「%s」", name)
	}
	value := s.off
	if on {
		value = s.on
	}
	return d.do(func(ctx context.Context, client *drive.Client) error {
		return client.SetUserSetting(ctx, s.item, value)
	})
}
