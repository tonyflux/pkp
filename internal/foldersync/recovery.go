package foldersync

import (
	"context"
)

// Validate the complete pending deletion batch before resuming any of it. Planned
// requests have no side effect yet and can be discarded when their evidence changes.
func (m *Manager) validatePending(ctx context.Context, r *runtimePair, b Backend) error {
	deletes := false
	for _, o := range r.record.Operations {
		if o.Action == "trash" {
			deletes = true
		}
	}
	if !deletes {
		return nil
	}
	s, _, err := m.collect(ctx, r.record.Pair.Config, r.record.Identity, r.record.Owner, new(r.record.Root), r.record.Snapshot)
	if err != nil {
		return err
	}
	for _, o := range r.record.Operations {
		if o.Action != "trash" || o.Stage != "planned" {
			continue
		}
		if !same(nodeAt(s.Local, o.Path), o.Local) || !cloudProof(nodeAt(s.Cloud, o.Path), o.Cloud) {
			m.mu.Lock()
			kept := r.record.Operations[:0]
			for _, pending := range r.record.Operations {
				if pending.Stage != "planned" {
					kept = append(kept, pending)
				}
			}
			r.record.Operations = kept
			r.record.Approved = ""
			err = m.saveLocked()
			m.mu.Unlock()
			if err != nil {
				return err
			}
			return ErrChanged
		}
	}
	return nil
}
