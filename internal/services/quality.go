package services

import (
	"slices"
	"strconv"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
)

// A video plays as one of its renditions: the original file, or one of the
// transcodes the server made of it. Measured on the production API
// (2026-10-12): a transcoded video has the original plus 1080P, 720P and 480P
// HEVC MPEG-TS transcodes, each a plain progressive link on the same CDN as
// the original; the 1080P transcode is the server's is_default. Small or odd
// files have the original only, sometimes without any picture metadata.

// Quality choices. A rendition's key is OriginKey for the original and its
// tier ("1080", "720", …) for a transcode; AutoQuality leaves it to the player.
const (
	AutoQuality = "auto"
	OriginKey   = "origin"
)

// Quality is one rendition the playing video can switch to.
type Quality struct {
	// Key names it to SetPlaybackQuality: "origin", or the tier for a
	// transcode. There is at most one transcode per tier.
	Key string `json:"key"`
	// Origin marks the untranscoded file.
	Origin bool `json:"origin"`
	// Tier is the resolution class: 2160, 1440, 1080, 720, 480, or the
	// short side below that; 0 when the server did not describe the picture.
	Tier    int   `json:"tier"`
	Width   int   `json:"width"`
	Height  int   `json:"height"`
	BitRate int64 `json:"bitRate"`
}

// QualityState is what the player knows about the renditions of the video
// it plays. It travels inside PlayerState, so every window agrees on it.
type QualityState struct {
	// Options are the renditions, the original first, then the transcodes
	// from the highest tier down. Fewer than two leave nothing to choose.
	Options []Quality `json:"options"`
	// Current is the key of the rendition playing; "" when nothing plays.
	Current string `json:"current"`
	// Choice is the saved preference: "auto", "origin" or a tier.
	Choice string `json:"choice"`
	// Switching is the key being loaded in place of Current, "" otherwise.
	Switching string `json:"switching"`
	// Failed is the key whose switch failed last and Error says why;
	// playback stayed on Current. Both clear on the next switch or file.
	Failed string `json:"failed"`
	Error  string `json:"error"`
	// Lowered is set while Current is a step down auto took because
	// playback kept stalling.
	Lowered bool `json:"lowered"`
}

// rendition is a Quality with the link that plays it and the server's id of
// that media ("" for an original reached through its download link).
type rendition struct {
	Quality
	url     string
	mediaID string
}

// tierSteps are the classes and, for each, the short or long side that
// reaches it. Either side counts: a cinema crop (1920×800) is still 1080p,
// 4:3 (1440×1080) too, and a portrait picture classes by its width.
var tierSteps = []struct{ tier, short, long int }{
	{2160, 2160, 3840},
	{1440, 1440, 2560},
	{1080, 1080, 1920},
	{720, 720, 1280},
	{480, 480, 852},
}

// tierOf is the resolution class of a picture. The server's own
// resolution_name is not used: it names a 960×1280 portrait transcode and a
// 640×852 one both "480P".
func tierOf(width, height int) int {
	short, long := min(width, height), max(width, height)
	if short <= 0 {
		return 0
	}
	for _, s := range tierSteps {
		if short >= s.short || long >= s.long {
			return s.tier
		}
	}
	return short
}

// renditionsOf lists what a file plays as. The original always heads the
// list: through its media link when the account may stream it, through the
// download link otherwise — the same bytes either way. Then one transcode per
// tier the account may use (the server sometimes sends two of a tier; the
// richer one stays), highest first. A transcode without picture metadata
// cannot be named, so it is left out.
func renditionsOf(f drive.File) []rendition {
	var out []rendition
	origin := rendition{Quality: Quality{Key: OriginKey, Origin: true}}
	transcodes := map[int]rendition{}
	for _, m := range f.Medias {
		q := Quality{Width: m.Video.Width, Height: m.Video.Height, BitRate: int64(m.Video.BitRate)}
		q.Tier = tierOf(q.Width, q.Height)
		usable := m.Link.URL != "" && !m.NeedMoreQuota
		if m.IsOrigin {
			q.Key, q.Origin = OriginKey, true
			origin.Quality = q
			if usable {
				origin.url, origin.mediaID = m.Link.URL, m.MediaID
			}
			continue
		}
		if !usable || !m.IsVisible || q.Tier == 0 {
			continue
		}
		q.Key = strconv.Itoa(q.Tier)
		if have, ok := transcodes[q.Tier]; !ok || q.BitRate > have.BitRate {
			transcodes[q.Tier] = rendition{Quality: q, url: m.Link.URL, mediaID: m.MediaID}
		}
	}
	if origin.url == "" {
		origin.url = f.ContentLink()
	}
	if origin.url != "" {
		out = append(out, origin)
	}
	tiers := make([]int, 0, len(transcodes))
	for tier := range transcodes {
		tiers = append(tiers, tier)
	}
	slices.Sort(tiers)
	slices.Reverse(tiers)
	for _, tier := range tiers {
		out = append(out, transcodes[tier])
	}
	return out
}

func qualitiesOf(rs []rendition) []Quality {
	out := make([]Quality, len(rs))
	for i, r := range rs {
		out[i] = r.Quality
	}
	return out
}

// validChoice reports whether s is a preference SetPlaybackQuality accepts.
func validChoice(s string) bool {
	if s == AutoQuality || s == OriginKey {
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && n > 0 && strconv.Itoa(n) == s
}

// pick resolves a preference against one video's renditions and returns the
// key to play. Auto and "origin" play the original, the best there is; auto
// steps down from there only when playback stalls. A tier plays the richest
// rendition at or under it — a transcode before an original of the same
// tier, the original when it is the one that fits — and the smallest one
// when every rendition is bigger, so a data-saving choice never plays more
// than it has to.
func pick(options []Quality, choice string) string {
	if len(options) == 0 {
		return ""
	}
	limit, err := strconv.Atoi(choice)
	if err != nil {
		for _, o := range options {
			if o.Origin {
				return o.Key
			}
		}
		return options[0].Key
	}
	best, smallest := -1, -1
	for i, o := range options {
		if o.Tier == 0 {
			continue
		}
		if smallest < 0 || o.Tier < options[smallest].Tier {
			smallest = i
		}
		if o.Tier > limit {
			continue
		}
		if best < 0 || o.Tier > options[best].Tier || (o.Tier == options[best].Tier && options[best].Origin) {
			best = i
		}
	}
	switch {
	case best >= 0:
		return options[best].Key
	case smallest >= 0:
		return options[smallest].Key
	default:
		return options[0].Key
	}
}

// lower is the rendition auto steps down to from current: the next one in
// the list that carries less data, "" when current is already the lightest.
// An unknown bit rate (an original without metadata) counts as heavier.
func lower(options []Quality, current string) string {
	i := slices.IndexFunc(options, func(o Quality) bool { return o.Key == current })
	if i < 0 {
		return ""
	}
	from := options[i].BitRate
	for _, o := range options[i+1:] {
		if from <= 0 || o.BitRate <= 0 || o.BitRate < from {
			return o.Key
		}
	}
	return ""
}

func find(rs []rendition, key string) (rendition, bool) {
	i := slices.IndexFunc(rs, func(r rendition) bool { return r.Key == key })
	if i < 0 {
		return rendition{}, false
	}
	return rs[i], true
}

// errSwitchBusy refuses a second switch while one is still loading.
var errSwitchBusy = i18n.New("正在切换清晰度，请等这一次完成")

// SetPlaybackQuality saves the rendition preference — "auto", "origin" or a
// tier such as "720" — and applies it to the video playing now, from the
// same position: volume, speed, the chosen audio and subtitle tracks and the
// queue carry over. It returns once the new rendition is loading; the
// outcome arrives in PlayerState.Quality, and a failed switch leaves the
// previous rendition playing. The saved preference picks the rendition of
// every video played after this one.
func (d *DriveService) SetPlaybackQuality(choice string) error {
	if !validChoice(choice) {
		return i18n.Errorf("不支持的清晰度「%s」", choice)
	}
	if err := d.updateSettings(func(s *LocalSettings) { s.PlaybackQuality = choice }); err != nil {
		return err
	}
	if d.player == nil {
		return nil
	}
	account, fileID, q := d.player.qualityNow()
	if fileID == "" {
		return nil
	}
	if q.Switching != "" {
		return errSwitchBusy
	}
	target := pick(q.Options, choice)
	if target == q.Current {
		// Nothing to reload: auto while the original plays, or the tier a
		// lowered auto already landed on.
		d.player.setQualityChoice(fileID, choice)
		return nil
	}
	return d.switchQuality(account, fileID, choice, target, false)
}

// autoStepDown is the player's stall handler: auto moves to the next lighter
// rendition, keeping the position. A fixed choice is left alone — the user
// asked for that rendition.
func (d *DriveService) autoStepDown() {
	account, fileID, q := d.player.qualityNow()
	if fileID == "" || q.Choice != AutoQuality || q.Switching != "" {
		return
	}
	next := lower(q.Options, q.Current)
	if next == "" {
		return
	}
	if err := d.switchQuality(account, fileID, AutoQuality, next, true); err != nil {
		d.log.Warn("player: auto step down failed", "to", next, "error", err)
	}
}

// switchQuality reads fresh links — the ones from the start of playback may
// have expired — through the account the video belongs to, which need not
// be the one on screen, and hands the target rendition to the player.
func (d *DriveService) switchQuality(account, fileID, choice, key string, lowered bool) error {
	ticket, err := d.player.beginSwitch(fileID, choice, key, lowered)
	if err != nil {
		return err
	}
	file, err := d.ownerFile(account, fileID)
	if err != nil {
		d.player.failSwitch(ticket, key, err)
		return err
	}
	options := renditionsOf(*file)
	target, ok := find(options, key)
	if !ok {
		err := i18n.New("云盘已经不再提供这个清晰度")
		d.player.failSwitch(ticket, key, err)
		return err
	}
	return d.player.switchTo(ticket, target.url, d.mediaHeaders(), key, qualitiesOf(options), lowered)
}

// ownerFile reads the playing video's links through its account.
func (d *DriveService) ownerFile(account, fileID string) (*drive.File, error) {
	s := d.byKey(account)
	if s == nil {
		return nil, errNoSuchAccount
	}
	return d.getFile(s, fileID, drive.GetFileParams{Usage: drive.UsagePlay})
}
