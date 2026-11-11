package services

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/stream"
)

// A quality switch is a reload of the same video from another rendition's
// link: mpv cannot change the source of an open file. loadfile's start=
// opens the new rendition where the old one was (exact for the original MP4
// and the MPEG-TS transcodes alike, measured on the production CDN), and
// volume, mute and speed are global properties that outlive the file. What
// does not carry over by itself is put back: the pause state, the audio and
// subtitle choice, and — until mpv reports them for the new file — the
// position, duration and size the UI is showing.

// switchHoldLimit bounds how long a switch's position stands in for mpv's.
// A link round trip and the open of a remote file take seconds, not the
// fraction of one a seek does.
const switchHoldLimit = 20 * time.Second

// The stall watch behind auto quality: auto steps down once playback has
// spent stallLimit of the last stallWindow waiting for data, not counting
// stalls that begin within stallGrace of a load or a seek — those wait for
// data by nature. Variables so the tests can run them faster.
var (
	stallGrace  = 3 * time.Second
	stallWindow = 30 * time.Second
	stallLimit  = 8 * time.Second
)

// swap is a rendition switch in flight. The rendition it replaces keeps its
// proxy handle (PlayerService.current) until the new one has loaded, so a
// failure can go back to it.
type swap struct {
	key     string
	handle  stream.Handle
	url     string
	lowered bool
	// Where and how playback stood when the switch began.
	position  float64
	paused    bool
	audio     trackRef
	sub       trackRef
	reverting bool
}

// heldFigures are the replaced rendition's duration and size, reported while
// mpv says 0 between the two files: a timeline that collapses for a second
// reads as a failure.
type heldFigures struct {
	duration      float64
	width, height int
}

// trackRef remembers a selected track well enough to find its counterpart in
// another rendition, whose track ids need not match. The zero value means
// unknown — the track list could not be read — and puts nothing back.
type trackRef struct {
	known bool
	off   bool // no track of the kind was on
	lang  string
	title string
	index int // position among the tracks of its type
}

func selectedTrack(tracks []Track, kind string) trackRef {
	n := 0
	for _, t := range tracks {
		if t.Type != kind {
			continue
		}
		if t.Selected {
			return trackRef{known: true, lang: t.Lang, title: t.Title, index: n}
		}
		n++
	}
	return trackRef{known: true, off: true}
}

func selectedID(tracks []Track, kind string) int {
	for _, t := range tracks {
		if t.Type == kind && t.Selected {
			return t.ID
		}
	}
	return 0
}

// matchTrack finds ref's counterpart: the same language and title, then the
// same language, then the same name however it is carried, then the same
// base language, then the same position. 0 when nothing fits. The middle
// two pair a file's own subtitle with its extracted copy on a transcode,
// which names it differently: "chi" titled Simplified Chinese subtitles inside the file,
// "chiSimplified" or plain "Simplified Chinese subtitles" as a language once extracted.
func matchTrack(tracks []Track, kind string, ref trackRef) int {
	var same []Track
	for _, t := range tracks {
		if t.Type == kind {
			same = append(same, t)
		}
	}
	if ref.lang != "" {
		for _, t := range same {
			if t.Lang == ref.lang && t.Title == ref.title {
				return t.ID
			}
		}
		for _, t := range same {
			if t.Lang == ref.lang {
				return t.ID
			}
		}
	}
	if name := cmp.Or(ref.title, ref.lang); name != "" {
		for _, t := range same {
			if cmp.Or(t.Title, t.Lang) == name {
				return t.ID
			}
		}
	}
	if base := baseLanguage(ref.lang); base != "" {
		for _, t := range same {
			if baseLanguage(t.Lang) == base {
				return t.ID
			}
		}
	}
	if ref.index < len(same) {
		return same[ref.index].ID
	}
	return 0
}

// baseLanguage is a language code without what follows it — "chi" for
// "chiSimplified", "zh" for "zh-Hans" — or "" when lang is not a code.
func baseLanguage(lang string) string {
	n := 0
	for n < len(lang) && lang[n] >= 'a' && lang[n] <= 'z' {
		n++
	}
	if n < 2 || n > 3 {
		return ""
	}
	if n < len(lang) && !(lang[n] >= 'A' && lang[n] <= 'Z') && lang[n] != '-' && lang[n] != '_' {
		return ""
	}
	return lang[:n]
}

// stallWatch measures how long playback has been waiting for data.
type stallWatch struct {
	quiet time.Time // stalls starting before this are a load's or a seek's
	since time.Time // start of the stall in progress; zero when none
	spans []stallSpan
	fired bool // this rendition already asked for a step down
}

type stallSpan struct{ start, end time.Time }

// reset starts over for a newly loaded rendition.
func (w *stallWatch) reset(now time.Time) { *w = stallWatch{quiet: now.Add(stallGrace)} }

// jumped excuses the wait a seek causes.
func (w *stallWatch) jumped(now time.Time) {
	w.quiet = now.Add(stallGrace)
	w.since = time.Time{}
}

// edge records mpv entering or leaving paused-for-cache.
func (w *stallWatch) edge(stalled bool, now time.Time) {
	if stalled {
		if !now.Before(w.quiet) {
			w.since = now
		}
		return
	}
	if !w.since.IsZero() {
		w.spans = append(w.spans, stallSpan{w.since, now})
		w.since = time.Time{}
	}
}

// stalled sums the waiting within the window that ends now. A stall still in
// progress counts unless the user paused: then nobody is waiting.
func (w *stallWatch) stalled(now time.Time, paused bool) time.Duration {
	from := now.Add(-stallWindow)
	var total time.Duration
	kept := w.spans[:0]
	for _, s := range w.spans {
		if s.end.Before(from) {
			continue
		}
		kept = append(kept, s)
		total += s.end.Sub(later(s.start, from))
	}
	w.spans = kept
	if !w.since.IsZero() && !paused {
		total += now.Sub(later(w.since, from))
	}
	return total
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// checkStall asks DriveService for a lighter rendition when auto playback
// has stalled enough. It runs on the emit loop's tick, once per rendition.
func (p *PlayerService) checkStall() {
	p.mu.Lock()
	q := p.state.Quality
	if p.onStall == nil || p.fileID == "" || q.Choice != AutoQuality || q.Switching != "" || p.swap != nil || p.stall.fired {
		p.mu.Unlock()
		return
	}
	if p.stall.stalled(time.Now(), p.state.Paused) < stallLimit {
		p.mu.Unlock()
		return
	}
	p.stall.fired = true
	onStall := p.onStall
	p.mu.Unlock()
	go onStall()
}

// qualityNow is the playing drive file, the account it belongs to and its
// quality state.
func (p *PlayerService) qualityNow() (account, fileID string, q QualityState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	q = p.state.Quality
	q.Options = slices.Clone(q.Options)
	return p.account, p.fileID, q
}

// setQualityChoice records a preference that needs no reload.
func (p *PlayerService) setQualityChoice(fileID, choice string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fileID != fileID {
		return
	}
	q := &p.state.Quality
	q.Choice = choice
	q.Lowered = false
	q.Failed, q.Error = "", ""
	p.dirty = true
}

// beginSwitch claims the one switch slot for fileID's video and says so, so
// the UI shows switching while the link is fetched. The ticket ties the rest of
// the switch to this video: opening another file or stopping voids it.
func (p *PlayerService) beginSwitch(fileID, choice, key string, lowered bool) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fileID == "" || p.fileID != fileID {
		return 0, i18n.New("这个视频已经不在播放了")
	}
	q := &p.state.Quality
	if q.Switching != "" || p.swap != nil || p.restoring != 0 {
		return 0, errSwitchBusy
	}
	p.gen++
	p.pendingLowered = lowered
	q.Choice = choice
	q.Switching = key
	q.Failed, q.Error = "", ""
	p.dirty = true
	return p.gen, nil
}

// failSwitch ends a switch that failed before mpv was involved; the
// rendition playing carries on untouched. Auto's own step downs fail quietly:
// the user asked for nothing.
func (p *PlayerService) failSwitch(ticket uint64, key string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ticket != p.gen {
		return
	}
	q := &p.state.Quality
	q.Switching = ""
	if !p.pendingLowered {
		q.Failed, q.Error = key, switchMessage(err)
	}
	p.dirty = true
}

// switchMessage is what quality says about a failed switch: the error when
// it is already in the page's words, else "" and the menu's own unable to open.
func switchMessage(err error) string {
	if strings.ContainsFunc(err.Error(), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		return err.Error()
	}
	return ""
}

// switchTo loads rawURL in place of the playing rendition, at the same
// position and pause state, keeping the audio and subtitle choice. It returns
// once mpv has the command: the event handler completes the switch when the
// new rendition has loaded, or goes back to the old one if it does not open.
func (p *PlayerService) switchTo(ticket uint64, rawURL string, headers map[string]string, key string, options []Quality, lowered bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	// From mpv itself, not the last emitted state, which may be 200ms old.
	var position float64
	positionErr := p.mpv.GetProperty(ctx, "time-pos", &position)
	var paused bool
	_ = p.mpv.GetProperty(ctx, "pause", &paused)
	var audio, sub trackRef
	if tracks, err := p.Tracks(); err == nil {
		audio, sub = selectedTrack(tracks, "audio"), selectedTrack(tracks, "sub")
	}

	handle, err := p.stream.Publish(stream.Source{URL: rawURL, Headers: headers})
	if err != nil {
		p.failSwitch(ticket, key, err)
		return err
	}

	// Between the ticket check and the loadfile no other load may run: a
	// file opened in between would be replaced by this video's rendition.
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	p.mu.Lock()
	if ticket != p.gen {
		p.mu.Unlock()
		p.stream.Unpublish(handle.ID)
		return i18n.New("切换时视频已经换了")
	}
	switch {
	case p.seek.pending:
		position = p.seek.target
	case positionErr != nil:
		position = p.state.Position
	}
	s := &swap{
		key: key, handle: handle, url: rawURL, lowered: lowered,
		position: position, paused: paused, audio: audio, sub: sub,
	}
	p.swap = s
	p.held = heldFigures{duration: p.state.Duration, width: p.state.Width, height: p.state.Height}
	p.state.Quality.Options = options
	p.seek = seekHold{pending: true, target: position, since: time.Now(), limit: switchHoldLimit}
	p.dirty = true
	p.mu.Unlock()

	start := "start=" + strconv.FormatFloat(position, 'f', 3, 64)
	if err := p.mpv.Command(ctx, "loadfile", handle.URL, "replace", "-1", start); err != nil {
		p.mu.Lock()
		if p.swap == s {
			p.swap = nil
			p.held = heldFigures{}
			p.seek.pending = false
		}
		p.mu.Unlock()
		p.stream.Unpublish(handle.ID)
		p.failSwitch(ticket, key, err)
		return err
	}
	if err := p.mpv.SetProperty(ctx, "pause", paused); err != nil {
		p.log.Warn("player: keep pause across a quality switch", "error", err)
	}
	return nil
}

// settleSwapLocked takes over the loaded rendition, or the old one after a
// fallback. The switch stays busy until its saved tracks are restored.
// Caller holds p.mu.
func (p *PlayerService) settleSwapLocked() {
	s := p.swap
	p.swap = nil
	if s.reverting {
		p.stream.Unpublish(s.handle.ID)
	} else {
		if p.current.ID != "" {
			p.stream.Unpublish(p.current.ID)
		}
		p.current = s.handle
		p.state.Source = s.url
		q := &p.state.Quality
		q.Current = s.key
		q.Lowered = s.lowered
		q.Failed, q.Error = "", ""
		p.stall.reset(time.Now())
	}
	// mpv must not be called from its own event goroutine. The extracted
	// subtitles go back first (a reload drops them), so the subtitle chosen
	// before the switch can be found among them.
	load, gen := p.loads, p.gen
	p.restoring = load
	go func() {
		p.loadMu.Lock()
		defer p.loadMu.Unlock()
		p.mu.Lock()
		current := p.loads == load && p.gen == gen
		p.mu.Unlock()
		if current {
			p.attachExtracted()
			p.restoreTracks(s.audio, s.sub)
		}
		p.mu.Lock()
		if p.restoring == load {
			p.restoring = 0
			if p.loads == load && p.gen == gen {
				p.state.Quality.Switching = ""
				p.dirty = true
			}
		}
		p.mu.Unlock()
	}()
}

// swapFailedLocked handles end-file with an error during a switch. The new
// rendition did not open: reload the old one where playback was. It reports
// false when there was no switch to fall back from — or the fallback itself
// failed — which makes the error the player's own. Caller holds p.mu.
func (p *PlayerService) swapFailedLocked() bool {
	s := p.swap
	if s == nil {
		return false
	}
	if s.reverting {
		p.dropSwapLocked()
		return false
	}
	s.reverting = true
	q := &p.state.Quality
	q.Switching = ""
	if !s.lowered {
		q.Failed, q.Error = s.key, i18n.T("无法打开")
	}
	target := s.position
	if p.seek.pending {
		target = p.seek.target
	}
	go p.revert(s, p.current.URL, target, s.paused)
	return true
}

// revert reloads the rendition a failed switch tried to replace — unless the
// video changed meanwhile, which leaves nothing to go back to.
func (p *PlayerService) revert(s *swap, url string, position float64, paused bool) {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	p.mu.Lock()
	ours := p.swap == s
	p.mu.Unlock()
	if !ours {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	err := errors.New("player: nothing to go back to")
	if url != "" {
		err = p.mpv.Command(ctx, "loadfile", url, "replace", "-1", "start="+strconv.FormatFloat(position, 'f', 3, 64))
	}
	if err == nil {
		err = p.mpv.SetProperty(ctx, "pause", paused)
	}
	if err == nil {
		return
	}
	p.log.Warn("player: could not return to the previous rendition", "error", err)
	p.mu.Lock()
	if p.swap == s {
		p.dropSwapLocked()
		p.state.Error = "playback failed"
		p.dirty = true
	}
	p.mu.Unlock()
}

// dropSwapLocked abandons a switch in flight and revokes its link. Caller
// holds p.mu.
func (p *PlayerService) dropSwapLocked() {
	if p.swap != nil {
		p.stream.Unpublish(p.swap.handle.ID)
		p.swap = nil
		p.state.Quality.Switching = ""
	}
	p.held = heldFigures{}
}

// restoreTracks selects the counterparts of the audio and subtitle tracks
// that were on before a switch; mpv picks its defaults afresh for every file.
// Subtitles switched off stay off. Audio is only ever re-pointed, never
// silenced: the UI offers no "audio off", so none before means none existed.
func (p *PlayerService) restoreTracks(audio, sub trackRef) {
	tracks, err := p.Tracks()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if audio.known && !audio.off {
		if id := matchTrack(tracks, "audio", audio); id > 0 && id != selectedID(tracks, "audio") {
			_ = p.mpv.SetProperty(ctx, "aid", id)
		}
	}
	if !sub.known {
		return
	}
	if sub.off {
		if selectedID(tracks, "sub") > 0 {
			_ = p.mpv.SetProperty(ctx, "sid", "no")
		}
		return
	}
	if id := matchTrack(tracks, "sub", sub); id > 0 && id != selectedID(tracks, "sub") {
		_ = p.mpv.SetProperty(ctx, "sid", id)
	}
}
