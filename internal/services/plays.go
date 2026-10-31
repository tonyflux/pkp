package services

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// The resident column's Continue Watching list is two sources merged: the account's
// cloud play history, which the official clients write, and PKP's own record
// of what it played, kept per account in plays-<device>.json. PKP reports
// nothing back to the cloud; its own plays live only on this machine.

const (
	// maxPlays bounds the local record: the list shows a handful, and a
	// file that fell this far down is not what anyone resumes.
	maxPlays = 50
	// playSaveStep is how far playback moves before the position is
	// written again. Closing the app or opening another file also writes
	// it, so this only bounds what a crash loses.
	playSaveStep = 15.0
	// cloudPlayPage is the first page of cloud history Continue Watching asks for.
	cloudPlayPage = 20
	// historyPage is one page of the playback-history list.
	historyPage = 50
	// historyPages caps how far the full media-library history walks the cloud log.
	historyPages = 4
	// defaultRecentPlays is what RecentPlays returns for a limit of zero.
	defaultRecentPlays = 10
	// playsVersion is bumped when the file's meaning changes.
	playsVersion = 1
)

// RecentPlay is one entry of the Continue Watching list.
type RecentPlay struct {
	FileID    string `json:"fileId"`
	Name      string `json:"name"`
	Thumbnail string `json:"thumbnail"`
	// Position and Duration are in seconds. A position under a few seconds
	// or an unknown (zero) duration means there is no progress to show:
	// the UI lists the file as recently opened.
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	// PlayedAt is RFC 3339 UTC: when the file was last watched, by PKP or
	// by another client.
	PlayedAt string `json:"playedAt"`
	// Available is false when the file is known to be gone or in the
	// recycle bin; the UI then offers only Remove.
	Available bool `json:"available"`
	// Local is true when PKP holds its own record of the play, which is
	// what ForgetPlay removes. An entry only the cloud knows cannot be
	// removed from here.
	Local bool `json:"local"`
}

// PlayHistoryPage is one cloud history page merged with current local records.
type PlayHistoryPage struct {
	Account       string       `json:"account"`
	Plays         []RecentPlay `json:"plays"`
	NextPageToken string       `json:"nextPageToken"`
	// UnavailableIDs reserves gone files so older pages cannot revive them.
	UnavailableIDs []string `json:"unavailableIds"`
}

// playRecord is one video PKP played, as plays-<device>.json keeps it.
type playRecord struct {
	FileID    string  `json:"fileId"`
	Name      string  `json:"name"`
	Thumbnail string  `json:"thumbnail"`
	Position  float64 `json:"position"`
	Duration  float64 `json:"duration"`
	PlayedAt  string  `json:"playedAt"`
}

type playsFile struct {
	Version   int          `json:"version"`
	Plays     []playRecord `json:"plays"`
	ClearedAt string       `json:"clearedAt,omitempty"`
}

// playLog is one account's local play record, newest first. Every change is
// written through at once: changes come at most every playSaveStep of
// playback, and the file is a few kilobytes.
type playLog struct {
	// path is "" without a config dir: the record lives in memory only.
	path string
	log  *slog.Logger

	mu      sync.Mutex
	records []playRecord
	// cleared is when this computer last hid history, RFC 3339 UTC. Cloud
	// events at or before it stay on the server; they are not shown here.
	cleared string
}

// openPlayLog restores the record at path. A file that cannot be read is
// moved aside rather than overwritten, as the upload journal does.
func openPlayLog(path string, log *slog.Logger) *playLog {
	l := &playLog{path: path, log: log}
	if path == "" {
		return l
	}
	records, cleared, err := readPlays(path)
	if err != nil {
		log.Warn("drive: play record unreadable, starting empty", "path", path, "error", err)
		if err := os.Rename(path, path+".bad"); err != nil {
			log.Warn("drive: could not move the unreadable play record aside", "error", err)
		}
	}
	l.records = records
	l.cleared = cleared
	return l
}

func readPlays(path string) ([]playRecord, string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var f playsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, "", err
	}
	if f.Version != playsVersion {
		return nil, "", fmt.Errorf("plays version %d is not %d", f.Version, playsVersion)
	}
	if len(f.Plays) > maxPlays {
		f.Plays = f.Plays[:maxPlays]
	}
	return f.Plays, f.ClearedAt, nil
}

// list copies the records, newest first.
func (l *playLog) list() []playRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.records)
}

// opened records a file PKP has just started playing, moving it to the top.
func (l *playLog) opened(rec playRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = slices.DeleteFunc(l.records, func(r playRecord) bool { return r.FileID == rec.FileID })
	l.records = slices.Insert(l.records, 0, rec)
	if len(l.records) > maxPlays {
		l.records = l.records[:maxPlays]
	}
	return l.saveLocked()
}

// progress moves a file's record to where playback is now; saved says it
// was written. A file with no record — never opened through PKP, or
// forgotten while playing — is left alone: forgetting must stick.
func (l *playLog) progress(fileID string, position, duration float64, at time.Time) (saved bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := slices.IndexFunc(l.records, func(r playRecord) bool { return r.FileID == fileID })
	if i < 0 {
		return false, nil
	}
	rec := l.records[i]
	rec.Position = position
	if duration > 0 {
		rec.Duration = duration
	}
	rec.PlayedAt = at.UTC().Format(time.RFC3339)
	l.records = slices.Delete(l.records, i, i+1)
	l.records = slices.Insert(l.records, 0, rec)
	if err := l.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

// forget drops a file's record; a file without one is not an error.
func (l *playLog) forget(fileID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.records)
	l.records = slices.DeleteFunc(l.records, func(r playRecord) bool { return r.FileID == fileID })
	if len(l.records) != n {
		return l.saveLocked()
	}
	return nil
}

// snapshot keeps local records and their cloud cutoff from the same moment.
func (l *playLog) snapshot() ([]playRecord, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.records), playedAt(l.cleared)
}

// clear drops every local record and hides cloud events at or before at.
// Files and the cloud log are untouched.
func (l *playLog) clear(at time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	records, cleared := l.records, l.cleared
	l.records = nil
	l.cleared = at.UTC().Format(time.RFC3339Nano)
	if err := l.saveLocked(); err != nil {
		l.records, l.cleared = records, cleared
		return err
	}
	return nil
}

// saveLocked replaces the file atomically. Caller holds mu. The record in
// memory stands either way; the error says the disk does not have it.
func (l *playLog) saveLocked() error {
	if l.path == "" {
		return nil
	}
	data, err := json.Marshal(playsFile{Version: playsVersion, Plays: l.records, ClearedAt: l.cleared})
	if err == nil {
		tmp := l.path + ".tmp"
		if err = os.WriteFile(tmp, data, 0o600); err == nil {
			err = os.Rename(tmp, l.path)
		}
	}
	if err != nil {
		return i18n.Errorf("无法保存播放记录：%w", err)
	}
	return nil
}

// playTrack follows the drive file mpv is playing well enough to say where
// it was left. PlayerService keeps one under its mu.
type playTrack struct {
	// account is the account the file was opened for, whose play record
	// it belongs in.
	account            string
	fileID             string
	position, duration float64
	// valid is set once mpv has reported this file's own figures; before
	// that, the state still describes the file it replaced.
	valid bool
	// saved is the position last handed to onPlayback.
	saved float64
}

// observe takes a state snapshot of the file playing as fileID and reports
// whether the position is worth writing: it moved playSaveStep since the
// last write, or playback reached the end.
func (t *playTrack) observe(fileID string, s PlayerState) bool {
	if fileID == "" || fileID != t.fileID || s.Loading || s.Duration <= 0 {
		return false
	}
	t.position, t.duration, t.valid = s.Position, s.Duration, true
	if math.Abs(t.position-t.saved) >= playSaveStep || (s.EOF && t.position != t.saved) {
		t.saved = t.position
		return true
	}
	return false
}

// last is the latest valid sample; ok is false before there is one.
func (t *playTrack) last() (account, fileID string, position, duration float64, ok bool) {
	return t.account, t.fileID, t.position, t.duration, t.valid && t.fileID != ""
}

// end is last for a file that is going away, and forgets it.
func (t *playTrack) end() (account, fileID string, position, duration float64, ok bool) {
	account, fileID, position, duration, ok = t.last()
	*t = playTrack{}
	return account, fileID, position, duration, ok
}

// notePlayback is PlayerService's onPlayback: where a drive file was left,
// written into the record of the account it was opened for — never the one
// on screen, which may be another. An account removed meanwhile keeps
// nothing.
func (d *DriveService) notePlayback(account, fileID string, position, duration float64) {
	if _, err := d.writePlayback(account, fileID, position, duration); err != nil {
		d.log.Warn("drive: could not save where playback was left", "error", err)
	}
}

func (d *DriveService) writePlayback(account, fileID string, position, duration float64) (bool, error) {
	s := d.byKey(account)
	if s == nil {
		return false, nil
	}
	l := s.playLog()
	if l == nil {
		return false, nil
	}
	return l.progress(fileID, position, duration, time.Now())
}

// savePlaying writes where the playing file is now; saved is false when
// there is nothing to write. The periodic write may be up to playSaveStep
// behind, so this runs before playback stops for good.
func (d *DriveService) savePlaying() (saved bool, err error) {
	if d.player == nil {
		return false, nil
	}
	account, fileID, position, duration, ok := d.player.lastPlayed()
	if !ok {
		return false, nil
	}
	return d.writePlayback(account, fileID, position, duration)
}

// SavePlayback writes where the playing video is now into the play record
// of the account it belongs to, before the webview stops it — switching
// accounts pauses playback by default and says the position was kept. saved
// is false when there is nothing to keep: nothing plays, mpv has not
// reported a position yet, or the video's record was removed. An error
// means the position is not on disk; it is still in memory until quit.
func (d *DriveService) SavePlayback() (bool, error) {
	return d.savePlaying()
}

// RecentPlays lists up to limit videos of the account on screen to continue
// watching, newest first: the first page of the account's cloud play
// history merged with PKP's own record. Finished videos are left out. When
// the cloud cannot be reached the local record alone is returned; only
// with neither is it an error.
func (d *DriveService) RecentPlays(limit int) ([]RecentPlay, error) {
	if limit <= 0 {
		limit = defaultRecentPlays
	}
	s, err := d.active()
	if err != nil {
		return nil, err
	}
	plays, err := d.playsOf(s, max(limit, cloudPlayPage), 1)
	if err != nil {
		return nil, err
	}
	plays = slices.DeleteFunc(plays, func(p RecentPlay) bool { return finished(p.Position, p.Duration) })
	if len(plays) > limit {
		plays = plays[:limit]
	}
	return plays, nil
}

// playsOf is every play of account s known to pages of its cloud history
// (page events each) and to PKP's own record, merged and newest first,
// finished ones included. Cloud events at or before a local clear are left
// out. When the cloud cannot be reached the local record alone is returned;
// only with neither is it an error.
func (d *DriveService) playsOf(s *accountSession, page, pages int) ([]RecentPlay, error) {
	plays := s.playLog()
	events, err := d.playEvents(s, page, pages)
	if err != nil {
		if plays == nil {
			return nil, err
		}
		d.log.Warn("drive: cloud play history unavailable, showing local plays only", "error", err)
		events = nil
	}
	merged, _ := d.mergePlaysOf(s, events)
	return merged, nil
}

// mergePlaysOf reads local records after the cloud request so clears and
// playback progress made while it was in flight are kept.
func (d *DriveService) mergePlaysOf(s *accountSession, events []drive.Event) ([]RecentPlay, []drive.Event) {
	var local []playRecord
	var cleared time.Time
	if plays := s.playLog(); plays != nil {
		local, cleared = plays.snapshot()
	}
	if !cleared.IsZero() {
		events = slices.DeleteFunc(events, func(ev drive.Event) bool {
			return !playedAt(ev.UpdatedTime).After(cleared)
		})
	}
	lookup := func(fileID string) (index.Row, bool, bool) { return d.indexedFile(s, fileID) }
	return mergePlays(events, local, lookup), events
}

// playEvents reads up to pages of the cloud play log, newest first. A failure
// of the first page is returned; a later page keeps what already arrived.
func (d *DriveService) playEvents(s *accountSession, page, pages int) ([]drive.Event, error) {
	if pages < 1 {
		pages = 1
	}
	var out []drive.Event
	token := ""
	for i := range pages {
		list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.EventList, error) {
			return client.ListEvents(ctx, drive.ListEventsParams{
				Type:          drive.EventTypePlay,
				Limit:         page,
				ThumbnailSize: drive.ThumbnailMedium,
				PageToken:     token,
			})
		})
		if err != nil {
			if i == 0 {
				return nil, err
			}
			d.log.Warn("drive: later cloud play page unavailable", "error", err)
			return out, nil
		}
		if list == nil || len(list.Events) == 0 {
			return out, nil
		}
		out = append(out, list.Events...)
		if list.NextPageToken == "" || list.NextPageToken == token {
			return out, nil
		}
		token = list.NextPageToken
	}
	return out, nil
}

// indexedFile looks a file up in an account's index; ok is false when the
// index has not completed a walk and so cannot say that a file is gone.
func (d *DriveService) indexedFile(s *accountSession, fileID string) (row index.Row, found, ok bool) {
	idx := s.index()
	if idx == nil || !idx.syncer.Ready() {
		return index.Row{}, false, false
	}
	row, found, err := idx.store.Get(fileID)
	if err != nil {
		d.log.Warn("drive: index lookup failed", "file", fileID, "error", err)
		return index.Row{}, false, false
	}
	return row, found, true
}

// mergePlays joins cloud events and local records by file, the newest play
// of each winning, newest first. A cloud event whose file is gone or trashed
// is dropped, and marks a local record of the same file unavailable; a
// local-only record is checked against the index.
func mergePlays(events []drive.Event, local []playRecord, lookup func(string) (index.Row, bool, bool)) []RecentPlay {
	byID := map[string]*RecentPlay{}
	gone := map[string]bool{}
	for _, ev := range events {
		if ev.FileID == "" || byID[ev.FileID] != nil || gone[ev.FileID] {
			continue // newest first: a repeat is older
		}
		ref := ev.Reference
		if ref == nil || ref.Trashed {
			gone[ev.FileID] = true
			continue
		}
		position, duration := ev.Played()
		name := cmp.Or(ref.Name, ev.FileName)
		byID[ev.FileID] = &RecentPlay{
			FileID: ev.FileID, Name: name, Thumbnail: ref.ThumbnailLink,
			Position: position, Duration: duration,
			PlayedAt: utcTime(ev.UpdatedTime), Available: true,
		}
	}
	for _, rec := range local {
		if p := byID[rec.FileID]; p != nil {
			p.Local = true
			if playedAt(rec.PlayedAt).After(playedAt(p.PlayedAt)) {
				p.Position, p.PlayedAt = rec.Position, utcTime(rec.PlayedAt)
				if rec.Duration > 0 {
					p.Duration = rec.Duration
				}
			}
			continue
		}
		p := &RecentPlay{
			FileID: rec.FileID, Name: rec.Name, Thumbnail: rec.Thumbnail,
			Position: rec.Position, Duration: rec.Duration,
			PlayedAt: utcTime(rec.PlayedAt), Available: !gone[rec.FileID], Local: true,
		}
		if p.Available {
			if row, found, ok := lookup(rec.FileID); ok {
				p.Available = found && !row.Trashed
				if p.Available {
					// The index has the current name and a fresh thumbnail.
					p.Name = cmp.Or(row.Name, p.Name)
					p.Thumbnail = cmp.Or(row.ThumbnailLink, p.Thumbnail)
				}
			}
		}
		byID[rec.FileID] = p
	}

	out := make([]RecentPlay, 0, len(byID))
	for _, p := range byID {
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b RecentPlay) int {
		if c := playedAt(b.PlayedAt).Compare(playedAt(a.PlayedAt)); c != 0 {
			return c
		}
		return cmp.Compare(a.FileID, b.FileID)
	})
	return out
}

// finished says a video was watched to the end, credits included.
func finished(position, duration float64) bool {
	return duration > 0 && (position >= 0.98*duration || duration-position <= 10)
}

// playedAt parses an RFC 3339 stamp; the zero time sorts an unreadable one
// last.
func playedAt(stamp string) time.Time {
	t, _ := time.Parse(time.RFC3339, stamp)
	return t
}

// utcTime renders a stamp in UTC so cloud (+08:00) and local times read
// alike; one that does not parse is passed through.
func utcTime(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.UTC().Format(time.RFC3339)
}

// ForgetPlay removes PKP's own record of a file from the Continue Watching list of
// the account on screen. A play the cloud history holds stays there.
func (d *DriveService) ForgetPlay(fileID string) error {
	s, err := d.active()
	if err != nil {
		return err
	}
	l := s.playLog()
	if l == nil {
		return drive.ErrNoCredentials
	}
	return l.forget(fileID)
}

// LocalPlayHistory returns this account's local records, finished plays included,
// with current indexed availability and without contacting the cloud.
func (d *DriveService) LocalPlayHistory(account string) ([]RecentPlay, error) {
	if account == "" {
		return nil, errNoSuchAccount
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	plays, _ := d.mergePlaysOf(s, nil)
	return plays, nil
}

// PlayHistoryPage reads one cloud page for an explicit account and merges the
// latest local records. Cloud errors are returned so callers can keep their
// local snapshot visible while reporting a background sync failure.
func (d *DriveService) PlayHistoryPage(account, pageToken string) (PlayHistoryPage, error) {
	page := PlayHistoryPage{Account: account}
	if account == "" {
		return page, errNoSuchAccount
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return page, err
	}
	list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.EventList, error) {
		return client.ListEvents(ctx, drive.ListEventsParams{
			Type:          drive.EventTypePlay,
			Limit:         historyPage,
			ThumbnailSize: drive.ThumbnailMedium,
			PageToken:     pageToken,
		})
	})
	if err != nil {
		return page, err
	}
	var events []drive.Event
	if list != nil {
		events = list.Events
		page.NextPageToken = list.NextPageToken
	}
	page.Plays, events = d.mergePlaysOf(s, events)
	seen := make(map[string]bool, len(events))
	for _, ev := range events {
		if ev.FileID == "" || seen[ev.FileID] {
			continue
		}
		seen[ev.FileID] = true
		if ev.Reference == nil || ev.Reference.Trashed {
			page.UnavailableIDs = append(page.UnavailableIDs, ev.FileID)
		}
	}
	return page, nil
}

// ClearPlays forgets PKP's play record of the account on screen and hides
// cloud play events at or before now on this computer. Files are not deleted,
// and the cloud log other clients read is left as it is.
func (d *DriveService) ClearPlays() error {
	s, err := d.active()
	if err != nil {
		return err
	}
	l := s.playLog()
	if l == nil {
		return drive.ErrNoCredentials
	}
	return l.clear(time.Now())
}
