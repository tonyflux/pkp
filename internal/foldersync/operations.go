package foldersync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tonyflux/pkp/internal/syncfs"
)

func splitPath(p string) []string { return strings.Split(p, "/") }
func (m *Manager) journal(r *runtimePair, o *operation, stage string) error {
	if strings.HasSuffix(stage, "-intent") || stage == "keep-preserve" {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		m.mu.Lock()
		stop := r.record.Pair.Paused || r.record.Removing || r.editing || m.closed
		m.mu.Unlock()
		if stop {
			return context.Canceled
		}
		if _, _, err := m.roots(ctx, r.record.Pair.Config, r.record.Identity, r.record.Owner, new(r.record.Root)); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o.Stage = stage
	return m.saveLocked()
}
func (m *Manager) progress(r *runtimePair, o *operation) func(int64, int64) {
	return func(done, total int64) {
		m.mu.Lock()
		r.record.Pair.Progress.Path = o.Path
		r.record.Pair.Progress.Direction = o.Direction
		r.record.Pair.Progress.Done = done
		r.record.Pair.Progress.Total = total
		m.mu.Unlock()
		m.notify()
	}
}
func (m *Manager) local(ctx context.Context, r *runtimePair, rel string) (*Node, error) {
	e, err := syncfs.Inspect(ctx, r.record.Identity, rel)
	if err != nil || e == nil {
		return nil, err
	}
	return new(Node{Path: e.Path, Name: e.Name, ID: e.FileID, Hash: e.Hash, Modified: e.ModTime, Version: e.Version, Directory: e.Directory, Size: e.Size}), nil
}
func freshCloud(ctx context.Context, b Backend, n *Node) (*Node, error) {
	if n == nil {
		return nil, nil
	}
	fresh, err := b.Get(ctx, n.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fresh.Path = n.Path
	if !fresh.Directory {
		if !complete(fresh) {
			return nil, ErrChanged
		}
		fresh.Hash, err = b.Hash(ctx, fresh)
		if err != nil {
			return nil, err
		}
	}
	return new(fresh), nil
}
func cloudProof(a, b *Node) bool {
	if !same(a, b) {
		return false
	}
	if a == nil {
		return true
	}
	// The server may finalize GCID and timestamps after COMPLETE. Actual
	// content hashes plus stable identity/location prove the tracked version.
	return a.ID == b.ID && a.ParentID == b.ParentID && a.Name == b.Name
}
func (m *Manager) preflight(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	if o.ConfigVersion != configProof(r.record.Pair.Config) {
		return ErrRepair
	}
	if _, _, err := m.roots(ctx, r.record.Pair.Config, r.record.Identity, r.record.Owner, new(r.record.Root)); err != nil {
		return err
	}
	localPath := o.Path
	if o.Action == "rename" && o.Direction == "download" {
		localPath = o.From
	}
	l, err := m.local(ctx, r, localPath)
	if err != nil {
		return err
	}
	if !same(l, o.Local) {
		return ErrChanged
	}
	c, err := freshCloud(ctx, b, o.Cloud)
	if err != nil {
		return err
	}
	if !cloudProof(o.Cloud, c) {
		return ErrChanged
	}
	if o.Cloud == nil {
		parent, err := m.parent(ctx, r, b, o.Path)
		if err != nil {
			return err
		}
		if err = m.available(ctx, r, b, parent, path.Base(o.Path), ""); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) parent(ctx context.Context, r *runtimePair, b Backend, rel string) (string, error) {
	parent := path.Dir(rel)
	if parent == "." {
		return r.record.Pair.CloudID, nil
	}
	m.mu.Lock()
	n, ok := r.record.Snapshot.Cloud[parent]
	m.mu.Unlock()
	if !ok || !n.Directory {
		return "", ErrChanged
	}
	fresh, err := b.Get(ctx, n.ID)
	if err != nil {
		return "", err
	}
	if !fresh.Directory || fresh.ParentID != n.ParentID || fresh.Name != n.Name {
		return "", ErrChanged
	}
	return n.ID, nil
}
func (m *Manager) available(ctx context.Context, r *runtimePair, b Backend, parent, name string, except string) error {
	children, err := b.List(ctx, parent)
	if err != nil {
		return err
	}
	for _, n := range children {
		if n.Name == name && n.ID != except {
			return ErrChanged
		}
	}
	return nil
}
func (m *Manager) stagePath(r *runtimePair, o *operation) (string, error) {
	stage, err := syncfs.EnsureStage(r.record.Identity, r.record.Pair.ID)
	if err != nil {
		return "", err
	}
	return filepath.Join(stage, o.ID+".part"), nil
}
func (m *Manager) execute(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	if _, _, err := m.roots(ctx, r.record.Pair.Config, r.record.Identity, r.record.Owner, new(r.record.Root)); err != nil {
		return err
	}
	if o.Action == "keep-both" {
		return m.executeKeep(ctx, r, b, o)
	}
	if o.Stage == "planned" {
		if err := m.preflight(ctx, r, b, o); err != nil {
			return err
		}
	}
	var err error
	switch o.Action {
	case "upload":
		err = m.executeUpload(ctx, r, b, o)
	case "download":
		err = m.executeDownload(ctx, r, b, o)
	case "mkdir":
		err = m.executeMkdir(ctx, r, b, o)
	case "rename":
		err = m.executeRename(ctx, r, b, o)
	case "trash":
		err = m.executeTrash(ctx, r, b, o)
	default:
		err = ErrRepair
	}
	if err != nil {
		return err
	}
	return m.commit(ctx, r, b, o)
}
func (m *Manager) recover(ctx context.Context, r *runtimePair, b Backend) error {
	if err := m.validatePending(ctx, r, b); err != nil {
		return err
	}
	for len(r.record.Operations) > 0 {
		o := r.record.Operations[0]
		if err := m.execute(ctx, r, b, o); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) uploadStage(ctx context.Context, r *runtimePair, o *operation, source string) (string, error) {
	dir, err := syncfs.EnsureStage(r.record.Identity, r.record.Pair.ID)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, o.ID+".upload")
	if info, err := os.Lstat(dest); err == nil {
		if !info.Mode().IsRegular() {
			return "", ErrUnknown
		}
		sum, err := syncfs.Hash(ctx, dest)
		if err != nil {
			return "", err
		}
		if sum != o.Local.Hash {
			return "", ErrUnknown
		}
		return dest, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if o.Stage != "upload-ready" {
		return "", ErrUnknown
	}
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	before, err := in.Stat()
	if err != nil {
		return "", err
	}
	fresh, err := os.Lstat(source)
	if err != nil || !os.SameFile(before, fresh) || !before.Mode().IsRegular() {
		return "", ErrChanged
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	sum, err := syncfs.Hash(ctx, dest)
	if err != nil {
		return "", err
	}
	if sum != o.Local.Hash {
		return "", ErrChanged
	}
	return dest, nil
}
func (m *Manager) executeUpload(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	parent, err := m.parent(ctx, r, b, o.Path)
	if err != nil {
		return err
	}
	abs, err := syncfs.ValidateRelative(r.record.Identity, o.Path)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if o.Temp == "" {
		o.Temp = ".pkp-sync-" + o.ID
	}
	m.mu.Unlock()
	if o.Stage == "planned" {
		if err = m.available(ctx, r, b, parent, o.Temp, ""); err != nil {
			return err
		}
		if err = m.journal(r, o, "upload-ready"); err != nil {
			return err
		}
	}
	if o.Stage == "upload-intent" && o.New == nil && o.FileID == "" {
		children, e := b.List(ctx, parent)
		if e != nil {
			return e
		}
		var found *Node
		for _, n := range children {
			if n.Name == o.Temp {
				if found != nil {
					return ErrUnknown
				}
				found = new(n)
			}
		}
		if found == nil {
			return ErrUnknown
		}
		if !complete(*found) {
			return ErrUnknown
		}
		found.Path = o.Path
		found.Hash, e = b.Hash(ctx, *found)
		if e != nil {
			return e
		}
		if !same(o.Local, found) {
			return ErrUnknown
		}
		m.mu.Lock()
		o.New = found
		m.mu.Unlock()
		if err = m.journal(r, o, "uploaded"); err != nil {
			return err
		}
	}
	if o.Stage == "upload-ready" || o.Stage == "upload-intent" && o.FileID != "" {
		current, e := m.local(ctx, r, o.Path)
		if e != nil {
			return e
		}
		if !same(current, o.Local) {
			return ErrChanged
		}
		abs, e = m.uploadStage(ctx, r, o, abs)
		if e != nil {
			return e
		}
		if err = m.journal(r, o, "upload-intent"); err != nil {
			return err
		}
		n, e := b.Upload(ctx, UploadRequest{ID: o.ID, Path: abs, ParentID: parent, Name: o.Temp, Checkpoint: filepath.Join(m.opts.Dir, r.record.Pair.ID, "checkpoints", o.ID), FileID: o.FileID, Grant: o.Grant, SaveSlot: func(id string, grant json.RawMessage) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			o.FileID = id
			o.Grant = append(o.Grant[:0], grant...)
			return m.saveLocked()
		}}, m.progress(r, o))
		if e != nil {
			return e
		}
		n.Path = o.Path
		if !complete(n) {
			return ErrUnknown
		}
		n.Hash, e = b.Hash(ctx, n)
		if e != nil {
			return e
		}
		if !same(o.Local, new(n)) {
			return ErrChanged
		}
		m.mu.Lock()
		o.New = new(n)
		m.mu.Unlock()
		if err = m.journal(r, o, "uploaded"); err != nil {
			return err
		}
	}
	if o.New == nil {
		return ErrUnknown
	}
	if o.Stage == "uploaded" {
		current, e := m.local(ctx, r, o.Path)
		if e != nil {
			return e
		}
		if !same(current, o.Local) {
			return ErrChanged
		}
		if o.Cloud != nil {
			fresh, e := freshCloud(ctx, b, o.Cloud)
			if e != nil {
				return e
			}
			if !cloudProof(o.Cloud, fresh) {
				return ErrChanged
			}
			if err = m.journal(r, o, "old-rename-intent"); err != nil {
				return err
			}
		} else {
			if err = m.journal(r, o, "new-rename-intent"); err != nil {
				return err
			}
		}
	}
	if o.Stage == "old-rename-intent" {
		fresh, e := freshCloud(ctx, b, o.Cloud)
		if e != nil {
			return e
		}
		if fresh == nil || !same(fresh, o.Cloud) {
			return ErrChanged
		}
		oldName := ".pkp-sync-old-" + o.ID
		if fresh.Name == o.Cloud.Name {
			if !cloudProof(o.Cloud, fresh) {
				return ErrChanged
			}
			n, e := b.Rename(ctx, *fresh, oldName, o.ID+"-old")
			if e != nil {
				return e
			}
			n.Path = o.Path
			n.Hash = fresh.Hash
			fresh = new(n)
		} else if fresh.Name != oldName {
			return ErrUnknown
		}
		m.mu.Lock()
		o.Cloud = fresh
		m.mu.Unlock()
		if err = m.journal(r, o, "new-rename-intent"); err != nil {
			return err
		}
	}
	if o.Stage == "new-rename-intent" {
		fresh, e := freshCloud(ctx, b, o.New)
		if e != nil {
			return e
		}
		if fresh == nil || !same(fresh, o.Local) {
			return ErrUnknown
		}
		if fresh.Name == o.Temp {
			if err = m.available(ctx, r, b, parent, path.Base(o.Path), fresh.ID); err != nil {
				return err
			}
			n, e := b.Rename(ctx, *fresh, path.Base(o.Path), o.ID+"-final")
			if e != nil {
				return e
			}
			n.Path = o.Path
			n.Hash = fresh.Hash
			fresh = new(n)
		} else if fresh.Name != path.Base(o.Path) {
			return ErrUnknown
		}
		m.mu.Lock()
		o.New = fresh
		m.mu.Unlock()
		if err = m.journal(r, o, "final-named"); err != nil {
			return err
		}
	}
	if o.Stage == "final-named" {
		if o.Cloud != nil {
			if err = m.journal(r, o, "old-trash-intent"); err != nil {
				return err
			}
		} else {
			return m.journal(r, o, "done")
		}
	}
	if o.Stage == "old-trash-intent" {
		final, e := freshCloud(ctx, b, o.New)
		if e != nil {
			return e
		}
		if !cloudProof(o.New, final) {
			return ErrChanged
		}
		fresh, e := freshCloud(ctx, b, o.Cloud)
		if e != nil {
			return e
		}
		if fresh != nil {
			if !cloudProof(o.Cloud, fresh) {
				return ErrChanged
			}
			if e = b.Trash(ctx, *fresh, o.ID+"-old-trash"); e != nil {
				return e
			}
		} else {
			trashed, e := b.Trashed(ctx, o.Cloud.ID)
			if e != nil {
				return e
			}
			if !trashed {
				return ErrUnknown
			}
		}
		if err = m.journal(r, o, "done"); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) executeDownload(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	stage, err := m.stagePath(r, o)
	if err != nil {
		return err
	}
	dest, err := syncfs.ValidateRelative(r.record.Identity, o.Path)
	if err != nil {
		return err
	}
	if o.Stage == "planned" {
		if err = m.journal(r, o, "download-intent"); err != nil {
			return err
		}
	}
	if o.Stage == "download-intent" {
		fresh, e := freshCloud(ctx, b, o.Cloud)
		if e != nil {
			return e
		}
		if !cloudProof(o.Cloud, fresh) {
			return ErrChanged
		}
		if _, e = os.Lstat(stage); errors.Is(e, os.ErrNotExist) {
			if e = b.Download(ctx, *fresh, stage, filepath.Join(m.opts.Dir, r.record.Pair.ID, "checkpoints", o.ID), o.ID, m.progress(r, o)); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		h, e := syncfs.Hash(ctx, stage)
		if e != nil {
			return e
		}
		if h != o.Cloud.Hash {
			return ErrChanged
		}
		if err = m.journal(r, o, "downloaded"); err != nil {
			return err
		}
	}
	if o.Stage == "downloaded" {
		current, e := m.local(ctx, r, o.Path)
		if e != nil {
			return e
		}
		if !same(current, o.Local) {
			return ErrChanged
		}
		fresh, e := freshCloud(ctx, b, o.Cloud)
		if e != nil {
			return e
		}
		if !cloudProof(o.Cloud, fresh) {
			return ErrChanged
		}
		if o.Local != nil {
			if err = m.journal(r, o, "local-trash-intent"); err != nil {
				return err
			}
		} else {
			if err = m.journal(r, o, "local-install-intent"); err != nil {
				return err
			}
		}
	}
	if o.Stage == "local-trash-intent" {
		if err = syncfs.Validate(r.record.Identity); err != nil {
			return err
		}
		current, e := m.local(ctx, r, o.Path)
		if e != nil {
			return e
		}
		if current == nil {
			return ErrUnknown
		}
		if !same(current, o.Local) {
			return ErrChanged
		}
		if e = syncfs.Trash(dest); e != nil {
			return e
		}
		if err = m.journal(r, o, "local-install-intent"); err != nil {
			return err
		}
	}
	if o.Stage == "local-install-intent" {
		if err = syncfs.Validate(r.record.Identity); err != nil {
			return err
		}
		fresh, e := freshCloud(ctx, b, o.Cloud)
		if e != nil {
			return e
		}
		if !cloudProof(o.Cloud, fresh) {
			return ErrChanged
		}
		current, e := m.local(ctx, r, o.Path)
		if e != nil {
			return e
		}
		if current != nil {
			if !same(current, o.Cloud) {
				return ErrChanged
			}
			if _, e = os.Lstat(stage); !errors.Is(e, os.ErrNotExist) {
				return ErrUnknown
			}
		} else {
			if err = syncfs.RenameNoReplace(stage, dest); err != nil {
				return err
			}
		}
		return m.journal(r, o, "done")
	}
	return nil
}

func (m *Manager) executeMkdir(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	if o.Direction == "download" {
		abs, err := syncfs.ValidateRelative(r.record.Identity, o.Path)
		if err != nil {
			return err
		}
		if o.Stage == "planned" {
			if err = m.journal(r, o, "mkdir-intent"); err != nil {
				return err
			}
		}
		st, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			if err = os.Mkdir(abs, 0755); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return ErrChanged
		}
		return m.journal(r, o, "done")
	}
	parent, err := m.parent(ctx, r, b, o.Path)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if o.Temp == "" {
		o.Temp = ".pkp-sync-" + o.ID
	}
	m.mu.Unlock()
	if o.Stage == "planned" {
		if err = m.available(ctx, r, b, parent, o.Temp, ""); err != nil {
			return err
		}
		if err = m.journal(r, o, "mkdir-ready"); err != nil {
			return err
		}
	}
	if o.Stage == "mkdir-intent" && o.New == nil {
		children, e := b.List(ctx, parent)
		if e != nil {
			return e
		}
		var found *Node
		for _, n := range children {
			if n.Name == o.Temp && n.Directory {
				if found != nil {
					return ErrUnknown
				}
				found = new(n)
			}
		}
		if found == nil {
			return ErrUnknown
		}
		m.mu.Lock()
		o.New = found
		m.mu.Unlock()
		if err = m.journal(r, o, "mkdir-created"); err != nil {
			return err
		}
	}
	if o.Stage == "mkdir-ready" {
		if err = m.journal(r, o, "mkdir-intent"); err != nil {
			return err
		}
		n, e := b.Mkdir(ctx, parent, o.Temp, o.ID)
		if e != nil {
			return e
		}
		m.mu.Lock()
		o.New = new(n)
		m.mu.Unlock()
		if err = m.journal(r, o, "mkdir-created"); err != nil {
			return err
		}
	}
	if o.Stage == "mkdir-created" {
		if err = m.journal(r, o, "mkdir-name-intent"); err != nil {
			return err
		}
	}
	if o.Stage == "mkdir-name-intent" {
		n, e := b.Get(ctx, o.New.ID)
		if e != nil {
			return e
		}
		if n.Name == o.Temp {
			n, e = b.Rename(ctx, n, path.Base(o.Path), o.ID+"-name")
			if e != nil {
				return e
			}
		} else if n.Name != path.Base(o.Path) {
			return ErrUnknown
		}
		n.Path = o.Path
		m.mu.Lock()
		o.New = new(n)
		m.mu.Unlock()
		return m.journal(r, o, "done")
	}
	return nil
}
func (m *Manager) executeRename(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	if o.Stage == "planned" {
		if err := m.journal(r, o, "rename-intent"); err != nil {
			return err
		}
	}
	if o.Direction == "upload" {
		parent, err := m.parent(ctx, r, b, o.Path)
		if err != nil {
			return err
		}
		n, err := b.Get(ctx, o.Cloud.ID)
		if err != nil {
			return err
		}
		if n.ParentID != parent || n.Name != path.Base(o.Path) {
			fresh, err := freshCloud(ctx, b, o.Cloud)
			if err != nil {
				return err
			}
			if !cloudProof(o.Cloud, fresh) {
				return ErrChanged
			}
			n, err = b.Move(ctx, n, parent, path.Base(o.Path), o.ID)
			if err != nil {
				return err
			}
		}
		n.Path = o.Path
		m.mu.Lock()
		o.New = new(n)
		m.mu.Unlock()
	} else {
		from, err := syncfs.ValidateRelative(r.record.Identity, o.From)
		if err != nil {
			return err
		}
		to, err := syncfs.ValidateRelative(r.record.Identity, o.Path)
		if err != nil {
			return err
		}
		caseOnly := collisionPath(o.From, r.record.Identity) == collisionPath(o.Path, r.record.Identity) && o.From != o.Path
		if caseOnly {
			stage, err := syncfs.EnsureStage(r.record.Identity, r.record.Pair.ID)
			if err != nil {
				return err
			}
			temp := filepath.Join(stage, o.ID+".rename")
			if o.Stage == "rename-intent" {
				if _, err = os.Lstat(temp); errors.Is(err, os.ErrNotExist) {
					if err = syncfs.RenameNoReplace(from, temp); err != nil {
						return err
					}
				} else if err != nil {
					return err
				}
				if err = m.journal(r, o, "rename-staged"); err != nil {
					return err
				}
			}
			if o.Stage == "rename-staged" {
				if err = m.journal(r, o, "rename-final-intent"); err != nil {
					return err
				}
			}
			if o.Stage == "rename-final-intent" {
				if _, err = os.Lstat(temp); err == nil {
					if err = syncfs.RenameNoReplace(temp, to); err != nil {
						return err
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				} else {
					n, e := m.local(ctx, r, o.Path)
					if e != nil {
						return e
					}
					if !same(n, o.Local) {
						return ErrUnknown
					}
				}
			}
		} else {
			_, err = os.Lstat(from)
			if errors.Is(err, os.ErrNotExist) {
				n, e := m.local(ctx, r, o.Path)
				if e != nil {
					return e
				}
				if !same(n, o.Local) {
					return ErrUnknown
				}
			} else if err != nil {
				return err
			} else {
				current, e := m.local(ctx, r, o.From)
				if e != nil {
					return e
				}
				if !same(current, o.Local) {
					return ErrChanged
				}
				if err = syncfs.RenameNoReplace(from, to); err != nil {
					return err
				}
			}
		}
	}
	return m.journal(r, o, "done")
}
func (m *Manager) executeTrash(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	if o.Stage == "planned" {
		if err := m.journal(r, o, "trash-intent"); err != nil {
			return err
		}
	}
	if o.Direction == "upload" {
		fresh, err := freshCloud(ctx, b, o.Cloud)
		if err != nil {
			return err
		}
		if fresh != nil {
			if !cloudProof(o.Cloud, fresh) {
				return ErrChanged
			}
			if fresh.Directory {
				children, e := b.List(ctx, fresh.ID)
				if e != nil {
					return e
				}
				if len(children) > 0 {
					return ErrChanged
				}
			}
			if err = b.Trash(ctx, *fresh, o.ID); err != nil {
				return err
			}
		} else {
			trashed, err := b.Trashed(ctx, o.Cloud.ID)
			if err != nil {
				return err
			}
			if !trashed {
				return ErrUnknown
			}
		}
	} else {
		current, err := m.local(ctx, r, o.Path)
		if err != nil {
			return err
		}
		if current == nil {
			return ErrUnknown
		}
		if !same(current, o.Local) {
			return ErrChanged
		}
		abs, err := syncfs.ValidateRelative(r.record.Identity, o.Path)
		if err != nil {
			return err
		}
		if current.Directory {
			entries, e := os.ReadDir(abs)
			if e != nil {
				return e
			}
			if len(entries) > 0 {
				return ErrChanged
			}
		}
		if err = syncfs.Trash(abs); err != nil {
			return err
		}
	}
	return m.journal(r, o, "done")
}
func (m *Manager) commit(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	if o.Stage != "done" {
		return ErrUnknown
	}
	if o.Committed {
		return nil
	}
	// Only affected paths advance. Failed/unrelated entries retain their old evidence.
	paths := []string{o.Path}
	if o.Action == "rename" {
		m.mu.Lock()
		moved := map[string]baseline{}
		for p, base := range r.record.Baseline {
			if under(p, o.From) {
				delete(r.record.Baseline, p)
				delete(r.record.Snapshot.Local, p)
				delete(r.record.Snapshot.Cloud, p)
				newPath := o.Path + strings.TrimPrefix(p, o.From)
				if base.Local != nil {
					base.Local = new(*base.Local)
					base.Local.Path = newPath
					base.Local.Name = path.Base(newPath)
					r.record.Snapshot.Local[newPath] = *base.Local
				}
				if base.Cloud != nil {
					base.Cloud = new(*base.Cloud)
					base.Cloud.Path = newPath
					base.Cloud.Name = path.Base(newPath)
					r.record.Snapshot.Cloud[newPath] = *base.Cloud
				}
				moved[newPath] = base
				paths = append(paths, newPath)
			}
		}
		for rel, base := range moved {
			r.record.Baseline[rel] = base
		}
		m.mu.Unlock()
	}
	for _, rel := range paths {
		l, err := m.local(ctx, r, rel)
		if err != nil {
			return err
		}
		var c *Node
		if o.Action != "trash" {
			parent, err := m.parent(ctx, r, b, rel)
			if err != nil {
				return err
			}
			children, err := b.List(ctx, parent)
			if err != nil {
				return err
			}
			for _, n := range children {
				if n.Name == path.Base(rel) {
					if c != nil {
						return ErrChanged
					}
					n.Path = rel
					if !n.Directory {
						n.Hash, err = b.Hash(ctx, n)
						if err != nil {
							return err
						}
					}
					c = new(n)
				}
			}
		}
		if o.Action != "trash" && !same(l, c) {
			return ErrChanged
		}
		m.mu.Lock()
		if l == nil && c == nil {
			delete(r.record.Baseline, rel)
			delete(r.record.Snapshot.Local, rel)
			delete(r.record.Snapshot.Cloud, rel)
		} else {
			r.record.Baseline[rel] = baseline{Local: l, Cloud: c}
			if l != nil {
				r.record.Snapshot.Local[rel] = *l
			}
			if c != nil {
				r.record.Snapshot.Cloud[rel] = *c
			}
		}
		m.mu.Unlock()
	}
	m.mu.Lock()
	o.Committed = true
	r.record.Pair.Activities = append(r.record.Pair.Activities, Activity{ID: o.ID, At: stamp(), Path: o.Path, Direction: o.Direction, Action: o.Action, Result: "complete"})
	if len(r.record.Pair.Activities) > 1000 {
		r.record.Pair.Activities = r.record.Pair.Activities[len(r.record.Pair.Activities)-1000:]
	}
	for i, op := range r.record.Operations {
		if op == o {
			r.record.Operations = append(r.record.Operations[:i], r.record.Operations[i+1:]...)
			break
		}
	}
	r.record.Pair.Progress.Completed++
	err := m.saveBaselineLocked(r)
	m.mu.Unlock()
	if err == nil {
		m.cleanupOperation(r, o)
	}
	return err
}

func (m *Manager) KeepBoth(ctx context.Context, pairID, issueID string) error {
	m.mu.Lock()
	r := m.pairs[pairID]
	if r == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	var issue *Issue
	for _, i := range r.record.Pair.Issues {
		if i.ID == issueID {
			issue = new(i)
		}
	}
	m.mu.Unlock()
	if issue == nil || issue.KeepName == "" {
		return ErrNotFound
	}
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	b, _, err := m.roots(ctx, r.record.Pair.Config, r.record.Identity, r.record.Owner, new(r.record.Root))
	if err != nil {
		return err
	}
	o := &operation{ID: issue.ID, Action: "keep-both", Path: issue.Path, From: issue.KeepName, Local: issue.Local, Cloud: issue.Cloud, Stage: "planned", ConfigVersion: configProof(r.record.Pair.Config)}
	if err = m.preflight(ctx, r, b, o); err != nil {
		return err
	}
	m.mu.Lock()
	for _, existing := range r.record.Operations {
		if existing.ID == o.ID {
			m.mu.Unlock()
			return ErrUnknown
		}
	}
	r.record.Operations = append(r.record.Operations, o)
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if err = m.executeKeep(ctx, r, b, o); err != nil {
		return err
	}
	m.signal(r)
	m.notify()
	return nil
}

func (m *Manager) executeKeep(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	mode := r.record.Pair.Mode
	keep := path.Join(path.Dir(o.Path), o.From)
	if o.Local != nil && o.Local.Directory || o.Cloud != nil && o.Cloud.Directory {
		return m.keepDirectory(ctx, r, b, o)
	}
	if o.Local == nil || o.Cloud == nil {
		if mode == Backup && o.Local == nil || mode == Download && o.Cloud == nil {
			return m.preserveTargetOnly(ctx, r, b, o)
		} else {
			direction := "upload"
			if o.Local == nil {
				direction = "download"
			}
			child := &operation{ID: o.ID + "-restore", Action: direction, Direction: direction, Path: o.Path, Local: o.Local, Cloud: o.Cloud, Stage: "planned", ConfigVersion: o.ConfigVersion}
			if err := m.child(ctx, r, b, o, child); err != nil {
				return err
			}
			if err := m.journal(r, o, "keep-done"); err != nil {
				return err
			}
		}
	}
	if o.Stage == "planned" {
		if err := m.preflight(ctx, r, b, o); err != nil {
			return err
		}
		if err := m.journal(r, o, "keep-preserve"); err != nil {
			return err
		}
	}
	if o.Stage == "keep-preserve" {
		if mode == Download {
			if o.Local != nil {
				from, err := syncfs.ValidateRelative(r.record.Identity, o.Path)
				if err != nil {
					return err
				}
				to, err := syncfs.ValidateRelative(r.record.Identity, keep)
				if err != nil {
					return err
				}
				if _, err = os.Lstat(from); err == nil {
					if err = syncfs.RenameNoReplace(from, to); err != nil {
						return err
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				} else {
					n, e := m.local(ctx, r, keep)
					if e != nil {
						return e
					}
					if !same(n, o.Local) {
						return ErrUnknown
					}
				}
			}
		} else if o.Cloud != nil {
			n, err := b.Get(ctx, o.Cloud.ID)
			if err != nil {
				return err
			}
			if n.Name != o.From {
				fresh, err := freshCloud(ctx, b, o.Cloud)
				if err != nil {
					return err
				}
				if !cloudProof(o.Cloud, fresh) {
					return ErrChanged
				}
				n, err = b.Rename(ctx, *fresh, o.From, o.ID+"-keep")
				if err != nil {
					return err
				}
			}
			n.Path = keep
			n.Hash = o.Cloud.Hash
			m.mu.Lock()
			o.New = new(n)
			m.mu.Unlock()
		}
		m.recordTargetKeep(r, o, keep)
		if err := m.journal(r, o, "keep-source"); err != nil {
			return err
		}
	}
	if o.Stage == "keep-source" {
		if mode == Download {
			if o.Cloud != nil {
				child := &operation{ID: o.ID + "-source", Action: "download", Direction: "download", Path: o.Path, Cloud: o.Cloud, Stage: "planned", ConfigVersion: o.ConfigVersion}
				if err := m.child(ctx, r, b, o, child); err != nil {
					return err
				}
			}
		} else if o.Local != nil {
			child := &operation{ID: o.ID + "-source", Action: "upload", Direction: "upload", Path: o.Path, Local: o.Local, Stage: "planned", ConfigVersion: o.ConfigVersion}
			if err := m.child(ctx, r, b, o, child); err != nil {
				return err
			}
		}
		if err := m.journal(r, o, "keep-copy"); err != nil {
			return err
		}
	}
	if o.Stage == "keep-copy" {
		if mode == Bidirectional && o.New != nil {
			cloud := *o.New
			cloud.Path = keep
			child := &operation{ID: o.ID + "-copy", Action: "download", Direction: "download", Path: keep, Cloud: new(cloud), Stage: "planned", ConfigVersion: o.ConfigVersion}
			if err := m.child(ctx, r, b, o, child); err != nil {
				return err
			}
		}
		if err := m.journal(r, o, "keep-done"); err != nil {
			return err
		}
	}
	if o.Stage == "keep-done" {
		return m.finishKeep(r, o)
	}
	return nil
}

// Nested operations are journaled before dispatch and retained within the parent
// so a crash cannot create a second conflict copy with a different identity.
func (m *Manager) child(ctx context.Context, r *runtimePair, b Backend, parent, child *operation) error {
	m.mu.Lock()
	for _, saved := range parent.Children {
		if saved.ID == child.ID {
			child = saved
			break
		}
	}
	found := false
	for _, saved := range parent.Children {
		if saved == child {
			found = true
		}
	}
	if !found {
		parent.Children = append(parent.Children, child)
	}
	err := m.saveLocked()
	committed := child.Committed
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if committed {
		return nil
	}
	return m.execute(ctx, r, b, child)
}
