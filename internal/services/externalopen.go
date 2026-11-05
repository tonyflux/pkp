package services

import (
	"encoding/hex"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/tonyflux/pkp/internal/torrent"
)

// EventExternalOpen only signals that Pending may have changed; paths and links
// stay behind the same App Lock gate as the other private service calls.
const EventExternalOpen = "external:open"

// ExternalOpen is an OS request awaiting the Cloud Add confirmation UI.
type ExternalOpen struct {
	ID    uint   `json:"id"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// ExternalOpenService retains requests until the page has safely staged them.
// It never reads files or creates downloads, including while signed out or locked.
type ExternalOpenService struct {
	mu      sync.Mutex
	nextID  uint
	pending []ExternalOpen
	notify  func()
}

func NewExternalOpenService() *ExternalOpenService {
	return &ExternalOpenService{}
}

// Attach listens before Run so cold-start Apple events cannot beat the page.
// Windows/Linux events in beta.28 are synthesized from the initial argv, which
// OpenArgs already handles (including multiple arguments and magnet:? links).
//
//wails:ignore
func (s *ExternalOpenService) Attach(app *application.App, menu *MenuBarService, workingDir string) {
	if runtime.GOOS == "darwin" {
		app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
			s.OpenURL(e.Context().URL())
		})
		app.Event.OnApplicationEvent(events.Common.ApplicationOpenedWithFile, func(e *application.ApplicationEvent) {
			s.OpenFile(e.Context().Filename(), workingDir)
		})
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		s.start(func() {
			menu.Open("app", "", "")
			app.Event.Emit(EventExternalOpen)
		})
	})
}

func (s *ExternalOpenService) start(notify func()) {
	s.mu.Lock()
	s.notify = notify
	pending := len(s.pending) != 0
	s.mu.Unlock()
	if pending {
		notify()
	}
}

// Pending is a snapshot, not a drain: a page that is not ready loses nothing.
func (s *ExternalOpenService) Pending() []ExternalOpen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ExternalOpen{}, s.pending...)
}

// Ack removes only the request safely staged by the page, never a newer arrival.
func (s *ExternalOpenService) Ack(id uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.pending {
		if item.ID == id {
			copy(s.pending[i:], s.pending[i+1:])
			s.pending[len(s.pending)-1] = ExternalOpen{}
			s.pending = s.pending[:len(s.pending)-1]
			return
		}
	}
}

// OpenURL accepts only magnet links, never URLs that could name local files.
//
//wails:ignore
func (s *ExternalOpenService) OpenURL(value string) bool {
	if !externalMagnet(value) {
		return false
	}
	if value[:len("magnet:")] != "magnet:" {
		value = "magnet:" + value[len("magnet:"):]
	}
	s.enqueue("magnet", value)
	return true
}

// OpenFile accepts filesystem input from the OS, not a browser URL callback.
// Content validation belongs to ReadTorrent after the owner opens Cloud Add.
//
//wails:ignore
func (s *ExternalOpenService) OpenFile(value, workingDir string) bool {
	path := externalTorrentPath(value, workingDir)
	if path == "" {
		return false
	}
	s.enqueue("torrent", path)
	return true
}

// OpenArgs accepts the complete argv, including the executable at index zero.
// Second-instance relative paths belong to the launching process's directory.
//
//wails:ignore
func (s *ExternalOpenService) OpenArgs(args []string, workingDir string) {
	for i, value := range args {
		if i != 0 && !s.OpenURL(value) {
			s.OpenFile(value, workingDir)
		}
	}
}

func (s *ExternalOpenService) enqueue(kind, value string) {
	s.mu.Lock()
	for _, item := range s.pending {
		if item.Kind == kind && item.Value == value {
			notify := s.notify
			s.mu.Unlock()
			if notify != nil {
				notify()
			}
			return
		}
	}
	s.nextID++
	s.pending = append(s.pending, ExternalOpen{ID: s.nextID, Kind: kind, Value: value})
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
}

func externalText(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

func externalMagnet(value string) bool {
	if len(value) < len("magnet:?") || !strings.EqualFold(value[:len("magnet:?")], "magnet:?") || !externalText(value) || strings.ContainsFunc(value, unicode.IsSpace) {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") || u.Opaque != "" || u.Host != "" || u.User != nil || u.Path != "" || u.Fragment != "" || strings.Contains(value, "#") {
		return false
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for key, rows := range values {
		if !externalText(key) {
			return false
		}
		for _, row := range rows {
			if row != "" && !externalText(row) {
				return false
			}
		}
	}
	if torrent.BTIH(value) != "" {
		return true
	}
	for key, rows := range values {
		if key != "xt" && !strings.HasPrefix(key, "xt.") {
			continue
		}
		for _, row := range rows {
			const prefix = "urn:btmh:1220"
			if len(row) == len(prefix)+64 && strings.EqualFold(row[:len(prefix)], prefix) {
				if _, err := hex.DecodeString(row[len(prefix):]); err == nil {
					return true
				}
			}
		}
	}
	return false
}

func externalTorrentPath(value, workingDir string) string {
	if !externalText(value) {
		return ""
	}
	// A Windows drive letter is a filesystem volume, not a URL scheme.
	if filepath.VolumeName(value) == "" {
		u, err := url.Parse(value)
		if err != nil && strings.Contains(value, ":") {
			return ""
		}
		if err == nil && u.Scheme != "" {
			if !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" || u.User != nil || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) || strings.HasPrefix(u.Path, "//") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(value, "#") {
				return ""
			}
			value = u.Path
			if runtime.GOOS == "windows" && len(value) >= 3 && value[0] == '/' && value[2] == ':' {
				value = value[1:]
			}
			value = filepath.FromSlash(value)
			if !filepath.IsAbs(value) {
				return ""
			}
		}
	}
	if !externalText(value) || !strings.EqualFold(filepath.Ext(value), ".torrent") {
		return ""
	}
	if !filepath.IsAbs(value) {
		if filepath.VolumeName(value) != "" {
			return ""
		}
		if !filepath.IsAbs(workingDir) {
			return ""
		}
		value = filepath.Join(workingDir, value)
	}
	return filepath.Clean(value)
}
