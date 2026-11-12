package services

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/pcm"
	"github.com/tonyflux/pkp/internal/stream"
	"github.com/tonyflux/pkp/internal/subsrelay"
)

// AI subtitles: captions subsrelay makes from the video's own audio while it
// plays. The audio is decoded on a connection of its own (internal/pcm) a
// little ahead of the picture and streamed up; the captions come back on the
// video's timeline and are drawn by mpv over the video, where the file's own
// subtitles would be.

// AISubtitleState is the AI subtitle switch as every window shows it.
type AISubtitleState struct {
	// Available is false when no subsrelay deployment is configured; the
	// UI then offers nothing.
	Available          bool            `json:"available"`
	RequiresMembership bool            `json:"requiresMembership"`
	Quota              AISubtitleQuota `json:"quota"`
	// On is the user's choice. It carries over to the next video, until it
	// is turned off or a failure turns it off.
	On bool `json:"on"`
	// Language is the caption language; "" is the language spoken.
	Language string `json:"language"`
	// Status is "connecting" until captions can flow, then "live"; "" while
	// off or while nothing plays.
	Status string `json:"status"`
	// Error is why AI subtitles turned themselves off; turning them on
	// again clears it.
	Error string `json:"error"`
	// ErrorRevision identifies each accepted failure, including identical errors.
	ErrorRevision uint64 `json:"errorRevision"`
}

// AISubtitleQuota is the latest server snapshot for one playing account.
// Known distinguishes a zero allowance from a quota the server has not sent.
type AISubtitleQuota struct {
	Account       string `json:"account"`
	Known         bool   `json:"known"`
	Exceeded      bool   `json:"exceeded"`
	Limit         int64  `json:"limit"`
	Usage         int64  `json:"usage"`
	NextResetTime string `json:"nextResetTime"`
}

const (
	// aiLeadMS is how far ahead of the picture audio goes up. A caption comes
	// back once its sentence is over, seconds after the sentence began;
	// sent this far ahead, it is there when the sentence starts on screen.
	// The lead builds up at one second per second, never in a burst: the
	// service's qwen backend gets no timestamps and dates text by how much
	// audio it has received, so 10 s sent at once smeared the first caption
	// over 10 s and squeezed the next ones into the last second (measured
	// against the dev deployment, 2026-10-14).
	aiLeadMS = 10_000
	// aiSlackMS is how far playback may be off the audio sent before that
	// counts as a jump: a seek, or falling behind.
	aiSlackMS = 1_500
	// aiFrameMS is the audio in one frame, the size the service expects.
	aiFrameMS = 100
	// aiIdle is how long the service waits without audio before it pauses
	// the session on its own (30 s), less a margin.
	aiIdle = 25 * time.Second
	aiTick = 50 * time.Millisecond
	// aiPauseDrop is how long a pause lasts before the service has acted on
	// it (it does after 5 s) and dropped the audio sent ahead.
	aiPauseDrop = 4 * time.Second
	// aiOverlay is the osd-overlay the captions are drawn on.
	aiOverlay = 4201
)

// aiSubs runs AI subtitles for whatever the player plays: one subsrelay
// session per video, while the switch is on.
type aiSubs struct {
	d      *DriveService
	p      *PlayerService
	client *subsrelay.Client
	log    *slog.Logger

	// control serializes user changes with shutdown and waits for stopped
	// sessions before a normal subtitle track can replace the overlay.
	control sync.Mutex
	wg      sync.WaitGroup

	mu     sync.Mutex
	on     bool
	lang   string
	closed bool
	// gen is bumped by every change that needs a new session: turning on,
	// another language, another audio track.
	gen uint64
	// looping is the manager goroutine running; last is the done channel
	// of the newest session, whose slot the next one has to wait for — the
	// service allows an account one session at a time.
	looping bool
	last    <-chan struct{}
	wake    chan struct{}
	stop    chan struct{}
}

func newAISubs(d *DriveService, p *PlayerService, client *subsrelay.Client) *aiSubs {
	p.setAI(func(s *AISubtitleState) {
		s.Available = true
		s.RequiresMembership = client.UsesAccount()
	})
	return &aiSubs{d: d, p: p, client: client, log: d.log, wake: make(chan struct{}, 1), stop: make(chan struct{})}
}

// SetAISubtitles turns AI subtitles on in language ("" for the language
// spoken), or off. On, they replace the file's own subtitles, and every
// video after this one gets them too until they are turned off.
func (d *DriveService) SetAISubtitles(on bool, language string) error {
	if d.ai == nil {
		return i18n.New("AI 字幕服务未配置")
	}
	return d.ai.set(on, language)
}

func (a *aiSubs) set(on bool, language string) error {
	a.control.Lock()
	defer a.control.Unlock()
	a.p.mu.Lock()
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		a.p.mu.Unlock()
		return errAICancelled
	}
	if on != a.on || language != a.lang {
		a.gen++
	}
	a.on, a.lang = on, language
	start := on && !a.looping
	if start {
		a.looping = true
		a.wg.Add(1)
	}
	a.p.setAILocked(func(s *AISubtitleState) {
		s.On, s.Language = on, language
		if on {
			s.Error = ""
		} else {
			s.Status = ""
		}
	})
	a.mu.Unlock()
	a.p.mu.Unlock()
	if start {
		go a.manage()
	}
	a.nudge()
	if !on {
		a.wg.Wait()
	}
	return nil
}

// restart starts the session over, for an audio track that changed.
func (a *aiSubs) restart() {
	a.mu.Lock()
	a.gen++
	a.mu.Unlock()
	a.nudge()
}

func (a *aiSubs) nudge() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *aiSubs) close() {
	a.control.Lock()
	defer a.control.Unlock()
	a.p.mu.Lock()
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		a.on = false
		a.gen++
		close(a.stop)
	}
	a.p.setAILocked(func(s *AISubtitleState) { s.On, s.Status = false, "" })
	a.mu.Unlock()
	a.p.mu.Unlock()
	a.wg.Wait()
}

// manage keeps one session going for the video playing while the switch is
// on: a new one for every video, none while nothing plays.
func (a *aiSubs) manage() {
	var cur *aiRun
	defer func() {
		if cur != nil {
			cur.cancel()
		}
		a.mu.Lock()
		last := a.last
		a.mu.Unlock()
		if last != nil {
			<-last
		}
		a.wg.Done()
	}()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		a.mu.Lock()
		on, lang, gen, last := a.on, a.lang, a.gen, a.last
		a.mu.Unlock()
		account, fileID, target, ready := a.p.aiTarget()

		if cur != nil {
			select {
			case <-cur.done:
				// It ended on its own: a refusal or a failure. Trying
				// again would fail the same way, and the time used counts
				// against the account; the user turns it back on.
				if cur.err != nil {
					a.fail(cur)
				}
				cur = nil
				continue
			default:
			}
			if !on || !ready || gen != cur.gen || target != cur.target || account != cur.account || fileID != cur.fileID {
				cur.cancel()
				cur = nil
			}
		}

		switch {
		case !on:
			a.mu.Lock()
			if !a.on {
				a.looping = false
				a.mu.Unlock()
				return
			}
			a.mu.Unlock()
			continue
		case cur == nil && fileID != "" && ready:
			cur = a.begin(account, fileID, lang, gen, target, last)
			a.mu.Lock()
			a.last = cur.done
			a.mu.Unlock()
		case cur == nil:
			a.p.setAI(func(s *AISubtitleState) { s.Status = "" })
		}

		select {
		case <-tick.C:
		case <-a.wake:
		case <-a.stop:
			if cur != nil {
				cur.cancel()
			}
			return
		}
	}
}

// fail turns off only the run that failed, not a newer user or player choice.
func (a *aiSubs) fail(r *aiRun) {
	a.p.mu.Lock()
	a.mu.Lock()
	current := r.currentLocked() && r.ctx.Err() == nil
	if current {
		a.on = false
		a.gen++
		a.p.setAILocked(func(s *AISubtitleState) {
			s.On, s.Status, s.Error = false, "", aiMessage(r.err)
			s.ErrorRevision++
		})
	}
	a.mu.Unlock()
	a.p.mu.Unlock()
	if !current {
		return
	}
	a.log.Warn("ai subtitles: stopped", "error", r.err)
}

// aiMessage is what the user is told when AI subtitles stop.
func aiMessage(err error) string {
	var quota *aiQuotaError
	if errors.As(err, &quota) {
		if t, perr := time.Parse(time.RFC3339, quota.resets); perr == nil {
			return i18n.F("本期 AI 字幕时长已用完，%s 恢复", aiResetTime(t.Local()))
		}
		return i18n.T("本期 AI 字幕时长已用完")
	}
	var refused *subsrelay.Error
	if errors.As(err, &refused) {
		switch refused.Code {
		case "vip_required":
			return i18n.T("AI 字幕需要 PikPak 会员")
		case "quota_exceeded":
			// Before a session exists this is the account's one session
			// being in use elsewhere, not the time allowance.
			return i18n.T("AI 字幕正在别处使用，同一账号同时只能开一处")
		case "unauthenticated", "Unauthorized":
			return i18n.T("AI 字幕服务不接受这个账号的登录")
		case "invalid_request":
			return i18n.T("这个视频不能生成 AI 字幕")
		case "bad_start":
			return i18n.F("AI 字幕服务拒绝了这个视频或语言：%s", refused.Description)
		}
		return i18n.F("AI 字幕服务暂时不可用（%s）", refused.Code)
	}
	var source *aiSourceError
	if errors.As(err, &source) {
		return source.message
	}
	return i18n.T("与 AI 字幕服务的连接中断了")
}

// aiResetTime is when the AI subtitle time comes back, as a date and time of
// the language in force.
func aiResetTime(t time.Time) string {
	switch i18n.Current() {
	case i18n.English:
		return t.Format("Jan 2 15:04")
	case i18n.Korean:
		return t.Format("1월 2일 15:04") // i18n-ignore: date layout
	default:
		return t.Format("1月2日 15:04") // i18n-ignore: date layout
	}
}

// aiQuotaError is the account's AI subtitle time running out.
type aiQuotaError struct{ resets string }

func (e *aiQuotaError) Error() string { return "ai subtitles: quota used up until " + e.resets }

// aiSourceError is a video that cannot be captioned, with what to say.
type aiSourceError struct {
	message string
	err     error
}

func (e *aiSourceError) Error() string { return fmt.Sprintf("ai subtitles: %s: %v", e.message, e.err) }
func (e *aiSourceError) Unwrap() error { return e.err }

// aiRun is one video's session.
type aiRun struct {
	a                     *aiSubs
	account, fileID, lang string
	gen, target           uint64
	ctx                   context.Context
	cancel                context.CancelFunc
	done                  chan struct{}
	// err is why the run ended by itself; nil when it was cancelled.
	err error
}

// currentLocked requires both the player and preference locks, in that order.
func (r *aiRun) currentLocked() bool {
	a, p := r.a, r.a.p
	return a.on && !a.closed && a.gen == r.gen && p.gen == r.target &&
		p.account == r.account && p.fileID == r.fileID && p.aiReadyLocked()
}

func (r *aiRun) setStatus(status string) bool {
	p, a := r.a.p, r.a
	p.mu.Lock()
	defer p.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.ctx.Err() != nil || !r.currentLocked() {
		return false
	}
	p.setAILocked(func(s *AISubtitleState) { s.Status = status })
	return true
}

func (r *aiRun) setQuota(quota *subsrelay.Quota) bool {
	if quota == nil {
		return false
	}
	p, a := r.a.p, r.a
	p.mu.Lock()
	defer p.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.ctx.Err() != nil || !r.currentLocked() {
		return false
	}
	p.setAILocked(func(s *AISubtitleState) {
		s.Quota = AISubtitleQuota{
			Account: r.account, Known: true, Exceeded: quota.Exceeded,
			Limit: quota.Limit, Usage: quota.Usage, NextResetTime: quota.NextResetTime,
		}
	})
	return true
}

func (a *aiSubs) begin(account, fileID, lang string, gen, target uint64, last <-chan struct{}) *aiRun {
	ctx, cancel := context.WithCancel(context.Background())
	r := &aiRun{a: a, account: account, fileID: fileID, lang: lang, gen: gen, target: target, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	r.setStatus("connecting")
	go func() {
		defer close(r.done)
		err := r.run(last)
		if ctx.Err() == nil {
			if err == nil {
				err = errors.New("ai subtitles: the session ended")
			}
			r.err = err
		}
	}()
	return r
}

// aiSource is what a session decodes and how the service knows the video.
type aiSource struct {
	start  subsrelay.Start
	handle stream.Handle
	stream int
}

func (r *aiRun) run(last <-chan struct{}) error {
	if last != nil {
		// Even a cancelled waiter owns its predecessor's release barrier.
		// Otherwise the next generation can bypass a still-closing session.
		<-last
	}
	if !r.setStatus("connecting") {
		r.cancel()
		return nil
	}
	if r.a.client.UsesAccount() {
		membership, err := r.a.d.membership(r.ctx, r.account)
		if err != nil {
			return err
		}
		if !membership.Active {
			return &subsrelay.Error{Code: "vip_required"}
		}
	}
	src, err := r.a.d.aiSource(r.ctx, r.account, r.fileID)
	if err != nil {
		return err
	}
	defer r.a.p.stream.Unpublish(src.handle.ID)
	src.start.TargetLanguage = r.lang
	sess, err := r.connect(src.start)
	if err != nil {
		return err
	}
	defer func() {
		// The service frees the account's session only once it has
		// closed; the next video waits for that.
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		sess.Close(ctx)
	}()
	defer drawCaption(r.a.p, "", captionLook{})
	if !r.setStatus("live") {
		r.cancel()
		return nil
	}
	// A new video may have picked its own subtitles on load.
	_ = r.a.p.SelectTrack("subtitle", 0)
	return r.stream(sess, src)
}

// connect finds the video's node and opens the session there. Right after
// the previous video's session closed, the service may not have freed it
// yet and refuses the account's second one for a moment.
func (r *aiRun) connect(start subsrelay.Start) (*subsrelay.Session, error) {
	for attempt := 0; ; attempt++ {
		token := ""
		if r.a.client.UsesAccount() {
			var err error
			if token, err = r.a.d.accessToken(r.ctx, r.account); err != nil {
				return nil, err
			}
		}
		ctx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
		node, err := r.a.client.Node(ctx, start.GCID, token)
		r.setQuota(node.Quota)
		var sess *subsrelay.Session
		if err == nil {
			sess, err = r.a.client.Dial(ctx, node.URL, token, start)
		}
		cancel()
		switch {
		case err == nil:
			return sess, nil
		case errors.Is(err, subsrelay.ErrQuotaUsedUp):
			return nil, &aiQuotaError{resets: node.Quota.NextResetTime}
		}
		var refused *subsrelay.Error
		if !errors.As(err, &refused) || refused.Code != "quota_exceeded" || attempt >= 4 {
			return nil, err
		}
		select {
		case <-time.After(time.Second):
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
	}
}

// stream feeds the session the video's audio, aiLeadMS ahead of the picture,
// and draws the captions that come back.
func (r *aiRun) stream(sess *subsrelay.Session, src aiSource) error {
	tick := time.NewTicker(aiTick)
	defer tick.Stop()
	var (
		dec       *aiDecoder
		pending   *aiFrame
		next      int64 // media ms right after the audio sent
		budget    int64 // media ms the audio may go up to by now
		lastTick  time.Time
		sent      bool      // audio went up: later jumps need "seek"
		pausedAt  time.Time // "pause" went up then; zero while playing
		ended     bool      // the decoder reached the end of the audio
		lastSent  time.Time
		failures  int
		retryAt   time.Time
		cues      aiCues
		drawn     string
		drawnLook captionLook
		quota     subsrelay.Quota
		refused   *subsrelay.Error
		// startup is how long a run takes from having the decoder to its
		// first audio: a seek, one request at the new position, through a
		// CDN up to a second or more. A new run starts that far ahead of
		// the picture (up to the lead), where the picture will be by then.
		// Started at the picture, a run slower than the slack was behind
		// the moment it began and was started over, again and again, with
		// no audio ever going up.
		startup time.Duration
	)
	// The session's decoder belongs to the audio goroutine, which serves one
	// run at a time; work holds the newest run it has not taken yet.
	audioCtx, stopAudio := context.WithCancel(r.ctx)
	work := make(chan *aiDecoder, 1)
	audioDone := make(chan struct{})
	go func() {
		defer close(audioDone)
		r.audio(audioCtx, src, work)
	}()
	defer func() {
		stopAudio()
		<-audioDone
	}()
	restart := func(pos int64, speed float64) {
		if dec != nil {
			dec.stop()
			startup = dec.startup(startup)
		}
		at := pos + min(int64(float64(startup.Milliseconds())*speed), aiLeadMS)
		dec = newAIDecoder(audioCtx, at)
		select {
		case <-work: // the one stopped above, not taken yet
		default:
		}
		work <- dec
		pending, next, budget, ended = nil, at, at, false
	}

	for {
		select {
		case <-r.ctx.Done():
			return nil
		case m, ok := <-sess.Messages():
			if !ok {
				return sess.Err()
			}
			switch {
			case m.Cue != nil:
				cues.add(*m.Cue)
			case m.Quota != nil:
				quota = *m.Quota
				r.setQuota(m.Quota)
			case m.Error != nil:
				// soniox_unavailable: the next audio retries. The rest
				// are followed by the server's stop.
				r.a.log.Warn("ai subtitles: service error", "code", m.Error.Code, "description", m.Error.Description)
				if m.Error.Code != "soniox_unavailable" && m.Error.Code != "bad_audio_frame" && m.Error.Code != "bad_message" {
					refused = m.Error
				}
			case m.Stop:
				if m.Reason == "quota_exceeded" || quota.Exceeded {
					return &aiQuotaError{resets: quota.NextResetTime}
				}
				if refused != nil {
					return refused
				}
				return errors.New("ai subtitles: the service ended the session")
			}
			continue
		case <-tick.C:
		}

		st := r.a.p.State()
		// Only time spent playing grows the send budget: a pause or a load
		// added to it would come out as a burst.
		now := time.Now()
		var dt time.Duration
		if !lastTick.IsZero() {
			dt = now.Sub(lastTick)
		}
		lastTick = now
		if st.Loading {
			continue // another rendition or file loading; its position is held
		}
		pos := int64(st.Position * 1000)
		// Subtitle delay moves AI captions as it moves the file's own subtitles.
		text := cues.at(pos - int64(math.Round(st.SubtitleDelay*1000)))
		if look := r.a.p.captionLook(text != ""); text != drawn || look != drawnLook {
			drawCaption(r.a.p, text, look)
			drawn, drawnLook = text, look
		}
		if st.Paused {
			if sent && pausedAt.IsZero() {
				sess.Send("pause")
				pausedAt = time.Now()
			}
			continue
		}

		if dec == nil && time.Now().Before(retryAt) {
			continue // a failed decoder waits out its backoff
		}
		behind := dec != nil && !ended && pos > next+aiSlackMS && dec.warm()
		back := dec != nil && pos < next-aiLeadMS-aiSlackMS
		// A pause the service acted on dropped the audio sent ahead of it:
		// that stretch goes up again, as after a seek.
		dropped := !pausedAt.IsZero() && time.Since(pausedAt) >= aiPauseDrop
		switch {
		case dec == nil || behind || back || dropped:
			if sent {
				// Also takes back a pause.
				sess.Send("seek")
				cues.cut(pos)
			}
			restart(pos, st.Speed)
			pausedAt = time.Time{}
		case !pausedAt.IsZero():
			sess.Send("resume")
			pausedAt = time.Time{}
		}
		// Playback's own pace plus one second per second, up to the lead.
		budget += int64(float64(dt.Milliseconds()) * (st.Speed + 1))
		budget = min(max(budget, pos), pos+aiLeadMS)

		for {
			if pending == nil {
				f, open, ok := dec.poll()
				if !ok {
					break
				}
				if !open {
					if err := dec.err(); err != nil {
						failures++
						if failures > 3 {
							return &aiSourceError{message: i18n.T("读不到这个视频的声音"), err: err}
						}
						// Reopened at the picture, after a pause that grows.
						r.a.log.Warn("ai subtitles: audio decoder failed", "error", err, "attempt", failures)
						dec.stop()
						dec, retryAt = nil, time.Now().Add(time.Duration(failures)*time.Second)
					} else if !ended {
						ended = true
						sess.Send("finalize")
					}
					break
				}
				pending = &f
			}
			if pending.pos >= budget {
				break
			}
			if sent && time.Since(lastSent) > aiIdle {
				// The service paused on its own and drops audio until told.
				sess.Send("resume")
			}
			if !sess.Audio(pending.pos, pending.pcm) {
				break // the network is behind; the same frame next tick
			}
			sent, lastSent, failures = true, time.Now(), 0
			next = pending.pos + int64(len(pending.pcm)/pcm.BytesPerMS)
			pending = nil
		}
	}
}

// aiFrame is aiFrameMS of audio starting at pos (media ms).
type aiFrame struct {
	pos int64
	pcm []byte
}

// aiDecoder is one decoding run: frames from one position on, until the end
// of the audio, a failure, or stop. The session's audio goroutine serves the
// runs one after another with the same decoder.
type aiDecoder struct {
	from   int64
	ctx    context.Context
	cancel context.CancelFunc
	out    chan aiFrame
	began  time.Time
	mu     sync.Mutex
	// ready is when the run had the decoder, open, to seek; zero before.
	ready time.Time
	// first is how long after ready the first frame came out, set with flowing.
	first   time.Duration
	flowing bool
	done    bool
	fault   error
}

// newAIDecoder is a run from media ms from on, for the session's audio
// goroutine, whose context ctx is.
func newAIDecoder(ctx context.Context, from int64) *aiDecoder {
	ctx, cancel := context.WithCancel(ctx)
	// A few seconds' worth: the decoder stays just ahead of the sending.
	return &aiDecoder{from: from, ctx: ctx, cancel: cancel, out: make(chan aiFrame, 30), began: time.Now()}
}

// audio decodes the session's sound for one run at a time, the newest, with
// one decoder kept open across them. A run after the first seeks it: one
// request at the new position. Opening the file again would be a few
// requests in a row — the header, a moov at the end, the first packets —
// seconds through a CDN, with the whole moov downloaded every time.
func (r *aiRun) audio(ctx context.Context, src aiSource, work <-chan *aiDecoder) {
	var dec *pcm.Decoder
	defer func() {
		if dec != nil {
			dec.Close()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-work:
			dec = d.serve(ctx, src, dec)
		}
	}
}

// serve decodes the run's frames with dec, opening one first if there is
// none, and returns the decoder to go on with: nil once it failed, for the
// next run to open anew.
func (d *aiDecoder) serve(ctx context.Context, src aiSource, dec *pcm.Decoder) *pcm.Decoder {
	defer close(d.out)
	var flowing bool
	defer func() {
		if !flowing && d.ctx.Err() == nil {
			d.mu.Lock()
			d.done = true
			d.mu.Unlock()
		}
	}()
	if dec == nil {
		var err error
		// On the session's context, not the run's: a run started over while
		// the file opens leaves the open to the next one.
		if dec, err = pcm.Open(ctx, src.handle.URL, src.stream, d.from); err != nil {
			if ctx.Err() == nil {
				d.finish(err)
			}
			return nil
		}
		d.start()
	} else {
		d.start()
		if err := dec.Seek(d.ctx, d.from); err != nil {
			return d.fail(dec, err)
		}
	}
	var frame aiFrame
	flush := func() bool {
		if len(frame.pcm) == 0 {
			return true
		}
		select {
		case d.out <- frame:
			frame = aiFrame{}
			if !flowing {
				flowing = true
				d.mu.Lock()
				d.flowing, d.first = true, time.Since(d.ready)
				d.mu.Unlock()
			}
			return true
		case <-d.ctx.Done():
			return false
		}
	}
	for {
		data, pos, err := dec.Next(d.ctx)
		if err != nil {
			if !flush() || errors.Is(err, io.EOF) {
				return dec // stopped, or at the end: a seek back still uses it
			}
			return d.fail(dec, err)
		}
		// Samples that do not continue the frame start a new one: every
		// frame carries its own position.
		if len(frame.pcm) > 0 && absDiff(frame.pos+int64(len(frame.pcm)/pcm.BytesPerMS), pos) > 2 {
			if !flush() {
				return dec
			}
		}
		for len(data) > 0 {
			if len(frame.pcm) == 0 {
				frame.pos = pos
			}
			room := aiFrameMS*pcm.BytesPerMS - len(frame.pcm)
			n := min(room, len(data))
			frame.pcm = append(frame.pcm, data[:n]...)
			data = data[n:]
			pos += int64(n / pcm.BytesPerMS)
			if len(frame.pcm) == aiFrameMS*pcm.BytesPerMS && !flush() {
				return dec
			}
		}
	}
}

func (d *aiDecoder) start() {
	d.mu.Lock()
	d.ready = time.Now()
	d.mu.Unlock()
}

// fail ends the run with err, unless the run was stopped, and returns what
// is left of dec: nothing after a failure, since what broke may still be
// broken.
func (d *aiDecoder) fail(dec *pcm.Decoder, err error) *pcm.Decoder {
	if d.ctx.Err() != nil {
		return dec
	}
	d.finish(err)
	dec.Close()
	return nil
}

func (d *aiDecoder) finish(err error) {
	d.mu.Lock()
	d.fault = err
	d.mu.Unlock()
}

// poll takes a frame if one is ready: ok false when none is yet, open false
// once the decoder has finished.
func (d *aiDecoder) poll() (f aiFrame, open, ok bool) {
	select {
	case f, open = <-d.out:
		return f, open, true
	default:
		return aiFrame{}, true, false
	}
}

func (d *aiDecoder) err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fault
}

// warm is past the opening: until the run has the decoder, open, playback
// moving on is the file opening, not a jump.
func (d *aiDecoder) warm() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.ready.IsZero() || time.Since(d.began) > 5*time.Second
}

// startup is how long the run took from having the decoder to its first
// audio. One stopped before that took at least as long as it had run, which
// can only raise was, the time known before; one that never had the decoder
// tells nothing.
func (d *aiDecoder) startup(was time.Duration) time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.flowing:
		return d.first
	case d.ready.IsZero() || d.done:
		return was
	}
	return max(was, time.Since(d.ready))
}

func (d *aiDecoder) stop() { d.cancel() }

func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

// aiCues are the captions of one session in time order. The newest caption
// to start replaces one still running: the service pads a caption to a
// second's length without knowing what comes next.
type aiCues struct{ list []subsrelay.Cue }

func (c *aiCues) add(cue subsrelay.Cue) {
	i := sort.Search(len(c.list), func(i int) bool { return c.list[i].StartMS >= cue.StartMS })
	for j := i; j < len(c.list) && c.list[j].StartMS == cue.StartMS; j++ {
		if c.list[j].Text == cue.Text {
			return // sent again after a seek
		}
	}
	if i > 0 && c.list[i-1].EndMS > cue.StartMS {
		c.list[i-1].EndMS = cue.StartMS
	}
	if i < len(c.list) && cue.EndMS > c.list[i].StartMS {
		cue.EndMS = c.list[i].StartMS
	}
	c.list = slices.Insert(c.list, i, cue)
}

// at is the caption to show at pos, "" for none.
func (c *aiCues) at(pos int64) string {
	i := sort.Search(len(c.list), func(i int) bool { return c.list[i].StartMS > pos }) - 1
	if i >= 0 && pos < c.list[i].EndMS {
		return c.list[i].Text
	}
	return ""
}

// cut drops what a seek to pos makes the service send again: every caption
// starting from half a second before pos, replayed from its cache in its own
// (sentence-sized) cues, which would not line up with the ones held.
func (c *aiCues) cut(pos int64) {
	c.list = slices.DeleteFunc(c.list, func(cue subsrelay.Cue) bool { return cue.StartMS >= pos-500 })
}

// drawCaption shows text over the video in look, where subtitles go, or
// clears it. A variable so tests can watch what is drawn.
var drawCaption = func(p *PlayerService, text string, look captionLook) {
	_ = p.withContext(func(ctx context.Context) error {
		if text == "" {
			return p.mpv.Command(ctx, "osd-overlay", aiOverlay, "none", "")
		}
		return p.mpv.Command(ctx, "osd-overlay", aiOverlay, "ass-events", assCaption(text, look), look.resX, aiCaptionResY, 0, "no", "no")
	})
}

// aiTarget is the drive file playing and whether it is far enough along to
// caption: loaded, with a duration, and no switch in flight.
func (p *PlayerService) aiTarget() (account, fileID string, target uint64, ready bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.account, p.fileID, p.gen, p.aiReadyLocked()
}

func (p *PlayerService) aiReadyLocked() bool {
	return p.fileID != "" && p.state.Duration > 0 && !p.state.Loading &&
		p.state.Quality.Switching == "" && p.swap == nil && p.restoring == 0
}

// setAI changes the AI subtitle state, waking the windows only when it did
// change: the manager reports "nothing playing" every round.
func (p *PlayerService) setAI(change func(*AISubtitleState)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setAILocked(change)
}

func (p *PlayerService) setAILocked(change func(*AISubtitleState)) {
	old := p.state.AISubtitles
	change(&p.state.AISubtitles)
	if p.state.AISubtitles != old {
		p.dirty = true
	}
}

// aiAudio is the audio the user hears: how many audio tracks the playing
// rendition has, the selected one's index in the container, and whether it
// is the first of them.
func (p *PlayerService) aiAudio(ctx context.Context) (count, ffIndex int, first bool, err error) {
	var tracks []struct {
		Type     string `json:"type"`
		Selected bool   `json:"selected"`
		FFIndex  int    `json:"ff-index"`
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := p.mpv.GetProperty(ctx, "track-list", &tracks); err != nil {
		return 0, 0, false, err
	}
	ffIndex, lowest := -1, -1
	for _, t := range tracks {
		if t.Type != "audio" {
			continue
		}
		count++
		if lowest < 0 || t.FFIndex < lowest {
			lowest = t.FFIndex
		}
		if t.Selected {
			ffIndex = t.FFIndex
		}
	}
	return count, ffIndex, ffIndex == lowest, nil
}

// aiSource resolves what a session decodes. With one audio track that is
// the video's lightest rendition — the transcodes carry the same sound on
// the same timeline at a fraction of the bytes — and with several, the track
// the user picked, from the rendition playing. The service caches captions
// per GCID: a track other than the first gets an identity of its own, so
// two languages of one file never share a cache.
func (d *DriveService) aiSource(ctx context.Context, account, fileID string) (aiSource, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return aiSource{}, err
	}
	file, err := d.getFile(ctx, s, fileID, drive.GetFileParams{Usage: drive.UsagePlay})
	if err != nil {
		return aiSource{}, err
	}
	if len(file.Hash) != 40 {
		return aiSource{}, &aiSourceError{message: i18n.T("这个视频不能生成 AI 字幕"), err: fmt.Errorf("file %s has no GCID", fileID)}
	}
	options := renditionsOf(*file)
	if len(options) == 0 {
		return aiSource{}, &aiSourceError{message: i18n.T("这个视频不能生成 AI 字幕"), err: fmt.Errorf("file %s has no playable link", fileID)}
	}
	count, ffIndex, first, err := d.player.aiAudio(ctx)
	if err != nil {
		return aiSource{}, err
	}
	if count == 0 {
		return aiSource{}, &aiSourceError{message: i18n.T("这个视频没有声音"), err: fmt.Errorf("file %s has no audio track", fileID)}
	}
	src := aiSource{start: subsrelay.Start{GCID: strings.ToUpper(file.Hash), FileID: fileID}, stream: -1}
	chosen := options[len(options)-1]
	if count > 1 {
		_, _, q := d.player.qualityNow()
		if r, ok := find(options, q.Current); ok {
			chosen = r
		} else {
			chosen = options[0]
		}
		src.stream = ffIndex
		if !first {
			sum := sha1.Sum([]byte(fmt.Sprintf("%s/audio/%d", file.Hash, ffIndex)))
			src.start.GCID = strings.ToUpper(hex.EncodeToString(sum[:]))
		}
	}
	src.start.MediaID = chosen.mediaID
	if src.start.MediaID == "" {
		src.start.MediaID = fileID
	}
	if err := ctx.Err(); err != nil {
		return aiSource{}, err
	}
	if src.handle, err = d.player.stream.Publish(stream.Source{URL: chosen.url, Headers: d.mediaHeaders()}); err != nil {
		return aiSource{}, err
	}
	return src, nil
}

// accessToken is a usable access token of an account, for services that
// take the PikPak session itself.
func (d *DriveService) accessToken(ctx context.Context, account string) (string, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return "", err
	}
	s.mu.RLock()
	tokens := s.tokens
	s.mu.RUnlock()
	if tokens == nil {
		return "", drive.ErrNoCredentials
	}
	var tok drive.Token
	err = d.doIn(ctx, s, func(ctx context.Context, _ *drive.Client) error {
		var err error
		tok, err = tokens.Token(ctx)
		return err
	})
	return tok.AccessToken, err
}
