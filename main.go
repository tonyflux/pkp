// Command pkp is a high-performance third-party PikPak desktop client.
//
// The webview hosts the library and controls; playback is delegated to
// libmpv, linked into the process and rendering into a native view below
// the webview. It is fed through a loopback HTTP proxy so that every
// upstream request carries the headers the CDN requires while mpv itself
// stays oblivious to them.
//
// Nothing here depends on a PikPak library: the API client in internal/drive
// is written against the HTTP contract using the standard library only.
package main

import (
	"cmp"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/tonyflux/pkp/internal/foldersync"
	"github.com/tonyflux/pkp/internal/services"
	"github.com/tonyflux/pkp/internal/winframe"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Registering events gives the binding generator typed JS/TS APIs for
	// the state streams instead of opaque payloads.
	application.RegisterEvent[services.PlayerState](services.EventPlayerState)
	application.RegisterEvent[services.IndexProgress](services.EventIndexProgress)
	application.RegisterEvent[services.UploadTask](services.EventUploadState)
	application.RegisterEvent[services.DownloadTask](services.EventDownloadState)
	application.RegisterEvent[[]foldersync.Pair](services.EventSyncState)
	application.RegisterEvent[application.Void](services.EventAccounts)
	application.RegisterEvent[services.OrganizeProgress](services.EventOrganizeProgress)
	application.RegisterEvent[services.RemoveProgress](services.EventRemoveProgress)
	// Organize on Import: an account's arrivals changed.
	application.RegisterEvent[services.InboxChanged](services.EventInboxChanged)
	application.RegisterEvent[services.InboxChanged](services.EventArrivalSuggestionsChanged)
	// AI Assistant: a streamed piece of the model's answer.
	application.RegisterEvent[services.AIDelta](services.EventAIDelta)
	// The separate player window: the main window's session mirrored there,
	// the user's requests sent back, and the window going away.
	application.RegisterEvent[services.PlayerSession](services.EventPlayerSession)
	application.RegisterEvent[services.PlayerCommand](services.EventPlayerCommand)
	application.RegisterEvent[application.Void](services.EventPlayerWindowClosed)
	application.RegisterEvent[services.ViewerReturnRequest](services.EventViewerReturn)
	application.RegisterEvent[uint](services.EventViewerReturnCancelled)
	// The menu bar panel's state, and the main window brought back from it.
	application.RegisterEvent[services.MenuBarState](services.EventMenuBarState)
	application.RegisterEvent[services.MenuBarOpen](services.EventMenuBarOpen)
	// App Lock: locked, unlocked, switched on or off.
	application.RegisterEvent[services.AppLockState](services.EventAppLockState)
	// UI Language: every window switches together.
	application.RegisterEvent[services.LanguageState](services.EventLanguageState)
	// The menu bar's items that act on the page (appmenu.go).
	application.RegisterEvent[string](eventMenuCommand)
	application.RegisterEvent[services.UpdateState](services.EventUpdateState)
}

// defaultTelemetryAPI is where a release build sends Anonymous Usage Statistics: the
// Worker in cloudflare/telemetry, at its own Custom Domain.
// PKP_TELEMETRY_API overrides it in any build, e.g.
// http://127.0.0.1:8788 for one run by `wrangler dev`.
const defaultTelemetryAPI = "https://telemetry.pkpapp.cc"

// version is the app version, set by the Makefile from Info.plist
// (-X main.version=…); a build without it reports "unknown".
var version string

func telemetryAPI() string {
	if api := os.Getenv("PKP_TELEMETRY_API"); api != "" || !releaseBuild {
		return api
	}
	return defaultTelemetryAPI
}

// instanceID names the one PKP process allowed per config dir: the bundle id
// plus a digest of the dir, or the bare bundle id when there is none.
func instanceID(configDir string) string {
	if configDir == "" {
		return "com.pkp.desktop"
	}
	sum := sha256.Sum256([]byte(configDir))
	return "com.pkp.desktop." + hex.EncodeToString(sum[:6])
}

func main() {
	releaseInstallLock, err := holdUpdateInstallLock()
	if err != nil {
		log.Fatal(err)
	}
	defer releaseInstallLock()
	player := services.NewPlayerService(services.PlayerOptions{})

	library := services.NewDriveService(services.DriveOptions{
		// The default profile is drive.AndroidProfile; see THIRD_PARTY.md for
		// public comparison evidence and the maintainer's reuse confirmation.
		ConfigDir:   os.Getenv("PKP_CONFIG_DIR"),
		APIBaseURL:  os.Getenv("PKP_DRIVE_API"),
		AuthBaseURL: os.Getenv("PKP_AUTH_API"),
	}, player)
	// UI Language before anything Go says reaches the user.
	library.ApplyLanguage()
	appearance := services.NewAppearanceService(os.Getenv("PKP_CONFIG_DIR"))
	// Closing the window keeps PKP in the menu bar; quitting asks first when
	// local transfers or unsubmitted input would be lost.
	menu := services.NewMenuBarService(library, player)
	// App Lock: with it on, PKP starts locked; while it holds, the
	// pages' calls into Go wait at the gate below.
	lock := services.NewAppLockService(library, player, menu)
	// System Notifications: transfers, cloud adds and runs that end while the user is
	// elsewhere are told by the system.
	notify := services.NewNotifyService(library, menu, lock)
	// Anonymous Usage Statistics (Settings · Privacy & Security): daily counts of what is used, no content.
	telemetry := services.NewTelemetryService(library, telemetryAPI(), cmp.Or(version, "unknown"))
	updates := services.NewUpdateService(library, menu, lock, cmp.Or(version, "unknown"), releaseBuild)

	// The config dir names this profile's single instance and keeps the last
	// normal frame beside the settings. Without one the window is still
	// placed; nothing is remembered.
	configDir, configErr := services.ResolveConfigDir(os.Getenv("PKP_CONFIG_DIR"))

	app := application.New(application.Options{
		Name:        "pkp",
		Description: "High-performance PikPak desktop client",
		Services: []application.Service{
			application.NewService(player),
			application.NewService(library),
			// Also serves the custom wallpaper to the page, under its route.
			application.NewServiceWithOptions(appearance, application.ServiceOptions{Route: services.AppearanceRoute}),
			application.NewService(menu),
			application.NewService(lock),
			application.NewService(notify),
			application.NewService(telemetry),
			application.NewService(updates),
		},
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: services.LockGate(lock),
		},
		ShouldQuit: menu.ShouldQuit,
		// Opening PKP again while it runs brings the running one's window
		// back instead of a second process over the same journals. One per
		// config dir: PKP_CONFIG_DIR keeps a test profile apart.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: instanceID(configDir),
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				menu.Open("app", "", "")
			},
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "PKP",
		// The frame is decided by winframe once the screens are known; the
		// window stays hidden until then so it never jumps. This size is
		// only what an unplaceable window falls back to.
		Hidden: true,
		Width:  1100,
		Height: 760,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		// Opaque until a page shows the stage: on Windows the webview turns
		// see-through only then (PlayerService.followStage), since a
		// transparent WebView2 draws no backdrop-filter blur.
		BackgroundColour: application.NewRGB(14, 16, 24),
		URL:              "/",
		// Files dragged from Finder onto a [data-file-drop-target] element
		// arrive as WindowFilesDropped below.
		EnableFileDrop: true,
	})

	// First, before the player's and the frame keeper's closing hooks: a
	// close that only hides the window must not reach them.
	menu.Attach(app, window)
	lock.Attach(app, window)
	notify.Attach(app)
	updates.Attach(app)
	// The macOS menu bar, in UI Language; its page commands bring the window
	// back first, as the Dock does.
	attachAppMenu(app, func() { menu.Open("app", "", "") })

	framePath := ""
	if configErr == nil {
		framePath = filepath.Join(configDir, winframe.FileName)
	} else {
		slog.Warn("window: frame will not be remembered", "error", configErr)
	}
	winframe.Attach(app, window, framePath, slog.Default())

	// A drop on the file list uploads into the folder the list shows, of the
	// account it shows. The target element names them in data-upload-parent
	// (empty is the root) and data-upload-account; the queue's own events
	// carry the outcome to the page.
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		target := event.Context().DropTargetDetails()
		if target == nil {
			return
		}
		parent, ok := target.Attributes["data-upload-parent"]
		if !ok {
			return
		}
		account := target.Attributes["data-upload-account"]
		if _, err := library.UploadFiles(account, event.Context().DroppedFiles(), parent); err != nil {
			log.Printf("upload: %v", err)
			return
		}
		telemetry.Count("upload")
	})

	// The video surface lives inside this window's content view, below the
	// webview, unless the separate player window has borrowed it. The service
	// cannot look the window up on its own.
	player.AttachWindow(window)

	// The page's own image loads (thumbnails, avatars) are WebKit's, not Go's, so
	// Network & Proxy has to be told to the WKWebView as well. It exists only
	// once the window's runtime reports in — ApplicationStarted still races
	// the native window's creation — and applying it again is harmless, so
	// a reopened window is covered too.
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		library.AttachWebview(window)
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
