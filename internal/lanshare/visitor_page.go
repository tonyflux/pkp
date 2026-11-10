package lanshare

import "context"

func (m *Manager) visitorEntries(ctx context.Context, s spaceRecord, parent, token string) ([]Entry, string, error) {
	if s.Kind == "cloud" {
		if cloud, ok := m.cloud.(interface {
			VisitorPage(context.Context, string, string, string, string, string) ([]Entry, string, error)
		}); ok {
			entries, next, err := cloud.VisitorPage(ctx, s.AccountID, s.RootID, parent, s.AccountIdentity, token)
			if err != nil {
				return nil, "", sourceError(err)
			}
			if len(entries) > 10000 || next != "" && next == token {
				return nil, "", errInvalid
			}
			for i := range entries {
				if entries[i].IsDir {
					continue
				}
				entries[i].HasThumbnail = entries[i].HasThumbnail || entries[i].Thumbnail != ""
				m.decorate(&entries[i], s.ID)
				if !entries[i].Ready {
					entries[i].Ready = m.preparedReady(s.ID, entries[i])
				}
				if entries[i].Ready {
					entries[i].NeedsVerification = false
				}
			}
			if _, err := m.space(s.ID, false); err != nil {
				return nil, "", err
			}
			if entries == nil {
				entries = []Entry{}
			}
			return entries, next, nil
		}
	}
	if token != "" {
		return nil, "", errInvalid
	}
	entries, err := m.list(ctx, s.ID, parent)
	return entries, "", err
}
