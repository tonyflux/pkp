// Package services holds the types bound into the webview. They are thin:
// transport, state fan-out and argument validation only. The playback engine
// lives in internal/mpv and the loopback proxy in internal/stream.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/mpv"
	"github.com/tonyflux/pkp/internal/stream"
)

// EventPlayerState is the Wails event carrying PlayerState to the frontend.
// It is coalesced to at most one emission per stateInterval.
const EventPlayerState = "player:state"

// stateInterval bounds how often the frontend is woken. mpv reports time-pos
// per frame; at 60fps that is 60 events/s of which 55 are invisible to a human.
const stateInterval = 200 * time.Millisecond

// seekHoldLimit bounds how long a seek target stands in for time-pos if mpv
// never reports landing — a stuck bar would be worse than one late jump.
const seekHoldLimit = 3 * time.Second

// seekHold is a seek in flight: the position the user asked for, reported
// in place of mpv's time-pos until mpv restarts playback there. limit is how
// long it may stand in; zero means seekHoldLimit.
type seekHold struct {
	pending bool
	target  float64
	since   time.Time
	limit   time.Duration
}

// commandTimeout is the context handed to every mpv call. libmpv is linked
// in-process and its calls are synchronous and uncancellable, so the
// deadline is not enforced; it is kept so that a future engine that does
// honour contexts gets a bound without touching every call site.
const commandTimeout = 5 * time.Second

// errArchivedMessage is PlayerState.Error for a file PikPak keeps in archive
// (cold) storage: its CDN answers with no bytes until the object is thawed,
// which the user cannot tell from a broken player without being told.
func errArchivedMessage() string {
	return i18n.T("该文件处于 PikPak 归档（冷）存储，暂时无法读取，请稍后再试")
}

// observedProperties are the mpv properties mirrored into PlayerState.
var observedProperties = []string{
	"pause",
	"time-pos",
	"duration",
	"volume",
	"mute",
	"speed",
	"width",
	"height",
	"dwidth",
	"dheight",
	"media-title",
	"demuxer-cache-duration",
	"paused-for-cache",
	"cache-buffering-state",
	"cache-speed",
	"seeking",
	"eof-reached",
	"core-idle",
	"sub-delay",
	"audio-delay",
}

// PlayerState is the snapshot pushed to the frontend.
type PlayerState struct {
	Running      bool    `json:"running"`
	Idle         bool    `json:"idle"`
	Source       string  `json:"source"`
	Title        string  `json:"title"`
	Paused       bool    `json:"paused"`
	Position     float64 `json:"position"`
	Duration     float64 `json:"duration"`
	Volume       float64 `json:"volume"`
	Muted        bool    `json:"muted"`
	Speed        float64 `json:"speed"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Buffering    bool    `json:"buffering"`
	CacheSeconds float64 `json:"cacheSeconds"`
	EOF          bool    `json:"eof"`
	Error        string  `json:"error"`
	// BufferPercent is how far a wait for data has got while Buffering,
	// 0–99: mpv's cache-buffering-state, the share of cache-pause-wait
	// (1s of playback) read ahead of the position.
	BufferPercent int `json:"bufferPercent"`
	// CacheSpeed is how fast data is coming in, in bytes per second over
	// the last second: mpv's cache-speed, its reads from the stream proxy.
	CacheSpeed int64 `json:"cacheSpeed"`
	// Seeking is mpv's: a seek or a start is under way and playback has
	// not restarted at the new place yet.
	Seeking bool `json:"seeking"`
	// Loading is true from the moment a play is requested — link lookup
	// included — until mpv has the file open or gives up. It is what lets
	// the UI show feedback for a click while the previous file is still
	// the one everything else in the state describes.
	Loading bool `json:"loading"`
	// Quality describes the renditions of the video DriveService opened;
	// empty for a bare Open.
	Quality QualityState `json:"quality"`
	// SubtitleStyle is how subtitles are drawn, the saved preference.
	SubtitleStyle SubtitleStyle `json:"subtitleStyle"`
	// SubtitleDelay and AudioDelay shift the subtitles and the sound
	// against the picture, in seconds; positive is later. Both start at
	// 0 with every video.
	SubtitleDelay float64 `json:"subtitleDelay"`
	AudioDelay    float64 `json:"audioDelay"`

	// displayW×displayH is the picture as shown, with its aspect applied:
	// what the separate window takes its proportions from.
	displayW, displayH int
}

// Track is one entry of mpv's track-list.
type Track struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	// Title is mpv's, or else the name an MP4 keeps in the track's metadata
	// (trackTitle).
	Title    string `json:"title"`
	Lang     string `json:"lang"`
	Codec    string `json:"codec"`
	Selected bool   `json:"selected"`
	Default  bool   `json:"default"`
	// Channels is an audio track's layout as mpv names it: "mono",
	// "stereo", "5.1(side)"…; empty for the other kinds.
	Channels string `json:"channels"`
	// External is a track added from outside the file: PikPak's extracted
	// subtitles on a transcode (extsubs.go).
	External bool `json:"external"`
}

// genericHandler matches the handler names muxers write for themselves
// ("SoundHandler", "Core Media Audio", "ISO Media file produced by Google
// Inc."), which say nothing about the track.
var genericHandler = regexp.MustCompile(`(?i)handler|^core media|^iso media|media file`)

// trackTitle is the name an MP4 gives a track where mpv does not look: its
// "name" (the udta title) or, as Chinese releases often label Mandarin and Cantonese,
// a handler name that is not the muxer's own.
func trackTitle(metadata map[string]string) string {
	if name := strings.TrimSpace(metadata["name"]); name != "" {
		return name
	}
	if handler := strings.TrimSpace(metadata["handler_name"]); !genericHandler.MatchString(handler) {
		return handler
	}
	return ""
}

// PlayerOptions configures PlayerService.
type PlayerOptions struct {
	// Headless disables mpv's video and audio output, and skips the render
	// surface entirely. Test-only.
	Headless bool
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// PlayerService owns the embedded mpv core, the native video surface it
// renders into, and the loopback proxy that feeds it.
type PlayerService struct {
	log    *slog.Logger
	mpv    *mpv.Client
	stream *stream.Server

	startMu sync.Mutex
	// loadMu orders the decisions that end in a loadfile — open, stop, a
	// switch, the way back from a failed one — so none of them replaces a
	// file another has just started.
	loadMu sync.Mutex

	mu            sync.Mutex
	state         PlayerState
	dirty         bool
	seek          seekHold
	current       stream.Handle
	previews      map[previewOwnerKey]map[string]stream.Handle
	previewClosed map[previewOwnerKey]bool
	window        *application.WebviewWindow
	attached      bool
	headless      bool

	// Quality switching, also under mu. fileID is the drive file
	// DriveService opened, the one the quality state describes; "" after a
	// bare Open. account is the account it was opened for, which a switch
	// reads links through. gen counts opens, stops and switches, so a
	// switch whose video changed under it stands down. pendingLowered marks
	// the announced switch as auto's own step down.
	fileID         string
	account        string
	gen            uint64
	pendingLowered bool
	swap           *swap
	// loads counts the files mpv has loaded (file-loaded), quality
	// switches included; subs are the playing video's extracted subtitles
	// and the load they were last added to.
	loads uint64
	subs  extSubs
	held  heldFigures
	stall stallWatch
	// onStall is DriveService's auto step down, called when auto playback
	// keeps stalling. Set once, before playback starts.
	onStall func()
	// onPlayback is DriveService's play record: where a drive file was
	// left, reported as playback moves, when another file replaces it and
	// on stop, with the account it belongs to. Set once, before playback
	// starts; track follows the file.
	onPlayback func(account, fileID string, position, duration float64)
	track      playTrack
	// audio is the music page's last read of a file's tags and cover.
	audio audioMemo

	// surfaceMu orders where the surface is and where it is placed: a move
	// and a rectangle from the window that just lost the surface must not
	// interleave. host is the ID of the window holding it.
	surfaceMu sync.Mutex
	host      uint
	// stageMu orders the windows' background switches (Windows only, see
	// followStage); seeThrough is, per window ID, whether its webview is
	// see-through right now. Kept apart from surfaceMu: the switch waits on
	// the main thread.
	stageMu    sync.Mutex
	seeThrough map[uint]bool
	// popoutMu guards popout, the separate player window while it is open;
	// picture, the display size last seen; shaped, the proportions the
	// window was last shaped for (0 for none yet); and buttons, whether its
	// page wants the traffic lights shown.
	popoutMu    sync.Mutex
	popout      *application.WebviewWindow
	picture     [2]int
	shaped      float64
	buttons     bool
	popoutAudio bool

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// AttachWindow names the main window, whose content area hosts the video
// surface unless the separate player window has it. It has to be called
// before the frontend reports a stage rectangle, which main does right after
// creating the window.
func (p *PlayerService) AttachWindow(window *application.WebviewWindow) {
	p.mu.Lock()
	p.window = window
	p.mu.Unlock()
	p.surfaceMu.Lock()
	p.host = window.ID()
	p.surfaceMu.Unlock()

	// The separate window only mirrors the session this window owns, so it
	// goes with it. The surface comes home first, so the window it leaves is
	// never destroyed with the surface inside.
	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		if popout := p.returnHome(); popout != nil {
			popout.Close()
		}
	})
}

// stageBackdrop is a window's webview background while no picture shows
// through it: the main window's colour, which the page covers anyway.
var stageBackdrop = application.NewRGB(14, 16, 24)

// followStage makes a window's webview see-through only while its page shows
// the stage. On Windows the surface is a child window below WebView2, which
// paints its default background over it unless that colour is fully
// transparent — but with a transparent default background WebView2 draws
// every CSS backdrop-filter unblurred (WebView2Feedback#4945; user
// screenshots 2026-10-17: the login card and every sheet showed the page
// behind them sharp). So the webview is opaque by default and transparent
// only while there is a picture to show. WebKit is cleared by the surface
// itself and keeps its blur, so macOS is left alone.
func (p *PlayerService) followStage(window application.Window, visible bool) {
	if runtime.GOOS != "windows" {
		return
	}
	p.stageMu.Lock()
	defer p.stageMu.Unlock()
	// A window not seen yet is opaque: that is how it was created.
	if p.seeThrough[window.ID()] == visible {
		return
	}
	if p.seeThrough == nil {
		p.seeThrough = map[uint]bool{}
	}
	p.seeThrough[window.ID()] = visible
	colour := stageBackdrop
	if visible {
		colour.Alpha = 0
	}
	window.SetBackgroundColour(colour)
}

// SetStage positions the video surface under the hole the calling page
// leaves in its background. Coordinates are CSS pixels relative to that
// window's content area, which is what the page can measure. A page in a
// window that does not hold the surface is ignored: on its way out it
// reports "hidden", which would otherwise blank the picture in the other.
// Either way the calling window's webview follows its page (followStage).
func (p *PlayerService) SetStage(ctx context.Context, rect mpv.Rect) error {
	if err := p.ensureSurface(); err != nil {
		return err
	}
	caller := callerWindow(ctx)
	if caller != nil {
		p.followStage(caller, rect.Visible)
	}
	p.surfaceMu.Lock()
	defer p.surfaceMu.Unlock()
	if caller != nil && caller.ID() != p.host {
		return nil
	}
	p.mpv.SetSurfaceRect(rect)
	return nil
}

// SurfaceStats reports what the video surface has drawn. The sampled pixels
// are the only evidence available that frames reached the window, since the
// webview cannot see the layer below it.
func (p *PlayerService) SurfaceStats() mpv.SurfaceStats { return p.mpv.SurfaceStats() }

// ensureSurface binds the render context the first time it is needed. The
// window's content view does not exist when the service starts, so this
// cannot happen in ServiceStartup.
func (p *PlayerService) ensureSurface() error {
	p.mu.Lock()
	window, attached, headless := p.window, p.attached, p.headless
	p.mu.Unlock()

	if attached || headless {
		return nil
	}
	if window == nil {
		return i18n.New("没有可以显示视频的窗口")
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := p.ensureRunning(ctx); err != nil {
		return err
	}
	if err := p.mpv.AttachSurface(window.NativeWindow()); err != nil {
		return err
	}

	p.mu.Lock()
	p.attached = true
	p.mu.Unlock()
	p.log.Info("video surface attached", "state", p.mpv.SurfaceStats().Diagnostic)
	return nil
}

// NewPlayerService wires the engine but starts nothing; ServiceStartup does.
func NewPlayerService(opts PlayerOptions) *PlayerService {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &PlayerService{
		log:      log,
		headless: opts.Headless,
		mpv: mpv.New(mpv.Options{
			Headless: opts.Headless,
			Logger:   log,
		}),
		stream: stream.NewServer(log),
		stop:   make(chan struct{}),
		state:  PlayerState{Idle: true, Volume: 100, Speed: 1, SubtitleStyle: defaultSubtitleStyle},
	}
}

// ServiceName labels the service in Wails logs and bindings metadata.
func (p *PlayerService) ServiceName() string { return "PlayerService" }

// ServiceStartup starts the loopback proxy and the in-process mpv engine.
// Where this build has no usable engine — no backend for the platform
// (mpv.ErrUnsupported), or a Windows build run without its libmpv-2.dll
// (mpv.ErrNoLibrary) — the rest of the app still starts; playing then fails
// with that error, as every player command does there.
func (p *PlayerService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	if err := p.stream.Start(); err != nil {
		return err
	}
	p.mpv.SetEventHandler(p.onMpvEvent)
	if err := p.ensureRunning(ctx); errors.Is(err, mpv.ErrUnsupported) || errors.Is(err, mpv.ErrNoLibrary) {
		p.log.Warn("player: no embedded playback in this build", "error", err)
	} else if err != nil {
		_ = p.stream.Close()
		return err
	}

	p.wg.Add(1)
	go p.emitLoop()

	p.log.Info("player ready", "proxy", p.stream.BaseURL())
	return nil
}

// ServiceShutdown stops mpv and the proxy.
func (p *PlayerService) ServiceShutdown() error {
	p.stopOnce.Do(func() { close(p.stop) })
	p.wg.Wait()
	err := p.mpv.Close()
	if cerr := p.stream.Close(); err == nil {
		err = cerr
	}
	return err
}

// ProxyBaseURL is the loopback root the player pulls from. Useful for
// diagnostics and for handing the same URL to an external tool.
func (p *PlayerService) ProxyBaseURL() string { return p.stream.BaseURL() }

// MpvVersion reports the engine's version string, proving libmpv is loaded.
func (p *PlayerService) MpvVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := p.ensureRunning(ctx); err != nil {
		return "", err
	}
	var v string
	if err := p.mpv.GetProperty(ctx, "mpv-version", &v); err != nil {
		return "", err
	}
	return v, nil
}

// Open publishes a remote URL on the loopback proxy and plays it. headers are
// sent upstream on every request — that is where credentials, User-Agent and
// Referer belong. The returned string is the local URL mpv was given.
func (p *PlayerService) Open(rawURL string, headers map[string]string) (string, error) {
	return p.openVideo("", "", rawURL, headers, QualityState{}, 0)
}

// openVideo is Open for a drive file of account: fileID and its renditions
// are what later quality switches work from. start > 0 opens it that many
// seconds in.
func (p *PlayerService) openVideo(account, fileID, rawURL string, headers map[string]string, quality QualityState, start float64) (string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", i18n.New("没有可播放的地址")
	}
	p.setLoading(true)
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := p.ensureRunning(ctx); err != nil {
		p.setLoading(false)
		return "", err
	}
	// Without a render surface mpv cannot initialise its video output and
	// the file dies with a bare "playback failed". Saying why is the whole
	// difference between a bug report and a fix.
	if err := p.ensureSurface(); err != nil {
		p.setLoading(false)
		p.log.Warn("player: video output unavailable", "error", err)
		return "", i18n.New("无法显示视频画面，请重新打开播放器")
	}

	handle, err := p.stream.Publish(stream.Source{URL: rawURL, Headers: headers})
	if err != nil {
		p.setLoading(false)
		return "", err
	}
	// The video being replaced stops being switchable at once, and a switch
	// still loading for it is dropped before mpv reports anything about the
	// new file. loadMu keeps any switch from loading until the new file and
	// its generation are in place.
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	p.mu.Lock()
	p.gen++
	p.fileID, p.account = "", ""
	p.subs = extSubs{}
	p.dropSwapLocked()
	outAccount, outID, outPos, outDur, outOK := p.track.end()
	p.mu.Unlock()
	p.reportPlayback(outAccount, outID, outPos, outDur, outOK)
	p.resetShifts(ctx)
	if err := p.load(ctx, handle.URL, start); err != nil {
		p.stream.Unpublish(handle.ID)
		p.setLoading(false)
		return "", err
	}

	p.mu.Lock()
	if p.current.ID != "" {
		p.stream.Unpublish(p.current.ID)
	}
	p.current = handle
	p.gen++
	p.fileID, p.account = fileID, account
	p.track = playTrack{account: account, fileID: fileID}
	p.state.Quality = quality
	p.state.Source = rawURL
	p.state.EOF = false
	p.state.Error = ""
	p.stall.reset(time.Now())
	p.dirty = true
	p.mu.Unlock()

	return handle.URL, nil
}

// setLoading marks a play request in flight. DriveService raises it before
// the link lookup so the UI answers the click at once; mpv's file-loaded
// (or a failure, or stop) lowers it.
func (p *PlayerService) setLoading(on bool) {
	p.mu.Lock()
	if p.state.Loading != on {
		p.state.Loading = on
		p.dirty = true
	}
	p.mu.Unlock()
}

// load replaces the current file and makes sure it plays. keep-open leaves
// mpv paused on the last frame of the previous file, and the pause property
// is global: without this the next file in a playlist loads and sits still.
// start > 0 opens the file that many seconds in, as a quality switch does.
func (p *PlayerService) load(ctx context.Context, target string, start float64) error {
	args := []any{"loadfile", target, "replace"}
	if start > 0 {
		args = append(args, "-1", "start="+strconv.FormatFloat(start, 'f', 3, 64))
	}
	if err := p.mpv.Command(ctx, args...); err != nil {
		return err
	}
	return p.mpv.SetProperty(ctx, "pause", false)
}

// reportPlayback hands a file's last position to onPlayback; ok false is a
// file mpv never reported figures for. Called without p.mu.
func (p *PlayerService) reportPlayback(account, fileID string, position, duration float64, ok bool) {
	if ok && p.onPlayback != nil {
		p.onPlayback(account, fileID, position, duration)
	}
}

// lastPlayed is where the drive file playing now was last seen, and whose
// it is; ok is false when there is none, or mpv has not reported on it yet.
func (p *PlayerService) lastPlayed() (account, fileID string, position, duration float64, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.track.last()
}

// openPreview publishes one window's read-only resource without touching playback.
// Each request owns its URL, so a late response can be revoked independently.
func (p *PlayerService) openPreview(owner previewOwnerKey, rawURL string, headers map[string]string, contentType string) (string, error) {
	if application.System.IsServer() && owner.clientID == "" {
		return "", context.Canceled
	}
	if strings.TrimSpace(rawURL) == "" {
		return "", i18n.New("没有可播放的地址")
	}
	handle, err := p.stream.Publish(stream.Source{URL: rawURL, Headers: headers, ContentType: contentType})
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.previewClosed[owner] {
		p.stream.Unpublish(handle.ID)
		return "", context.Canceled
	}
	if p.previews == nil {
		p.previews = make(map[previewOwnerKey]map[string]stream.Handle)
	}
	if p.previews[owner] == nil {
		p.previews[owner] = make(map[string]stream.Handle)
	}
	p.previews[owner][handle.URL] = handle
	return handle.URL, nil
}

// closePreview revokes only the calling window's URL; empty revokes its whole set.
func (p *PlayerService) closePreview(owner previewOwnerKey, url string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, handle := range p.previews[owner] {
		if url == "" || key == url {
			p.stream.Unpublish(handle.ID)
			delete(p.previews[owner], key)
		}
	}
	if len(p.previews[owner]) == 0 {
		delete(p.previews, owner)
	}
}

func (p *PlayerService) closePreviewWindow(owner previewOwnerKey) {
	p.mu.Lock()
	if p.previewClosed == nil {
		p.previewClosed = make(map[previewOwnerKey]bool)
	}
	p.previewClosed[owner] = true
	p.mu.Unlock()
	p.closePreview(owner, "")
}

// TogglePause flips playback and returns the resulting paused flag.
func (p *PlayerService) TogglePause() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := p.mpv.Command(ctx, "cycle", "pause"); err != nil {
		return false, err
	}
	var paused bool
	if err := p.mpv.GetProperty(ctx, "pause", &paused); err != nil {
		return false, err
	}
	return paused, nil
}

// SetPaused forces the paused state.
func (p *PlayerService) SetPaused(paused bool) error {
	return p.withContext(func(ctx context.Context) error {
		return p.mpv.SetProperty(ctx, "pause", paused)
	})
}

// Seek jumps by delta seconds, forward or backward.
func (p *PlayerService) Seek(delta float64) error {
	p.mu.Lock()
	target := p.state.Position + delta
	if p.seek.pending {
		target = p.seek.target + delta
	}
	duration := p.state.Duration
	p.mu.Unlock()
	target = max(0, target)
	if duration > 0 {
		target = min(target, duration)
	}
	return p.seekTo(target, "relative", delta)
}

// SeekTo jumps to an absolute position in seconds.
func (p *PlayerService) SeekTo(position float64) error {
	return p.seekTo(position, "absolute", position)
}

// seekTo sends the seek and reports target as the position until mpv has
// actually landed. Between the command and mpv's playback-restart, time-pos
// still describes the old place; showing that for a tick made the progress
// bar jump back to where it was and then forward again.
func (p *PlayerService) seekTo(target float64, mode string, amount float64) error {
	p.mu.Lock()
	p.seek = seekHold{pending: true, target: target, since: time.Now()}
	p.stall.jumped(time.Now())
	p.dirty = true
	p.mu.Unlock()

	err := p.withContext(func(ctx context.Context) error {
		return p.mpv.Command(ctx, "seek", amount, mode)
	})
	if err != nil {
		p.mu.Lock()
		p.seek.pending = false
		p.dirty = true
		p.mu.Unlock()
	}
	return err
}

// maxVolume is the ceiling the UI offers and mpv is configured with:
// the source level, no software gain.
const maxVolume = 100

// SetVolume sets the output volume, clamped to 0–maxVolume.
func (p *PlayerService) SetVolume(volume float64) error {
	volume = min(max(volume, 0), maxVolume)
	return p.withContext(func(ctx context.Context) error {
		return p.mpv.SetProperty(ctx, "volume", volume)
	})
}

// SetMuted silences the output without touching the volume level.
func (p *PlayerService) SetMuted(muted bool) error {
	return p.withContext(func(ctx context.Context) error {
		return p.mpv.SetProperty(ctx, "mute", muted)
	})
}

// SetSpeed sets the playback rate, 1 being normal.
func (p *PlayerService) SetSpeed(speed float64) error {
	if speed <= 0 {
		return i18n.New("播放速度必须大于 0")
	}
	return p.withContext(func(ctx context.Context) error {
		return p.mpv.SetProperty(ctx, "speed", speed)
	})
}

// StopPlayback unloads the current file and revokes its proxy handle.
func (p *PlayerService) StopPlayback() error {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	p.mu.Lock()
	p.gen++
	p.fileID, p.account = "", ""
	p.subs = extSubs{}
	p.dropSwapLocked()
	outAccount, outID, outPos, outDur, outOK := p.track.end()
	p.mu.Unlock()
	p.reportPlayback(outAccount, outID, outPos, outDur, outOK)
	err := p.withContext(func(ctx context.Context) error {
		return p.mpv.Command(ctx, "stop")
	})

	p.mu.Lock()
	if p.current.ID != "" {
		p.stream.Unpublish(p.current.ID)
		p.current = stream.Handle{}
	}
	p.state.Quality = QualityState{}
	p.state.Source = ""
	p.state.Position = 0
	p.state.Duration = 0
	p.state.Loading = false
	p.dirty = true
	p.mu.Unlock()
	return err
}

// Screenshot saves the picture as shown — subtitles included, at the
// surface's backing resolution — as a PNG in ~/Pictures/PKP, named after
// the playing file and the position, and returns the path. name is the
// drive file's name; mpv's own media-title is the proxy URL's last segment,
// which says nothing. The picture comes from the surface (mpv.Client.Capture)
// rather than mpv's own screenshot-to-file, which on macOS cannot read the
// direct videotoolbox frames hardware decoding produces.
func (p *PlayerService) Screenshot(name string) (string, error) {
	p.mu.Lock()
	state := p.snapshotLocked()
	p.mu.Unlock()
	if state.Duration <= 0 {
		return "", i18n.New("现在没有在播放")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	img, err := p.mpv.Capture(ctx)
	if err != nil {
		return "", err
	}
	dir, err := screenshotDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := screenshotPath(dir, name, state.Position)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// screenshotDir is the user's Pictures folder, in a folder of the app's own.
func screenshotDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Pictures", "PKP"), nil
}

// RevealScreenshot shows a screenshot in the file manager, selected. The
// path comes back from the page, so only a file of the screenshot folder is
// shown; one moved or deleted since leaves the folder itself to open.
func (p *PlayerService) RevealScreenshot(path string) error {
	dir, err := screenshotDir()
	if err != nil {
		return err
	}
	if filepath.Dir(filepath.Clean(path)) != dir {
		return i18n.New("这不是 PKP 保存的截图")
	}
	if _, err := os.Stat(path); err != nil {
		return revealPath(dir, false)
	}
	return revealPath(path, true)
}

// screenshotPath is "<stem> HH.MM.SS.png" in dir, with " (n)" appended while
// that name is taken: two shots within one second must both survive.
func screenshotPath(dir, name string, position float64) string {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if strings.TrimSpace(stem) == "" {
		stem = i18n.T("截图")
	}
	secs := int(position)
	base := fmt.Sprintf("%s %02d.%02d.%02d", download.SafeName(stem, runtime.GOOS), secs/3600, secs/60%60, secs%60)
	path := filepath.Join(dir, base+".png")
	for n := 2; ; n++ {
		if _, err := os.Lstat(path); err != nil {
			return path
		}
		path = filepath.Join(dir, fmt.Sprintf("%s (%d).png", base, n))
	}
}

// Tracks lists the video, audio and subtitle tracks of the loaded file.
func (p *PlayerService) Tracks() ([]Track, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	var listed []struct {
		Track
		DemuxChannels string            `json:"demux-channels"`
		Metadata      map[string]string `json:"metadata"`
	}
	if err := p.mpv.GetProperty(ctx, "track-list", &listed); err != nil {
		return nil, err
	}
	tracks := make([]Track, 0, len(listed))
	for _, l := range listed {
		t := l.Track
		if t.Title == "" {
			t.Title = trackTitle(l.Metadata)
		}
		t.Channels = l.DemuxChannels
		tracks = append(tracks, t)
	}
	return tracks, nil
}

// Chapter is one entry of mpv's chapter-list: a named point on the timeline.
type Chapter struct {
	Title string  `json:"title"`
	Time  float64 `json:"time"`
}

// Chapters lists the loaded file's chapters, empty for files without any.
func (p *PlayerService) Chapters() ([]Chapter, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	chapters := []Chapter{}
	if err := p.mpv.GetProperty(ctx, "chapter-list", &chapters); err != nil {
		return nil, err
	}
	return chapters, nil
}

// SelectTrack switches the active track. kind is "video", "audio" or
// "subtitle"; an id below 1 disables that stream.
func (p *PlayerService) SelectTrack(kind string, id int) error {
	var property string
	switch kind {
	case "video":
		property = "vid"
	case "audio":
		property = "aid"
	case "subtitle":
		property = "sid"
	default:
		return i18n.Errorf("不支持的轨道类型「%s」", kind)
	}

	value := any(id)
	if id < 1 {
		value = "no"
	}
	return p.withContext(func(ctx context.Context) error {
		return p.mpv.SetProperty(ctx, property, value)
	})
}

// State returns the latest known player state without touching mpv.
func (p *PlayerService) State() PlayerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.snapshotLocked()
	state.Running = p.mpv.Running()
	return state
}

// snapshotLocked copies the state with a pending seek's target standing in
// for time-pos, and a switch's held figures for the zeros mpv reports between
// two renditions. Caller holds p.mu.
func (p *PlayerService) snapshotLocked() PlayerState {
	state := p.state
	limit := p.seek.limit
	if limit == 0 {
		limit = seekHoldLimit
	}
	if p.seek.pending && time.Since(p.seek.since) > limit {
		p.seek.pending = false
	}
	if p.seek.pending {
		state.Position = p.seek.target
	}
	if state.Duration == 0 {
		state.Duration = p.held.duration
	}
	if state.Width == 0 {
		state.Width, state.Height = p.held.width, p.held.height
	}
	return state
}

// StreamStats exposes the proxy counters and the upstream throttling signals.
func (p *PlayerService) StreamStats() stream.Stats { return p.stream.Stats() }

func (p *PlayerService) withContext(fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return fn(ctx)
}

// ensureRunning starts mpv if it is not attached, and re-registers the
// property observations, which do not survive a process restart.
func (p *PlayerService) ensureRunning(ctx context.Context) error {
	p.startMu.Lock()
	defer p.startMu.Unlock()

	if p.mpv.Running() {
		return nil
	}
	if err := p.mpv.Start(ctx); err != nil {
		return err
	}
	for _, name := range observedProperties {
		if err := p.mpv.ObserveProperty(ctx, name); err != nil {
			return fmt.Errorf("observe %s: %w", name, err)
		}
	}

	p.mu.Lock()
	p.state.Running = true
	p.state.Error = ""
	p.dirty = true
	style := p.state.SubtitleStyle
	p.mu.Unlock()
	// mpv's options outlive a file but not mpv. A style it refuses leaves
	// subtitles as they come, not the player unusable.
	if err := p.applySubtitleStyle(ctx, style); err != nil {
		p.log.Warn("player: subtitle style not applied", "error", err)
	}
	return nil
}

func (p *PlayerService) onMpvEvent(ev mpv.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch ev.Name {
	case "property-change":
		buffering := p.state.Buffering
		applyProperty(&p.state, ev.Property, ev.Data)
		if p.state.Buffering != buffering {
			p.stall.edge(p.state.Buffering, time.Now())
		}
	case "file-loaded":
		p.state.EOF = false
		p.state.Error = ""
		p.state.Loading = false
		p.loads++
		if p.swap != nil {
			// The switch's position stands until playback restarts there.
			p.settleSwapLocked()
		} else {
			p.seek.pending = false
			// mpv must not be called from its own event goroutine.
			go p.attachExtracted()
		}
	case mpv.EventPlaybackRestart:
		// time-pos is authoritative again from here on, and the new
		// rendition has said its own duration and size.
		p.seek.pending = false
		p.held = heldFigures{}
	case "end-file":
		// A switch replaces the playing rendition, which ends with "stop";
		// its position hold has to outlive that.
		if p.swap == nil {
			p.seek.pending = false
		}
		// A replace first ends the previous file ("stop"); only a failure
		// of the new one ends the load.
		switch ev.Reason {
		case "eof":
			p.state.EOF = true
		case "error":
			if p.swapFailedLocked() {
				break
			}
			p.state.Error = "playback failed"
			if p.stream.Archived(p.current.ID) {
				p.state.Error = errArchivedMessage()
			}
			p.state.Loading = false
		}
	case mpv.EventShutdown:
		p.state.Running = false
		p.state.Idle = true
		p.state.Paused = false
		p.state.Loading = false
		p.dropSwapLocked()
		// The render context died with the core; the next stage report has
		// to build a new one.
		p.attached = false
	}
	p.dirty = true
}

func (p *PlayerService) emitLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(stateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.checkStall()
			p.mu.Lock()
			if !p.dirty {
				p.mu.Unlock()
				continue
			}
			p.dirty = false
			state := p.snapshotLocked()
			// Keep reporting while a seek is in flight, so a stale time-pos
			// never replaces the target on screen.
			p.dirty = p.seek.pending
			save := p.track.observe(p.fileID, state)
			track := p.track
			p.mu.Unlock()
			p.notePicture(state.displayW, state.displayH)
			if save {
				p.reportPlayback(track.account, track.fileID, track.position, track.duration, true)
			}

			state.Running = p.mpv.Running()
			if app := application.Get(); app != nil {
				app.Event.Emit(EventPlayerState, state)
			}
		}
	}
}

// applyProperty folds one mpv property-change into the state. mpv sends null
// for most numeric properties while idle, which decodes to the zero value and
// is exactly what the UI should show.
func applyProperty(state *PlayerState, name string, data json.RawMessage) {
	switch name {
	case "pause":
		decode(data, &state.Paused)
	case "time-pos":
		decode(data, &state.Position)
	case "duration":
		decode(data, &state.Duration)
	case "volume":
		decode(data, &state.Volume)
	case "mute":
		decode(data, &state.Muted)
	case "width":
		decode(data, &state.Width)
	case "height":
		decode(data, &state.Height)
	case "dwidth":
		decode(data, &state.displayW)
	case "dheight":
		decode(data, &state.displayH)
	case "speed":
		decode(data, &state.Speed)
	case "media-title":
		decode(data, &state.Title)
	case "demuxer-cache-duration":
		decode(data, &state.CacheSeconds)
	case "paused-for-cache":
		decode(data, &state.Buffering)
	case "cache-buffering-state":
		decode(data, &state.BufferPercent)
	case "cache-speed":
		decode(data, &state.CacheSpeed)
	case "seeking":
		decode(data, &state.Seeking)
	case "eof-reached":
		decode(data, &state.EOF)
	case "core-idle":
		decode(data, &state.Idle)
	// mpv keeps these as float32: -0.3 comes back as -0.30000001.
	case "sub-delay":
		decode(data, &state.SubtitleDelay)
		state.SubtitleDelay = math.Round(state.SubtitleDelay*1000) / 1000
	case "audio-delay":
		decode(data, &state.AudioDelay)
		state.AudioDelay = math.Round(state.AudioDelay*1000) / 1000
	}
}

func decode[T any](data json.RawMessage, out *T) {
	var zero T
	if len(data) == 0 || string(data) == "null" {
		*out = zero
		return
	}
	if err := json.Unmarshal(data, out); err != nil {
		*out = zero
	}
}
