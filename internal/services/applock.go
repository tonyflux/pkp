package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/tonyflux/pkp/internal/applock"
	"github.com/tonyflux/pkp/internal/i18n"
)

// App Lock: once switched on,
// PKP shows its lock screen instead of the library until the owner is
// confirmed — whenever it starts, the screen locks, the main window closes,
// someone presses Lock PKP Now, or the user has been away from the app for the
// chosen time. Two ways in, one chosen: System Authentication (Touch ID or the login
// password, Windows Hello — the system's own dialog) or PKP Standalone Password, a
// password of this computer's PKP alone (applock_password.go). Under System Authentication
// a standalone password, if set, is the way in when the system's is not. Switching the
// lock on or off and changing the way in are proven with the way in now.
//
// The lock state lives here, not in a page: while it holds, the pages' calls
// into Go wait at LockGate, so nothing private reaches a window until the
// owner is back, and the windows are kept out of screenshots. Transfers run
// on in Go; a playing video is paused and its position written. It belongs
// to the computer, not to an account.

// EventAppLockState carries AppLockState to every window whenever it changes.
const EventAppLockState = "applock:state"

// Auto-Lock After Leaving App is any whole number of minutes from 0 (Immediately) to
// maxAppLockDelay: the page offers 5, 15, 30 and 60 and lets the user type
// the rest (Custom).
const (
	defaultAppLockDelay = 5
	maxAppLockDelay     = 24 * 60
)

// validAppLockDelay reports whether minutes is a delay PKP can keep.
func validAppLockDelay(minutes int) bool {
	return minutes >= 0 && minutes <= maxAppLockDelay
}

// appLockInstant is how long Immediately waits after the app goes to the
// background: the system's own authentication dialog can take the app out
// of the front for a moment, and coming back within this undoes it.
var appLockInstant = time.Second

// maxHeldCalls bounds the calls LockGate keeps waiting; any more are
// refused rather than piling up while the lock holds for hours.
const maxHeldCalls = 256

// The reasons the system's dialog gives, completing 「“PKP” is trying to…」, in
// the language in force when it opens.
func unlockReason() string      { return i18n.T("解锁 PKP") }
func enableReason() string      { return i18n.T("开启应用锁") }
func disableReason() string     { return i18n.T("关闭应用锁") }
func toSystemReason() string    { return i18n.T("改用系统验证解锁") }
func setPasswordReason() string { return i18n.T("设置 PKP 独立密码") }

// What AppLockState.Playback says locking did to the video that was playing.
const (
	lockPlaybackPaused = "paused"
	lockPlaybackSaved  = "saved"
)

// errAppLocked refuses a change that must wait for the owner, and is what a
// call LockGate refuses says.
var errAppLocked = i18n.New("PKP 已锁定，解锁后再试")

// AppLockState is how App Lock stands, for the settings page, the lock screen
// and the menu bar panel.
type AppLockState struct {
	// Enabled is the setting: PKP locks itself.
	Enabled bool `json:"enabled"`
	// Delay is Auto-Lock After Leaving App in minutes, 0 (Immediately) to 1440.
	Delay int `json:"delay"`
	// Locked: the lock screen is up and calls wait for the owner.
	Locked bool `json:"locked"`
	// Method is the way in: "system" or "password".
	Method string `json:"method"`
	// HasPassword: a PKP standalone password is set on this computer.
	HasPassword bool `json:"hasPassword"`
	// ProveWith is what a change to the lock is proven with now: "system"
	// (the system's dialog), "password" (the standalone password) or "" (nothing can be
	// asked; nothing is protected either).
	ProveWith string `json:"proveWith"`
	// Available is whether the system can authenticate its owner now;
	// Unavailable says why not ("" when it can).
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable"`
	// Authenticating: the system's dialog is up.
	Authenticating bool `json:"authenticating"`
	// AttemptsLeft is how many wrong standalone password attempts remain before a cooldown;
	// CooldownUntil the end of the cooldown under way, unix milliseconds,
	// 0 for none.
	AttemptsLeft  int   `json:"attemptsLeft"`
	CooldownUntil int64 `json:"cooldownUntil"`
	// Playback is what locking did to the video playing then: "" nothing
	// was playing, "paused" paused (its position could not be confirmed
	// written), "saved" paused with its position written.
	Playback string `json:"playback"`
}

// AppLockResult is how a request that had to prove the owner ended. Result
// is "ok"; "cancelled", "failed", "unavailable" or "busy" (a dialog is up
// already) from the system; "wrong" (the standalone password is not that), "cooldown"
// (too many wrong ones, wait) or "invalid" (a new password breaks a rule).
// Message says why for anything but "ok".
type AppLockResult struct {
	Result  string       `json:"result"`
	Message string       `json:"message"`
	State   AppLockState `json:"state"`
}

func (r AppLockResult) ok() bool { return r.Result == "ok" }

// The system's side and the clock, seams for tests.
var (
	lockAvailable    = applock.Available
	lockAuthenticate = applock.Authenticate
	lockClock        = time.Now
)

// AppLockService owns App Lock. Create it with the services it pauses and
// redacts, Attach it to the app and its main window, and put LockGate in the
// asset server's middleware.
type AppLockService struct {
	drive   *DriveService
	player  *PlayerService
	menu    *MenuBarService
	secrets lockSecretStore
	log     *slog.Logger

	app  *application.App
	main *application.WebviewWindow

	// The binding calls a locked window may still make, by id and by name.
	freeIDs   map[uint32]bool
	freeNames map[string]bool

	// pwMu takes one standalone password guess at a time, so the count holds.
	pwMu sync.Mutex

	mu       sync.Mutex
	locked   bool
	authing  bool
	playback string
	// open is closed while unlocked; a held call waits on it.
	open chan struct{}
	held int
	// leave locks after the user has been away; leaveGen voids a timer
	// that fired as it was being stopped.
	leave    *time.Timer
	leaveGen int
	// lockGen voids a playback report of an earlier lock.
	lockGen int
}

// NewAppLockService prepares App Lock. With the setting on, PKP starts locked:
// quitting and opening it again is no way around the lock. A lock nothing
// could open (System Authentication gone and no standalone password) is not put up.
func NewAppLockService(drive *DriveService, player *PlayerService, menu *MenuBarService) *AppLockService {
	l := &AppLockService{
		drive:   drive,
		player:  player,
		menu:    menu,
		secrets: newLockSecretStore(drive.configDir),
		log:     slog.Default(),
		open:    make(chan struct{}),
	}
	l.freeIDs, l.freeNames = lockFreeMethods()
	if s := drive.LocalSettings(); s.AppLock {
		if ok, _ := lockable(s); ok {
			l.locked = true
		}
	}
	if !l.locked {
		close(l.open)
	}
	if menu != nil {
		menu.lock = l
	}
	return l
}

// lockable reports whether a lock could be opened again, and if not, why.
func lockable(s LocalSettings) (bool, string) {
	if s.AppLockMethod == lockMethodPassword {
		if s.AppLockPassword {
			return true, ""
		}
		return false, i18n.T("还没有设置 PKP 独立密码")
	}
	ok, why := lockAvailable()
	if ok || s.AppLockPassword {
		return true, ""
	}
	return false, why
}

// proveWith is what a change is proven with under s: see AppLockState.ProveWith.
func proveWith(s LocalSettings, systemOK bool) string {
	switch {
	case s.AppLockMethod == lockMethodPassword && s.AppLockPassword:
		return lockMethodPassword
	case systemOK:
		return lockMethodSystem
	case s.AppLockPassword:
		return lockMethodPassword
	}
	return ""
}

// Attach watches what locks PKP by itself: the screen locking, and the user
// leaving the app for the chosen time. Call it before app.Run.
//
//wails:ignore
func (l *AppLockService) Attach(app *application.App, main *application.WebviewWindow) {
	l.app = app
	l.main = main
	applock.WatchScreenLock(l.lockIfEnabled)
	// Started locked: the windows are kept out of screenshots from the first.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if l.Locked() {
			l.shieldAll(true)
		}
	})
	if runtime.GOOS == "darwin" {
		// The app, not a window: moving between PKP's own windows is not
		// leaving, and the menu bar panel never activates PKP.
		app.Event.OnApplicationEvent(events.Mac.ApplicationDidResignActive, func(*application.ApplicationEvent) { l.left() })
		app.Event.OnApplicationEvent(events.Mac.ApplicationDidBecomeActive, func(*application.ApplicationEvent) { l.returned() })
		return
	}
	// Elsewhere the app is away once none of its windows has focus.
	watch := func(w application.Window) {
		w.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { l.returned() })
		w.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
			// Focus moving to another of PKP's windows arrives as a loss
			// here before the gain there.
			time.AfterFunc(200*time.Millisecond, func() {
				for _, other := range app.Window.GetAll() {
					if other.IsFocused() {
						return
					}
				}
				l.left()
			})
		})
	}
	for _, w := range app.Window.GetAll() {
		watch(w)
	}
	app.Window.OnCreate(watch)
}

// State is how App Lock stands now.
func (l *AppLockService) State() AppLockState {
	s := l.drive.LocalSettings()
	ok, why := lockAvailable()
	state := AppLockState{
		Enabled:      s.AppLock,
		Delay:        s.AppLockDelay,
		Method:       s.AppLockMethod,
		HasPassword:  s.AppLockPassword,
		ProveWith:    proveWith(s, ok),
		Available:    ok,
		Unavailable:  why,
		AttemptsLeft: s.AppLockThrottle.attemptsLeft(),
	}
	if s.AppLockThrottle.cooling(lockClock()) > 0 {
		state.CooldownUntil = s.AppLockThrottle.Until
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state.Locked = l.locked
	state.Authenticating = l.authing
	state.Playback = l.playback
	return state
}

// Locked reports whether the lock screen is up.
func (l *AppLockService) Locked() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.locked
}

// settled fills a result's state.
func (l *AppLockService) settled(r AppLockResult) AppLockResult {
	r.State = l.State()
	return r
}

// SetEnabled switches App Lock, once the owner is proven with the way in now
// (password is the standalone password when that is it, unused otherwise). It is not
// switched on with a lock nothing could open.
func (l *AppLockService) SetEnabled(ctx context.Context, on bool, password string) (AppLockResult, error) {
	if l.Locked() {
		return AppLockResult{}, errAppLocked
	}
	s := l.drive.LocalSettings()
	if on {
		if ok, why := lockable(s); !ok {
			return l.settled(AppLockResult{Result: "unavailable", Message: why}), nil
		}
	}
	reason := enableReason()
	if !on {
		reason = disableReason()
	}
	if r := l.prove(ctx, s, password, reason); !r.ok() {
		return l.settled(r), nil
	}
	if !on {
		l.stopLeave()
	}
	if err := l.drive.updateSettings(func(s *LocalSettings) { s.AppLock = on }); err != nil {
		return AppLockResult{}, err
	}
	l.publish()
	return l.settled(AppLockResult{Result: "ok"}), nil
}

// SetMethod chooses the way in: "system" or "password". With the lock on, the
// switch to the standalone password is proven with it (password), and the switch to System Authentication
// with the system's dialog, which also shows it works before it becomes the
// only way in.
func (l *AppLockService) SetMethod(ctx context.Context, method, password string) (AppLockResult, error) {
	if l.Locked() {
		return AppLockResult{}, errAppLocked
	}
	if method != lockMethodSystem && method != lockMethodPassword {
		return AppLockResult{}, i18n.Errorf("不支持的解锁方式「%s」", method)
	}
	s := l.drive.LocalSettings()
	if s.AppLockMethod == method {
		return l.settled(AppLockResult{Result: "ok"}), nil
	}
	if s.AppLock {
		var r AppLockResult
		if method == lockMethodPassword {
			if !s.AppLockPassword {
				return l.settled(AppLockResult{Result: "invalid", Message: i18n.T("请先设置 PKP 独立密码")}), nil
			}
			r = l.checkPassword(password)
		} else {
			r = l.ask(ctx, toSystemReason())
		}
		if !r.ok() {
			return l.settled(r), nil
		}
	}
	if err := l.drive.updateSettings(func(s *LocalSettings) { s.AppLockMethod = method }); err != nil {
		return AppLockResult{}, err
	}
	l.publish()
	return l.settled(AppLockResult{Result: "ok"}), nil
}

// SetPassword sets the PKP standalone password to next, or changes it once current is
// proven. Adding one to a lock that is on under system verification asks the system
// first: a backup way in is a way in.
func (l *AppLockService) SetPassword(ctx context.Context, current, next string) (AppLockResult, error) {
	if l.Locked() {
		return AppLockResult{}, errAppLocked
	}
	if problem := lockPasswordProblem(next); problem != "" {
		return l.settled(AppLockResult{Result: "invalid", Message: problem}), nil
	}
	s := l.drive.LocalSettings()
	if s.AppLockPassword {
		if r := l.checkPassword(current); !r.ok() {
			return l.settled(r), nil
		}
	} else if s.AppLock {
		if r := l.prove(ctx, s, "", setPasswordReason()); !r.ok() {
			return l.settled(r), nil
		}
	}
	verifier, err := newLockVerifier(next)
	if err != nil {
		l.log.Warn("applock: password verifier not made", "error", err)
		return AppLockResult{}, i18n.New("无法保存独立密码")
	}
	if err := l.secrets.Save(verifier); err != nil {
		l.log.Warn("applock: password not saved", "error", err)
		return AppLockResult{}, i18n.New("无法保存独立密码，系统没有接受这条记录")
	}
	if err := l.drive.updateSettings(func(s *LocalSettings) {
		s.AppLockPassword = true
		s.AppLockThrottle = LockThrottle{}
	}); err != nil {
		return AppLockResult{}, err
	}
	l.publish()
	return l.settled(AppLockResult{Result: "ok"}), nil
}

// RemovePassword removes the separate password, once current is proven. Only a backup
// may go: not while it is the way in, nor while system verification cannot stand in.
func (l *AppLockService) RemovePassword(current string) (AppLockResult, error) {
	if l.Locked() {
		return AppLockResult{}, errAppLocked
	}
	s := l.drive.LocalSettings()
	if !s.AppLockPassword {
		return l.settled(AppLockResult{Result: "ok"}), nil
	}
	if s.AppLockMethod == lockMethodPassword {
		return AppLockResult{}, i18n.New("独立密码是当前的解锁方式，不能移除")
	}
	if ok, _ := lockAvailable(); s.AppLock && !ok {
		return AppLockResult{}, i18n.New("系统验证不可用时，独立密码是唯一的解锁方式，不能移除")
	}
	if r := l.checkPassword(current); !r.ok() {
		return l.settled(r), nil
	}
	if err := l.secrets.Delete(); err != nil {
		l.log.Warn("applock: password not removed", "error", err)
		return AppLockResult{}, i18n.New("无法移除独立密码，系统没有删除这条记录")
	}
	if err := l.drive.updateSettings(func(s *LocalSettings) {
		s.AppLockPassword = false
		s.AppLockThrottle = LockThrottle{}
	}); err != nil {
		return AppLockResult{}, err
	}
	l.publish()
	return l.settled(AppLockResult{Result: "ok"}), nil
}

// SetDelay sets auto-lock after leaving the app in minutes: 0 (immediately) to 1440 (24 hours).
func (l *AppLockService) SetDelay(minutes int) error {
	if l.Locked() {
		return errAppLocked
	}
	if !validAppLockDelay(minutes) {
		return i18n.New("自动锁定时间需在 0 到 1440 分钟（24 小时）之间")
	}
	if err := l.drive.updateSettings(func(s *LocalSettings) { s.AppLockDelay = minutes }); err != nil {
		return err
	}
	l.publish()
	return nil
}

// Lock is lock now in PKP. It needs app lock to be on.
func (l *AppLockService) Lock() error {
	s := l.drive.LocalSettings()
	if !s.AppLock {
		return i18n.New("请先开启应用锁")
	}
	if ok, why := lockable(s); !ok {
		return errors.New(why)
	}
	l.lock()
	return nil
}

// Unlock asks the system to confirm the owner and, once it has, takes the
// lock screen away; the calls held meanwhile go through. A cancelled or
// refused dialog leaves PKP locked. Under separate password the system is not asked.
func (l *AppLockService) Unlock(ctx context.Context) AppLockResult {
	if !l.Locked() {
		return l.settled(AppLockResult{Result: "ok"})
	}
	if l.drive.LocalSettings().AppLockMethod == lockMethodPassword {
		return l.settled(AppLockResult{Result: "unavailable", Message: i18n.T("这台电脑上的 PKP 只能用独立密码解锁")})
	}
	r := l.ask(ctx, unlockReason())
	if r.ok() {
		l.unlock()
	}
	return l.settled(r)
}

// UnlockWithPassword takes the lock screen away once password is the separate password:
// the way in under separate password, the backup under system verification. Wrong guesses count
// towards a cooldown.
func (l *AppLockService) UnlockWithPassword(password string) AppLockResult {
	if !l.Locked() {
		return l.settled(AppLockResult{Result: "ok"})
	}
	if !l.drive.LocalSettings().AppLockPassword {
		return l.settled(AppLockResult{Result: "unavailable", Message: i18n.T("这台电脑上的 PKP 没有设置独立密码")})
	}
	r := l.checkPassword(password)
	if r.ok() {
		l.unlock()
	}
	return l.settled(r)
}

// ForgetAndSignOut is forgot password on the lock screen: the separate password has no way
// back, so every account signs out and this computer's data of them goes
// (accounts' sessions, indexes, records, unfinished local transfers); the
// lock and its password go with them. Files in the cloud, and files already
// downloaded, are untouched. Only while locked, and PKP stays locked when an
// account could not be signed out.
func (l *AppLockService) ForgetAndSignOut() error {
	if !l.Locked() {
		return i18n.New("只有锁定时才能这样做")
	}
	// The video belongs to an account about to go.
	if l.player != nil {
		if l.player.poppedOut() {
			_ = l.player.CloseWindow()
		}
		if err := l.player.StopPlayback(); err != nil {
			l.log.Warn("applock: playback not stopped", "error", err)
		}
	}
	var failed []error
	for _, a := range l.drive.Accounts() {
		if _, err := l.drive.RemoveAccount(a.ID); err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		l.log.Warn("applock: accounts not signed out", "error", errors.Join(failed...))
		return i18n.Errorf("有 %d 个账号没能退出，PKP 保持锁定", len(failed))
	}
	if err := l.secrets.Delete(); err != nil {
		l.log.Warn("applock: password not cleared", "error", err)
		return i18n.New("无法清除独立密码，系统没有删除这条记录")
	}
	if err := l.drive.updateSettings(func(s *LocalSettings) {
		s.AppLock = false
		s.AppLockMethod = lockMethodSystem
		s.AppLockPassword = false
		s.AppLockThrottle = LockThrottle{}
	}); err != nil {
		return err
	}
	l.unlock()
	return nil
}

// ShieldEntry keeps the calling window out of screenshots while a separate password
// field is up in it (on), and lets it back in after (off) — unless the lock
// screen holds, which keeps every window shielded.
func (l *AppLockService) ShieldEntry(ctx context.Context, on bool) {
	if !on && l.Locked() {
		return
	}
	if w, ok := ctx.Value(application.WindowKey).(application.Window); ok && w != nil {
		applock.ShieldWindow(w.NativeWindow(), on)
	}
}

// shieldAll keeps every window out of screenshots, or lets them back in.
func (l *AppLockService) shieldAll(on bool) {
	if l.app == nil {
		return
	}
	for _, w := range l.app.Window.GetAll() {
		applock.ShieldWindow(w.NativeWindow(), on)
	}
}

// prove proves the owner the way a change is proven under s (ProveWith).
func (l *AppLockService) prove(ctx context.Context, s LocalSettings, password, reason string) AppLockResult {
	ok, _ := lockAvailable()
	switch proveWith(s, ok) {
	case lockMethodPassword:
		return l.checkPassword(password)
	case lockMethodSystem:
		return l.ask(ctx, reason)
	}
	return AppLockResult{Result: "ok"}
}

// checkPassword compares password with the separate password, counting a wrong one;
// during a cooldown it does not look.
func (l *AppLockService) checkPassword(password string) AppLockResult {
	l.pwMu.Lock()
	defer l.pwMu.Unlock()
	now := lockClock()
	t := l.drive.LocalSettings().AppLockThrottle
	if left := t.cooling(now); left > 0 {
		return AppLockResult{Result: "cooldown", Message: cooldownMessage(left)}
	}
	verifier, err := l.secrets.Load()
	if errors.Is(err, errNoLockSecret) {
		return AppLockResult{Result: "unavailable", Message: i18n.T("这台电脑上找不到 PKP 独立密码的记录")}
	}
	if err != nil {
		l.log.Warn("applock: password record not read", "error", err)
		return AppLockResult{Result: "unavailable", Message: i18n.T("无法读取独立密码的记录")}
	}
	match, err := lockVerifierMatches(verifier, password)
	if err != nil {
		l.log.Warn("applock: password record unusable", "error", err)
		return AppLockResult{Result: "unavailable", Message: i18n.T("独立密码的记录已损坏，无法验证")}
	}
	if match {
		if t != (LockThrottle{}) {
			l.saveThrottle(LockThrottle{})
		}
		return AppLockResult{Result: "ok"}
	}
	if t.Until != 0 {
		t.Until = 0 // the cooldown is over
	}
	t = t.failed(now)
	l.saveThrottle(t)
	if left := t.cooling(now); left > 0 {
		return AppLockResult{Result: "cooldown", Message: cooldownMessage(left)}
	}
	return AppLockResult{Result: "wrong", Message: i18n.N(t.attemptsLeft(), "密码不正确，还可以再试 %d 次", t.attemptsLeft())}
}

func (l *AppLockService) saveThrottle(t LockThrottle) {
	if err := l.drive.updateSettings(func(s *LocalSettings) { s.AppLockThrottle = t }); err != nil {
		l.log.Warn("applock: wrong guesses not recorded", "error", err)
	}
}

// cooldownMessage says how long to wait, never anything of the password.
func cooldownMessage(left time.Duration) string {
	if left > time.Minute {
		return i18n.F("密码错误 %d 次，请 %d 分钟后再试", lockMaxFails, int((left+time.Minute-1)/time.Minute))
	}
	return i18n.F("密码错误 %d 次，请 %d 秒后再试", lockMaxFails, int((left+time.Second-1)/time.Second))
}

// ask shows the system's dialog, one at a time.
func (l *AppLockService) ask(ctx context.Context, reason string) AppLockResult {
	l.mu.Lock()
	if l.authing {
		l.mu.Unlock()
		return AppLockResult{Result: "busy", Message: i18n.T("系统身份验证已经打开")}
	}
	l.authing = true
	l.mu.Unlock()
	l.publish()

	outcome := lockAuthenticate(l.nativeWindow(ctx), reason)

	l.mu.Lock()
	l.authing = false
	l.mu.Unlock()
	l.publish()

	switch outcome.Result {
	case applock.Verified:
		return AppLockResult{Result: "ok"}
	case applock.Cancelled:
		return AppLockResult{Result: "cancelled", Message: outcome.Message}
	case applock.Failed:
		return AppLockResult{Result: "failed", Message: outcome.Message}
	}
	return AppLockResult{Result: "unavailable", Message: outcome.Message}
}

// nativeWindow is the window the dialog belongs to — the one that asked —
// where the system wants one (Windows Hello is parented to it).
func (l *AppLockService) nativeWindow(ctx context.Context) unsafe.Pointer {
	if runtime.GOOS != "windows" {
		return nil
	}
	if w, ok := ctx.Value(application.WindowKey).(application.Window); ok && w != nil {
		return w.NativeWindow()
	}
	if l.main != nil {
		return l.main.NativeWindow()
	}
	return nil
}

// lockIfEnabled locks when the setting is on and the lock can be opened.
func (l *AppLockService) lockIfEnabled() {
	s := l.drive.LocalSettings()
	if !s.AppLock {
		return
	}
	if ok, why := lockable(s); !ok {
		l.log.Warn("applock: not locking, nothing could open it", "why", why)
		return
	}
	l.lock()
}

func (l *AppLockService) lock() {
	l.mu.Lock()
	if l.locked {
		l.mu.Unlock()
		return
	}
	l.locked = true
	l.playback = ""
	l.open = make(chan struct{})
	l.lockGen++
	gen := l.lockGen
	l.stopLeaveLocked()
	l.mu.Unlock()
	l.shieldAll(true)
	l.publish()
	go l.quiet(gen)
}

func (l *AppLockService) unlock() {
	l.mu.Lock()
	if !l.locked {
		l.mu.Unlock()
		return
	}
	l.locked = false
	l.playback = ""
	close(l.open)
	l.mu.Unlock()
	l.shieldAll(false)
	l.publish()
}

// quiet pauses the video, in either window, and writes where it is. The
// lock screen says progress saved only when the write succeeded.
func (l *AppLockService) quiet(gen int) {
	if l.player == nil {
		return
	}
	s := l.player.State()
	if !s.Running || s.Idle {
		return
	}
	if !s.Paused {
		if err := l.player.SetPaused(true); err != nil {
			l.log.Warn("applock: playback not paused", "error", err)
			return
		}
	}
	report := lockPlaybackPaused
	saved, err := l.drive.SavePlayback()
	if err != nil {
		l.log.Warn("applock: playback position not saved", "error", err)
	} else if saved {
		report = lockPlaybackSaved
	}
	l.mu.Lock()
	current := l.locked && gen == l.lockGen
	if current {
		l.playback = report
	}
	l.mu.Unlock()
	if current {
		l.publish()
	}
}

// left starts the away timer: the app went to the background.
func (l *AppLockService) left() {
	settings := l.drive.LocalSettings()
	if !settings.AppLock {
		return
	}
	delay := time.Duration(settings.AppLockDelay) * time.Minute
	if delay == 0 {
		delay = appLockInstant
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locked || l.authing {
		return
	}
	l.stopLeaveLocked()
	gen := l.leaveGen
	l.leave = time.AfterFunc(delay, func() {
		l.mu.Lock()
		current := gen == l.leaveGen
		l.mu.Unlock()
		if current {
			l.lockIfEnabled()
		}
	})
}

// returned stops the away timer: the app is in front again.
func (l *AppLockService) returned() {
	l.stopLeave()
}

func (l *AppLockService) stopLeave() {
	l.mu.Lock()
	l.stopLeaveLocked()
	l.mu.Unlock()
}

func (l *AppLockService) stopLeaveLocked() {
	if l.leave != nil {
		l.leave.Stop()
		l.leave = nil
	}
	l.leaveGen++
}

// publish tells every window, and the menu bar panel, how the lock stands.
func (l *AppLockService) publish() {
	if l.menu != nil {
		l.menu.mark()
	}
	if l.app != nil {
		l.app.Event.Emit(EventAppLockState, l.State())
	}
}

/* ---------------- the gate ---------------- */

// lockFreeMethods are the binding calls a locked window may make: all of
// AppLockService's — the lock screen's own — and the menu bar panel's, whose
// state MenuBarService redacts while locked. Wails names a method by the
// FNV-1a hash of "<package path>.<type>.<method>", the generated bindings by
// that id.
func lockFreeMethods() (map[uint32]bool, map[string]bool) {
	ids, names := map[uint32]bool{}, map[string]bool{}
	add := func(service any, methods ...string) {
		t := reflect.TypeOf(service)
		if len(methods) == 0 {
			for i := range t.NumMethod() {
				methods = append(methods, t.Method(i).Name)
			}
		}
		prefix := t.Elem().PkgPath() + "." + t.Elem().Name() + "."
		for _, m := range methods {
			if _, ok := t.MethodByName(m); !ok {
				panic("applock: no method " + prefix + m)
			}
			fqn := prefix + m
			h := fnv.New32a()
			h.Write([]byte(fqn))
			ids[h.Sum32()] = true
			names[fqn] = true
		}
	}
	add((*AppLockService)(nil))
	add((*MenuBarService)(nil), "State", "Fit", "Dismiss", "Open", "Quit", "SetUnsaved")
	return ids, names
}

// runtimeCall is the part of a Wails runtime request the gate reads.
type runtimeCall struct {
	Object *int `json:"object"`
	Method *int `json:"method"`
	Args   struct {
		MethodID   uint32 `json:"methodID"`
		MethodName string `json:"methodName"`
	} `json:"args"`
}

// The Wails runtime's names: object 0 is Call, its method 0 CallBinding.
const (
	runtimePath        = "/wails/runtime"
	runtimeCallObject  = 0
	runtimeCallBinding = 0
	runtimeChunkHeader = "x-wails-chunk-id"
	// A call's body is far smaller; one larger is read no further and held.
	runtimeBodyLimit = 1 << 20
)

// LockGate is the asset server middleware that makes the lock hold in Go:
// while PKP is locked, a page's call into a Go service waits until the owner
// unlocks — then it runs as if nothing happened, so no page is left with
// errors — unless it is one of the lock screen's or the menu bar panel's
// own. The rest of the runtime (events, window controls) passes.
func LockGate(l *AppLockService) application.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != runtimePath || !l.Locked() {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get(runtimeChunkHeader) == "" {
				body, err := io.ReadAll(io.LimitReader(r.Body, runtimeBodyLimit))
				// Whatever was read goes back in front of the rest.
				r.Body = struct {
					io.Reader
					io.Closer
				}{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}
				if err == nil && l.free(body) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if !l.wait(r.Context()) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_ = json.NewEncoder(w).Encode(application.CallError{Kind: application.RuntimeError, Message: errAppLocked.Error()})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// free reports whether a runtime request may pass a locked gate: anything
// but a binding call, or a call to a lock-free method.
func (l *AppLockService) free(body []byte) bool {
	var call runtimeCall
	if json.Unmarshal(body, &call) != nil || call.Object == nil || call.Method == nil {
		return false
	}
	if *call.Object != runtimeCallObject || *call.Method != runtimeCallBinding {
		return true
	}
	if call.Args.MethodName != "" {
		return l.freeNames[call.Args.MethodName]
	}
	return l.freeIDs[call.Args.MethodID]
}

// wait holds a call until PKP is unlocked; false when the request went away
// first, or too many are waiting already.
func (l *AppLockService) wait(ctx context.Context) bool {
	for {
		l.mu.Lock()
		if !l.locked {
			l.mu.Unlock()
			return true
		}
		if l.held >= maxHeldCalls {
			l.mu.Unlock()
			return false
		}
		l.held++
		open := l.open
		l.mu.Unlock()

		var gone bool
		select {
		case <-open:
		case <-ctx.Done():
			gone = true
		}
		l.mu.Lock()
		l.held--
		l.mu.Unlock()
		if gone {
			return false
		}
	}
}
