package foldersync

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/tonyflux/pkp/internal/syncfs"
)

func (m *Manager) settleRemoval(ctx context.Context, r *runtimePair) error {
	// Planned steps have not touched either side. Only accepted steps need
	// recovery; never start the rest of a cached synchronization plan here.
	m.mu.Lock()
	pending := append([]*operation(nil), r.record.Operations...)
	m.mu.Unlock()
	var backend Backend
	for _, op := range pending {
		if op.Stage == "planned" && len(op.Children) == 0 {
			continue
		}
		if backend == nil {
			var err error
			backend, _, err = m.roots(ctx, r.record.Pair.Config, r.record.Identity, r.record.Owner, new(r.record.Root))
			if err != nil {
				return err
			}
		}
		if err := m.execute(ctx, r, backend, op); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) releaseStage(ctx context.Context, r *runtimePair) error {
	stagePath := filepath.Join(r.record.Identity.Path, ".pkp-sync")
	if _, err := os.Lstat(stagePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	stage, err := syncfs.EnsureStage(r.record.Identity, r.record.Pair.ID)
	if err != nil {
		return err
	}
	var clean func(*operation) error
	clean = func(o *operation) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if o.Action == "rename" && o.Direction == "download" {
			temp := filepath.Join(stage, o.ID+".rename")
			if _, err := os.Lstat(temp); err == nil {
				original, err := syncfs.ValidateRelative(r.record.Identity, o.From)
				if err != nil {
					return err
				}
				if err = syncfs.RenameNoReplace(temp, original); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		for _, suffix := range []string{".upload", ".part", ".part.pkpdownload"} {
			p := filepath.Join(stage, o.ID+suffix)
			if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() {
				if err = os.Remove(p); err != nil {
					return err
				}
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		for _, child := range o.Children {
			if err := clean(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, o := range r.record.Operations {
		if err = clean(o); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	if len(entries) == 1 && entries[0].Name() == "owner.json" {
		if err = os.Remove(filepath.Join(stage, "owner.json")); err != nil {
			return err
		}
		return os.Remove(stage)
	}
	// Unknown contents are never swept, even in an owned staging directory.
	return nil
}
