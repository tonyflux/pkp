package services

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// Online extraction: the decompress service lists an archive kept in the
// drive and extracts picked members into a drive folder as a cloud task.
// Nothing here downloads the archive; the password, when one is needed,
// travels with each request and is never kept.

// Outcomes of reading or extracting an archive, as the sheet tells them
// apart. A password, a timeout and a format the service cannot open are
// different things and are never folded into one another.
const (
	ArchiveOK            = "ok"
	ArchiveNeedsPassword = "password"
	ArchiveWrongPassword = "wrongPassword"
	// ArchiveRunning: an extraction of this archive is already under way
	// (its task id comes with it).
	ArchiveRunning     = "running"
	ArchiveTimeout     = "timeout"
	ArchiveUnsupported = "unsupported"
	ArchiveTooMany     = "tooMany"
	ArchiveTooBig      = "tooBig"
	ArchiveNoQuota     = "quota"
	// ArchiveNoSpace: the drive has no room for what would be extracted.
	ArchiveNoSpace = "noSpace"
	// ArchiveUnfinished: the archive itself is not completely in the
	// drive yet (an upload or cloud add still under way).
	ArchiveUnfinished = "unfinished"
	// ArchiveRefused covers what the service says without a remedy the
	// sheet can offer (deleted, under review, sensitive…); Message holds
	// the service's own words when it sent any.
	ArchiveRefused = "refused"

	// ExtractStarted: the task was created.
	ExtractStarted = "started"
	// ExtractNameTaken: the destination already holds an entry named as
	// the folder the extraction makes. The service would fail the task on
	// it, so the sheet asks for another place instead.
	ExtractNameTaken = "nameTaken"
	// ExtractDestinationGone: the destination folder was deleted, trashed
	// or is no folder any more.
	ExtractDestinationGone = "destinationGone"
)

// archiveListTries is how many times a listing the service timed out on is
// asked again before the sheet says so; archiveRetryPause is the wait in
// between (a test seam).
const archiveListTries = 3

var archiveRetryPause = 2 * time.Second

// ArchiveItem is a top-level member of an archive: a file, or a folder
// standing for everything under it.
type ArchiveItem struct {
	// Index picks a file for extraction; folders have none.
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Folder bool   `json:"folder"`
	// Size is a file's uncompressed size; 0 for folders, whose size the
	// service does not report.
	Size int64 `json:"size"`
	// Path is the member's path inside the archive, a folder's ending in
	// "/": what extraction is asked for.
	Path string `json:"path"`
	// Category buckets a file like the file page does, for its icon.
	Category string `json:"category"`
}

// ArchiveContents is the outcome of reading an archive's top level.
type ArchiveContents struct {
	Status string `json:"status"`
	// Message is the service's own text for a refusal, if it sent one.
	Message string `json:"message"`
	// TaskID is the extraction under way when Status is ArchiveRunning.
	TaskID string        `json:"taskId"`
	Items  []ArchiveItem `json:"items"`
}

// ArchiveContents reads the top level of the archive fileID in account
// ("" is the account on screen). password is "" until the service has said
// one is needed.
func (d *DriveService) ArchiveContents(account, fileID, password string) (ArchiveContents, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return ArchiveContents{}, err
	}
	file, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
		return client.GetFile(ctx, fileID, drive.GetFileParams{})
	})
	if err != nil {
		return ArchiveContents{}, err
	}
	if !archiveReady(*file) {
		return ArchiveContents{Status: ArchiveUnfinished, Items: []ArchiveItem{}}, nil
	}
	params := drive.ArchiveListParams{FileID: fileID, GCID: file.Hash, Password: password}
	var listing *drive.ArchiveListing
	for try := 1; ; try++ {
		listing, err = callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.ArchiveListing, error) {
			return client.ListArchive(ctx, params)
		})
		if err != nil {
			return ArchiveContents{}, err
		}
		if listing.Status != drive.ArchiveListTimeout || try == archiveListTries {
			break
		}
		time.Sleep(archiveRetryPause)
	}
	out := ArchiveContents{Status: archiveStatus(listing.Status), TaskID: listing.TaskID, Items: []ArchiveItem{}}
	if out.Status == ArchiveRefused {
		out.Message = listing.StatusText
	}
	if out.Status != ArchiveOK {
		return out, nil
	}
	for _, e := range listing.Files {
		item := ArchiveItem{Index: e.Index, Name: e.Name, Folder: e.Kind == drive.KindFolder, Size: int64(e.Size), Path: e.Path}
		if item.Folder {
			item.Category = "folder"
		} else {
			item.Category = categoryOf(drive.File{Name: e.Name, MimeType: e.MimeType})
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// archiveReady reports whether the archive's bytes are all in the drive:
// the service needs its content id, which an unfinished file lacks.
func archiveReady(f drive.File) bool {
	return !f.IsFolder() && len(f.Hash) == 40 && (f.Phase == "" || f.Phase == drive.PhaseComplete)
}

// archiveStatus maps the response's status field to outcomes shown by the sheet.
func archiveStatus(status string) string {
	switch status {
	case drive.ArchiveOK:
		return ArchiveOK
	case drive.ArchivePasswordEmpty:
		return ArchiveNeedsPassword
	case drive.ArchivePasswordWrong:
		return ArchiveWrongPassword
	case drive.ArchiveRunningTask:
		return ArchiveRunning
	case drive.ArchiveListTimeout:
		return ArchiveTimeout
	case drive.ArchiveInvalidFormat:
		return ArchiveUnsupported
	case drive.ArchiveTooManySubfiles:
		return ArchiveTooMany
	case drive.ArchiveTooBig:
		return ArchiveTooBig
	case drive.ArchiveNeedMoreQuota:
		return ArchiveNoQuota
	}
	return ArchiveRefused
}

// ArchivePick is one member to extract, as ArchiveItem gave it.
type ArchivePick struct {
	Index  int    `json:"index"`
	Folder bool   `json:"folder"`
	Path   string `json:"path"`
}

// ExtractRequest starts an extraction. Picks empty extracts everything
// (the sheet sends that when every item is ticked: the listing it shows
// may be cut short by the service, the archive is not).
type ExtractRequest struct {
	FileID   string        `json:"fileId"`
	Password string        `json:"password"`
	ParentID string        `json:"parentId"`
	Picks    []ArchivePick `json:"picks"`
}

// ExtractStart is the outcome of ExtractArchive.
type ExtractStart struct {
	// Status is ExtractStarted, ArchiveRunning, ExtractNameTaken,
	// ExtractDestinationGone or one of the listing outcomes.
	Status  string `json:"status"`
	Message string `json:"message"`
	TaskID  string `json:"taskId"`
	// Files is how many archive members the task extracts, folder entries
	// included: the sheet says items, not files.
	Files int `json:"files"`
	// Folder is the name of the folder the extraction makes inside the
	// destination: the archive's name without its extension.
	Folder string `json:"folder"`
}

// ExtractArchive submits the extraction of an archive in account into the
// folder req.ParentID ("" = root). The destination is checked first: it
// must still be a live folder, and must not already hold an entry named
// as the folder the extraction makes.
func (d *DriveService) ExtractArchive(account string, req ExtractRequest) (ExtractStart, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return ExtractStart{}, err
	}
	file, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
		return client.GetFile(ctx, req.FileID, drive.GetFileParams{})
	})
	if err != nil {
		return ExtractStart{}, err
	}
	folder := strings.TrimSuffix(file.Name, path.Ext(file.Name))
	out := ExtractStart{Folder: folder}
	if !archiveReady(*file) {
		out.Status = ArchiveUnfinished
		return out, nil
	}
	trail, err := d.destination(s, req.ParentID)
	if errors.Is(err, errDestinationGone) {
		out.Status = ExtractDestinationGone
		return out, nil
	}
	if err != nil {
		return ExtractStart{}, err
	}
	taken, err := d.nameTaken(s, req.ParentID, folder)
	if err != nil {
		return ExtractStart{}, err
	}
	if taken {
		out.Status = ExtractNameTaken
		return out, nil
	}

	params := drive.ExtractParams{FileID: req.FileID, GCID: file.Hash, Password: req.Password, ParentID: req.ParentID}
	for _, c := range trail {
		params.ParentPath = append(params.ParentPath, c.Name)
	}
	for _, p := range req.Picks {
		pick := drive.ArchivePick{Index: p.Index, Kind: drive.KindFile, Path: p.Path}
		if p.Folder {
			pick.Kind = drive.KindFolder
		}
		params.Picks = append(params.Picks, pick)
	}
	// Creating a task is not idempotent: one attempt, no retry of its own.
	res, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.ExtractResult, error) {
		return client.Extract(ctx, params)
	})
	if status := extractRefusal(err); status != "" {
		out.Status = status
		return out, nil
	}
	if err != nil {
		return ExtractStart{}, err
	}
	out.TaskID = res.TaskID
	out.Files = res.FilesNum
	switch res.Status {
	case drive.ArchiveOK:
		out.Status = ExtractStarted
		requestSync(s)
	default:
		out.Status = archiveStatus(res.Status)
		if out.Status == ArchiveRefused {
			out.Message = res.StatusText
		}
	}
	return out, nil
}

// extractRefusal names the refusals the service sends as errors rather
// than statuses; "" for anything else, nil included.
func extractRefusal(err error) string {
	var apiErr *drive.APIError
	if !errors.As(err, &apiErr) {
		return ""
	}
	switch {
	case apiErr.Name == "failed_precondition" && strings.Contains(apiErr.Description, "too many files"):
		return ArchiveTooMany
	case apiErr.Name == "file_size_exceeded":
		return ArchiveTooBig
	case apiErr.Name == "file_space_not_enough":
		return ArchiveNoSpace
	}
	return ""
}

var errDestinationGone = i18n.New("保存到的文件夹已经不在了")

// destination checks that folderID is still a live folder of the account
// and returns its trail from the root (empty for the root itself).
func (d *DriveService) destination(s *accountSession, folderID string) ([]Crumb, error) {
	if folderID == "" {
		return nil, nil
	}
	file, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.File, error) {
		return client.GetFile(ctx, folderID, drive.GetFileParams{})
	})
	if drive.IsNotFound(err) || drive.IsInRecycleBin(err) {
		return nil, errDestinationGone
	}
	if err != nil {
		return nil, err
	}
	if !file.IsFolder() || file.Trashed {
		return nil, errDestinationGone
	}
	return d.folderTrail(s, folderID)
}

// nameTaken reports whether folderID ("" = root) already holds a live
// entry called name. A complete index answers (it trails the drive by one
// sync pass at most; a name taken in between fails the task, which says
// so). Before that the server's listing of the folder is read: it has no
// filter by name (file_invalid_file_filters, probed 2026-10-17).
func (d *DriveService) nameTaken(s *accountSession, folderID, name string) (bool, error) {
	if idx := s.index(); idx != nil && idx.syncer.Ready() {
		return idx.store.HasChildNamed(folderID, name)
	}
	params := drive.ListFilesParams{ParentID: folderID, Filters: drive.OnlyLive(), Limit: drive.MaxListLimit}
	for {
		list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.FileList, error) {
			return client.ListFiles(ctx, params)
		})
		if err != nil {
			return false, err
		}
		for _, f := range list.Files {
			if f.Name == name && !f.Trashed {
				return true, nil
			}
		}
		if list.NextPageToken == "" || list.NextPageToken == params.PageToken {
			return false, nil
		}
		params.PageToken = list.NextPageToken
	}
}

// Phases of an extraction as the sheet shows them.
const (
	ExtractRunning = "running"
	ExtractDone    = "done"
	ExtractFailed  = "failed"
)

// ExtractState is how an extraction task stands.
type ExtractState struct {
	Phase string `json:"phase"`
	// Progress is the service's percentage, 0 while it has given none.
	Progress int `json:"progress"`
	// Next is how many seconds to wait before asking again.
	Next int `json:"next"`
	// FileID, once done, is the folder the extraction made (or the only
	// file it extracted).
	FileID string `json:"fileId"`
	// Error is the service's code for a failed task (file_space_not_enough,
	// has_sensitive_words…), "" when it gave none.
	Error string `json:"error"`
}

// ExtractProgress reads an extraction task of account.
func (d *DriveService) ExtractProgress(account, taskID string) (ExtractState, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return ExtractState{}, err
	}
	p, err := callIn(d, s, func(ctx context.Context, client *drive.Client) (*drive.ExtractProgress, error) {
		return client.ExtractProgressOf(ctx, taskID)
	})
	// A member account out of room gets an error here rather than a failed
	// phase (the service's VIP branch): the task has failed all the same.
	if extractRefusal(err) == ArchiveNoSpace {
		return ExtractState{Phase: ExtractFailed, Next: 2, Error: "file_space_not_enough"}, nil
	}
	if err != nil {
		return ExtractState{}, err
	}
	out := ExtractState{Phase: ExtractRunning, Progress: min(max(p.Progress, 0), 100), Next: p.ExpiresIn}
	switch p.Phase {
	case drive.PhaseComplete:
		out.Phase = ExtractDone
		out.FileID = p.FileID
		requestSync(s)
	case drive.PhaseError:
		out.Phase = ExtractFailed
		out.Error = p.Params["error_detail"]
	case drive.PhasePending, drive.PhaseRunning:
	default:
		// PHASE_TYPE_UNKNOW: the service lost track of the task.
		out.Phase = ExtractFailed
	}
	if out.Next <= 0 || out.Next > 30 {
		out.Next = 2
	}
	return out, nil
}

// FolderChoice is a folder offered as a destination.
type FolderChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Subfolders is how many folders it holds, -1 when unknown (the index
	// is not complete yet).
	Subfolders int `json:"subfolders"`
	// Trail runs from the root down to the folder itself; set on search
	// results, whose path is how same-named folders are told apart.
	Trail []Crumb `json:"trail"`
}

// PickFolders lists the folders directly under parentID ("" = root) of
// account, sorted by name, for a destination picker.
func (d *DriveService) PickFolders(account, parentID string) ([]FolderChoice, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	if idx := s.index(); idx != nil && idx.syncer.Ready() {
		rows, err := idx.store.Folders(parentID)
		if err != nil {
			return nil, err
		}
		return folderChoices(idx.store, rows, false)
	}
	out := []FolderChoice{}
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
			out = append(out, FolderChoice{ID: f.ID, Name: f.Name, Subfolders: -1})
		}
		if list.NextPageToken == "" || list.NextPageToken == params.PageToken {
			return out, nil
		}
		params.PageToken = list.NextPageToken
	}
}

// FolderSearch is the outcome of SearchFolders. Ready is false while the
// index is still being built: the server has no folder search, so the
// whole drive cannot be searched until then.
type FolderSearch struct {
	Ready   bool           `json:"ready"`
	Folders []FolderChoice `json:"folders"`
}

// folderSearchLimit caps the folders a search returns.
const folderSearchLimit = 100

// SearchFolders finds folders anywhere in account's drive whose name holds
// query, case aside, closest names first.
func (d *DriveService) SearchFolders(account, query string) (FolderSearch, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return FolderSearch{}, err
	}
	idx := s.index()
	if idx == nil || !idx.syncer.Ready() {
		return FolderSearch{Folders: []FolderChoice{}}, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return FolderSearch{Ready: true, Folders: []FolderChoice{}}, nil
	}
	rows, err := idx.store.FoldersNamed(query, true, folderSearchLimit)
	if err != nil {
		return FolderSearch{}, err
	}
	folders, err := folderChoices(idx.store, rows, true)
	if err != nil {
		return FolderSearch{}, err
	}
	return FolderSearch{Ready: true, Folders: folders}, nil
}

// folderChoices adds the subfolder counts, and the trails when asked, to
// index rows.
func folderChoices(store *index.Store, rows []index.Row, trails bool) ([]FolderChoice, error) {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts, err := store.SubfolderCounts(ids)
	if err != nil {
		return nil, err
	}
	out := make([]FolderChoice, 0, len(rows))
	for _, r := range rows {
		c := FolderChoice{ID: r.ID, Name: r.Name, Subfolders: counts[r.ID]}
		if trails {
			crumbs, err := store.Path(r.ID)
			if err != nil {
				return nil, err
			}
			c.Trail = make([]Crumb, len(crumbs))
			for i, k := range crumbs {
				c.Trail[i] = Crumb{ID: k.ID, Name: k.Name}
			}
		}
		out = append(out, c)
	}
	return out, nil
}
