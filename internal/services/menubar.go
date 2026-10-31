package services

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/menubar"
	"github.com/tonyflux/pkp/internal/upload"
)

// Menu Bar Residency: closing the
// main window — its close button, ⌘W — only hides it. The process, the menu
// bar item and every account's uploads and downloads, which run here in Go
// and not in the page, carry on; the item's panel says how they are doing
// and brings the same window back. Only Quit PKP, ⌘Q, the Dock's Quit and the
// system end the process, after asking when local transfers would stop or
// something typed would be lost.

// EventMenuBarState carries MenuBarState to the panel whenever it changes.
const EventMenuBarState = "menubar:state"

// EventMenuBarOpen tells the main window's page it was brought back and,
// from the panel, what to show.
const EventMenuBarOpen = "menubar:open"

const (
	menuBarURL = "/?window=menubar"
	// The panel is this wide (about 340) and as tall as its page
	// says, under the item by menuBarGap, at most the screen's free height
	// less menuBarMargin above and below.
	menuBarWidth  = 340
	menuBarGap    = 6
	menuBarMargin = 12
	// menuBarSettle coalesces bursts of task events (progress comes at up
	// to five a second per file) into one state.
	menuBarSettle = 250 * time.Millisecond
	// The cloud tasks are read when the panel opens and this often while
	// it stays open; never while it is closed.
	menuBarCloudEvery = 10 * time.Second
	// A click on the item within this of the panel hiding itself — the
	// click took its focus away — is the click that closed it.
	menuBarReclick = 400 * time.Millisecond
)

// MenuBarState is what the menu bar panel shows.
type MenuBarState struct {
	// WindowOpen is false while the main window is closed to the menu bar.
	WindowOpen bool `json:"windowOpen"`
	// Account is the account the file page shows (Account.ID), "" when none
	// is signed in.
	Account string `json:"account"`
	// Running counts every account's transfers under way, waiting or paused
	// — in progress, as the transfer list groups them — with the cloud tasks
	// still moving once they were read.
	Running int `json:"running"`
	// Attention counts local uploads and downloads that failed or were
	// interrupted: they wait for the user in needs attention.
	Attention int `json:"attention"`
	// Synchronization is deliberately not counted as ordinary transfer history.
	SyncRunning   int `json:"syncRunning"`
	SyncAttention int `json:"syncAttention"`
	// Inbox counts every account's arrivals still to organize (state "new"). They
	// light the item's dot too, but rank below Attention: a transfer that
	// stopped loses work, an arrival only waits.
	Inbox int `json:"inbox"`
	// InboxItem is the newest of them, the on-screen account's first; nil
	// when there is none.
	InboxItem *Arrival `json:"inboxItem"`
	// Cloud is how the cloud tasks stand: "" not read (the panel is closed
	// or the read is under way), "ok", or "unknown" when an account's could
	// not be read — status pending sync, never 0 or done.
	Cloud string `json:"cloud"`
	// Item is the one transfer the panel shows.
	Item MenuBarItem `json:"item"`
	// Locked: App Lock holds. The panel then shows only counts: no account,
	// no file or task name.
	Locked bool `json:"locked"`
}

// MenuBarItem is the transfer the panel shows: one under way first, else
// one that needs the user, else a cloud task moving, else one waiting.
// Kind says which of the three is set; "" is none.
type MenuBarItem struct {
	Kind     string        `json:"kind"`
	Upload   *UploadTask   `json:"upload"`
	Download *DownloadTask `json:"download"`
	Task     *TaskSummary  `json:"task"`
	// Blocked marks a transfer of an account that needs signing back in.
	Blocked bool `json:"blocked"`
}

// MenuBarOpen is the payload of EventMenuBarOpen. Target is "app" (just the
// window), "files", "transfers", "cloudAdd" or "assistant"; for transfers,
// Source and Key name the list and the task whose details open; for the
// assistant, Key is the arrival to organize.
type MenuBarOpen struct {
	Target string `json:"target"`
	Source string `json:"source"`
	Key    string `json:"key"`
}

// cloudRead is the last read of every signed-in account's moving cloud
// tasks. read is false until one finished; failed when an account's could
// not be read.
type cloudRead struct {
	read   bool
	failed bool
	tasks  []TaskSummary
}

// MenuBarService owns the menu bar item, its panel and what closing and
// quitting mean. Create it before the app (ShouldQuit goes into the app's
// options), then Attach it to the app and its main window.
type MenuBarService struct {
	drive  *DriveService
	player *PlayerService
	log    *slog.Logger
	// lock is App Lock, nil without one: closing the window locks, and the
	// panel's state is redacted while it holds.
	lock *AppLockService

	app   *application.App
	main  *application.WebviewWindow
	panel *application.WebviewWindow
	tray  *application.SystemTray

	dirty chan struct{}

	// hidden: the main window is closed to the menu bar. exitFull: it was
	// closed in full screen and hides once full screen has ended.
	hidden   atomic.Bool
	exitFull atomic.Bool
	// shown: the panel is up.
	shown atomic.Bool
	// reading: a cloud read is under way.
	reading atomic.Bool
	// quitting: the quit is decided; asking: its question is on screen.
	quitting atomic.Bool
	asking   atomic.Bool

	mu sync.Mutex
	// unsaved is what the main window's page has typed and not submitted,
	// in its words; "" for nothing.
	unsaved string
	// Only an explicit update request may attach work to a confirmed quit.
	updateQuit *updateQuit
	cloud      cloudRead
	// inbox is every account's new arrivals as last read; inboxRead
	// serialises the reads so an older one never lands last.
	inbox     []Arrival
	inboxRead sync.Mutex
	// cloudGen voids a read that was under way when the panel closed.
	cloudGen int
	// autoHidden is when the panel last hid itself.
	autoHidden time.Time
	// sent is the last state emitted; badge the item's last dot and name.
	sent      []byte
	badgeOn   bool
	badgeText string
}

// NewMenuBarService prepares the service; Attach wires it once the app and
// its main window exist.
func NewMenuBarService(drive *DriveService, player *PlayerService) *MenuBarService {
	return &MenuBarService{
		drive:  drive,
		player: player,
		log:    slog.Default(),
		dirty:  make(chan struct{}, 1),
	}
}

// Attach creates the item and its panel and takes over main's closing. Call
// it before app.Run and before anything else hooks main's WindowClosing:
// the first hook cancels a close that only hides, so later hooks — the
// player taking its video home, the frame keeper — run only on a real quit.
//
//wails:ignore
func (m *MenuBarService) Attach(app *application.App, main *application.WebviewWindow) {
	m.app = app
	m.main = main

	main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if m.quitting.Load() {
			return
		}
		e.Cancel()
		m.closeMain()
	})
	main.OnWindowEvent(events.Mac.WindowDidExitFullScreen, func(*application.WindowEvent) {
		if m.exitFull.Swap(false) && m.hidden.Load() {
			m.main.Hide()
		}
	})
	// The Dock icon, or opening PKP again (Finder, Launchpad): the window
	// comes back as it was. A hook, cancelling the event: Wails' own
	// listener would otherwise Show() every window that is not visible —
	// the menu bar panel too, unplaced, away from the item (and the player
	// window, were it hidden).
	app.Event.RegisterApplicationEventHook(events.Mac.ApplicationShouldHandleReopen, func(e *application.ApplicationEvent) {
		e.Cancel()
		go m.Open("app", "", "")
	})
	// The separate player window closed while the main one is closed too:
	// the video went home to a window nobody sees.
	app.Event.On(EventPlayerWindowClosed, func(*application.CustomEvent) {
		if m.hidden.Load() {
			go m.quiet()
		}
	})
	for _, name := range []string{EventUploadState, EventDownloadState, EventSyncState} {
		app.Event.On(name, func(*application.CustomEvent) { m.mark() })
	}
	// An account going away takes its arrivals with it.
	for _, name := range []string{EventInboxChanged, EventAccounts} {
		app.Event.On(name, func(*application.CustomEvent) { go m.readInbox() })
	}

	m.panel = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "menubar",
		Title:            "PKP",
		URL:              menuBarURL,
		Width:            menuBarWidth,
		Height:           420,
		Frameless:        true,
		Hidden:           true,
		DisableResize:    true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Mac: application.MacWindow{
			Backdrop:     application.MacBackdropTranslucent,
			CornerRadius: 16,
			WindowLevel:  application.MacWindowLevelPopUpMenu,
			// Over whatever Space or full-screen app is in front, like a menu.
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary,
			// Opening the panel leaves the app in front where it is; only
			// what the panel opens brings PKP forward.
			WindowClass:      application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{NonActivating: true},
		},
	})
	m.panel.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		m.hidePanel(false)
	})
	// A click in another of PKP's windows takes the panel's focus; one in
	// another app is caught by menubar.WatchClicks.
	m.panel.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		m.hidePanel(true)
	})

	m.tray = app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		m.tray.SetTemplateIcon(menubar.Icon)
	} else {
		// No template tinting here: a black glyph on a dark taskbar is all
		// but invisible. Wails swaps the two when the taskbar's theme flips.
		m.tray.SetIcon(menubar.Icon).SetDarkModeIcon(menubar.IconOnDark)
	}
	m.tray.OnClick(m.toggle)
	m.tray.OnRightClick(m.toggle)

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		menubar.WatchClicks(func() {
			if m.shown.Load() {
				m.hidePanel(true)
			}
		})
		// The item exists by now; give it its name even if nothing changes.
		time.AfterFunc(time.Second, func() { m.badge(m.State(), true) })
		go m.readInbox()
	})
	// The item's name was drawn in the language before a switch.
	i18n.OnChange(func() { m.badge(m.State(), false) })
}

// ServiceStartup starts publishing the state.
func (m *MenuBarService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	go m.loop(ctx)
	return nil
}

func (m *MenuBarService) loop(ctx context.Context) {
	tick := time.NewTicker(menuBarCloudEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.dirty:
			m.publish()
			select {
			case <-ctx.Done():
				return
			case <-time.After(menuBarSettle):
			}
		case <-tick.C:
			if m.shown.Load() {
				go m.readCloud()
			}
		}
	}
}

// mark asks for the state to be worked out and sent again.
func (m *MenuBarService) mark() {
	select {
	case m.dirty <- struct{}{}:
	default:
	}
}

func (m *MenuBarService) publish() {
	state := m.State()
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	m.mu.Lock()
	same := bytes.Equal(data, m.sent)
	m.sent = data
	m.mu.Unlock()
	if same {
		return
	}
	m.badge(state, false)
	if m.app != nil {
		m.app.Event.Emit(EventMenuBarState, state)
	}
}

// badge puts the attention dot and the readable name on the item when
// they changed, or always when force is set.
func (m *MenuBarService) badge(state MenuBarState, force bool) {
	on, text := attentionOn(state), badgeLabel(state)
	m.mu.Lock()
	changed := force || on != m.badgeOn || text != m.badgeText
	m.badgeOn, m.badgeText = on, text
	m.mu.Unlock()
	if changed {
		menubar.Badge(on, text)
	}
}

// State is what the panel shows now.
func (m *MenuBarService) State() MenuBarState {
	m.mu.Lock()
	cloud, inbox := m.cloud, m.inbox
	m.mu.Unlock()
	state := summarize(m.drive.Uploads(), m.drive.Downloads(), m.drive.Accounts(), cloud, inbox)
	if pairs, err := m.drive.SyncPairs(); err == nil {
		for _, pair := range pairs {
			if pair.State == "scanning" || pair.State == "syncing" || pair.State == "pausing" {
				state.SyncRunning++
			}
			if len(pair.Issues) > 0 || slices.Contains([]string{"disk", "auth", "permission", "delete-guard", "repair", "failed", "unknown"}, pair.State) {
				state.SyncAttention++
			}
		}
	}
	state.WindowOpen = !m.hidden.Load()
	if m.lock != nil && m.lock.Locked() {
		state = lockedMenuBarState(state)
	}
	return state
}

// lockedMenuBarState is what the panel may show while App Lock holds: whether
// the window is open and how many transfers stand where — never which
// account, file or task.
func lockedMenuBarState(s MenuBarState) MenuBarState {
	return MenuBarState{
		WindowOpen:    s.WindowOpen,
		Running:       s.Running,
		Attention:     s.Attention,
		SyncRunning:   s.SyncRunning,
		SyncAttention: s.SyncAttention,
		Inbox:         s.Inbox,
		Cloud:         s.Cloud,
		Locked:        true,
	}
}

// readCloud reads every signed-in account's moving cloud tasks. An account
// that needs signing back in is left out: its tasks cannot be read, and
// the transfer list already says status pending sync on them.
func (m *MenuBarService) readCloud() {
	if !m.reading.CompareAndSwap(false, true) {
		return
	}
	defer m.reading.Store(false)
	m.mu.Lock()
	gen := m.cloudGen
	m.mu.Unlock()

	read := cloudRead{read: true}
	for _, a := range m.drive.Accounts() {
		if a.NeedsPassword {
			continue
		}
		page, err := m.drive.ActiveTasks(a.ID)
		if err != nil {
			m.log.Debug("menubar: cloud tasks unread", "account", a.ID, "error", err)
			read.failed = true
			continue
		}
		read.tasks = append(read.tasks, page.Tasks...)
	}

	m.mu.Lock()
	if gen == m.cloudGen {
		m.cloud = read
	}
	m.mu.Unlock()
	m.mark()
}

// readInbox reads every account's arrivals waiting to be organized. Arrivals
// are this computer's record, so an account that needs signing back in
// keeps its own.
func (m *MenuBarService) readInbox() {
	m.inboxRead.Lock()
	defer m.inboxRead.Unlock()
	var waiting []Arrival
	for _, a := range m.drive.Accounts() {
		list, err := m.drive.Arrivals(a.ID)
		if err != nil {
			m.log.Debug("menubar: arrivals unread", "account", a.ID, "error", err)
			continue
		}
		for _, x := range list {
			if x.State == ArrivalNew {
				waiting = append(waiting, x)
			}
		}
	}
	m.mu.Lock()
	m.inbox = waiting
	m.mu.Unlock()
	m.mark()
}

/* ---------------- the panel ---------------- */

func (m *MenuBarService) toggle() {
	if m.panel == nil {
		return
	}
	if m.shown.Load() {
		m.hidePanel(false)
		return
	}
	m.mu.Lock()
	reclick := time.Since(m.autoHidden) < menuBarReclick
	m.mu.Unlock()
	if reclick {
		return
	}
	if err := m.tray.PositionWindow(m.panel, menuBarGap); err != nil {
		m.log.Warn("menubar: panel not placed", "error", err)
	}
	m.shown.Store(true)
	m.panel.Show()
	m.panel.Focus()
	go m.readCloud()
	m.mark()
}

// hidePanel puts the panel away; auto when it went by itself (focus left,
// a click elsewhere) rather than by the item or Esc.
func (m *MenuBarService) hidePanel(auto bool) {
	if m.panel == nil || !m.shown.Swap(false) {
		return
	}
	m.panel.Hide()
	m.mu.Lock()
	if auto {
		m.autoHidden = time.Now()
	}
	// The next opening reads the cloud afresh; until then nothing claims
	// what an old read said.
	m.cloudGen++
	m.cloud = cloudRead{}
	m.mu.Unlock()
	m.mark()
}

// Dismiss closes the panel (Esc).
func (m *MenuBarService) Dismiss() {
	m.hidePanel(false)
}

// Fit makes the panel as tall as its page, height in points.
func (m *MenuBarService) Fit(height int) {
	if m.panel == nil {
		return
	}
	if runtime.GOOS == "darwin" {
		menubar.Fit(m.panel.NativeWindow(), height, menuBarMargin)
		return
	}
	// Elsewhere the tray decides where the panel goes (above a bottom
	// taskbar, so its bottom edge is the one that stays): resize, then place
	// it again. Sizes are DIPs, the page's CSS pixels.
	if screen, err := m.panel.GetScreen(); err == nil && screen != nil {
		height = min(height, max(screen.WorkArea.Height-2*menuBarMargin, 120))
	}
	if _, h := m.panel.Size(); h == height {
		return
	}
	m.panel.SetSize(menuBarWidth, height)
	if m.shown.Load() {
		if err := m.tray.PositionWindow(m.panel, menuBarGap); err != nil {
			m.log.Warn("menubar: panel not placed", "error", err)
		}
	}
}

// Open closes the panel, brings the main window back — the same window,
// where it was, on the page it showed — and tells its page what to show:
// target "app" (nothing more), "files", "transfers" (source and key pick the
// list and the task to open), "cloudAdd" or "assistant" (key is the arrival
// to organize).
func (m *MenuBarService) Open(target, source, key string) {
	m.hidePanel(false)
	if m.main == nil {
		return
	}
	m.hidden.Store(false)
	m.exitFull.Store(false)
	if m.main.IsMinimised() {
		m.main.UnMinimise()
	}
	m.main.Show()
	m.main.Focus()
	m.mark()
	if m.app != nil {
		m.app.Event.Emit(EventMenuBarOpen, MenuBarOpen{Target: target, Source: source, Key: key})
	}
}

// Quit is the panel's Quit PKP: the same quit as ⌘Q, question included.
func (m *MenuBarService) Quit() {
	m.hidePanel(false)
	if m.app != nil {
		m.app.Quit()
	}
}

type updateQuit struct {
	start  func() error
	cancel func()
}

func (m *MenuBarService) requestUpdateInstall(start func() error, cancel func()) error {
	m.mu.Lock()
	if m.app == nil || m.quitting.Load() || m.asking.Load() || m.updateQuit != nil {
		m.mu.Unlock()
		return i18n.New("更新操作正在进行")
	}
	m.updateQuit = &updateQuit{start: start, cancel: cancel}
	m.mu.Unlock()
	go m.app.Quit()
	return nil
}

func (m *MenuBarService) cancelUpdateInstall() {
	m.mu.Lock()
	pending := m.updateQuit
	m.updateQuit = nil
	m.mu.Unlock()
	if pending != nil {
		pending.cancel()
	}
}

func (m *MenuBarService) approveQuit() bool {
	m.mu.Lock()
	pending := m.updateQuit
	m.updateQuit = nil
	m.mu.Unlock()
	if pending != nil {
		if err := pending.start(); err != nil {
			return false
		}
	}
	m.quitting.Store(true)
	return true
}

// SetUnsaved is told by the main window's page what it has typed and not
// submitted, in its words ("cloud-added link"), "" for nothing: quitting loses it.
func (m *MenuBarService) SetUnsaved(what string) {
	m.mu.Lock()
	m.unsaved = what
	m.mu.Unlock()
}

/* ---------------- closing and quitting ---------------- */

// closeMain hides the main window. In full screen it first leaves full
// screen (a hidden full-screen window would leave its Space behind) and
// hides once that is done.
func (m *MenuBarService) closeMain() {
	// App Lock: closing the window always locks at once. Off
	// AppKit's thread: locking reads the settings and tells every window.
	if m.lock != nil {
		go m.lock.lockIfEnabled()
	}
	m.hidden.Store(true)
	if m.main.IsFullscreen() {
		m.exitFull.Store(true)
		m.main.UnFullscreen()
	} else {
		m.main.Hide()
	}
	go m.quiet()
	m.mark()
}

// quiet pauses a video playing in the main window, which nobody sees any
// more, and writes where it is. A video in the separate player window plays
// on: that window is still open.
func (m *MenuBarService) quiet() {
	if m.player == nil || m.player.poppedOut() {
		return
	}
	if s := m.player.State(); s.Running && !s.Idle && !s.Paused {
		if err := m.player.SetPaused(true); err != nil {
			m.log.Warn("menubar: playback not paused", "error", err)
		}
	}
	if _, err := m.drive.SavePlayback(); err != nil {
		m.log.Warn("menubar: playback position not saved", "error", err)
	}
}

// ShouldQuit decides a quit: ⌘Q, the Dock's Quit, the panel's Quit PKP or the
// system. It goes ahead at once when nothing would be lost or the system
// asks (logging out, restarting, shutting down: the journals keep what
// can resume); otherwise it asks, defaulting to Keep Running, and quits again
// only once Quit PKP is chosen. AppKit calls it on the main thread.
//
//wails:ignore
func (m *MenuBarService) ShouldQuit() bool {
	if m.quitting.Load() {
		return true
	}
	if menubar.QuitBySystem() {
		m.cancelUpdateInstall()
		m.quitting.Store(true)
		return true
	}
	m.mu.Lock()
	unsaved := m.unsaved
	m.mu.Unlock()
	question := quitQuestion(m.drive.Uploads(), m.drive.Downloads(), unsaved)
	if manager, err := m.drive.syncManager(); err == nil {
		if n := manager.ActiveCount(); n > 0 {
			question = i18n.F("目前有 %d 个同步对正在执行。退出将停止本机同步；下次启动会先重新核对。", n) + question
		}
	}
	if question == "" || m.app == nil {
		return m.approveQuit()
	}
	if m.asking.CompareAndSwap(false, true) {
		m.ask(question)
	}
	return false
}

func (m *MenuBarService) ask(question string) {
	menubar.Activate()
	dialog := m.app.Dialog.Question().SetTitle(i18n.T("退出 PKP？")).SetMessage(question)
	stay := dialog.AddButton(i18n.T("继续运行")).OnClick(func() {
		m.asking.Store(false)
		m.cancelUpdateInstall()
	})
	dialog.AddButton(i18n.T("退出 PKP")).OnClick(func() {
		m.asking.Store(false)
		if m.approveQuit() {
			go m.app.Quit()
		}
	})
	dialog.SetDefaultButton(stay)
	dialog.SetCancelButton(stay)
	dialog.Show()
}

/* ---------------- the rules ---------------- */

// localTask is one upload or download in the order the panel weighs them.
type localTask struct {
	upload   *UploadTask
	download *DownloadTask
	account  string
	// group is where the transfer list puts it: moving, waiting, paused,
	// attention (failed or interrupted), or "" (done, cancelled).
	group   string
	updated string
}

func uploadGroup(state string) string {
	switch state {
	case upload.StateUploading:
		return "moving"
	case upload.StateQueued:
		return "waiting"
	case upload.StateFailed, upload.StateInterrupted:
		return "attention"
	}
	return ""
}

func downloadGroup(state string) string {
	switch state {
	case download.StateDownloading:
		return "moving"
	case download.StateQueued:
		return "waiting"
	case download.StatePaused:
		return "paused"
	case download.StateFailed, download.StateInterrupted:
		return "attention"
	}
	return ""
}

// summarize works out the panel's state from every account's uploads and
// downloads, the accounts, the last cloud read and the new arrivals.
func summarize(uploads []UploadTask, downloads []DownloadTask, accounts []Account, cloud cloudRead, arrivals []Arrival) MenuBarState {
	var state MenuBarState
	blocked := map[string]bool{}
	order := map[string]int{}
	for i, a := range accounts {
		blocked[a.ID] = a.NeedsPassword
		order[a.ID] = i + 1
		if a.Current {
			state.Account = a.ID
			order[a.ID] = 0
		}
	}

	// The account on screen first, then the others in their order; each
	// account's downloads, then its uploads, oldest first as the queues
	// list them.
	locals := make([]localTask, 0, len(uploads)+len(downloads))
	for i := range downloads {
		t := &downloads[i]
		locals = append(locals, localTask{download: t, account: t.Account, group: downloadGroup(t.State), updated: t.UpdatedAt})
	}
	for i := range uploads {
		t := &uploads[i]
		locals = append(locals, localTask{upload: t, account: t.Account, group: uploadGroup(t.State), updated: t.UpdatedAt})
	}
	slices.SortStableFunc(locals, func(a, b localTask) int { return cmp.Compare(order[a.account], order[b.account]) })

	var moving, attention, waiting, paused *localTask
	for i := range locals {
		t := &locals[i]
		switch t.group {
		case "moving":
			state.Running++
			if moving == nil {
				moving = t
			}
		case "waiting":
			state.Running++
			if waiting == nil {
				waiting = t
			}
		case "paused":
			state.Running++
			if paused == nil {
				paused = t
			}
		case "attention":
			state.Attention++
			if attention == nil || newer(t.updated, attention.updated) {
				attention = t
			}
		}
	}

	var cloudMoving, cloudWaiting *TaskSummary
	if cloud.read {
		state.Cloud = "ok"
		if cloud.failed {
			state.Cloud = "unknown"
		}
		state.Running += len(cloud.tasks)
		for i := range cloud.tasks {
			t := &cloud.tasks[i]
			if t.Phase == drive.PhaseRunning && cloudMoving == nil {
				cloudMoving = t
			}
			if t.Phase != drive.PhaseRunning && cloudWaiting == nil {
				cloudWaiting = t
			}
		}
	}

	local := func(t *localTask) MenuBarItem {
		item := MenuBarItem{Blocked: blocked[t.account]}
		if t.upload != nil {
			item.Kind, item.Upload = "upload", t.upload
		} else {
			item.Kind, item.Download = "download", t.download
		}
		return item
	}
	cloudItem := func(t *TaskSummary) MenuBarItem {
		return MenuBarItem{Kind: "cloud", Task: t, Blocked: blocked[t.Account]}
	}
	switch {
	case moving != nil:
		state.Item = local(moving)
	case attention != nil:
		state.Item = local(attention)
	case cloudMoving != nil:
		state.Item = cloudItem(cloudMoving)
	case waiting != nil:
		state.Item = local(waiting)
	case cloudWaiting != nil:
		state.Item = cloudItem(cloudWaiting)
	case paused != nil:
		state.Item = local(paused)
	}

	// Arrivals of accounts still on this computer; the on-screen
	// account's newest first, then the newest of any other.
	for i := range arrivals {
		a := &arrivals[i]
		if _, known := order[a.Account]; !known || a.State != ArrivalNew {
			continue
		}
		state.Inbox++
		best := state.InboxItem
		switch {
		case best == nil:
			state.InboxItem = a
		case (a.Account == state.Account) != (best.Account == state.Account):
			if a.Account == state.Account {
				state.InboxItem = a
			}
		case newer(a.CompletedAt, best.CompletedAt):
			state.InboxItem = a
		}
	}
	return state
}

// attentionOn is whether the item shows its dot: something waits for the
// user, a stopped transfer or an arrival to organize.
func attentionOn(s MenuBarState) bool {
	return s.Attention > 0 || s.Inbox > 0 || s.SyncAttention > 0
}

// newer reports whether RFC 3339 time a is after b; an unreadable time is
// the oldest.
func newer(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	switch {
	case errA != nil:
		return false
	case errB != nil:
		return true
	}
	return ta.After(tb)
}

// badgeLabel is the item's name for VoiceOver and its tooltip: the state in
// words, so the dot is never the only sign.
func badgeLabel(s MenuBarState) string {
	switch {
	case s.SyncAttention > 0:
		return i18n.N(s.SyncAttention, "PKP，%d 个同步对需要处理", s.SyncAttention)
	case s.SyncRunning > 0 && s.Running == 0:
		return i18n.N(s.SyncRunning, "PKP，%d 个同步对正在执行", s.SyncRunning)
	case s.Attention > 0:
		return i18n.N(s.Attention, "PKP，%d 项传输需要处理", s.Attention)
	case s.Inbox > 0:
		return i18n.N(s.Inbox, "PKP，%d 项新入库待整理", s.Inbox)
	case s.Running > 0:
		return i18n.N(s.Running, "PKP，%d 项传输正在进行", s.Running)
	case s.Cloud == "unknown":
		return i18n.T("PKP，任务状态待同步")
	}
	return i18n.T("PKP，暂无活动传输")
}

// quitQuestion is what quitting asks, "" when nothing would be lost: local
// uploads and downloads queued or moving stop (paused downloads stay as
// they are), and what the page has not submitted is gone. Cloud tasks are
// the server's: quitting decides nothing about them.
func quitQuestion(uploads []UploadTask, downloads []DownloadTask, unsaved string) string {
	var up, down int
	for _, t := range uploads {
		if t.Active() {
			up++
		}
	}
	for _, t := range downloads {
		if t.Active() {
			down++
		}
	}
	// Whole sentences per case, so every language orders them its own way;
	// a translation ending a sentence before another carries its own space.
	var b strings.Builder
	switch {
	case down > 0 && up > 0:
		b.WriteString(i18n.F("目前有 %d 项下载到电脑和 %d 项本地上传。退出将停止这些本机传输；云端任务是否继续由服务端决定。", down, up))
	case down == 1:
		b.WriteString(i18n.F("目前有 %d 项下载到电脑。退出将停止这项本机传输；云端任务是否继续由服务端决定。", down))
	case down > 1:
		b.WriteString(i18n.F("目前有 %d 项下载到电脑。退出将停止这些本机传输；云端任务是否继续由服务端决定。", down))
	case up == 1:
		b.WriteString(i18n.F("目前有 %d 项本地上传。退出将停止这项本机传输；云端任务是否继续由服务端决定。", up))
	case up > 1:
		b.WriteString(i18n.F("目前有 %d 项本地上传。退出将停止这些本机传输；云端任务是否继续由服务端决定。", up))
	}
	if unsaved != "" {
		b.WriteString(i18n.F("%s还没有提交，退出后不会保留。", unsaved))
	}
	if b.Len() == 0 {
		return ""
	}
	b.WriteString(i18n.T("只想收起窗口，请选择「继续运行」。"))
	return b.String()
}
