package foldersync

import (
	"context"
	"path"
	"sort"
	"strings"
)

func (m *Manager) finishKeep(r *runtimePair, o *operation) error {
	m.mu.Lock()
	o.Committed = true
	for i, op := range r.record.Operations {
		if op == o {
			r.record.Operations = append(r.record.Operations[:i], r.record.Operations[i+1:]...)
			break
		}
	}
	issues := r.record.Pair.Issues[:0]
	for _, i := range r.record.Pair.Issues {
		if i.ID != o.ID {
			issues = append(issues, i)
		}
	}
	r.record.Pair.Issues = issues
	r.record.Pair.Activities = append(r.record.Pair.Activities, Activity{ID: o.ID, At: stamp(), Path: o.Path, Action: "keep", Result: "complete"})
	err := m.saveBaselineLocked(r)
	m.mu.Unlock()
	if err == nil {
		m.cleanupOperation(r, o)
	}
	return err
}

func (m *Manager) keepDirectory(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	if o.Local != nil && o.Cloud != nil {
		return ErrChanged
	}
	mode := r.record.Pair.Mode
	if mode == Backup && o.Local == nil || mode == Download && o.Cloud == nil {
		m.mu.Lock()
		preserved := "local"
		if mode == Download {
			preserved = "cloud"
		}
		for rel := range r.record.Baseline {
			if under(rel, o.Path) {
				r.record.Baseline[rel] = baseline{Local: nodeAt(r.record.Snapshot.Local, rel), Cloud: nodeAt(r.record.Snapshot.Cloud, rel), Preserved: preserved}
			}
		}
		m.mu.Unlock()
		return m.finishKeep(r, o)
	}
	if len(o.Children) == 0 {
		s, _, err := m.collect(ctx, r.record.Pair.Config, r.record.Identity, r.record.Owner, new(r.record.Root), r.record.Snapshot)
		if err != nil {
			return err
		}
		nodes := s.Local
		direction := "upload"
		if o.Local == nil {
			nodes = s.Cloud
			direction = "download"
		}
		keys := []string{}
		for rel := range nodes {
			if under(rel, o.Path) {
				keys = append(keys, rel)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			di, dj := strings.Count(keys[i], "/"), strings.Count(keys[j], "/")
			if di == dj {
				return keys[i] < keys[j]
			}
			return di < dj
		})
		m.mu.Lock()
		for _, rel := range keys {
			n := nodes[rel]
			action := direction
			if n.Directory {
				action = "mkdir"
			}
			child := &operation{ID: digest([]string{o.ID, rel})[:32], Action: action, Direction: direction, Path: rel, Local: nodeAt(s.Local, rel), Cloud: nodeAt(s.Cloud, rel), Stage: "planned", ConfigVersion: o.ConfigVersion}
			o.Children = append(o.Children, child)
		}
		o.Stage = "keep-directory"
		err = m.saveLocked()
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	for _, child := range o.Children {
		if child.Committed {
			continue
		}
		if err := m.execute(ctx, r, b, child); err != nil {
			return err
		}
	}
	return m.finishKeep(r, o)
}

func (m *Manager) preserveTargetOnly(ctx context.Context, r *runtimePair, b Backend, o *operation) error {
	// A source deletion plus a target edit is a decision to retain that target,
	// not permission to write a conflict copy into the one-way source.
	if err := m.preflight(ctx, r, b, o); err != nil {
		return err
	}
	preserved := "local"
	if r.record.Pair.Mode == Download {
		preserved = "cloud"
	}
	m.mu.Lock()
	r.record.Baseline[o.Path] = baseline{Local: o.Local, Cloud: o.Cloud, Preserved: preserved}
	m.mu.Unlock()
	return m.finishKeep(r, o)
}

func (m *Manager) recordTargetKeep(r *runtimePair, o *operation, rel string) {
	if o.New == nil {
		return
	}
	n := *o.New
	n.Path = rel
	n.Name = path.Base(rel)
	m.mu.Lock()
	r.record.Snapshot.Cloud[rel] = n
	m.mu.Unlock()
}
