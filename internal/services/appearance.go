package services

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/i18n"
)

// AppearanceRoute is where the asset server mounts AppearanceService; the
// page loads the custom wallpaper from AppearanceRoute + "/wallpaper".
const AppearanceRoute = "/appearance"

// maxWallpaperBytes bounds what is copied into the config directory. An 8K
// PNG is well under it; anything bigger is not a picture worth decoding at
// every launch.
const maxWallpaperBytes = 64 << 20

// wallpaperTypes are the formats every webview the app runs in can draw.
var wallpaperTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

// AppearanceService keeps the custom wallpaper (Settings · General · Wallpaper) and serves
// it to the page. The rest of the appearance — light or dark, the accent
// colour — lives in the webview's localStorage; an image is too big for
// that, so it is copied into the config directory instead.
type AppearanceService struct {
	dir string
	// err is why dir could not be resolved; nothing is kept then.
	err error
	// mu serialises replacing and removing the file.
	mu sync.Mutex
}

// NewAppearanceService keeps the wallpaper in configDir, or in the per-user
// config directory DriveService also uses when configDir is empty.
func NewAppearanceService(configDir string) *AppearanceService {
	dir, err := ResolveConfigDir(configDir)
	if err != nil {
		slog.Warn("appearance: no config directory", "error", err)
		err = errConfigDir
	}
	return &AppearanceService{dir: dir, err: err}
}

// ServiceName labels the service in Wails logs and bindings metadata.
func (a *AppearanceService) ServiceName() string { return "AppearanceService" }

func (a *AppearanceService) wallpaperPath() string {
	return filepath.Join(a.dir, "wallpaper")
}

// SetWallpaper copies the image at path into the config directory, replacing
// the previous one, and returns a version for the page to put in the
// wallpaper's URL, so the webview fetches the new picture rather than its
// cached copy of the old one. JPEG, PNG, WebP and GIF are taken.
func (a *AppearanceService) SetWallpaper(path string) (string, error) {
	if a.err != nil {
		return "", a.err
	}
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > maxWallpaperBytes {
		return "", i18n.Errorf("「%s」超过 %d MB，换一张小一点的图片", filepath.Base(path), maxWallpaperBytes>>20)
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	if !wallpaperTypes[http.DetectContentType(head[:n])] {
		return "", i18n.Errorf("「%s」不是 JPEG、PNG、WebP 或 GIF 图片", filepath.Base(path))
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return "", err
	}
	// Copied beside the target and renamed over it: a copy cut short leaves
	// the previous wallpaper in place, not half a picture.
	tmp, err := os.CreateTemp(a.dir, "wallpaper-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), a.wallpaperPath()); err != nil {
		return "", err
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36), nil
}

// ClearWallpaper goes back to the built-in scene. Having none is not an error.
func (a *AppearanceService) ClearWallpaper() error {
	if a.err != nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.Remove(a.wallpaperPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ServeHTTP answers AppearanceRoute + "/wallpaper" with the custom wallpaper,
// typed by its content, and 404 when there is none.
func (a *AppearanceService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wallpaper" || a.err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(a.wallpaperPath())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The page versions the URL; revalidating as well keeps a cache that
	// ignores the query from showing a replaced picture.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", info.ModTime(), f)
}
