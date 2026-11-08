package services

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/foldersync"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/torrent"
)

// EventIndexProgress reports an account's local index: whether it can serve
// listings yet, and how far a walk has got. Every signed-in account keeps
// its index current, so the payload names the account.
const EventIndexProgress = "drive:index"

// IndexProgress is the payload of EventIndexProgress.
type IndexProgress struct {
	index.Progress
	// Account is the account the index belongs to (Account.ID).
	Account string `json:"account"`
	// Hash is the BitTorrent info hash of a magnet task (40 upper-case hex
	// digits), "" for anything else; the AI assistant's offline card re-adds
	// a failed task by it.
	Hash string `json:"hash"`
}

// driveTimeout bounds one API call made on behalf of the UI, its retries
// included: a read that stalls is dropped after 10 s and sent again, up to
// three times (drive.stallTimeout), and the last try waits for what is
// left. A mutation goes once, within the HTTP client's 30 s. Sign-in gets
// its own budget: it is three round trips (captcha, sign-in, first list)
// and risk control is slower than the data path.
const (
	driveTimeout  = 45 * time.Second
	signInTimeout = 45 * time.Second
)

// indexSyncInterval is how often the background loop pulls changes. One
// incremental pass is a single request, so this is cheap.
const indexSyncInterval = 2 * time.Minute

// listPageSize is one page of an indexed listing.
const listPageSize = 200

// DriveOptions configures the account-facing service.
type DriveOptions struct {
	// ConfigDir holds accounts.json, each account's credentials, index,
	// upload records and play record. Empty resolves to the per-user config
	// directory.
	ConfigDir string
	// Profile overrides the application identity. The zero value uses
	// drive.AndroidProfile; see THIRD_PARTY.md for public comparison evidence
	// and the maintainer's reuse confirmation.
	Profile drive.ClientProfile
	// APIBaseURL and AuthBaseURL override the production hosts.
	APIBaseURL  string
	AuthBaseURL string
	// NoIndexSync opens the index but never walks the server on its own.
	// Tests populate the store directly and must not race a background
	// walk; the index is still consulted when it says it is ready.
	NoIndexSync bool
	// Passwords keeps remembered sign-in passwords; nil means the system's:
	// the keychain on macOS, the Credential Manager on Windows. Without one,
	// Remember account password and Auto login are not offered.
	Passwords PasswordVault
	// TMDBBaseURL overrides TMDB's API (Media Organization's title matching) and
	// TMDBHTTP is the HTTP client its requests go through, nil for its
	// own. Tests point them at stand-ins.
	TMDBBaseURL string
	TMDBHTTP    *http.Client

	Logger *slog.Logger
}

// DriveStatus describes the account on screen: whether the UI can browse it,
// has to ask for its password, or has no account to show at all.
type DriveStatus struct {
	// AccountID is the account on screen (Account.ID), "" when this
	// computer has none: the UI then shows the sign-in page.
	AccountID  string `json:"accountId"`
	Configured bool   `json:"configured"`
	Account    string `json:"account"`
	ClientID   string `json:"clientId"`
	DeviceID   string `json:"deviceId"`
	UserID     string `json:"userId"`
	APIBaseURL string `json:"apiBaseUrl"`
	// TokenExpiry is RFC3339, empty when no session has been minted.
	TokenExpiry string `json:"tokenExpiry"`
	ConfigDir   string `json:"configDir"`
	// NeedsPassword marks an account on screen whose session died. The
	// account is known, the password is not: the UI asks for it in place.
	NeedsPassword bool `json:"needsPassword"`
}

// Entry is one row of the file browser.
type Entry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// ParentID is the containing folder, "" at the root. The move/copy
	// picker uses it to refuse an entry's own folder, which the server does.
	ParentID     string `json:"parentId"`
	Kind         string `json:"kind"`
	MimeType     string `json:"mimeType"`
	Size         int64  `json:"size"`
	ModifiedTime string `json:"modifiedTime"`
	Thumbnail    string `json:"thumbnail"`
	Folder       bool   `json:"folder"`
	Playable     bool   `json:"playable"`
	Starred      bool   `json:"starred"`
	Trashed      bool   `json:"trashed"`
	Phase        string `json:"phase"`
	// Category groups the entry for filtering and for the fallback icon:
	// video, image, audio, document, archive or other.
	Category string `json:"category"`
	// Duration in seconds, present only when the listing carried media
	// variants. The card hides the badge rather than showing a zero.
	Duration float64 `json:"duration"`
	// Preview selects the viewer family: image, pdf, text, document, or empty.
	Preview string `json:"preview"`
	// Path is where the entry lives, "All Files / A / B", set only where the
	// folder is not implied: search results and the whole-drive file view.
	Path string `json:"path"`
	// DeleteTime is when the server will purge a trashed entry (its
	// delete_time); "" for live ones. The recycle bin shows it as is.
	DeleteTime string `json:"deleteTime"`
}

// FileDetail is everything the inspector shows for one entry. Fields the
// server did not provide stay zero; the UI renders those as "Not provided" rather
// than inventing a value.
type FileDetail struct {
	Entry
	CreatedTime string `json:"createdTime"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Hash        string `json:"hash"`
	// Children is the number of live entries directly in a folder, as the
	// local index knows them; -1 for a file, or while the index has not
	// completed a full walk and could only undercount.
	Children int `json:"children"`
}

// ListRequest selects a page of the browser.
type ListRequest struct {
	View     string `json:"view"`     // "all" | "starred" | "trash" | "search" | "files" (every file of the drive)
	ParentID string `json:"parentId"` // View == "all"; "" is the root
	Query    string `json:"query"`    // View == "search" (required) | "files"
	Category string `json:"category"` // "all" | "video" | "image" | "document" | "audio" | "other"
	Sort     string `json:"sort"`     // "modified" | "name" | "size"
	Asc      bool   `json:"asc"`
	// Offset pages an indexed listing; PageToken pages a live one.
	Offset    int    `json:"offset"`
	PageToken string `json:"pageToken"`
}

// ListPage is one page of a listing, from the index or from the server.
type ListPage struct {
	Entries []Entry `json:"entries"`
	// Total is the number of rows matching the request, -1 when served
	// live because the server does not say.
	Total  int `json:"total"`
	Offset int `json:"offset"`
	// Live marks a page answered by the server because the index is not
	// ready. Category filtering and sorting are then the caller's job.
	Live          bool   `json:"live"`
	NextPageToken string `json:"nextPageToken"` // live only
}

// OfflineResult is the outcome of one line of a batch cloud-add. A batch
// reports per-line outcomes rather than failing as a whole, because a single
// malformed link should not discard the rest.
type OfflineResult struct {
	URL    string `json:"url"`
	OK     bool   `json:"ok"`
	TaskID string `json:"taskId"`
	Name   string `json:"name"`
	Error  string `json:"error"`
}

// QuotaInfo is the account's storage picture, flattened for display.
type QuotaInfo struct {
	Limit        int64 `json:"limit"`
	Usage        int64 `json:"usage"`
	UsageInTrash int64 `json:"usageInTrash"`
	IsUnlimited  bool  `json:"isUnlimited"`
	// Breakdown splits Usage by kind of file; nil when the local index
	// cannot vouch for it (see usageBreakdown).
	Breakdown *UsageBreakdown `json:"breakdown"`
}

// TaskSummary is one offline download as the UI shows it.
type TaskSummary struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Phase    string  `json:"phase"`
	Progress float64 `json:"progress"`
	FileID   string  `json:"fileId"`
	FileSize int64   `json:"fileSize"`
	// Message is the server's own words for the task, English as it
	// sends them ("Saved", "File deleted"); ErrorDetail is the code a
	// failed task carries beside it (params.error_detail, e.g.
	// "task_file_deleted"), which says more than the message does.
	Message     string `json:"message"`
	ErrorDetail string `json:"errorDetail"`
	// Category is the bucket the task's file name falls in, so the
	// transfer list can show the same fallback icon as the browser.
	Category string `json:"category"`
	// CreatedTime and UpdatedTime are the server's RFC 3339 stamps; a
	// finished task's UpdatedTime is when it finished.
	CreatedTime string `json:"createdTime"`
	UpdatedTime string `json:"updatedTime"`
	// SavedTo names the destination folder, "All Files / Media"; empty while
	// the local index cannot place it.
	SavedTo string `json:"savedTo"`
	// Account is the account the task belongs to (Account.ID). A task
	// stays its account's, whichever account is on screen.
	Account string `json:"account"`
	// Hash is the BitTorrent info hash of a magnet task (40 upper-case hex
	// digits), "" for anything else; the AI assistant's offline card re-adds
	// a failed task by it.
	Hash string `json:"hash"`
}

// DriveService exposes the accounts to the webview. Each signed-in account
// is a session of its own (accountSession); the one on screen is what the
// file browser, the context column, shares and settings act on, while every
// account's uploads and index keep working. It hands playback links to
// PlayerService.
type DriveService struct {
	opts   DriveOptions
	log    *slog.Logger
	player *PlayerService
	// configDir holds accounts.json, credentials, indexes and settings;
	// configErr is why it could not be resolved, in which case nothing is
	// persisted.
	configDir string
	configErr error
	// vault keeps the Remember account password passwords; nil where there is none.
	vault PasswordVault

	// webview is the main window, once AttachWebview has one: Network & Proxy
	// has to tell WebKit separately, and from whichever goroutine the page
	// called on, so the pointer is guarded.
	webviewMu sync.Mutex
	webview   *application.WebviewWindow

	viewerMu      sync.Mutex
	viewerWindows map[uint]viewerWindow
	previewOwners map[uint]bool
	shareImports  shareImportState

	// mu guards accounts and current. A session's own fields are under its
	// own lock, taken after this one.
	mu       sync.RWMutex
	accounts []*accountSession
	current  *accountSession
	// idxMu serialises index swaps. Closing an index waits for its walk,
	// which may be mid-request, so the swap cannot run under mu.
	idxMu sync.Mutex
	// settingsMu serialises settings.json, accountsMu accounts.json.
	settingsMu sync.Mutex
	accountsMu sync.Mutex
	// signIn is the sign-in in flight, which CancelSignIn can call off.
	signInMu       sync.Mutex
	signIn         *signInAttempt
	signInCommitMu sync.Mutex
	// organizing and removing hold the stop of each account's Cloud Drive Organization
	// and Delete run in flight.
	organizing runs
	removing   runs
	// dups is each account's last Duplicate Files scan, so paging does not
	// regroup the drive; a remove run on the account drops it.
	dupMu sync.Mutex
	dups  map[string]*dupScan
	// aiCalls holds the cancel of each AIComplete in flight, by the
	// caller's id, for AICancel.
	aiCallsMu sync.Mutex
	aiCalls   map[string]context.CancelFunc
	// tmdb is TMDB's client and the credential as read from the keychain.
	tmdb *tmdbState
	// convMu serialises the AI assistant's conversation files.
	convMu sync.Mutex
	// inboxMu serialises the Organize on Import arrival files (inbox.go).
	inboxMu sync.Mutex
	// cloudMoving is each account's cloud-add tasks pending or running at
	// the watcher's last read, id → name, under inboxMu (System Notifications says
	// which of them failed).
	cloudMoving map[string]map[string]string
	// notices is System Notifications' intake (notify.go), nil until it is attached.
	notices atomic.Pointer[func(notice)]
	// Folder synchronization keeps its baselines and operation journal apart
	// from the disposable browsing index and ordinary transfer histories.
	folderSyncOnce     sync.Once
	folderSync         *foldersync.Manager
	folderSyncErr      error
	syncPreviewMu      sync.Mutex
	syncPreviews       map[string]context.CancelFunc
	connectivityMu     sync.Mutex
	connectivityRun    sync.Mutex
	connectivity       ConnectivityResult
	connectivityCancel context.CancelFunc
	connectivityWG     sync.WaitGroup
	connectivityWake   chan struct{}
}

// indexSession is one account's index together with the goroutine that
// keeps it current. It lives while the account's session does.
type indexSession struct {
	store  *index.Store
	syncer *index.Syncer
	// ctx is cancelled by replaceIndex; every walk runs under it.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// syncNow wakes the loop early; capacity 1 so a burst collapses to one.
	syncNow chan struct{}
	// rebuild asks the loop for a full walk from scratch, same shape.
	rebuild chan struct{}
	// inboxNow wakes the arrival watcher (inboxLoop), same shape.
	inboxNow chan struct{}
}

// NewDriveService wires the service. player receives the links it resolves.
func NewDriveService(opts DriveOptions, player *PlayerService) *DriveService {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	d := &DriveService{opts: opts, log: log, player: player, vault: opts.Passwords, tmdb: newTMDBState(opts)}
	if d.vault == nil {
		d.vault = newSystemVault()
	}
	d.configDir, d.configErr = ResolveConfigDir(opts.ConfigDir)
	if d.configErr != nil {
		log.Warn("drive: nothing will be persisted", "error", d.configErr)
		d.configErr = errConfigDir
	}
	// Network & Proxy before anything goes out: the first API call, the token
	// refresh behind it and every later request follow the saved proxy.
	d.installProxy(d.LocalSettings())
	if player != nil {
		// Before ServiceStartup, so before anything plays.
		player.onStall = d.autoStepDown
		player.onPlayback = d.notePlayback
		_ = player.setSubtitleStyle(d.LocalSettings().SubtitleStyle)
	}
	return d
}

// ResolveConfigDir is dir, or the per-user config directory when dir is
// empty: where credentials, the index, settings, the wallpaper and the
// window frame live.
func ResolveConfigDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(base, "pkp"), nil
}

// ServiceName labels the service in Wails logs and bindings metadata.
func (d *DriveService) ServiceName() string { return "DriveService" }

// ServiceStartup restores every stored session.
func (d *DriveService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	// Library users have no application event loop; explicit tests remain available.
	if application.Get() != nil {
		d.watchConnectivity(ctx)
	}
	if err := d.restore(); err != nil && !errors.Is(err, drive.ErrNoCredentials) {
		// A broken credentials file must not stop the app from starting: the
		// user still needs the UI to fix it.
		d.log.Warn("drive: no usable session", "error", err)
	}
	if manager, err := d.syncManager(); err != nil {
		d.log.Error("sync: durable store unavailable", "error", err)
	} else {
		manager.StartLoop(ctx)
	}
	return nil
}

// Status describes the account on screen.
func (d *DriveService) Status() DriveStatus {
	status := DriveStatus{
		ClientID:   d.profile().ClientID,
		APIBaseURL: d.apiBaseURL(),
		ConfigDir:  d.configDir,
	}
	s, err := d.active()
	if err != nil {
		return status
	}
	s.mu.RLock()
	client, tokens, needsPassword, rec := s.client, s.tokens, s.needsPassword, s.rec
	s.mu.RUnlock()

	status.AccountID = s.key
	status.Account = rec.Login
	status.UserID = rec.UserID
	status.NeedsPassword = needsPassword
	status.Configured = client != nil
	if tokens == nil {
		return status
	}
	status.DeviceID = tokens.DeviceID()
	status.ClientID = tokens.Profile().ClientID
	if tok, err := tokens.Current(); err == nil {
		if tok.UserID != "" {
			status.UserID = tok.UserID
		}
		if !tok.ExpiresAt.IsZero() {
			status.TokenExpiry = tok.ExpiresAt.UTC().Format(time.RFC3339)
		}
	}
	return status
}

// ServiceShutdown writes where playback was left and stops every account's
// transfers and index loop. It runs before PlayerService's shutdown, whose
// position it records.
func (d *DriveService) ServiceShutdown() error {
	d.CancelSignIn()
	d.connectivityMu.Lock()
	if d.connectivityCancel != nil {
		d.connectivityCancel()
	}
	d.connectivityMu.Unlock()
	d.connectivityWG.Wait()
	// Stop synchronization first: its direct transfers use the account queues.
	if d.folderSync != nil {
		if err := d.folderSync.Close(); err != nil {
			d.log.Error("sync: shutdown did not finish cleanly", "error", err)
		}
	}
	if _, err := d.savePlaying(); err != nil {
		d.log.Warn("drive: could not save where playback was left", "error", err)
	}
	// Each queue gives its jobs a moment to wind down; the queues wait side
	// by side, not one after another.
	var wg sync.WaitGroup
	for _, s := range d.sessions() {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if q := s.downloadQueue(); q != nil {
				q.Close()
			}
		}()
		go func() {
			defer wg.Done()
			if q := s.queue(); q != nil {
				q.Close()
			}
			d.replaceIndex(s, nil)
		}()
	}
	wg.Wait()
	return nil
}

// IndexProgress reports the index of the account on screen. Without one the
// zero value says "not ready", which is also what makes List go to the
// server.
func (d *DriveService) IndexProgress() IndexProgress {
	s, err := d.active()
	if err != nil {
		return IndexProgress{Progress: index.Progress{Phase: "idle"}}
	}
	if idx := s.index(); idx != nil {
		return IndexProgress{Progress: idx.syncer.Progress(), Account: s.key}
	}
	return IndexProgress{Progress: index.Progress{Phase: "idle"}, Account: s.key}
}

// RequestSync asks an account's background loop ("" is the account on
// screen) for an incremental pass now.
func (d *DriveService) RequestSync(account string) {
	if s, err := d.sessionFor(account); err == nil {
		requestSync(s)
	}
}

// requestSync asks an account's loop for an incremental pass. It never
// blocks; a pass already queued absorbs the request.
func requestSync(s *accountSession) {
	if idx := s.index(); idx != nil {
		select {
		case idx.syncNow <- struct{}{}:
		default:
		}
	}
}

// RebuildIndex empties the index of the account on screen and walks its
// whole drive again. It returns at once; progress arrives on
// EventIndexProgress and listings go live until the walk completes. The
// loop does the walk so that a rebuild and an incremental pass never
// overlap.
func (d *DriveService) RebuildIndex() error {
	s, err := d.active()
	if err != nil {
		return err
	}
	idx := s.index()
	if idx == nil {
		return i18n.New("本地索引暂时不可用")
	}
	select {
	case idx.rebuild <- struct{}{}:
	default:
	}
	return nil
}

// List returns one page of the browser: from the index once a full walk
// has completed, from the server before that.
func (d *DriveService) List(req ListRequest) (ListPage, error) {
	s, err := d.active()
	if err != nil {
		return ListPage{}, err
	}
	if idx := s.index(); idx != nil && idx.syncer.Ready() {
		return listIndexed(idx.store, req)
	}
	return d.listLive(s, req)
}

func listIndexed(store *index.Store, req ListRequest) (ListPage, error) {
	q := index.Query{
		ParentID: req.ParentID,
		Needle:   strings.TrimSpace(req.Query),
		Category: req.Category,
		Sort:     req.Sort,
		Asc:      req.Asc,
		Offset:   req.Offset,
		Limit:    listPageSize,
	}
	if q.Category == "all" {
		q.Category = ""
	}
	switch req.View {
	case "all":
		q.Scope = index.ScopeFolder
	case "starred":
		q.Scope = index.ScopeStarred
	case "trash":
		q.Scope = index.ScopeTrash
	case "search":
		q.Scope = index.ScopeSearch
		if q.Needle == "" {
			return ListPage{Entries: []Entry{}, Offset: req.Offset}, nil
		}
	case "files":
		q.Scope = index.ScopeFiles
	default:
		return ListPage{}, i18n.Errorf("不支持的视图「%s」", req.View)
	}

	rows, total, err := store.Query(q)
	if err != nil {
		return ListPage{}, err
	}
	page := ListPage{Entries: make([]Entry, 0, len(rows)), Total: total, Offset: req.Offset}
	// Rows of one folder share a path: walk each folder's ancestors once.
	paths := map[string]string{}
	for _, row := range rows {
		entry := toEntry(rowToFile(row))
		if req.View == "search" || req.View == "files" {
			path, ok := paths[row.ParentID]
			if !ok {
				crumbs, err := store.Path(row.ParentID)
				if err != nil {
					return ListPage{}, err
				}
				path = folderPath(crumbs)
				paths[row.ParentID] = path
			}
			entry.Path = path
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}

// folderPath renders ancestors as the breadcrumb the search list shows. A
// chain the index cannot follow to the root is rendered as far as it goes.
func folderPath(crumbs []index.Crumb) string {
	var b strings.Builder
	// Data, not display: agent.ts (modelPath) and the assistant read this
	// root by its Simplified Chinese name and translate it where they show it.
	b.WriteString("全部文件") // i18n-ignore
	for _, c := range crumbs {
		b.WriteString(" / ")
		b.WriteString(c.Name)
	}
	return b.String()
}

// listLive answers from the server. Search has no server equivalent, so it
// returns an empty live page and the UI filters the folder it already has.
// The whole-drive file view walks the tree page by page; its rows carry no
// path (the ancestors are not known without the index).
func (d *DriveService) listLive(s *accountSession, req ListRequest) (ListPage, error) {
	params := drive.ListFilesParams{
		PageToken:     req.PageToken,
		Limit:         drive.MaxListLimit,
		ThumbnailSize: drive.ThumbnailMedium,
	}
	switch req.View {
	case "all":
		params.ParentID = req.ParentID
		params.Filters = drive.OnlyLive()
		params.Order = liveOrder(req)
	case "starred":
		params.ParentID = drive.WholeTree
		params.Filters = drive.OnlyLive().Starred()
	case "trash":
		params.ParentID = drive.WholeTree
		params.Filters = drive.NewFilters().Eq("trashed", true)
	case "search":
		return ListPage{Entries: []Entry{}, Total: -1, Live: true}, nil
	case "files":
		params.ParentID = drive.WholeTree
		params.Filters = drive.OnlyLive()
	default:
		return ListPage{}, i18n.Errorf("不支持的视图「%s」", req.View)
	}

	page := ListPage{Entries: []Entry{}, Total: -1, Live: true}
	// A whole-tree page may hold only folders, which the file view drops:
	// read on (a few pages at most) rather than answer with nothing.
	for reads := 0; ; reads++ {
		list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.FileList, error) {
			return client.ListFiles(ctx, params)
		})
		if err != nil {
			return ListPage{}, err
		}
		for _, f := range list.Files {
			entry := toEntry(f)
			if req.View == "files" && entry.Folder {
				continue
			}
			page.Entries = append(page.Entries, entry)
		}
		page.NextPageToken = list.NextPageToken
		if len(page.Entries) > 0 || list.NextPageToken == "" || req.View != "files" || reads >= 4 {
			return page, nil
		}
		params.PageToken = list.NextPageToken
	}
}

// liveOrder maps the UI sort onto the server's order parameter.
func liveOrder(req ListRequest) string {
	switch req.Sort {
	case "name":
		if req.Asc {
			return drive.OrderNameAsc
		}
		return drive.OrderNameDesc
	case "size":
		if req.Asc {
			return drive.OrderSizeAsc
		}
		return drive.OrderSizeDesc
	}
	if req.Asc {
		return drive.OrderModifyTimeAsc
	}
	return drive.OrderModifyTimeDesc
}

// FileDetail reads everything the inspector shows about one entry.
func (d *DriveService) FileDetail(fileID string) (FileDetail, error) {
	s, err := d.active()
	if err != nil {
		return FileDetail{}, err
	}
	file, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
		file, err := client.GetFile(ctx, fileID, drive.GetFileParams{
			Usage:         drive.UsageAll,
			ThumbnailSize: drive.ThumbnailLarge,
		})
		if drive.IsInRecycleBin(err) {
			file, err = recycledFile(ctx, client, fileID)
		}
		return file, err
	})
	if err != nil {
		return FileDetail{}, err
	}

	detail := FileDetail{
		Entry:       toEntry(*file),
		CreatedTime: file.CreatedTime,
		Hash:        file.Hash,
		Children:    -1,
	}
	if file.IsFolder() {
		detail.Children = d.indexedChildren(s, file.ID)
	}
	// Resolution only exists for transcoded media; a document or a
	// still-uploading file legitimately has none.
	for _, media := range file.Medias {
		if media.Video.Width > 0 {
			detail.Width = media.Video.Width
			detail.Height = media.Video.Height
			break
		}
	}
	return detail, nil
}

// indexedChildren counts a folder's live direct children in the index, -1
// when the index cannot vouch for the figure.
func (d *DriveService) indexedChildren(s *accountSession, folderID string) int {
	idx := s.index()
	if idx == nil || !idx.syncer.Ready() {
		return -1
	}
	n, err := idx.store.CountChildren(folderID)
	if err != nil {
		d.log.Warn("drive: child count unavailable", "folder", folderID, "error", err)
		return -1
	}
	return n
}

var (
	// errConfigDir is a computer with no per-user config directory.
	errConfigDir = i18n.New("找不到保存设置的文件夹，改动无法保存")
	// errNoPlayer is a build without the player wired in.
	errNoPlayer = i18n.New("播放器还没有准备好")
	// errNoDownloadLink is a file the server offers no link for yet;
	// frontend/src/viewer.ts matches its words.
	errNoDownloadLink = i18n.New("这个文件暂时没有可用的下载地址，可能还没有处理完")
)

// recycledFile reads one entry of the recycle bin. The server refuses a
// direct read of anything trashed, but the bin listing narrowed to the id
// answers with the full row in one request. Listings carry no media
// variants, so a trashed video has no resolution to report.
func recycledFile(ctx context.Context, client *drive.Client, fileID string) (*drive.File, error) {
	list, err := client.ListFiles(ctx, drive.ListFilesParams{
		ParentID:      drive.WholeTree,
		Filters:       drive.NewFilters().Eq("trashed", true).Eq("id", fileID),
		ThumbnailSize: drive.ThumbnailLarge,
	})
	if err != nil {
		return nil, err
	}
	for i := range list.Files {
		if list.Files[i].ID == fileID {
			return &list.Files[i], nil
		}
	}
	return nil, i18n.New("回收站里已经没有这个文件")
}

// SetStarred sets or clears the favourite mark on several entries at once.
func (d *DriveService) SetStarred(fileIDs []string, starred bool) error {
	s, err := d.active()
	if err != nil {
		return err
	}
	err = d.doIn(s, func(ctx context.Context, client *drive.Client) error {
		if starred {
			return client.Star(ctx, fileIDs)
		}
		return client.Unstar(ctx, fileIDs)
	})
	if err != nil {
		return err
	}
	d.patchIndex(s, fileIDs, index.Patch{Starred: &starred})
	return nil
}

// MoveFiles relocates entries under parentID ("" = root). The server applies
// a move before answering, so the index follows at once. It refuses a move
// into the entries' own folder; one into a moved folder's subtree is
// accepted and then fails in the background, so the UI must not offer it.
func (d *DriveService) MoveFiles(fileIDs []string, parentID string) error {
	s, err := d.active()
	if err != nil {
		return err
	}
	err = d.doIn(s, func(ctx context.Context, client *drive.Client) error {
		return client.Move(ctx, fileIDs, parentID)
	})
	if err != nil {
		return err
	}
	d.patchIndex(s, fileIDs, index.Patch{ParentID: &parentID})
	return nil
}

// CopyFiles duplicates entries under parentID ("" = root). The copy runs
// as a server task that finishes after the call returns and the new ids are
// not reported, so the index learns about them from an incremental pass.
func (d *DriveService) CopyFiles(fileIDs []string, parentID string) error {
	s, err := d.active()
	if err != nil {
		return err
	}
	err = d.doIn(s, func(ctx context.Context, client *drive.Client) error {
		return client.Copy(ctx, fileIDs, parentID)
	})
	if err != nil {
		return err
	}
	requestSync(s)
	return nil
}

// ListFolders returns every live folder directly under parentID ("" = root),
// sorted by name: one level of the move/copy destination picker.
func (d *DriveService) ListFolders(parentID string) ([]Entry, error) {
	s, err := d.active()
	if err != nil {
		return nil, err
	}
	if idx := s.index(); idx != nil && idx.syncer.Ready() {
		rows, err := idx.store.Folders(parentID)
		if err != nil {
			return nil, err
		}
		out := make([]Entry, 0, len(rows))
		for _, row := range rows {
			out = append(out, toEntry(rowToFile(row)))
		}
		return out, nil
	}

	// One timeout per page: a deep folder list must not be cut short
	// because the first pages used up the budget.
	out := []Entry{}
	params := drive.ListFilesParams{
		ParentID: parentID,
		Filters:  drive.OnlyLive().Eq("kind", drive.KindFolder),
		Order:    drive.OrderNameAsc,
	}
	for {
		list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.FileList, error) {
			return client.ListFiles(ctx, params)
		})
		if err != nil {
			return nil, err
		}
		for _, f := range list.Files {
			out = append(out, toEntry(f))
		}
		if list.NextPageToken == "" {
			return out, nil
		}
		params.PageToken = list.NextPageToken
	}
}

// CreateFolder makes a folder in account under parentID ("" = root); an empty
// account means the one on screen. On a name clash the server makes a numbered
// copy (see drive.Client.EnsureFolder), so the entry returned carries the name
// to show. It goes into that account's index at once, without a sync pass.
func (d *DriveService) CreateFolder(account, parentID, name string) (Entry, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return Entry{}, err
	}
	file, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
		return client.CreateFolder(ctx, parentID, name)
	})
	if err != nil {
		return Entry{}, err
	}
	if idx := s.index(); idx != nil {
		d.indexErr(idx.store.Upsert([]index.Row{toRow(*file)}))
	}
	return toEntry(*file), nil
}

// RenameFile changes the display name of one entry.
func (d *DriveService) RenameFile(fileID, name string) (Entry, error) {
	s, err := d.active()
	if err != nil {
		return Entry{}, err
	}
	file, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
		return client.Rename(ctx, fileID, name)
	})
	if err != nil {
		return Entry{}, err
	}
	if idx := s.index(); idx != nil {
		d.indexErr(idx.store.Upsert([]index.Row{toRow(*file)}))
	}
	return toEntry(*file), nil
}

// patchIndex applies the same partial update to every id in an account's
// index, after the server accepted the change. A missing index is not an
// error: the next listing simply goes to the server.
func (d *DriveService) patchIndex(s *accountSession, ids []string, p index.Patch) {
	idx := s.index()
	if idx == nil {
		return
	}
	for _, id := range ids {
		d.indexErr(idx.store.Patch(id, p))
	}
}

// indexErr logs a failed index write. The server already accepted the
// change, so the user is not told; the next walk repairs the row.
func (d *DriveService) indexErr(err error) {
	if err != nil {
		d.log.Warn("drive: index write failed", "error", err)
	}
}

// PlayFile resolves the renditions of an account's file ("" is the account
// on screen), picks the one the saved quality preference asks for and hands
// it to the player. It returns the loopback URL mpv was given. A queue
// carries on in the account it was started in: the next video of a session
// that keeps playing across a switch is still that account's.
func (d *DriveService) PlayFile(account, fileID string) (string, error) {
	return d.play(account, fileID, 0)
}

// ResumeFile is PlayFile starting at position seconds: Continue Watching picks a
// video up where it was left.
func (d *DriveService) ResumeFile(account, fileID string, position float64) (string, error) {
	return d.play(account, fileID, max(position, 0))
}

func (d *DriveService) play(account, fileID string, start float64) (string, error) {
	if d.player == nil {
		return "", errNoPlayer
	}
	s, err := d.sessionFor(account)
	if err != nil {
		return "", err
	}
	// The link lookup is the first of the two waits a click incurs; the UI
	// gets its loading state from the player for both.
	d.player.setLoading(true)
	file, err := d.getFile(s, fileID, drive.GetFileParams{Usage: drive.UsagePlay})
	if err != nil {
		d.player.setLoading(false)
		return "", err
	}
	options := renditionsOf(*file)
	if len(options) == 0 {
		d.player.setLoading(false)
		d.log.Warn("drive: no playable link", "file", fileID, "phase", file.Phase)
		return "", i18n.New("这个视频暂时没有可播放的地址，可能还没有处理完")
	}
	choice := d.LocalSettings().PlaybackQuality
	qualities := qualitiesOf(options)
	chosen, _ := find(options, pick(qualities, choice))
	// The video is the account's that opened it for as long as it plays:
	// its position goes into that account's play record and a quality
	// switch reads links through that account, whatever is on screen then.
	local, err := d.player.openVideo(s.key, fileID, chosen.url, d.mediaHeaders(), QualityState{
		Options: qualities,
		Current: chosen.Key,
		Choice:  choice,
	}, start)
	if err != nil {
		return "", err
	}
	// After openVideo, which has written where the outgoing file was left:
	// replaying the same file must end up with this play's position.
	d.notePlay(s, *file, start)
	// The transcodes lost the file's own subtitles; the drive keeps them.
	// Fetched now, whichever rendition plays first, so a switch to a
	// transcode finds them ready.
	if hasTranscode(options) {
		go d.loadExtracted(s, fileID, *file)
	}
	return local, nil
}

// notePlay puts a file PKP has just started playing at the top of the
// account's local play record. A play link carries no thumbnail; the index
// usually has one.
func (d *DriveService) notePlay(s *accountSession, file drive.File, start float64) {
	l := s.playLog()
	if l == nil {
		return
	}
	thumbnail := file.ThumbnailLink
	if row, found, ok := d.indexedFile(s, file.ID); thumbnail == "" && ok && found {
		thumbnail = row.ThumbnailLink
	}
	duration := mediaDuration(file)
	if duration == 0 {
		duration, _ = strconv.ParseFloat(file.Params["duration"], 64)
	}
	if err := l.opened(playRecord{
		FileID: file.ID, Name: file.Name, Thumbnail: thumbnail,
		Position: start, Duration: duration,
		PlayedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		d.log.Warn("drive: could not save the play record", "error", err)
	}
}

// getFile reads one entry of an account with the given usage, classified.
func (d *DriveService) getFile(s *accountSession, fileID string, params drive.GetFileParams) (*drive.File, error) {
	return callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
		return client.GetFile(ctx, fileID, params)
	})
}

// activeFile is getFile on the account on screen.
func (d *DriveService) activeFile(fileID string, params drive.GetFileParams) (*drive.File, error) {
	s, err := d.active()
	if err != nil {
		return nil, err
	}
	return d.getFile(s, fileID, params)
}

// DownloadLink resolves the plain download URL for a file.
func (d *DriveService) DownloadLink(fileID string) (string, error) {
	file, err := d.activeFile(fileID, drive.GetFileParams{Usage: drive.UsageFetch})
	if err != nil {
		return "", err
	}
	link := file.ContentLink()
	if link == "" {
		d.log.Warn("drive: no download link", "file", fileID, "phase", file.Phase)
		return "", errNoDownloadLink
	}
	return link, nil
}

// PreviewFile publishes a window-owned, range-readable document capability.
// The signed upstream link stays in Go; PDFs retain their explicit MIME type.
func (d *DriveService) PreviewFile(ctx context.Context, fileID string) (string, error) {
	if d.player == nil {
		return "", errNoPlayer
	}
	owner := d.previewOwner(ctx)
	s, err := d.viewerAccount(ctx)
	if err != nil {
		return "", err
	}
	file, err := d.getFile(s, fileID, drive.GetFileParams{Usage: drive.UsageFetch})
	if err != nil {
		return "", err
	}
	link := file.ContentLink()
	if link == "" {
		d.log.Warn("drive: no download link", "file", fileID, "phase", file.Phase)
		return "", errNoDownloadLink
	}
	contentType := ""
	if previewOf(*file) == "pdf" {
		contentType = "application/pdf"
	}
	return d.player.openPreview(owner, link, d.mediaHeaders(), contentType)
}

// ClosePreview cannot revoke resources belonging to another window.
func (d *DriveService) ClosePreview(ctx context.Context, url string) {
	if d.player != nil {
		d.player.closePreview(d.previewOwner(ctx), url)
	}
}

// textPreviewLimit caps how much of a text file is fetched for the panel.
// Logs and subtitle dumps run to many megabytes; the first half megabyte
// says whether this is the file you wanted.
const textPreviewLimit = 512 * 1024

// TextPreview is the decoded head of a text file.
type TextPreview struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	// Encoding is "utf-8", "utf-16le", "utf-16be", or "unknown" when the
	// bytes decode as none of those — the panel says so instead of showing
	// mojibake as if it were the file.
	Encoding string `json:"encoding"`
}

// ReadText fetches the first textPreviewLimit bytes of a file and decodes
// them. UTF-8 and BOM-marked UTF-16 are the encodings the standard library
// can name; anything else is reported, not guessed.
func (d *DriveService) ReadText(ctx context.Context, fileID string) (TextPreview, error) {
	s, err := d.viewerAccount(ctx)
	if err != nil {
		return TextPreview{}, err
	}
	file, err := d.getFile(s, fileID, drive.GetFileParams{Usage: drive.UsageFetch})
	if err != nil {
		return TextPreview{}, err
	}
	link := file.ContentLink()
	if link == "" {
		return TextPreview{}, errNoDownloadLink
	}
	if err != nil {
		return TextPreview{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return TextPreview{}, err
	}
	req.Header.Set("User-Agent", d.profile().UserAgent)
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", textPreviewLimit))
	// netproxy.Client, not http.DefaultClient: the CDN is upstream, so this
	// fetch follows the Proxy setting like every other outbound request.
	resp, err := netproxy.Client().Do(req)
	if err != nil {
		return TextPreview{}, fmt.Errorf("drive: fetch text: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return TextPreview{}, fmt.Errorf("drive: fetch text: upstream status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, textPreviewLimit+1))
	if err != nil {
		return TextPreview{}, fmt.Errorf("drive: fetch text: %w", err)
	}
	truncated := len(raw) > textPreviewLimit
	if truncated {
		raw = raw[:textPreviewLimit]
	}
	text, encoding := decodeText(raw)
	return TextPreview{Text: text, Truncated: truncated, Encoding: encoding}, nil
}

// decodeText turns raw bytes into a string and names the encoding it used.
// A multi-byte sequence cut at the limit is not evidence against UTF-8, so
// up to three trailing bytes are dropped before judging.
func decodeText(raw []byte) (string, string) {
	switch {
	case len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE:
		return decodeUTF16(raw[2:], binary.LittleEndian), "utf-16le"
	case len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF:
		return decodeUTF16(raw[2:], binary.BigEndian), "utf-16be"
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	trimmed := raw
	for i := 0; i < 3 && len(trimmed) > 0 && !utf8.Valid(trimmed); i++ {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if utf8.Valid(trimmed) {
		return string(trimmed), "utf-8"
	}
	return "", "unknown"
}

func decodeUTF16(raw []byte, order binary.ByteOrder) string {
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, order.Uint16(raw[i:]))
	}
	return string(utf16.Decode(units))
}

// Quota reads the account's storage usage, split by kind of file when the
// local index agrees with the server.
func (d *DriveService) Quota() (QuotaInfo, error) {
	s, err := d.active()
	if err != nil {
		return QuotaInfo{}, err
	}
	about, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.About, error) {
		return client.About(ctx)
	})
	if err != nil {
		return QuotaInfo{}, err
	}
	usage := int64(about.Quota.Usage)
	return QuotaInfo{
		Limit:        int64(about.Quota.Limit),
		Usage:        usage,
		UsageInTrash: int64(about.Quota.UsageInTrash),
		IsUnlimited:  about.Quota.IsUnlimited,
		Breakdown:    d.usageBreakdown(s, usage),
	}, nil
}

// TaskPage is one page of the cloud task list.
type TaskPage struct {
	Tasks []TaskSummary `json:"tasks"`
	// NextPageToken continues the list; "" on the last page.
	NextPageToken string `json:"nextPageToken"`
	// ExpiresIn is how long the server holds this reading fresh, in
	// seconds (10 on a real account, 2026-10-15): how soon to read again
	// while a task is moving. 0 when the server did not say.
	ExpiresIn int `json:"expiresIn"`
}

// Tasks lists one page of an account's offline downloads ("" is the account
// on screen), as many as the server serves at once; pageToken "" starts the
// list. An account keeps every task it ever ran, thousands of them, so the
// caller walks the pages.
func (d *DriveService) Tasks(account, pageToken string) (TaskPage, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return TaskPage{}, err
	}
	return d.taskPage(s, drive.ListTasksParams{Type: drive.TaskTypeOffline, Limit: drive.MaxTaskListLimit, PageToken: pageToken})
}

// ActiveTasks lists an account's offline downloads that are still pending
// or running, every page of them: what the transfer list polls while a task
// is moving. The server filters by phase, so this is one short page (65 ms
// against 1.3 s for each 500 of the whole list, measured 2026-10-15)
// however many finished tasks the account keeps.
func (d *DriveService) ActiveTasks(account string) (TaskPage, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return TaskPage{}, err
	}
	params := drive.ListTasksParams{
		Type:    drive.TaskTypeOffline,
		Limit:   drive.MaxTaskListLimit,
		Filters: drive.NewFilters().Set("phase", "in", drive.PhasePending+","+drive.PhaseRunning),
	}
	var out TaskPage
	for {
		page, err := d.taskPage(s, params)
		if err != nil {
			return TaskPage{}, err
		}
		out.Tasks = append(out.Tasks, page.Tasks...)
		out.ExpiresIn = page.ExpiresIn
		// A token that does not move would walk forever.
		if page.NextPageToken == "" || page.NextPageToken == params.PageToken {
			return out, nil
		}
		params.PageToken = page.NextPageToken
	}
}

// taskPage reads one page of tasks and names where each one saves.
func (d *DriveService) taskPage(s *accountSession, params drive.ListTasksParams) (TaskPage, error) {
	list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.TaskList, error) {
		return client.ListTasks(ctx, params)
	})
	if err != nil {
		return TaskPage{}, err
	}
	// Destinations come from the index only: one server walk per task would
	// cost more than the list itself, and an unplaced folder just says less.
	// Tasks mostly land in a few folders, so each is looked up once a page.
	var store *index.Store
	if idx := s.index(); idx != nil && idx.syncer.Ready() {
		store = idx.store
	}
	places := map[string]string{}
	out := make([]TaskSummary, 0, len(list.Tasks))
	for _, task := range list.Tasks {
		summary := toTaskSummary(task)
		summary.Account = s.key
		if store != nil && task.ReferenceResource != nil {
			parent := task.ReferenceResource.ParentID
			place, seen := places[parent]
			if !seen {
				place = savedTo(store, parent)
				places[parent] = place
			}
			summary.SavedTo = place
		}
		out = append(out, summary)
	}
	return TaskPage{Tasks: out, NextPageToken: list.NextPageToken, ExpiresIn: list.ExpiresIn}, nil
}

// savedTo names a destination folder as the transfer list shows it; "" when
// the index does not know the folder, rather than a path that claims root.
func savedTo(store *index.Store, folderID string) string {
	if folderID == "" {
		return folderPath(nil)
	}
	crumbs, err := store.Path(folderID)
	if err != nil || len(crumbs) == 0 {
		return ""
	}
	return folderPath(crumbs)
}

// AddOfflineTasks submits a batch for an account ("" is the account on
// screen), one line at a time, and reports each outcome separately. The
// account is the one the sheet was opened for: a batch never goes to
// another because the screen changed. Lines are sent sequentially rather
// than in parallel: a burst of task creations is exactly the shape risk
// control throttles, and the user is waiting on the slowest line either way.
func (d *DriveService) AddOfflineTasks(account string, urls []string, parentID string) ([]OfflineResult, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	client, err := s.api()
	if err != nil {
		return nil, err
	}

	results := make([]OfflineResult, 0, len(urls))
	for _, raw := range urls {
		link := strings.TrimSpace(raw)
		if link == "" {
			continue
		}
		result := OfflineResult{URL: link}

		ctx, cancel := context.WithTimeout(context.Background(), driveTimeout)
		created, err := client.CreateOfflineTask(ctx, drive.OfflineTaskParams{URL: link, ParentID: parentID})
		cancel()

		if err != nil {
			result.Error = d.classify(s, client, err).Error()
		} else {
			result.OK = true
			result.TaskID = created.Task.ID
			result.Name = created.Task.Name
			if result.Name == "" {
				result.Name = created.File.Name
			}
		}
		results = append(results, result)
	}
	// New files arrive through the next incremental pass; ask for it now
	// rather than waiting on the ticker.
	requestSync(s)
	kickInbox(s)
	return results, nil
}

// taskRemovalChunk bounds the ids one removal request carries. They travel
// as repeated task_ids query parameters, about 30 bytes each, so a few
// hundred would pass the 8 KiB request line many gateways stop at.
const taskRemovalChunk = 100

// RemoveTasks drops an account's tasks from the transfer list ("" is the
// account on screen). deleteFiles also discards whatever they already
// downloaded, which is why it is an explicit argument. A long selection goes
// in chunks; a failing chunk stops the rest, and the error says how many
// went before it.
// The server does not say which files went with the tasks, so the index
// learns about the removals from the next incremental pass, as after a copy.
func (d *DriveService) RemoveTasks(account string, taskIDs []string, deleteFiles bool) error {
	s, err := d.sessionFor(account)
	if err != nil {
		return err
	}
	for start := 0; start < len(taskIDs); start += taskRemovalChunk {
		chunk := taskIDs[start:min(start+taskRemovalChunk, len(taskIDs))]
		err := d.doIn(s, func(ctx context.Context, client *drive.Client) error {
			return client.DeleteTasks(ctx, chunk, deleteFiles)
		})
		if err != nil {
			if start > 0 {
				err = i18n.Errorf("已移除 %d 项（共 %d 项），其余没有移除：%w", start, len(taskIDs), err)
			}
			return err
		}
	}
	requestSync(s)
	return nil
}

// do runs one API call on the UI's behalf on the account on screen.
func (d *DriveService) do(fn func(context.Context, *drive.Client) error) error {
	s, err := d.active()
	if err != nil {
		return err
	}
	return d.doIn(s, fn)
}

// doIn runs one API call for an account: its client, one driveTimeout, and
// the error classified for its session.
func (d *DriveService) doIn(s *accountSession, fn func(context.Context, *drive.Client) error) error {
	client, err := s.api()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), driveTimeout)
	defer cancel()
	return d.classify(s, client, fn(ctx, client))
}

// call is do for calls that return a value.
func call[T any](d *DriveService, fn func(context.Context, *drive.Client) (T, error)) (T, error) {
	s, err := d.active()
	if err != nil {
		var zero T
		return zero, err
	}
	return callIn(d, s, fn)
}

// callIn is doIn for calls that return a value.
func callIn[T any](d *DriveService, s *accountSession, fn func(context.Context, *drive.Client) (T, error)) (T, error) {
	var out T
	err := d.doIn(s, func(ctx context.Context, client *drive.Client) error {
		var err error
		out, err = fn(ctx, client)
		return err
	})
	return out, err
}

// classify ends an account's session once its token manager reports that
// it cannot be renewed: the refresh token is dead and there is no password,
// or not even an account, to sign in with. Retrying would fail the same
// way, so that account needs Log in again.
func (d *DriveService) classify(s *accountSession, client *drive.Client, err error) error {
	if sessionEnded(err) {
		d.expire(s, client)
	}
	return err
}

func sessionEnded(err error) bool {
	return errors.Is(err, drive.ErrPasswordRequired) || errors.Is(err, drive.ErrNoCredentials)
}

// expire tears down an account's session that the server will no longer
// renew: its client and index go, its transfers stop and stay listed, and the
// token manager stays so the account can be signed back in. Other accounts
// carry on. Only the client that failed is dropped; a sign-in that replaced
// it in the meantime stands. idxMu is held throughout so such a sign-in
// cannot open its index before this one is closed.
func (d *DriveService) expire(s *accountSession, client *drive.Client) {
	d.idxMu.Lock()
	defer d.idxMu.Unlock()

	s.mu.Lock()
	if client == nil || s.client != client {
		s.mu.Unlock()
		return
	}
	uploads, downloads := s.uploads, s.downloads
	s.client = nil
	// It was turned down, or there was none: the next sign-in brings one.
	s.password = ""
	s.needsPassword = true
	s.mu.Unlock()

	d.log.Warn("drive: session expired, sign-in required", "account", s.key)
	if uploads != nil {
		// Closed, the queues still list what was waiting.
		uploads.Close()
	}
	if downloads != nil {
		downloads.Close()
	}
	d.closeIndexLocked(s)
	d.emitAccounts()
	name := ""
	for _, a := range d.Accounts() {
		if a.ID == s.key {
			name = accountName(a)
		}
	}
	d.announce(notice{Kind: noticeSession, Account: s.key, Name: name, Failed: true})
}

// mediaHeaders are replayed by the loopback proxy on every upstream range
// request. Direct links are pre-signed, so only the identity matters.
func (d *DriveService) mediaHeaders() map[string]string {
	return map[string]string{"User-Agent": d.profile().UserAgent}
}

func (d *DriveService) profile() drive.ClientProfile {
	if d.opts.Profile.Valid() {
		return d.opts.Profile
	}
	return drive.AndroidProfile()
}

func (d *DriveService) apiBaseURL() string {
	if d.opts.APIBaseURL != "" {
		return d.opts.APIBaseURL
	}
	return drive.DefaultAPIBaseURL
}

// replaceIndex closes an account's index, if open, and opens it again for
// client; a nil client leaves the account without one. The whole swap runs
// under idxMu so that two sign-ins cannot interleave their close and open
// and leave a loop writing into a closed store.
func (d *DriveService) replaceIndex(s *accountSession, client *drive.Client) {
	d.idxMu.Lock()
	defer d.idxMu.Unlock()

	d.closeIndexLocked(s)
	if client != nil {
		d.openIndex(s, client)
	}
}

// closeIndexLocked stops an account's index loop and closes its store.
// Caller holds idxMu.
func (d *DriveService) closeIndexLocked(s *accountSession) {
	s.mu.Lock()
	old := s.idx
	s.idx = nil
	s.mu.Unlock()
	if old != nil {
		old.cancel()
		old.wg.Wait()
		if err := old.store.Close(); err != nil {
			d.log.Warn("drive: close index", "error", err)
		}
	}
}

// openIndex opens an account's index and starts the loop that keeps it in
// step. The file is named by the account's key, so it is per account, and
// the stored account mark only guards against a copied file. Failure to
// open is logged and leaves the account's listings on the server, as before
// the index. Caller holds idxMu.
func (d *DriveService) openIndex(s *accountSession, client *drive.Client) {
	if d.configErr != nil {
		d.log.Warn("drive: index unavailable", "error", d.configErr)
		return
	}
	if err := os.MkdirAll(d.configDir, 0o700); err != nil {
		d.log.Warn("drive: index unavailable", "error", err)
		return
	}
	store, err := index.Open(d.accountFile(s.key, "index-", ".sqlite"))
	if err != nil {
		d.log.Warn("drive: index unavailable", "error", err)
		return
	}
	if got, err := store.Meta(index.MetaAccount); err != nil || got != s.key {
		if err == nil {
			err = store.Clear()
		}
		if err == nil {
			err = store.SetMeta(index.MetaAccount, s.key)
		}
		if err != nil {
			d.log.Warn("drive: index unavailable", "error", err)
			store.Close()
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	idx := &indexSession{
		store: store, ctx: ctx, cancel: cancel,
		syncNow:  make(chan struct{}, 1),
		rebuild:  make(chan struct{}, 1),
		inboxNow: make(chan struct{}, 1),
	}
	key := s.key
	idx.syncer = index.NewSyncer(store, client, toRow, func(p index.Progress) { d.publishIndex(key, p) }, d.log)
	s.mu.Lock()
	s.idx = idx
	s.mu.Unlock()

	if !d.opts.NoIndexSync {
		idx.wg.Add(2)
		go d.syncLoop(s, idx, client)
		go d.inboxLoop(s, idx, client)
	}
}

// syncLoop runs an incremental pass at start, then every
// indexSyncInterval or whenever RequestSync asks, and a full rebuild when
// RebuildIndex asks. Errors are already logged by the syncer and reach the
// UI through the progress event; a session that can no longer be renewed
// ends the loop and the session with it.
func (d *DriveService) syncLoop(s *accountSession, idx *indexSession, client *drive.Client) {
	defer idx.wg.Done()
	ticker := time.NewTicker(indexSyncInterval)
	defer ticker.Stop()
	ended := func(err error) bool {
		if !sessionEnded(err) {
			return false
		}
		// expire closes this index session, which waits for this loop.
		go d.expire(s, client)
		return true
	}
	if ended(idx.syncer.Sync(idx.ctx)) {
		return
	}
	for {
		var err error
		select {
		case <-idx.ctx.Done():
			return
		case <-ticker.C:
			err = idx.syncer.Sync(idx.ctx)
		case <-idx.syncNow:
			err = idx.syncer.Sync(idx.ctx)
		case <-idx.rebuild:
			err = idx.syncer.Rebuild(idx.ctx)
		}
		if ended(err) {
			return
		}
	}
}

func (d *DriveService) publishIndex(account string, progress index.Progress) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventIndexProgress, IndexProgress{Progress: progress, Account: account})
	}
}

// toRow is what the index stores for one listing entry. Category is
// decided here so the chips can filter in SQL.
func toRow(f drive.File) index.Row {
	return index.Row{
		ID:            f.ID,
		ParentID:      f.ParentID,
		Kind:          f.Kind,
		Name:          f.Name,
		MimeType:      f.MimeType,
		FileExtension: f.FileExtension,
		Size:          int64(f.Size),
		Starred:       f.Starred,
		Trashed:       f.Trashed,
		DeleteTime:    f.DeleteTime,
		Phase:         f.Phase,
		CreatedTime:   f.CreatedTime,
		ModifiedTime:  f.ModifiedTime,
		ThumbnailLink: f.ThumbnailLink,
		Hash:          f.Hash,
		FolderType:    f.FolderType,
		Category:      categoryOf(f),
	}
}

// rowToFile is the inverse of toRow, so an indexed row goes through the
// same toEntry as a server one and playability, preview and category are
// decided in one place.
func rowToFile(r index.Row) drive.File {
	return drive.File{
		ID:            r.ID,
		ParentID:      r.ParentID,
		Kind:          r.Kind,
		Name:          r.Name,
		MimeType:      r.MimeType,
		FileExtension: r.FileExtension,
		Size:          drive.Int64(r.Size),
		Starred:       r.Starred,
		Trashed:       r.Trashed,
		DeleteTime:    r.DeleteTime,
		Phase:         r.Phase,
		CreatedTime:   r.CreatedTime,
		ModifiedTime:  r.ModifiedTime,
		ThumbnailLink: r.ThumbnailLink,
		Hash:          r.Hash,
		FolderType:    r.FolderType,
	}
}

func toEntry(f drive.File) Entry {
	return Entry{
		ID:           f.ID,
		Name:         f.Name,
		ParentID:     f.ParentID,
		Kind:         f.Kind,
		MimeType:     f.MimeType,
		Size:         int64(f.Size),
		ModifiedTime: f.ModifiedTime,
		Thumbnail:    f.ThumbnailLink,
		Folder:       f.IsFolder(),
		Playable:     isPlayable(f),
		Starred:      f.Starred,
		Trashed:      f.Trashed,
		Phase:        f.Phase,
		Category:     categoryOf(f),
		Duration:     mediaDuration(f),
		Preview:      previewOf(f),
		DeleteTime:   f.DeleteTime,
	}
}

// mediaDuration returns the runtime the server reported, or zero when the
// response carried no media variants — a listing without usage=PLAY does not.
func mediaDuration(f drive.File) float64 {
	for _, media := range f.Medias {
		if media.Video.Duration > 0 {
			return float64(media.Video.Duration)
		}
	}
	return 0
}

// isPlayable decides whether the row gets a play button. Phase matters as
// much as the type: an offline download still in flight has no bytes yet.
// The type comes from categoryOf so a file with no MIME (common for offline
// tasks) is still playable by extension, exactly as it is filed under Videos.
func isPlayable(f drive.File) bool {
	if f.IsFolder() || (f.Phase != "" && f.Phase != drive.PhaseComplete) {
		return false
	}
	switch categoryOf(f) {
	case "video", "audio":
		return true
	}
	return false
}

// categoryOf buckets an entry for the filter chips and the fallback icon.
// The MIME type is authoritative when the server sent one; the extension is
// the fallback, because offline tasks often land with an empty MIME type.
func categoryOf(f drive.File) string {
	if f.IsFolder() {
		return "folder"
	}
	switch {
	case strings.HasPrefix(f.MimeType, "video/"):
		return "video"
	case strings.HasPrefix(f.MimeType, "image/"):
		return "image"
	case strings.HasPrefix(f.MimeType, "audio/"):
		return "audio"
	}

	ext := extensionOf(f)
	switch ext {
	case "mp4", "mkv", "mov", "avi", "ts", "flv", "webm", "m4v", "wmv", "rmvb":
		return "video"
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "heic", "avif":
		return "image"
	case "mp3", "flac", "aac", "wav", "m4a", "ogg", "ape":
		return "audio"
	case "pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt", "md", "markdown", "mdown", "mkd", "mkdn", "epub", "csv", "tsv", "srt", "ass", "ssa", "vtt", "lrc":
		return "document"
	case "zip", "rar", "7z", "tar", "gz", "iso", "cbz", "cbr":
		return "archive"
	}
	return "other"
}

// previewOf exposes a reader only after the file's bytes are complete.
func previewOf(f drive.File) string {
	if f.IsFolder() || (f.Phase != "" && f.Phase != drive.PhaseComplete) {
		return ""
	}
	switch {
	case strings.HasPrefix(f.MimeType, "image/"):
		return "image"
	case f.MimeType == "application/pdf":
		return "pdf"
	case strings.HasPrefix(f.MimeType, "text/"):
		return "text"
	}
	switch extensionOf(f) {
	case "epub", "cbz", "docx", "xlsx":
		return "document"
	case "pdf":
		return "pdf"
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "heic", "avif":
		return "image"
	case "txt", "md", "markdown", "mdown", "mkd", "mkdn", "log", "json", "yaml", "yml", "xml", "csv", "tsv",
		"ini", "cfg", "conf", "toml", "env", "nfo", "srt", "ass", "ssa", "vtt", "lrc",
		"sh", "bash", "zsh", "bat", "ps1", "py", "js", "ts", "go", "rs", "c", "h", "cpp",
		"hpp", "java", "kt", "swift", "rb", "php", "html", "htm", "css", "sql", "diff", "patch":
		return "text"
	}
	return ""
}

func extensionOf(f drive.File) string {
	ext := strings.ToLower(strings.TrimPrefix(f.FileExtension, "."))
	if ext == "" {
		if dot := strings.LastIndex(f.Name, "."); dot >= 0 {
			ext = strings.ToLower(f.Name[dot+1:])
		}
	}
	return ext
}

func toTaskSummary(task drive.Task) TaskSummary {
	return TaskSummary{
		ID:          task.ID,
		Name:        task.Name,
		Phase:       task.Phase,
		Progress:    task.Progress,
		FileID:      task.FileID,
		FileSize:    int64(task.FileSize),
		Message:     task.Message,
		ErrorDetail: task.Params["error_detail"],
		Category:    categoryOf(drive.File{Name: task.Name}),
		CreatedTime: task.CreatedTime,
		UpdatedTime: task.UpdatedTime,
		Hash:        torrent.BTIH(task.Params["url"]),
	}
}
