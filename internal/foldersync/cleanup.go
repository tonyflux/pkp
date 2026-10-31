package foldersync

import (
	"os"
	"path/filepath"

	"github.com/tonyflux/pkp/internal/syncfs"
)

func (m *Manager) cleanupOperation(r *runtimePair, o *operation) {
	if !o.Committed {
		return
	}
	stage, err := syncfs.EnsureStage(r.record.Identity, r.record.Pair.ID)
	if err != nil {
		return
	}
	// Only exact committed operation names are ours; no glob or user-tree cleanup.
	for _, suffix := range []string{".upload", ".part", ".part.pkpdownload"} {
		p := filepath.Join(stage, o.ID+suffix)
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			_ = os.Remove(p)
		}
	}
	_ = os.RemoveAll(filepath.Join(m.opts.Dir, r.record.Pair.ID, "checkpoints", o.ID))
	for _, child := range o.Children {
		m.cleanupOperation(r, child)
	}
}
