package services

import "github.com/tonyflux/pkp/internal/index"

// UsageBreakdown splits the account's usage by kind of file, as summed from
// the local index. It is only offered when it adds up to the usage the
// server reports, so the bar never shows segments that contradict the total.
type UsageBreakdown struct {
	Video    int64 `json:"video"`
	Image    int64 `json:"image"`
	Document int64 `json:"document"`
	// Other is everything else: audio, archives, unrecognised files.
	Other int64 `json:"other"`
	// SyncedAt is the RFC 3339 time of the index's last completed walk, the
	// moment the figures describe.
	SyncedAt string `json:"syncedAt"`
}

// usageSlack is the least drift between the index and the server tolerated,
// in bytes. The real account's index sums to within a few bytes of the
// server's usage; a file added or removed since the last walk moves it by
// far more than either bound.
const usageSlack = 1 << 20

// usageBreakdown sums the index by category for a quota whose usage the
// server reported. nil when the index has not completed a full walk, cannot
// be read, or disagrees with the server: the UI then shows used and free
// only. A failure here never fails Quota.
func (d *DriveService) usageBreakdown(s *accountSession, usage int64) *UsageBreakdown {
	idx := s.index()
	if idx == nil || !idx.syncer.Ready() {
		return nil
	}
	sums, err := idx.store.UsageByCategory()
	if err != nil {
		d.log.Warn("drive: usage breakdown unavailable", "error", err)
		return nil
	}
	syncedAt, err := idx.store.Meta(index.MetaSyncedAt)
	if err != nil {
		d.log.Warn("drive: usage breakdown unavailable", "error", err)
		return nil
	}
	b := breakdownOf(sums, usage, syncedAt)
	if b == nil {
		d.log.Info("drive: index and server usage disagree, breakdown withheld", "usage", usage)
	}
	return b
}

// breakdownOf buckets the index's per-category sums, or returns nil when
// their total is further from usage than a thousandth of it (and more than
// usageSlack): the index is out of step with the server.
func breakdownOf(sums map[string]int64, usage int64, syncedAt string) *UsageBreakdown {
	b := UsageBreakdown{SyncedAt: syncedAt}
	var total int64
	for category, size := range sums {
		total += size
		switch category {
		case "video":
			b.Video += size
		case "image":
			b.Image += size
		case "document":
			b.Document += size
		default:
			b.Other += size
		}
	}
	drift := total - usage
	if drift < 0 {
		drift = -drift
	}
	if drift > max(usage/1000, usageSlack) {
		return nil
	}
	return &b
}
