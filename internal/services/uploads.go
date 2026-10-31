package services

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
	"github.com/tonyflux/pkp/internal/upload"
)

// EventUploadState carries one UploadTask every time it changes: state
// transitions at once, progress at most five times a second per file.
const EventUploadState = "upload:state"

// UploadTask is the payload of EventUploadState and the row of Uploads():
// the queue's view of one file plus the icon bucket its name falls in, by
// the rule the browser uses, and the account it uploads into. Task ids are
// per account: the pair of Account and ID names one upload.
type UploadTask struct {
	upload.Task
	Category string `json:"category"`
	// Account is the account the upload belongs to (Account.ID), fixed
	// when it was queued.
	Account string `json:"account"`
}

func toUploadTask(account string, t upload.Task) UploadTask {
	return UploadTask{Task: t, Category: categoryOf(drive.File{Name: t.Name}), Account: account}
}

// Uploads lists every account's upload records, each account's oldest
// first, including the ones an earlier session left in the journal. An
// account whose session ended still lists what it was uploading.
func (d *DriveService) Uploads() []UploadTask {
	out := []UploadTask{}
	for _, s := range d.sessions() {
		q := s.queue()
		if q == nil {
			continue
		}
		for _, t := range q.List() {
			out = append(out, toUploadTask(s.key, t))
		}
	}
	return out
}

// Crumb is one folder on the way from the root, as the breadcrumb shows it.
type Crumb struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FolderTrail returns the folders from the root down to folderID itself,
// so the file page can open it with a correct breadcrumb. Empty for the
// root. Each hop comes from the index when it already has the folder and
// from the server otherwise: a folder an upload just made is not indexed
// until the next walk.
func (d *DriveService) FolderTrail(folderID string) ([]Crumb, error) {
	s, err := d.active()
	if err != nil {
		return nil, err
	}
	return d.folderTrail(s, folderID)
}

func (d *DriveService) folderTrail(s *accountSession, folderID string) ([]Crumb, error) {
	var reversed []Crumb
	var store *index.Store
	if idx := s.index(); idx != nil {
		store = idx.store
	}
	for hops := 0; folderID != "" && hops < 64; hops++ {
		if store != nil {
			if row, ok, err := store.Get(folderID); err == nil && ok {
				reversed = append(reversed, Crumb{ID: row.ID, Name: row.Name})
				folderID = row.ParentID
				continue
			}
		}
		id := folderID
		file, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
			return client.GetFile(ctx, id, drive.GetFileParams{})
		})
		if err != nil {
			return nil, err
		}
		reversed = append(reversed, Crumb{ID: file.ID, Name: file.Name})
		folderID = file.ParentID
	}
	out := make([]Crumb, len(reversed))
	for i, c := range reversed {
		out[len(reversed)-1-i] = c
	}
	return out, nil
}

// UploadFiles queues local files and folders for parentID (empty for the
// root) in an account ("" is the account on screen): the account whose
// folder was chosen, even if another is on screen by the time the files
// are picked. A folder is walked here and queued file by file, each
// carrying its path relative to the folder's parent so the tree is
// recreated on the server. Dotfiles (.DS_Store and friends) and empty
// folders are skipped. Paths are checked as they are added; the rest still
// go in when one is rejected, and the error names every one that was.
func (d *DriveService) UploadFiles(account string, paths []string, parentID string) ([]UploadTask, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	q, err := s.liveQueue()
	if err != nil {
		return nil, err
	}
	// The destination is named once per batch: the index or a few server
	// reads up the tree, never once per file.
	label := ""
	if trail, err := d.folderTrail(s, parentID); err == nil {
		crumbs := make([]index.Crumb, len(trail))
		for i, c := range trail {
			crumbs[i] = index.Crumb{ID: c.ID, Name: c.Name}
		}
		label = folderPath(crumbs)
	}
	var (
		added []UploadTask
		errs  []error
	)
	add := func(path, dir string) {
		to := upload.Target{ParentID: parentID, Dir: dir, SavedTo: label}
		if label != "" && dir != "" {
			to.SavedTo = label + " / " + strings.ReplaceAll(dir, "/", " / ")
		}
		task, err := q.Add(path, to)
		if err != nil {
			errs = append(errs, err)
			return
		}
		added = append(added, toUploadTask(s.key, task))
	}
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !info.IsDir() {
			add(root, "")
			continue
		}
		base := filepath.Dir(root)
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				errs = append(errs, err)
				return nil
			}
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(base, filepath.Dir(path))
			add(path, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return added, errors.Join(errs...)
}

// CancelUpload stops one of an account's queued or running uploads; its
// placeholder file on the server and whatever it had sent are discarded,
// Resumable upload or not.
func (d *DriveService) CancelUpload(account, id string) error {
	q, err := d.uploadsOf(account)
	if err != nil {
		return err
	}
	return q.Cancel(id)
}

// RetryUpload re-queues a failed, cancelled or interrupted upload of an
// account. With Resumable upload it continues where it stopped while its grant has
// time left, and otherwise starts from the beginning.
func (d *DriveService) RetryUpload(account, id string) error {
	q, err := d.uploadsOf(account)
	if err != nil {
		return err
	}
	return q.Retry(id)
}

// RemoveUpload drops one of an account's uploads from the list, stopping it
// first if needed. A placeholder it kept for Resumable upload goes to the trash.
func (d *DriveService) RemoveUpload(account, id string) error {
	q, err := d.uploadsOf(account)
	if err != nil {
		return err
	}
	return q.Remove(id)
}

// uploadsOf is an account's working queue: an account whose session ended
// cannot act on its uploads until it is signed back in.
func (d *DriveService) uploadsOf(account string) (*upload.Queue, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	return s.liveQueue()
}

// publishUpload forwards a queue change to the webview and System Notifications and,
// once a file has landed, asks the account's index for it.
func (d *DriveService) publishUpload(s *accountSession, task upload.Task) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventUploadState, toUploadTask(s.key, task))
	}
	d.announce(notice{Kind: noticeUpload, Account: s.key, ID: task.ID, Name: task.Name, State: task.State, Error: task.Error, Where: task.SavedTo})
	if task.State == upload.StateDone {
		requestSync(s)
	}
}
