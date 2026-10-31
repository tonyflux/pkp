package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// EventDownloadState carries one DownloadTask every time it changes: state
// transitions at once, progress at most five times a second per file.
const EventDownloadState = "download:state"

// DownloadTask is the payload of EventDownloadState and the row of
// Downloads(): the queue's view of one file plus the icon bucket its name
// falls in, the account it comes from, and the local folder it is saved in.
// Task ids are per account: the pair of Account and ID names one download.
type DownloadTask struct {
	download.Task
	Category string `json:"category"`
	Account  string `json:"account"`
	// SavedTo is the folder the file is saved in, the home folder written "~".
	SavedTo string `json:"savedTo"`
}

// DownloadItem is one entry the file page asks to download: a file, or a
// folder to download whole.
type DownloadItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Folder   bool   `json:"folder"`
	ParentID string `json:"parentId"`
}

func toDownloadTask(account string, t download.Task) DownloadTask {
	saved := filepath.Dir(t.Path)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if saved == home {
			saved = "~"
		} else if rest, ok := strings.CutPrefix(saved, home+string(filepath.Separator)); ok {
			saved = filepath.Join("~", rest)
		}
	}
	return DownloadTask{Task: t, Category: categoryOf(drive.File{Name: t.Name}), Account: account, SavedTo: saved}
}

// Downloads lists every account's download records, each account's oldest
// first, including the ones an earlier session left in the journal. An
// account whose session ended still lists them.
func (d *DriveService) Downloads() []DownloadTask {
	out := []DownloadTask{}
	for _, s := range d.sessions() {
		q := s.downloadQueue()
		if q == nil {
			continue
		}
		for _, t := range q.List() {
			out = append(out, toDownloadTask(s.key, t))
		}
	}
	return out
}

// DownloadFiles downloads files and folders of an account ("" is the account
// on screen) into the download folder. A folder is walked here and queued
// file by file into a new local folder of its name ("Movies (1)" when Movies is
// taken), its tree recreated beneath; empty folders are not made. Files the
// drive is still saving are skipped. The rest go in when one is refused, and
// the error names every one that was.
func (d *DriveService) DownloadFiles(account string, items []DownloadItem) ([]DownloadTask, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	q, err := s.liveDownloads()
	if err != nil {
		return nil, err
	}
	// Not made when missing: an unplugged disk would get a stand-in folder
	// on the system disk.
	dir := d.LocalSettings().DownloadDir
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, i18n.Errorf("下载文件夹「%s」不可用，请在「设置 · 传输」里重新选择", dir)
	}

	// Where the items come from is named once per drive folder.
	labels := map[string]string{}
	label := func(parentID string) string {
		if l, ok := labels[parentID]; ok {
			return l
		}
		l := ""
		if trail, err := d.folderTrail(s, parentID); err == nil {
			crumbs := make([]index.Crumb, len(trail))
			for i, c := range trail {
				crumbs[i] = index.Crumb{ID: c.ID, Name: c.Name}
			}
			l = folderPath(crumbs)
		}
		labels[parentID] = l
		return l
	}
	var (
		added []DownloadTask
		errs  []error
	)
	add := func(src download.Source) bool {
		task, err := q.Add(src)
		switch {
		case errors.Is(err, download.ErrQueued):
			errs = append(errs, i18n.Errorf("「%s」已在下载列表里", src.Name))
		case err != nil:
			errs = append(errs, err)
		default:
			added = append(added, toDownloadTask(s.key, task))
			return true
		}
		return false
	}
	// walk queues the files under folder id into local, and says how many.
	var walk func(id, local, origin string, depth int) int
	walk = func(id, local, origin string, depth int) int {
		if depth > 64 {
			errs = append(errs, i18n.Errorf("「%s」下的文件夹太深，更深的部分已跳过", origin))
			return 0
		}
		n := 0
		// One timeout per page, as for ListFolders.
		params := drive.ListFilesParams{ParentID: id, Filters: drive.OnlyLive(), Limit: drive.MaxListLimit}
		for {
			list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.FileList, error) {
				return client.ListFiles(ctx, params)
			})
			if err != nil {
				errs = append(errs, err)
				return n
			}
			for _, f := range list.Files {
				switch {
				case f.IsFolder():
					n += walk(f.ID, filepath.Join(local, download.SafeName(f.Name, runtime.GOOS)), origin+" / "+f.Name, depth+1)
				case f.Phase != "" && f.Phase != drive.PhaseComplete:
					errs = append(errs, i18n.Errorf("「%s」还没有保存完成，已跳过", f.Name))
				default:
					if add(download.Source{FileID: f.ID, Name: f.Name, Size: int64(f.Size), Dir: local, Origin: origin}) {
						n++
					}
				}
			}
			if list.NextPageToken == "" {
				return n
			}
			params.PageToken = list.NextPageToken
		}
	}
	for _, it := range items {
		origin := label(it.ParentID)
		if !it.Folder {
			add(download.Source{FileID: it.ID, Name: it.Name, Size: it.Size, Dir: dir, Origin: origin})
			continue
		}
		root, err := download.FreeDir(dir, download.SafeName(it.Name, runtime.GOOS))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if origin != "" {
			origin += " / "
		}
		if walk(it.ID, root, origin+it.Name, 0) == 0 {
			// Nothing to download: no empty folder is left behind.
			_ = os.Remove(root)
		}
	}
	return added, errors.Join(errs...)
}

// PauseDownload stops one of an account's downloads and keeps what it has on
// disk.
func (d *DriveService) PauseDownload(account, id string) error {
	q, err := d.downloadsOf(account)
	if err != nil {
		return err
	}
	return q.Pause(id)
}

// ResumeDownload puts a paused, failed, cancelled or interrupted download back
// in line; it continues from what is on disk.
func (d *DriveService) ResumeDownload(account, id string) error {
	q, err := d.downloadsOf(account)
	if err != nil {
		return err
	}
	return q.Resume(id)
}

// RestartDownload throws away what a stopped download has on disk and starts
// it over.
func (d *DriveService) RestartDownload(account, id string) error {
	q, err := d.downloadsOf(account)
	if err != nil {
		return err
	}
	return q.Restart(id)
}

// CancelDownload stops a download for good and deletes its partial file.
func (d *DriveService) CancelDownload(account, id string) error {
	q, err := d.downloadsOf(account)
	if err != nil {
		return err
	}
	return q.Cancel(id)
}

// RemoveDownload drops one of an account's downloads from the list, stopping
// it first if needed. An unfinished one's partial file goes with it; a
// finished file stays.
func (d *DriveService) RemoveDownload(account, id string) error {
	q, err := d.downloadsOf(account)
	if err != nil {
		return err
	}
	return q.Remove(id)
}

// OpenDownload opens a finished download in the app the system picks for it.
// It only touches this computer, so it works while the account needs its
// password.
func (d *DriveService) OpenDownload(account, id string) error {
	q, err := d.localDownloads(account)
	if err != nil {
		return err
	}
	path, done, err := q.Locate(id)
	if err != nil {
		return err
	}
	if !done {
		return i18n.New("下载还没有完成")
	}
	if _, err := os.Stat(path); err != nil {
		return i18n.New("文件已被移动或删除")
	}
	return openPath(path)
}

// RevealDownload shows a download in the file manager: the file once it is
// there, the partial file while it is not, or else the nearest folder of its
// path that exists.
func (d *DriveService) RevealDownload(account, id string) error {
	q, err := d.localDownloads(account)
	if err != nil {
		return err
	}
	path, _, err := q.Locate(id)
	if err != nil {
		return err
	}
	for {
		if info, err := os.Stat(path); err == nil {
			return revealPath(path, !info.IsDir())
		}
		parent := filepath.Dir(path)
		if parent == path {
			return i18n.New("文件已被移动或删除")
		}
		path = parent
	}
}

// RevealDownloadDir opens the download folder in the file manager.
func (d *DriveService) RevealDownloadDir() error {
	return revealPath(d.LocalSettings().DownloadDir, false)
}

// openPath and revealPath hand a local path to the system; tests replace them.
var openPath = func(path string) error {
	if app := application.Get(); app != nil {
		return app.Browser.OpenFile(path)
	}
	return i18n.New("无法交给系统打开")
}

var revealPath = func(path string, selectFile bool) error {
	if app := application.Get(); app != nil {
		return app.Env.OpenFileManager(path, selectFile)
	}
	return i18n.New("无法交给系统打开")
}

// downloadsOf is an account's working download queue: an account whose
// session ended cannot download until it is signed back in.
func (d *DriveService) downloadsOf(account string) (*download.Queue, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	return s.liveDownloads()
}

// localDownloads is an account's download queue for what happens on this
// computer alone, which needs no session.
func (d *DriveService) localDownloads(account string) (*download.Queue, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	if q := s.downloadQueue(); q != nil {
		return q, nil
	}
	return nil, drive.ErrNoCredentials
}

// publishDownload forwards a queue change to the webview and System Notifications.
func (d *DriveService) publishDownload(s *accountSession, task download.Task) {
	row := toDownloadTask(s.key, task)
	if app := application.Get(); app != nil {
		app.Event.Emit(EventDownloadState, row)
	}
	d.announce(notice{Kind: noticeDownload, Account: s.key, ID: task.ID, Name: task.Name, State: task.State, Error: task.Error, Where: row.SavedTo})
}
