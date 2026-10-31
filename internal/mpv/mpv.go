// Package mpv embeds libmpv in this process and renders video into a native
// view owned by the application window.
//
// The player is a library, not a subprocess: the render API hands frames to a
// GL surface the webview sits on top of, which is the only way to get video
// inside the window on macOS — mpv's --wid embedding covers X11, win32 and
// Android, but not Cocoa. On Windows, where it does, mpv draws into a window
// of its own inside a child window below the WebView2 (surface_windows.go).
//
// The trade is explicit: linking libmpv makes the binary a GPL derivative
// (see LICENSE) and a decoder crash now takes the whole application with it.
package mpv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"maps"
	"strconv"
	"strings"
)

// Event names. Everything except EventShutdown mirrors mpv's own event names.
const (
	// EventPropertyChange carries an observed property's new value.
	EventPropertyChange = "property-change"
	// EventFileLoaded fires once a file's streams are open.
	EventFileLoaded = "file-loaded"
	// EventEndFile fires when playback of a file stops, with Reason set.
	EventEndFile = "end-file"
	// EventPlaybackRestart fires when playback resumes after a seek or a
	// load: the first moment time-pos reflects the new position.
	EventPlaybackRestart = "playback-restart"
	// EventShutdown fires when the core has terminated and no further
	// commands will succeed.
	EventShutdown = "shutdown"
)

// End-file reasons, as reported in Event.Reason.
const (
	ReasonEOF      = "eof"
	ReasonStop     = "stop"
	ReasonQuit     = "quit"
	ReasonError    = "error"
	ReasonRedirect = "redirect"
)

var (
	// ErrNotRunning is returned by every call made while no core is attached.
	ErrNotRunning = errors.New("mpv: not running")
	// ErrUnsupported is returned on platforms without an embedding backend.
	ErrUnsupported = errors.New("mpv: embedded playback is not implemented on this platform")
	// ErrNoLibrary is returned where libmpv is loaded at run time (Windows)
	// and is missing or will not load: pkp.exe run without the
	// libmpv-2.dll the installer puts beside it.
	ErrNoLibrary = errors.New("mpv: libmpv is not available")
)

// defaults are applied before mpv_initialize; see startOptions for what
// overrides them. There is no "osc": mpv's built-in libmpv profile keeps the
// on-screen controller off (and the bundled libmpv, built without Lua on
// both platforms, would refuse the option).
var defaults = map[string]string{
	"idle":                   "yes",
	"terminal":               "no",
	"keep-open":              "yes",
	"hwdec":                  "auto-safe",
	"input-default-bindings": "no",
	"input-vo-keyboard":      "no",
	// 100 is the source level. mpv's default ceiling of 130 is software
	// gain that clips; the UI offers 0–100 and mpv refuses more.
	"volume-max": "100",
	// ffmpeg's http reader gives up on the first failed request and reports
	// the file as truncated, which mpv plays through to the end. Reconnect
	// on dropped connections and on 5xx, including for a seek's Range
	// request, with a short cap so a dead link still surfaces as an error.
	"stream-lavf-o": "reconnect=1,reconnect_streamed=1,reconnect_on_http_error=5xx,reconnect_delay_max=4",
	// A start or a seek waits, paused for cache, until a second of playback
	// is read ahead instead of starting on the first frame and stalling at
	// once over the network; cache-buffering-state then counts that wait up,
	// which the player shows as a percentage.
	"cache-pause-initial": "yes",
}

// startOptions is what Start sets before mpv_initialize, later sources
// winning: defaults, the backend's video output, a headless core's null
// outputs, Options.Extra.
func startOptions(opts Options, backend map[string]string) map[string]string {
	options := maps.Clone(defaults)
	maps.Copy(options, backend)
	if opts.Headless {
		options["vo"] = "null"
		options["ao"] = "null"
	}
	maps.Copy(options, opts.Extra)
	return options
}

// scalarString renders a command argument or property value the way mpv's own
// parser expects. Floats keep full precision: a seek target rounded to an int
// would land on the wrong frame.
func scalarString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "yes"
		}
		return "no"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func trimNewline(s string) string { return strings.TrimRight(s, "\r\n") }

// videoRect is the video's place inside a w×h picture of the surface, in the
// same pixels; the bars around it are the page's background, not picture.
// The whole picture when mpv measures its output at another size.
func (c *Client) videoRect(ctx context.Context, w, h int) image.Rectangle {
	crop := image.Rect(0, 0, w, h)
	var dims struct {
		W, H, ML, MR, MT, MB int
	}
	if err := c.GetProperty(ctx, "osd-dimensions", &dims); err == nil && dims.W == w && dims.H == h {
		// Margins go negative when the video is zoomed past the surface.
		inner := image.Rect(dims.ML, dims.MT, w-dims.MR, h-dims.MB).Intersect(crop)
		if !inner.Empty() {
			crop = inner
		}
	}
	return crop
}

// Options configures a Client.
type Options struct {
	// Headless disables video and audio output. Tests use it; it also skips
	// the render surface entirely.
	Headless bool
	// Extra are raw mpv options applied before initialisation, so they can
	// override the defaults.
	Extra map[string]string
	// Logger receives mpv's own log messages at warning level and above.
	Logger *slog.Logger
}

// Event is one decoded mpv event. Property and Data are populated only for
// EventPropertyChange; Reason only for EventEndFile.
type Event struct {
	Name     string
	Property string
	Data     json.RawMessage
	Reason   string
}

// Rect is the surface position in web coordinates: origin at the top-left of
// the window's content area, measured in points.
type Rect struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Visible bool    `json:"visible"`
}

// SurfaceStats describes the render surface. TopPixel and BottomPixel are
// samples of the last rendered frame, taken from the upper and lower thirds
// of the view as the user sees it; they make "did a real frame land here"
// answerable without a screenshot.
type SurfaceStats struct {
	Attached    bool     `json:"attached"`
	Frames      uint64   `json:"frames"`
	TopPixel    [4]uint8 `json:"topPixel"`
	BottomPixel [4]uint8 `json:"bottomPixel"`
	Rect        Rect     `json:"rect"`
	// Diagnostic is the live AppKit state of the surface. "No picture" has
	// several causes that look identical from outside the process, and this
	// is the only place they can be told apart.
	Diagnostic string `json:"diagnostic"`
}
