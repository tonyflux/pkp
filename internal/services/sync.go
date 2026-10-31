package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/foldersync"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/syncfs"
	"github.com/tonyflux/pkp/internal/upload"
)

const EventSyncState = "sync:state"

// SyncStartResult returns a fresh preview instead of starting a stale plan.
type SyncStartResult struct {
	Pair    *foldersync.Pair    `json:"pair"`
	Preview *foldersync.Preview `json:"preview"`
}

func (d *DriveService) syncManager() (*foldersync.Manager, error) {
	d.folderSyncOnce.Do(func() {
		if d.configErr != nil {
			d.folderSyncErr = d.configErr
			return
		}
		d.folderSync, d.folderSyncErr = foldersync.New(foldersync.Options{
			Dir: filepath.Join(d.configDir, "folder-sync"),
			Backend: func(account string) (foldersync.Backend, error) {
				return d.syncBackend(account)
			},
			OnChange: func(pairs []foldersync.Pair) {
				if app := application.Get(); app != nil {
					app.Event.Emit(EventSyncState, pairs)
				}
			},
		})
	})
	return d.folderSync, d.folderSyncErr
}

// SyncPairs lists all locally owned relationships, including signed-out accounts.
func (d *DriveService) SyncPairs() ([]foldersync.Pair, error) {
	manager, err := d.syncManager()
	if err != nil {
		return nil, err
	}
	return manager.List(), nil
}

// SyncPreview reads both roots without authorizing writes. requestID also
// cancels an in-flight scan, before a completed preview ID exists.
func (d *DriveService) SyncPreview(requestID string, config foldersync.Config) (foldersync.Preview, error) {
	manager, err := d.syncManager()
	if err != nil {
		return foldersync.Preview{}, err
	}
	if requestID == "" || config.Account == "" {
		return foldersync.Preview{}, i18n.New("请选择同步所属账号并重新扫描")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.syncPreviewMu.Lock()
	if d.syncPreviews == nil {
		d.syncPreviews = map[string]context.CancelFunc{}
	}
	if _, exists := d.syncPreviews[requestID]; exists {
		d.syncPreviewMu.Unlock()
		return foldersync.Preview{}, i18n.New("这次同步扫描已经在进行")
	}
	d.syncPreviews[requestID] = cancel
	d.syncPreviewMu.Unlock()
	defer func() {
		d.syncPreviewMu.Lock()
		delete(d.syncPreviews, requestID)
		d.syncPreviewMu.Unlock()
	}()
	preview, err := manager.Preview(ctx, config)
	if ctx.Err() != nil {
		if preview.ID != "" {
			manager.CancelPreview(preview.ID)
		}
		return foldersync.Preview{}, ctx.Err()
	}
	return preview, err
}

func (d *DriveService) SyncCancelPreview(id string) {
	d.syncPreviewMu.Lock()
	cancel := d.syncPreviews[id]
	if cancel != nil {
		cancel()
	}
	d.syncPreviewMu.Unlock()
	if manager, err := d.syncManager(); err == nil {
		manager.CancelPreview(id)
	}
}

func (d *DriveService) SyncStart(previewID string) (SyncStartResult, error) {
	manager, err := d.syncManager()
	if err != nil {
		return SyncStartResult{}, err
	}
	pair, preview, err := manager.Start(context.Background(), previewID)
	result := SyncStartResult{Preview: preview}
	if pair.ID != "" {
		result.Pair = &pair
	}
	return result, err
}

func (d *DriveService) SyncBeginEdit(id string) error {
	manager, err := d.syncManager()
	if err != nil {
		return err
	}
	return manager.BeginEdit(context.Background(), id)
}

func (d *DriveService) SyncPause(id string, paused bool) error {
	manager, err := d.syncManager()
	if err != nil {
		return err
	}
	return manager.Pause(context.Background(), id, paused)
}

func (d *DriveService) SyncScan(id string) error {
	manager, err := d.syncManager()
	if err != nil {
		return err
	}
	return manager.Scan(context.Background(), id)
}

func (d *DriveService) SyncRemove(id string) error {
	manager, err := d.syncManager()
	if err != nil {
		return err
	}
	return manager.Remove(context.Background(), id)
}

func (d *DriveService) SyncKeepBoth(pairID, issueID string) error {
	manager, err := d.syncManager()
	if err != nil {
		return err
	}
	return manager.KeepBoth(context.Background(), pairID, issueID)
}

func (d *DriveService) SyncApproveDeletes(pairID, previewID string) error {
	manager, err := d.syncManager()
	if err != nil {
		return err
	}
	return manager.ApproveDeletes(context.Background(), pairID, previewID)
}

// SyncLocalFolder validates a native folder selection, never creating a pair.
func (d *DriveService) SyncLocalFolder(path string) (string, error) {
	identity, err := syncfs.Identify(path)
	if err != nil {
		return "", err
	}
	return identity.Path, nil
}

// RevealSyncLocalFolder opens an existing directory without changing its sync pair.
func (d *DriveService) RevealSyncLocalFolder(path string) error {
	root, err := d.SyncLocalFolder(path)
	if err != nil {
		return err
	}
	return revealPath(root, false)
}

// SyncFolders always reads the chosen account's real, complete directory
// listing. A disposable browsing index cannot authorize a sync root.
func (d *DriveService) SyncFolders(account, parentID string) ([]Entry, error) {
	backend, err := d.syncBackend(account)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), signInTimeout)
	defer cancel()
	if _, err := backend.Root(ctx, parentID); err != nil {
		return nil, err
	}
	nodes, err := backend.List(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, node := range nodes {
		if node.Directory {
			out = append(out, Entry{ID: node.ID, Name: node.Name, ParentID: node.ParentID, Folder: true, Kind: drive.KindFolder})
		}
	}
	slices.SortStableFunc(out, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (d *DriveService) SyncFolderTrail(account, id string) ([]Crumb, error) {
	backend, err := d.syncBackend(account)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), signInTimeout)
	defer cancel()
	var reversed []Crumb
	seen := map[string]bool{}
	for id != "" {
		if seen[id] {
			return nil, i18n.New("云端目录关系异常，请重新选择同步位置")
		}
		seen[id] = true
		node, err := backend.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if !node.Directory {
			return nil, i18n.New("同步位置必须是文件夹")
		}
		reversed = append(reversed, Crumb{ID: id, Name: node.Name})
		id = node.ParentID
	}
	slices.Reverse(reversed)
	return reversed, nil
}

type syncDriveBackend struct {
	d    *DriveService
	s    *accountSession
	user string
}

func (d *DriveService) syncBackend(account string) (*syncDriveBackend, error) {
	if account == "" {
		return nil, i18n.New("请选择同步所属账号并重新扫描")
	}
	s := d.byKey(account)
	if s == nil {
		return nil, errors.Join(foldersync.ErrAuth, errNoSuchAccount)
	}
	backend := &syncDriveBackend{d: d, s: s, user: s.record().UserID}
	if _, err := backend.client(); err != nil {
		return nil, err
	}
	return backend, nil
}

func (b *syncDriveBackend) AccountID() string { return b.user }

func (b *syncDriveBackend) client() (*drive.Client, error) {
	if b.d.byKey(b.s.key) != b.s || b.s.record().UserID != b.user || b.user == "" {
		return nil, errors.Join(foldersync.ErrAuth, i18n.New("同步所属账号已改变，请重新配对"))
	}
	client, err := b.s.api()
	return client, b.classify(err)
}

func (b *syncDriveBackend) classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, download.ErrChanged) {
		return errors.Join(foldersync.ErrChanged, err)
	}
	if sessionEnded(err) || drive.IsUnauthorized(err) {
		return errors.Join(foldersync.ErrAuth, err)
	}
	if drive.IsNotFound(err) || drive.IsInRecycleBin(err) {
		return errors.Join(foldersync.ErrNotFound, err)
	}
	var api *drive.APIError
	if errors.As(err, &api) {
		if api.Status == http.StatusForbidden {
			return errors.Join(foldersync.ErrPermission, err)
		}
		if api.Status >= 500 || api.Status == http.StatusTooManyRequests {
			return errors.Join(foldersync.ErrOffline, err)
		}
	}
	var network net.Error
	if errors.As(err, &network) {
		return errors.Join(foldersync.ErrOffline, err)
	}
	return err
}

// A transport/decode failure after a mutation is not proof of rejection.
func (b *syncDriveBackend) mutationError(err error) error {
	if err == nil {
		return nil
	}
	var api *drive.APIError
	if errors.As(err, &api) && api.Status >= 400 && api.Status < 500 {
		return b.classify(err)
	}
	if sessionEnded(err) {
		return b.classify(err)
	}
	return errors.Join(foldersync.ErrUnknown, b.classify(err))
}

func syncNode(file drive.File) foldersync.Node {
	// Signed links are excluded: minting FETCH links is not a file change.
	version, _ := json.Marshal(struct {
		ID, Parent, Name, Kind, Modified, Hash, MD5, Phase string
		Size                                               int64
	}{file.ID, file.ParentID, file.Name, file.Kind, file.ModifiedTime, file.Hash, file.MD5Checksum, file.Phase, int64(file.Size)})
	sum := sha256.Sum256(version)
	return foldersync.Node{
		ID: file.ID, ParentID: file.ParentID, Name: file.Name,
		Directory: file.IsFolder(), Size: int64(file.Size), Modified: file.ModifiedTime,
		Version: hex.EncodeToString(sum[:]), Phase: file.Phase,
	}
}

func (b *syncDriveBackend) Get(ctx context.Context, id string) (foldersync.Node, error) {
	if id == "" {
		return foldersync.Node{ID: "", Directory: true, Version: "drive-root:" + b.user}, nil
	}
	client, err := b.client()
	if err != nil {
		return foldersync.Node{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()
	file, err := client.GetFile(ctx, id, drive.GetFileParams{})
	if err != nil {
		return foldersync.Node{}, b.classify(err)
	}
	if file == nil || file.ID != id || file.Trashed || file.UserID != "" && file.UserID != b.user {
		return foldersync.Node{}, errors.Join(foldersync.ErrNotFound, i18n.New("云端同步位置已不可用"))
	}
	return syncNode(*file), nil
}

func (b *syncDriveBackend) Root(ctx context.Context, id string) (foldersync.Node, error) {
	root, err := b.Get(ctx, id)
	if err != nil {
		return foldersync.Node{}, err
	}
	if !root.Directory {
		return foldersync.Node{}, i18n.New("同步位置必须是文件夹")
	}
	root.Ancestors = []string{}
	var names []string
	seen := map[string]bool{}
	for node := root; node.ID != ""; {
		if seen[node.ID] {
			return foldersync.Node{}, i18n.New("云端目录关系异常，请重新选择同步位置")
		}
		seen[node.ID] = true
		names = append(names, node.Name)
		root.Ancestors = append(root.Ancestors, node.ParentID)
		if node.ParentID == "" {
			break
		}
		node, err = b.Get(ctx, node.ParentID)
		if err != nil {
			return foldersync.Node{}, err
		}
		if !node.Directory {
			return foldersync.Node{}, i18n.New("云端目录关系异常，请重新选择同步位置")
		}
	}
	slices.Reverse(names)
	root.Path = "全部文件" // i18n-ignore: canonical drive path, translated only at display.
	if len(names) > 0 {
		root.Path += " / " + strings.Join(names, " / ")
	}
	return root, nil
}

func (b *syncDriveBackend) List(ctx context.Context, parentID string) ([]foldersync.Node, error) {
	client, err := b.client()
	if err != nil {
		return nil, err
	}
	params := drive.ListFilesParams{ParentID: parentID, Filters: drive.OnlyLive(), Limit: drive.MaxListLimit, Order: drive.OrderNameAsc}
	out := []foldersync.Node{}
	seenTokens, seenIDs := map[string]bool{}, map[string]bool{}
	for {
		pageCtx, cancel := context.WithTimeout(ctx, driveTimeout)
		page, err := client.ListFiles(pageCtx, params)
		cancel()
		if err != nil {
			return nil, b.classify(err)
		}
		for _, file := range page.Files {
			if file.ID == "" || file.ParentID != parentID || file.Trashed || seenIDs[file.ID] || file.UserID != "" && file.UserID != b.user {
				return nil, i18n.New("云端目录在扫描期间发生变化，请重新核对")
			}
			seenIDs[file.ID] = true
			out = append(out, syncNode(file))
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		if seenTokens[page.NextPageToken] {
			return nil, i18n.New("云端目录分页未完整结束，未执行同步")
		}
		seenTokens[page.NextPageToken] = true
		params.PageToken = page.NextPageToken
	}
}

func sameSyncVersion(want, got foldersync.Node) bool {
	return want.ID == got.ID && want.ParentID == got.ParentID && want.Name == got.Name &&
		want.Directory == got.Directory && want.Size == got.Size && (want.Version == "" || want.Version == got.Version)
}

func (b *syncDriveBackend) current(ctx context.Context, node foldersync.Node) (foldersync.Node, error) {
	got, err := b.Get(ctx, node.ID)
	if err != nil {
		return foldersync.Node{}, err
	}
	if !sameSyncVersion(node, got) {
		b.d.log.Debug("sync: entry version changed", "id", node.ID, "expected", node.Version, "actual", got.Version, "nameChanged", node.Name != got.Name, "parentChanged", node.ParentID != got.ParentID, "sizeChanged", node.Size != got.Size, "phaseBefore", node.Phase, "phaseAfter", got.Phase, "modifiedBefore", node.Modified, "modifiedAfter", got.Modified)
		return foldersync.Node{}, errors.Join(foldersync.ErrChanged, i18n.New("文件在核对后发生变化，已保留原件，请重新扫描"))
	}
	return got, nil
}

func (b *syncDriveBackend) Hash(ctx context.Context, node foldersync.Node) (string, error) {
	if node.Directory {
		return "", nil
	}
	if _, err := b.current(ctx, node); err != nil {
		return "", err
	}
	base := filepath.Join(b.d.configDir, "folder-sync", "checks")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(base, "content-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "content")
	if err := b.Download(ctx, node, path, filepath.Join(dir, "checkpoint"), "hash-"+filepath.Base(dir), nil); err != nil {
		return "", err
	}
	hash, err := syncfs.Hash(ctx, path)
	if err != nil {
		return "", err
	}
	if _, err := b.current(ctx, node); err != nil {
		return "", err
	}
	return hash, nil
}

func (b *syncDriveBackend) Download(ctx context.Context, node foldersync.Node, path, checkpoint, operationID string, progress func(done, total int64)) error {
	if _, err := b.current(ctx, node); err != nil {
		return err
	}
	queue, err := b.s.liveDownloads()
	if err != nil {
		return b.classify(err)
	}
	err = queue.Receive(ctx, download.DirectRequest{ID: operationID, FileID: node.ID, Path: path, Checkpoint: checkpoint, Size: node.Size}, func(task download.Task) {
		if progress != nil {
			progress(task.Done, task.Size)
		}
	})
	if err != nil {
		return b.classify(err)
	}
	_, err = b.current(ctx, node)
	return err
}

func (b *syncDriveBackend) Upload(ctx context.Context, request foldersync.UploadRequest, progress func(done, total int64)) (foldersync.Node, error) {
	queue, err := b.s.liveQueue()
	if err != nil {
		return foldersync.Node{}, b.classify(err)
	}
	var slot *drive.UploadSlot
	if request.FileID != "" {
		if len(request.Grant) == 0 {
			return foldersync.Node{}, errors.Join(foldersync.ErrUnknown, i18n.New("上传授权记录不完整，已停止自动重试"))
		}
		slot = &drive.UploadSlot{File: drive.File{ID: request.FileID}}
		if err := json.Unmarshal(request.Grant, &slot.OSS); err != nil {
			return foldersync.Node{}, errors.Join(foldersync.ErrUnknown, err)
		}
	}
	fileID, err := queue.Send(ctx, upload.DirectRequest{
		ID: request.ID, Path: request.Path, ParentID: request.ParentID, Name: request.Name, Checkpoint: request.Checkpoint, Slot: slot,
		SaveSlot: func(slot *drive.UploadSlot) error {
			grant, err := json.Marshal(slot.OSS)
			if err != nil {
				return err
			}
			return request.SaveSlot(slot.File.ID, grant)
		},
	}, func(task upload.Task) {
		if progress != nil {
			progress(task.Done, task.Size)
		}
	})
	if err != nil {
		return foldersync.Node{ID: fileID}, b.mutationError(err)
	}
	// Sending every byte to OSS is not cloud completion. The final drive
	// object must be complete and its downloaded bytes must match the source.
	var node foldersync.Node
	deadline := time.NewTimer(taskWaitCap)
	defer deadline.Stop()
	for {
		node, err = b.Get(ctx, fileID)
		if err != nil {
			return node, errors.Join(foldersync.ErrUnknown, err)
		}
		if node.Phase == drive.PhaseComplete {
			break
		}
		if node.Phase == drive.PhaseError {
			return node, i18n.New("云端未完成同步上传，旧版本已保留")
		}
		select {
		case <-ctx.Done():
			return node, errors.Join(foldersync.ErrUnknown, ctx.Err())
		case <-deadline.C:
			return node, errors.Join(foldersync.ErrUnknown, i18n.New("云端尚未确认上传完成，旧版本已保留"))
		case <-time.After(taskPoll):
		}
	}
	if node.ParentID != request.ParentID || node.Name != request.Name {
		return node, errors.Join(foldersync.ErrUnknown, i18n.New("云端保存位置与计划不同，已保留所有版本"))
	}
	localHash, err := syncfs.Hash(ctx, request.Path)
	if err != nil {
		return node, err
	}
	node.Hash, err = b.Hash(ctx, node)
	if err != nil {
		return node, errors.Join(foldersync.ErrUnknown, err)
	}
	if node.Hash != localHash {
		return node, errors.Join(foldersync.ErrUnknown, i18n.New("同步文件内容核对不一致，旧版本已保留"))
	}
	requestSync(b.s)
	return node, nil
}

func (b *syncDriveBackend) availableName(ctx context.Context, parentID, name, exceptID string) error {
	if invalidName(name) {
		return i18n.New("同步文件名无效，请先处理名称冲突")
	}
	nodes, err := b.List(ctx, parentID)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.ID != exceptID && nameKey(node.Name) == nameKey(name) {
			return i18n.New("同步目标名称已被占用，未覆盖现有文件")
		}
	}
	return nil
}

func (b *syncDriveBackend) Mkdir(ctx context.Context, parentID, name, operationID string) (foldersync.Node, error) {
	if err := b.availableName(ctx, parentID, name, ""); err != nil {
		return foldersync.Node{}, err
	}
	client, err := b.client()
	if err != nil {
		return foldersync.Node{}, err
	}
	if err := ctx.Err(); err != nil {
		return foldersync.Node{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()
	file, err := client.CreateFolder(ctx, parentID, name)
	if err != nil {
		return foldersync.Node{}, b.mutationError(err)
	}
	node, err := b.Get(ctx, file.ID)
	if err == nil && (node.ParentID != parentID || node.Name != name || !node.Directory) {
		err = i18n.New("云端保存位置与计划不同，已保留所有版本")
	}
	if err != nil {
		return node, errors.Join(foldersync.ErrUnknown, err)
	}
	requestSync(b.s)
	return node, nil
}

func (b *syncDriveBackend) Rename(ctx context.Context, node foldersync.Node, name, operationID string) (foldersync.Node, error) {
	if _, err := b.current(ctx, node); err != nil {
		return foldersync.Node{}, err
	}
	if err := b.availableName(ctx, node.ParentID, name, node.ID); err != nil {
		return foldersync.Node{}, err
	}
	client, err := b.client()
	if err != nil {
		return foldersync.Node{}, err
	}
	if err := ctx.Err(); err != nil {
		return foldersync.Node{}, err
	}
	step, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()
	if _, err := client.Rename(step, node.ID, name); err != nil {
		return foldersync.Node{}, b.mutationError(err)
	}
	got, err := b.Get(ctx, node.ID)
	if err != nil || got.Name != name || got.ParentID != node.ParentID {
		return got, errors.Join(foldersync.ErrUnknown, err, i18n.New("云端尚未确认名称变更，已保留原件"))
	}
	requestSync(b.s)
	return got, nil
}

func (b *syncDriveBackend) Move(ctx context.Context, node foldersync.Node, parentID, name, operationID string) (foldersync.Node, error) {
	if parentID == node.ParentID {
		return b.Rename(ctx, node, name, operationID)
	}
	if _, err := b.current(ctx, node); err != nil {
		return foldersync.Node{}, err
	}
	// Move applies the original name before rename, so both must be free.
	if err := b.availableName(ctx, parentID, node.Name, node.ID); err != nil {
		return foldersync.Node{}, err
	}
	if err := b.availableName(ctx, parentID, name, node.ID); err != nil {
		return foldersync.Node{}, err
	}
	client, err := b.client()
	if err != nil {
		return foldersync.Node{}, err
	}
	if err := ctx.Err(); err != nil {
		return foldersync.Node{}, err
	}
	step, cancel := context.WithTimeout(ctx, driveTimeout)
	task, err := client.BatchMove(step, []string{node.ID}, parentID)
	cancel()
	if err != nil {
		return foldersync.Node{}, b.mutationError(err)
	}
	if err := b.await(ctx, task); err != nil {
		return foldersync.Node{}, errors.Join(foldersync.ErrUnknown, err)
	}
	got, err := b.Get(ctx, node.ID)
	if err != nil || got.ParentID != parentID || got.Name != node.Name {
		return got, errors.Join(foldersync.ErrUnknown, err, i18n.New("云端尚未确认移动结果，已保留所有版本"))
	}
	if got.Name != name {
		return b.Rename(ctx, got, name, operationID)
	}
	requestSync(b.s)
	return got, nil
}

func (b *syncDriveBackend) await(ctx context.Context, taskID string) error {
	if taskID == "" {
		return nil
	}
	client, err := b.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, taskWaitCap)
	defer cancel()
	for {
		step, done := context.WithTimeout(ctx, driveTimeout)
		task, err := client.GetTask(step, taskID)
		done()
		if err != nil {
			return b.classify(err)
		}
		if task.Done() {
			if task.Phase != drive.PhaseComplete {
				return i18n.New("云端未确认同步操作完成，请重新核对")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(taskPoll):
		}
	}
}

func (b *syncDriveBackend) Trash(ctx context.Context, node foldersync.Node, operationID string) error {
	if _, err := b.current(ctx, node); err != nil {
		return err
	}
	if node.Directory {
		children, err := b.List(ctx, node.ID)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return i18n.New("同步文件夹仍有内容，已保留且未删除")
		}
	}
	client, err := b.client()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	step, cancel := context.WithTimeout(ctx, driveTimeout)
	task, err := client.BatchTrash(step, []string{node.ID})
	cancel()
	if err != nil {
		return b.mutationError(err)
	}
	if err := b.await(ctx, task); err != nil {
		return errors.Join(foldersync.ErrUnknown, err)
	}
	trashed, err := b.Trashed(ctx, node.ID)
	if err != nil {
		return errors.Join(foldersync.ErrUnknown, err)
	}
	if trashed {
		requestSync(b.s)
		return nil
	}
	return errors.Join(foldersync.ErrUnknown, i18n.New("云端回收站尚未确认这项文件，未标记为已删除"))
}

func (b *syncDriveBackend) Trashed(ctx context.Context, id string) (bool, error) {
	client, err := b.client()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()
	page, err := client.ListFiles(ctx, drive.ListFilesParams{ParentID: drive.WholeTree, Filters: drive.NewFilters().Eq("trashed", true).Eq("id", id), Limit: drive.MaxListLimit})
	if err != nil {
		return false, b.classify(err)
	}
	for _, file := range page.Files {
		if file.ID == id && file.Trashed {
			return true, nil
		}
	}
	return false, nil
}
