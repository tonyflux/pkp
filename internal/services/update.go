package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/update"
)

const EventUpdateState = "update:state"

type UpdateInfo struct {
	Version     string `json:"version"`
	PublishedAt string `json:"publishedAt"`
	Notes       string `json:"notes"`
	Size        int64  `json:"size"`
	MinOS       string `json:"minOS"`
	Compatible  bool   `json:"compatible"`
}

type UpdateState struct {
	Current     string      `json:"current"`
	Supported   bool        `json:"supported"`
	Automatic   bool        `json:"automatic"`
	Phase       string      `json:"phase"`
	Error       string      `json:"error"`
	LastChecked string      `json:"lastChecked"`
	Info        *UpdateInfo `json:"info"`
	Received    int64       `json:"received"`
	Notice      bool        `json:"notice"`
}

// UpdateService owns one check/download at a time. It never uses the signed-in
// drive client: no account cookies, tokens or telemetry IDs reach GitHub.
type UpdateService struct {
	drive               *DriveService
	menu                *MenuBarService
	lock                *AppLockService
	app                 *application.App
	client              *update.Client
	platform, osVersion string
	ctx                 context.Context
	stop                context.CancelFunc
	wg                  sync.WaitGroup
	mu                  sync.Mutex
	state               UpdateState
	manifest            *update.Manifest
	path                string
	cacheDir            string
	cancel              context.CancelFunc
	closing             bool
}

func NewUpdateService(drive *DriveService, menu *MenuBarService, lock *AppLockService, version string, release bool) *UpdateService {
	ctx, stop := context.WithCancel(context.Background())
	platform, osVersion := updatePlatform()
	_, versionErr := update.Version(version)
	s := drive.LocalSettings()
	cache, cacheErr := os.UserCacheDir()
	if cacheErr == nil {
		profile := sha256.Sum256([]byte(drive.configDir))
		cache = filepath.Join(cache, "pkp", "updates", hex.EncodeToString(profile[:8]))
	}
	return &UpdateService{
		drive: drive, menu: menu, lock: lock, ctx: ctx, stop: stop,
		platform: platform, osVersion: osVersion,
		cacheDir: cache,
		client:   &update.Client{HTTP: &http.Client{Transport: netproxy.Client().Transport, CheckRedirect: update.Redirect}, PublicKey: update.PublicKey()},
		state:    UpdateState{Current: version, Supported: release && versionErr == nil && platform != "", Automatic: !s.UpdateChecksOff, Phase: "idle", LastChecked: s.UpdateLastCheck},
	}
}

//wails:ignore
func (u *UpdateService) Attach(app *application.App) { u.app = app }

func (u *UpdateService) ServiceStartup(context.Context, application.ServiceOptions) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closing || !u.state.Supported {
		return nil
	}
	// One process per profile is enforced before service startup. Remove only our
	// previous generated downloads, including files left by a terminated process.
	if u.cacheDir != "" {
		files, _ := filepath.Glob(filepath.Join(u.cacheDir, ".update-*"))
		for _, path := range files {
			_ = os.Remove(path)
		}
	}
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-u.ctx.Done():
				return
			case <-timer.C:
			}
			u.mu.Lock()
			last, _ := time.Parse(time.RFC3339, u.state.LastChecked)
			due := u.state.Automatic && time.Since(last) >= 24*time.Hour
			u.mu.Unlock()
			if due {
				_ = u.check(false)
			}
			timer.Reset(time.Hour)
		}
	}()
	return nil
}

func (u *UpdateService) ServiceShutdown() error {
	u.mu.Lock()
	u.closing = true
	u.stop()
	u.mu.Unlock()
	u.wg.Wait()
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.path != "" && u.state.Phase != "installing" {
		_ = os.Remove(u.path)
	}
	return nil
}

func (u *UpdateService) State() UpdateState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.snapshotLocked()
}

func (u *UpdateService) snapshotLocked() UpdateState {
	s := u.state
	if s.Info != nil {
		info := *s.Info
		s.Info = &info
	}
	return s
}

func (u *UpdateService) publishLocked() {
	if u.app != nil {
		u.app.Event.Emit(EventUpdateState, u.snapshotLocked())
	}
}

func (u *UpdateService) SetAutomatic(on bool) (UpdateState, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := u.drive.updateSettings(func(s *LocalSettings) { s.UpdateChecksOff = !on }); err != nil {
		return u.snapshotLocked(), err
	}
	u.state.Automatic = on
	u.publishLocked()
	return u.snapshotLocked(), nil
}

func (u *UpdateService) Check() error { return u.check(true) }

func (u *UpdateService) check(manual bool) error {
	u.mu.Lock()
	if !manual && (!u.state.Automatic || u.path != "") {
		u.mu.Unlock()
		return nil
	}
	if err := u.canStartLocked(); err != nil {
		u.mu.Unlock()
		return err
	}
	if u.path != "" {
		_ = os.Remove(u.path)
		u.path = ""
	}
	u.state.Phase, u.state.Error, u.state.Notice = "checking", "", false
	u.state.Info, u.manifest, u.state.Received = nil, nil, 0
	u.state.LastChecked = time.Now().UTC().Format(time.RFC3339)
	if err := u.drive.updateSettings(func(s *LocalSettings) { s.UpdateLastCheck = u.state.LastChecked }); err != nil {
		u.state.Phase, u.state.Error = "error", "storage"
		u.publishLocked()
		u.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithTimeout(u.ctx, 30*time.Second)
	u.cancel = cancel
	u.wg.Add(1)
	u.publishLocked()
	u.mu.Unlock()
	go func() {
		defer u.wg.Done()
		defer cancel()
		m, err := u.client.Latest(ctx)
		u.mu.Lock()
		defer u.mu.Unlock()
		u.cancel = nil
		if u.closing {
			return
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			u.state.Phase = "idle"
		} else if err != nil {
			u.state.Phase, u.state.Error = "error", updateError(err, "network")
		} else if !update.Newer(m.Version, u.state.Current) {
			u.state.Phase = "current"
		} else {
			p := m.Packages[u.platform]
			u.manifest = &m
			u.state.Info = &UpdateInfo{Version: m.Version, PublishedAt: m.PublishedAt, Notes: m.Notes, Size: p.Size, MinOS: p.MinOS, Compatible: update.SupportsOS(u.osVersion, p.MinOS)}
			u.state.Phase = "available"
			s := u.drive.LocalSettings()
			remind, _ := time.Parse(time.RFC3339, s.UpdateRemindAfter)
			u.state.Notice = !manual && u.state.Info.Compatible && m.Version != s.UpdateIgnored && !time.Now().Before(remind)
		}
		u.publishLocked()
	}()
	return nil
}

func (u *UpdateService) canStartLocked() error {
	if u.closing || !u.state.Supported {
		return i18n.New("此构建不支持应用内更新")
	}
	if u.cancel != nil || u.state.Phase == "installing" {
		return i18n.New("更新操作正在进行")
	}
	return nil
}

func (u *UpdateService) Download() error {
	u.mu.Lock()
	if err := u.canStartLocked(); err != nil {
		u.mu.Unlock()
		return err
	}
	if u.manifest == nil || u.state.Info == nil || !u.state.Info.Compatible {
		u.mu.Unlock()
		return i18n.New("没有适用于此系统的更新")
	}
	if u.path != "" {
		u.state.Phase = "ready"
		u.publishLocked()
		u.mu.Unlock()
		return nil
	}
	if u.cacheDir == "" {
		u.mu.Unlock()
		return i18n.New("无法保存更新文件或设置，请检查磁盘空间与目录权限。")
	}
	m := *u.manifest
	p := m.Packages[u.platform]
	ctx, cancel := context.WithTimeout(u.ctx, 2*time.Hour)
	u.cancel = cancel
	u.state.Phase, u.state.Error, u.state.Received, u.state.Notice = "downloading", "", 0, false
	u.wg.Add(1)
	u.publishLocked()
	u.mu.Unlock()
	go func() {
		defer u.wg.Done()
		defer cancel()
		var last time.Time
		path, err := u.client.Download(ctx, p, u.cacheDir, update.PackageName(m.Version, u.platform), func(n int64) {
			if time.Since(last) < 200*time.Millisecond && n != p.Size {
				return
			}
			last = time.Now()
			u.mu.Lock()
			defer u.mu.Unlock()
			u.state.Received = n
			u.publishLocked()
		})
		u.mu.Lock()
		defer u.mu.Unlock()
		u.cancel = nil
		if u.closing {
			if path != "" {
				_ = os.Remove(path)
			}
			return
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			if path != "" {
				_ = os.Remove(path)
			}
			u.state.Phase, u.state.Received = "available", 0
		} else if err != nil {
			u.state.Phase, u.state.Error = "error", updateError(err, "download")
		} else {
			u.path = path
			u.state.Phase, u.state.Received = "ready", p.Size
		}
		u.publishLocked()
	}()
	return nil
}

func updateError(err error, fallback string) string {
	switch {
	case errors.Is(err, update.ErrSignature):
		return "signature"
	case errors.Is(err, update.ErrIntegrity):
		return "integrity"
	case errors.Is(err, update.ErrManifest):
		return "manifest"
	case errors.Is(err, update.ErrNoRelease):
		return "noRelease"
	case errors.Is(err, update.ErrRateLimit):
		return "rateLimit"
	}
	var fileErr *os.PathError
	if errors.As(err, &fileErr) {
		return "storage"
	}
	return fallback
}

func (u *UpdateService) Cancel() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil {
		u.cancel()
	}
}

func (u *UpdateService) Ignore() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state.Info == nil {
		return nil
	}
	if err := u.drive.updateSettings(func(s *LocalSettings) { s.UpdateIgnored = u.state.Info.Version }); err != nil {
		return err
	}
	u.state.Notice = false
	u.publishLocked()
	return nil
}

func (u *UpdateService) RemindLater() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := u.drive.updateSettings(func(s *LocalSettings) {
		s.UpdateRemindAfter = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	}); err != nil {
		return err
	}
	u.state.Notice = false
	u.publishLocked()
	return nil
}

// Install rechecks the cached package before handing it to the OS. On Windows
// the installer is launched only after quit confirmation, before setting quitting;
// rejecting UAC therefore leaves PKP running. NSIS waits for this PID to exit.
func (u *UpdateService) Install() error {
	u.mu.Lock()
	if err := u.canStartLocked(); err != nil {
		u.mu.Unlock()
		return err
	}
	if u.path == "" || u.manifest == nil || u.state.Phase != "ready" {
		u.mu.Unlock()
		return i18n.New("请先下载并校验更新")
	}
	path, p := u.path, u.manifest.Packages[u.platform]
	u.state.Phase, u.state.Error = "installing", ""
	u.publishLocked()
	u.mu.Unlock()
	start := func() error {
		if u.lock != nil && u.lock.Locked() {
			u.installResult(errAppLocked)
			return errAppLocked
		}
		if err := update.CheckFile(path, p); err != nil {
			u.installResult(err)
			return err
		}
		err := openUpdatePackage(path)
		if err != nil || runtime.GOOS != "windows" {
			u.installResult(err)
		}
		return err
	}
	if runtime.GOOS == "windows" && u.menu != nil {
		if err := u.menu.requestUpdateInstall(start, func() { u.installResult(nil) }); err != nil {
			u.installResult(err)
			return err
		}
		return nil
	}
	return start()
}

func (u *UpdateService) installResult(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.state.Phase = "ready"
	if err != nil {
		u.state.Error = updateError(err, "install")
		if errors.Is(err, update.ErrIntegrity) || errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(u.path)
			u.path, u.state.Phase, u.state.Received = "", "error", 0
		}
	}
	u.publishLocked()
}
