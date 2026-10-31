package services

// System Notifications: what finished while the user was elsewhere is said by the
// system — a banner and Notification Center on macOS, a toast on Windows —
// not only by the page, which nobody sees while PKP is behind another app
// or closed to the menu bar. Everything is decided here in Go, where the
// transfers, the cloud-add watcher and the runs live: the window may be
// closed, its page not running.
//
// What is said:
//   - Download to Computer and Local Upload: a file that failed, at once (a burst is one
//     notice); files that finished, once the kind's queue has nothing left
//     queued or moving, as one notice for the lot.
//   - Cloud Add: tasks that finished (the inbox watcher's arrivals) or failed.
//   - Organize / Undo and Delete / Restore runs, when they end.
//   - An account whose session ended: it has to be signed back in.
//
// Nothing is said while PKP is the app in front with its window open,
// unless 「Notify Even When PKP Is in Foreground」 is on: the page shows it already. While App Lock
// holds, notices name no file and no account. Clicking a notice brings the
// window back on what it is about.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/upload"
)

// What a notice is about.
const (
	noticeDownload = "download"
	noticeUpload   = "upload"
	noticeCloud    = "cloud"
	noticeOrganize = "organize"
	noticeUndo     = "undo"
	noticeRemove   = "remove"
	noticeSession  = "session"
)

var (
	// noticeSettle is how long a burst of notices of one kind is gathered
	// before it is said as one.
	noticeSettle = 1500 * time.Millisecond
	// newNotifyCenter makes the system's notification center; tests swap it.
	newNotifyCenter = func() notifyCenter { return &systemCenter{} }
)

// notice is one thing that may be worth telling the user. Transfers post
// every change of their state; the service works out which are endings.
type notice struct {
	Kind    string
	Account string
	ID      string
	Name    string
	// State is a transfer's state now; Failed marks any other notice about
	// something that did not happen.
	State  string
	Failed bool
	Error  string
	// Where is the folder it landed in, as the transfer list says it.
	Where string
	// A run's outcome: Verb is the Delete run's (Move to Trash, Permanently Delete, Restore),
	// Done the items it did, Faults those it did not, Stopped that the user
	// stopped it.
	Verb    string
	Done    int
	Faults  int
	Stopped bool
}

// announce hands a notice to System Notifications, if it is attached.
func (d *DriveService) announce(n notice) {
	if sink := d.notices.Load(); sink != nil {
		(*sink)(n)
	}
}

// NotifyState is Settings · Notifications as it stands.
type NotifyState struct {
	// Enabled is System Notifications; Foreground is Notify Even When PKP Is in Foreground.
	Enabled    bool `json:"enabled"`
	Foreground bool `json:"foreground"`
	// Available says PKP can post notices at all here; Unavailable says
	// why not, in words.
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable"`
	// Allowed says the system lets PKP show them (macOS asks the user once;
	// the answer is changed in System Settings · Notifications).
	Allowed bool `json:"allowed"`
}

// notifyCenter is the system's notification center.
type notifyCenter interface {
	// start readies it; an error means notices cannot be posted here.
	start(onClick func(data map[string]any)) error
	allowed() (bool, error)
	// ask asks the user once; after that it reports the answer given.
	ask() (bool, error)
	send(notifications.NotificationOptions) error
}

// NotifyService posts System Notifications.
type NotifyService struct {
	drive *DriveService
	menu  *MenuBarService
	lock  *AppLockService
	log   *slog.Logger
	app   *application.App

	center notifyCenter
	// ready: the center started. why is what stopped it, worded in the
	// language in force when read (unavailableWhy).
	ready atomic.Bool
	why   atomic.Pointer[error]
	// away: PKP is not the app in front.
	away atomic.Bool

	mu sync.Mutex
	// last is each transfer's state as last posted, while it is under way:
	// only a transfer seen moving can be said to have finished.
	last map[string]string
	// held are the notices gathered per kind, waiting to be said.
	held   map[string]*noticeBatch
	timers map[string]*time.Timer
	// busy says whether a kind's transfers are still queued or moving;
	// tests swap it.
	busy func(kind string) bool
}

type noticeBatch struct {
	done   []notice
	failed []notice
}

// NewNotifyService prepares System Notifications and hooks it into the drive. Attach
// it to the app before Run.
func NewNotifyService(drive *DriveService, menu *MenuBarService, lock *AppLockService) *NotifyService {
	n := &NotifyService{
		drive:  drive,
		menu:   menu,
		lock:   lock,
		log:    slog.Default(),
		center: newNotifyCenter(),
		last:   map[string]string{},
		held:   map[string]*noticeBatch{},
		timers: map[string]*time.Timer{},
	}
	n.busy = n.queueBusy
	sink := n.post
	drive.notices.Store(&sink)
	return n
}

// Attach watches whether PKP is the app in front. Call it before app.Run.
//
//wails:ignore
func (n *NotifyService) Attach(app *application.App) {
	n.app = app
	if runtime.GOOS == "darwin" {
		app.Event.OnApplicationEvent(events.Mac.ApplicationDidResignActive, func(*application.ApplicationEvent) { n.away.Store(true) })
		app.Event.OnApplicationEvent(events.Mac.ApplicationDidBecomeActive, func(*application.ApplicationEvent) { n.away.Store(false) })
		return
	}
	// Elsewhere PKP is away once none of its windows has focus.
	watch := func(w application.Window) {
		w.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { n.away.Store(false) })
		w.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
			// Focus moving to another of PKP's windows arrives as a loss
			// here before the gain there.
			time.AfterFunc(200*time.Millisecond, func() {
				for _, other := range app.Window.GetAll() {
					if other.IsFocused() {
						return
					}
				}
				n.away.Store(true)
			})
		})
	}
	for _, w := range app.Window.GetAll() {
		watch(w)
	}
	app.Window.OnCreate(watch)
}

// ServiceStartup readies the system's center and, with System Notifications on, asks
// for permission the first time (macOS shows its own non-modal prompt).
func (n *NotifyService) ServiceStartup(context.Context, application.ServiceOptions) error {
	if err := n.center.start(n.clicked); err != nil {
		n.why.Store(&err)
		n.log.Info("notify: system notifications unavailable", "error", err)
		return nil
	}
	n.ready.Store(true)
	if !n.drive.LocalSettings().NotificationsOff {
		go func() {
			if _, err := n.center.ask(); err != nil {
				n.log.Debug("notify: permission not asked", "error", err)
			}
		}()
	}
	return nil
}

func unavailableWhy(err error) string {
	if strings.Contains(err.Error(), "bundle identifier") {
		return i18n.T("PKP 不是从应用程序包打开的，系统不让它发通知：请打开 PKP.app")
	}
	return i18n.T("这台电脑不支持系统通知")
}

// State is Settings · Notifications as it stands.
func (n *NotifyService) State() NotifyState {
	s := n.drive.LocalSettings()
	state := NotifyState{Enabled: !s.NotificationsOff, Foreground: s.NotifyInForeground}
	if !n.ready.Load() {
		if err := n.why.Load(); err != nil {
			state.Unavailable = unavailableWhy(*err)
		} else {
			state.Unavailable = i18n.T("系统通知还没准备好")
		}
		return state
	}
	state.Available = true
	ok, err := n.center.allowed()
	if err != nil {
		n.log.Debug("notify: permission unread", "error", err)
	}
	state.Allowed = ok
	return state
}

// SetEnabled switches System Notifications and saves it. Switching on asks the system
// for permission when it was never asked.
func (n *NotifyService) SetEnabled(on bool) (NotifyState, error) {
	if err := n.drive.updateSettings(func(s *LocalSettings) { s.NotificationsOff = !on }); err != nil {
		return n.State(), err
	}
	if on && n.ready.Load() {
		if _, err := n.center.ask(); err != nil {
			n.log.Debug("notify: permission not asked", "error", err)
		}
	}
	if !on {
		n.mu.Lock()
		n.dropHeldLocked()
		n.mu.Unlock()
	}
	return n.State(), nil
}

// SetForeground saves 「Notify Even When PKP Is in Foreground」.
func (n *NotifyService) SetForeground(on bool) (NotifyState, error) {
	err := n.drive.updateSettings(func(s *LocalSettings) { s.NotifyInForeground = on })
	return n.State(), err
}

// Allow asks the system for permission again. macOS asks the user only
// once; after a refusal only System Settings changes it (OpenSystemSettings).
func (n *NotifyService) Allow() (NotifyState, error) {
	if !n.ready.Load() {
		return n.State(), errors.New(n.State().Unavailable)
	}
	if _, err := n.center.ask(); err != nil {
		return n.State(), err
	}
	return n.State(), nil
}

// SendTest posts a sample notice now, whatever is in front.
func (n *NotifyService) SendTest() error {
	if !n.ready.Load() {
		return errors.New(n.State().Unavailable)
	}
	if ok, _ := n.center.allowed(); !ok {
		if runtime.GOOS == "windows" {
			return i18n.New("Windows 没有允许 PKP 发送通知，请在 Windows 设置的「系统 › 通知」里打开")
		}
		return i18n.New("系统没有允许 PKP 发送通知，请在系统设置的「通知」里打开")
	}
	return n.center.send(notifications.NotificationOptions{
		ID:       noticeID("test"),
		Title:    i18n.T("PKP 通知已开启"),
		Body:     i18n.T("下载、上传、云添加完成或失败时，会像这样告诉你。"),
		ThreadID: "test",
		Data:     map[string]any{"target": "app"},
	})
}

// OpenSystemSettings opens the system's notification settings.
func (n *NotifyService) OpenSystemSettings() error {
	if n.app == nil {
		return i18n.New("无法打开系统设置")
	}
	url := "x-apple.systempreferences:com.apple.preference.notifications"
	if runtime.GOOS == "windows" {
		url = "ms-settings:notifications"
	}
	return n.app.Browser.OpenURL(url)
}

/* ---------------- taking notices in ---------------- */

// post takes a notice from the drive. It is called on every transfer
// change, progress included, so it does as little as it can until one ends.
func (n *NotifyService) post(x notice) {
	switch x.Kind {
	case noticeDownload, noticeUpload:
		key := x.Kind + ":" + x.Account + ":" + x.ID
		n.mu.Lock()
		prev, seen := n.last[key]
		if transferMoving(x.Kind, x.State) {
			n.last[key] = x.State
			n.mu.Unlock()
			return
		}
		delete(n.last, key)
		n.mu.Unlock()
		if !seen || !transferMoving(x.Kind, prev) {
			return
		}
		switch x.State {
		case download.StateDone: // == upload.StateDone
		case download.StateFailed: // == upload.StateFailed
			x.Failed = true
		default:
			// Cancelled, paused, interrupted: the user's doing, or a quit.
			// It may have been the last one moving.
			n.mu.Lock()
			if n.held[x.Kind] != nil {
				n.settleLocked(x.Kind)
			}
			n.mu.Unlock()
			return
		}
		n.hold(x)
	case noticeCloud:
		n.hold(x)
	default:
		if x.Kind != noticeSession && x.Done+x.Faults == 0 {
			return // a run that touched nothing
		}
		go n.say(x.Kind, []notice{x}, x.Failed)
	}
}

func transferMoving(kind, state string) bool {
	if kind == noticeUpload {
		return upload.Task{State: state}.Active()
	}
	return download.Task{State: state}.Active()
}

// queueBusy reports whether any account still has transfers of kind queued
// or moving.
func (n *NotifyService) queueBusy(kind string) bool {
	switch kind {
	case noticeDownload:
		for _, t := range n.drive.Downloads() {
			if t.Active() {
				return true
			}
		}
	case noticeUpload:
		for _, t := range n.drive.Uploads() {
			if t.Active() {
				return true
			}
		}
	}
	return false
}

// hold gathers x with the other notices of its kind until the burst settles.
func (n *NotifyService) hold(x notice) {
	if n.drive.LocalSettings().NotificationsOff {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.held[x.Kind]
	if b == nil {
		b = &noticeBatch{}
		n.held[x.Kind] = b
	}
	if x.Failed {
		b.failed = append(b.failed, x)
	} else {
		b.done = append(b.done, x)
	}
	n.settleLocked(x.Kind)
}

// settleLocked (re)starts kind's wait before its flush; mu must be held.
// A flush already under way finds only what is still held.
func (n *NotifyService) settleLocked(kind string) {
	if t := n.timers[kind]; t != nil {
		t.Stop()
	}
	n.timers[kind] = time.AfterFunc(noticeSettle, func() { n.flush(kind) })
}

// flush says what a kind gathered: failures at once; finished transfers
// only once nothing of that kind is queued or moving any more, so a folder
// of a hundred files is one notice.
func (n *NotifyService) flush(kind string) {
	busy := n.busy(kind)
	n.mu.Lock()
	b := n.held[kind]
	if b == nil {
		n.mu.Unlock()
		return
	}
	failed := b.failed
	b.failed = nil
	var done []notice
	if !busy {
		done = b.done
		delete(n.held, kind)
	}
	n.mu.Unlock()
	if len(failed) > 0 {
		n.say(kind, failed, true)
	}
	if len(done) > 0 {
		n.say(kind, done, false)
	}
}

// dropHeldLocked forgets what was gathered; mu must be held.
func (n *NotifyService) dropHeldLocked() {
	for kind, t := range n.timers {
		t.Stop()
		delete(n.timers, kind)
	}
	clear(n.held)
}

/* ---------------- saying it ---------------- */

// say posts one notice about items, unless the user would not want it now.
func (n *NotifyService) say(kind string, items []notice, failed bool) {
	if !n.ready.Load() || len(items) == 0 {
		return
	}
	s := n.drive.LocalSettings()
	if s.NotificationsOff || (!n.away.Load() && !n.windowHidden() && !s.NotifyInForeground) {
		return
	}
	locked := n.lock != nil && n.lock.Locked()
	c := noticeText(kind, items, failed, locked)
	if !locked {
		c.Subtitle = n.whose(items)
	}
	target, source, key := "app", "", ""
	switch kind {
	case noticeDownload, noticeUpload, noticeCloud:
		target, source = "transfers", kind
		if len(items) == 1 && !locked {
			key = kind + ":" + items[0].Account + ":" + items[0].ID
		}
	}
	err := n.center.send(notifications.NotificationOptions{
		ID:       noticeID(kind),
		Title:    c.Title,
		Subtitle: c.Subtitle,
		Body:     c.Body,
		ThreadID: kind,
		Data:     map[string]any{"target": target, "source": source, "key": key},
	})
	if err != nil {
		n.log.Debug("notify: not posted", "kind", kind, "error", err)
	}
}

func (n *NotifyService) windowHidden() bool {
	return n.menu != nil && n.menu.hidden.Load()
}

// whose is the account the items belong to, named only when this computer
// has more than one and every item is that one's.
func (n *NotifyService) whose(items []notice) string {
	accounts := n.drive.Accounts()
	if len(accounts) < 2 {
		return ""
	}
	id := items[0].Account
	for _, x := range items[1:] {
		if x.Account != id {
			return ""
		}
	}
	for _, a := range accounts {
		if a.ID == id {
			return accountName(a)
		}
	}
	return ""
}

// clicked brings the window back on what the clicked notice was about.
func (n *NotifyService) clicked(data map[string]any) {
	if n.menu == nil {
		return
	}
	str := func(k string) string { s, _ := data[k].(string); return s }
	target := str("target")
	if target == "" {
		target = "app"
	}
	go n.menu.Open(target, str("source"), str("key"))
}

var noticeSerial atomic.Uint64

func noticeID(kind string) string {
	return fmt.Sprintf("pkp-%s-%d-%d", kind, time.Now().UnixNano(), noticeSerial.Add(1))
}

// noticeCopy is what a notice says.
type noticeCopy struct {
	Title    string
	Subtitle string
	Body     string
}

// noticeText words a notice about items of one kind; locked leaves out
// every file name (App Lock holds: the screen may be someone else's).
func noticeText(kind string, items []notice, failed, locked bool) noticeCopy {
	x := items[0]
	count := len(items)
	switch kind {
	case noticeDownload, noticeUpload, noticeCloud:
		c := noticeCopy{Title: map[string]string{
			noticeDownload: i18n.T("下载完成"),
			noticeUpload:   i18n.T("上传完成"),
			noticeCloud:    i18n.T("云添加完成"),
		}[kind]}
		if failed {
			c.Title = map[string]string{
				noticeDownload: i18n.T("下载失败"),
				noticeUpload:   i18n.T("上传失败"),
				noticeCloud:    i18n.T("云添加失败"),
			}[kind]
		}
		if locked {
			switch {
			case kind == noticeCloud && failed:
				c.Body = i18n.N(count, "%d 项云添加没有完成", count)
			case kind == noticeCloud:
				c.Body = i18n.N(count, "%d 项云添加已完成", count)
			case kind == noticeDownload && failed:
				c.Body = i18n.N(count, "%d 个文件没有下载成功", count)
			case kind == noticeDownload:
				c.Body = i18n.N(count, "%d 个文件已下载完成", count)
			case failed:
				c.Body = i18n.N(count, "%d 个文件没有上传成功", count)
			default:
				c.Body = i18n.N(count, "%d 个文件已上传完成", count)
			}
			return c
		}
		lead := quote(x.Name)
		switch {
		case count > 1 && kind == noticeCloud:
			lead = i18n.F("%s等 %d 项", lead, count)
		case count > 1:
			lead = i18n.F("%s等 %d 个文件", lead, count)
		}
		switch {
		case failed && kind == noticeCloud:
			c.Body = i18n.F("%s没有完成", lead)
		case failed && kind == noticeDownload:
			c.Body = i18n.F("%s没有下载成功", lead)
		case failed:
			c.Body = i18n.F("%s没有上传成功", lead)
		case kind == noticeDownload:
			c.Body = i18n.F("%s已下载", lead)
		case kind == noticeUpload:
			c.Body = i18n.F("%s已上传", lead)
		default:
			c.Body = i18n.F("%s已存入云盘", lead)
		}
		if failed && count == 1 {
			if why := reasonText(x.Error); why != "" {
				c.Body = i18n.F("%s：%s", c.Body, why)
			}
		} else if where := sameWhere(items); where != "" {
			switch kind {
			case noticeDownload:
				c.Body = i18n.F("%s，存在 %s", c.Body, where)
			default:
				c.Body = i18n.F("%s，在「%s」", c.Body, clip(where, 40))
			}
		}
		return c
	case noticeOrganize:
		c := noticeCopy{Title: i18n.T("整理完成"), Body: i18n.N(x.Done, "已整理 %d 项", x.Done)}
		if x.Stopped {
			c.Title = i18n.T("整理已停止")
		}
		if x.Faults > 0 {
			c.Body += i18n.F("，%d 项未处理", x.Faults)
		}
		return c
	case noticeUndo:
		c := noticeCopy{Title: i18n.T("撤销完成"), Body: i18n.N(x.Done, "已撤销 %d 项", x.Done)}
		if x.Stopped {
			c.Title = i18n.T("撤销已停止")
		}
		if x.Faults > 0 {
			c.Body += i18n.F("，%d 项未处理", x.Faults)
		}
		return c
	case noticeRemove:
		// Verb is remove.go's word for the run, data rather than text.
		restore := x.Verb == "还原" // i18n-ignore
		c := noticeCopy{Title: i18n.T("删除完成")}
		switch {
		case restore && x.Stopped:
			c.Title = i18n.T("还原已停止")
		case restore:
			c.Title = i18n.T("还原完成")
		case x.Stopped:
			c.Title = i18n.T("删除已停止")
		}
		switch {
		case restore:
			c.Body = i18n.N(x.Done, "已还原 %d 项", x.Done)
		case x.Verb == "彻底删除": // i18n-ignore
			c.Body = i18n.N(x.Done, "已彻底删除 %d 项", x.Done)
		default:
			c.Body = i18n.N(x.Done, "已移到回收站 %d 项", x.Done)
		}
		if x.Faults > 0 {
			c.Body += i18n.F("，%d 项没有成功", x.Faults)
		}
		return c
	case noticeSession:
		c := noticeCopy{Title: i18n.T("需要重新登录")}
		if locked {
			c.Body = i18n.T("有一个账号的登录已失效，它的上传和下载已停止")
		} else {
			c.Body = i18n.F("%s的登录已失效，它的上传和下载已停止，重新登录后可以继续", quote(x.Name))
		}
		return c
	}
	return noticeCopy{Title: "PKP"}
}

// sameWhere is the folder every item landed in, "" when they differ or
// none is known.
func sameWhere(items []notice) string {
	where := items[0].Where
	for _, x := range items[1:] {
		if x.Where != where {
			return ""
		}
	}
	return where
}

// reasonText is the part of an error worth showing: what follows the last
// "：" or ": ", kept short.
func reasonText(err string) string {
	err = strings.TrimSpace(err)
	if i := strings.LastIndex(err, ": "); i >= 0 {
		err = err[i+2:]
	}
	if i := strings.LastIndex(err, "："); i >= 0 && i+len("：") < len(err) {
		err = err[i+len("："):]
	}
	return clip(err, 60)
}

func quote(name string) string {
	if name == "" {
		name = i18n.T("未命名")
	}
	return i18n.F("「%s」", clip(name, 40))
}

// clip keeps the first n characters of s, marking a cut with "…".
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// accountName is how the page names an account: its local note, else its
// nickname, else its login masked (format.ts maskLogin).
func accountName(a Account) string {
	if a.Label != "" {
		return a.Label
	}
	if a.Nickname != "" {
		return a.Nickname
	}
	return maskLogin(a.Login)
}

func maskLogin(login string) string {
	s := strings.TrimSpace(login)
	if s == "" {
		return ""
	}
	if at := strings.LastIndex(s, "@"); at > 0 {
		name := []rune(s[:at])
		keep := 1
		if len(name) > 2 {
			keep = 2
		}
		return string(name[:keep]) + "••" + s[at:]
	}
	phone := strings.NewReplacer(" ", "", "-", "").Replace(s)
	digits := strings.TrimPrefix(phone, "+")
	if len(digits) >= 7 && strings.Trim(digits, "0123456789") == "" {
		return phone[:3] + "••••" + phone[len(phone)-4:]
	}
	r := []rune(s)
	if len(r) <= 4 {
		return string(r[0]) + "••"
	}
	return string(r[:2]) + "••" + string(r[len(r)-2:])
}

/* ---------------- the system's center ---------------- */

// systemCenter is Wails' notifications package, started by hand rather
// than as a service: its start fails where notices cannot be posted (an
// executable run outside its app bundle on macOS), and a failing service
// would stop PKP from opening at all.
type systemCenter struct {
	ns *notifications.NotificationService
}

func (c *systemCenter) start(onClick func(map[string]any)) error {
	ns := notifications.New()
	if err := ns.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		return err
	}
	ns.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil || r.Response.ActionIdentifier != notifications.DefaultActionIdentifier {
			return
		}
		onClick(r.Response.UserInfo)
	})
	c.ns = ns
	return nil
}

func (c *systemCenter) allowed() (bool, error) { return c.ns.CheckNotificationAuthorization() }

func (c *systemCenter) ask() (bool, error) { return c.ns.RequestNotificationAuthorization() }

func (c *systemCenter) send(o notifications.NotificationOptions) error {
	return c.ns.SendNotification(o)
}
