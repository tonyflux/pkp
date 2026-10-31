package foldersync

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/syncfs"
)

func (m *Manager) backend(c Config, owner string) (Backend, error) {
	b, err := m.opts.Backend(c.Account)
	if err != nil {
		return nil, err
	}
	if b == nil || b.AccountID() == "" {
		return nil, ErrAuth
	}
	if owner != "" && b.AccountID() != owner {
		return nil, ErrRepair
	}
	return b, nil
}

func (m *Manager) roots(ctx context.Context, c Config, id syncfs.Identity, owner string, oldRoot *Node) (Backend, Node, error) {
	if err := syncfs.Validate(id); err != nil {
		return nil, Node{}, err
	}
	b, err := m.backend(c, owner)
	if err != nil {
		return nil, Node{}, err
	}
	r, err := b.Root(ctx, c.CloudID)
	if err != nil {
		return nil, Node{}, err
	}
	if !r.Directory || r.ID != c.CloudID {
		return nil, Node{}, ErrRepair
	}
	if oldRoot != nil && (oldRoot.ParentID != r.ParentID || !equalStrings(oldRoot.Ancestors, r.Ancestors)) {
		return nil, Node{}, ErrRepair
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, other := range m.pairs {
		if key == c.ID {
			continue
		}
		o := other.record
		if syncfs.Overlaps(id, o.Identity) {
			return nil, Node{}, i18n.Errorf("本地同步目录与已有关系重叠：%s", o.Pair.Name+" · "+o.Pair.LocalPath)
		}
		if c.Account == o.Pair.Account && cloudOverlap(r, o.Root) {
			return nil, Node{}, i18n.Errorf("云端同步目录与已有关系重叠：%s", o.Pair.Name+" · "+o.Pair.CloudPath)
		}
	}
	return b, r, nil
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func cloudOverlap(a, b Node) bool {
	if a.ID == b.ID {
		return true
	}
	for _, x := range a.Ancestors {
		if x == b.ID {
			return true
		}
	}
	for _, x := range b.Ancestors {
		if x == a.ID {
			return true
		}
	}
	return false
}

func localCache(s *snapshot) map[string]syncfs.Entry {
	out := map[string]syncfs.Entry{}
	if s == nil {
		return out
	}
	for p, n := range s.Local {
		out[p] = syncfs.Entry{Path: p, Name: n.Name, Directory: n.Directory, Size: n.Size, ModTime: n.Modified, FileID: n.ID, Hash: n.Hash, Version: n.Version}
	}
	return out
}
func localNodes(entries []syncfs.Entry) map[string]Node {
	out := make(map[string]Node, len(entries))
	for _, e := range entries {
		out[e.Path] = Node{Path: e.Path, Name: e.Name, ID: e.FileID, Hash: e.Hash, Modified: e.ModTime, Version: e.Version, Directory: e.Directory, Size: e.Size}
	}
	return out
}

func (m *Manager) collect(ctx context.Context, c Config, id syncfs.Identity, owner string, oldRoot *Node, previous *snapshot) (*snapshot, Backend, error) {
	b, root, err := m.roots(ctx, c, id, owner, oldRoot)
	if err != nil {
		return nil, nil, err
	}
	entries, skipped, err := syncfs.Scan(ctx, id, localCache(previous))
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Lstat(filepath.Join(id.Path, ".pkp-sync")); err == nil {
		if _, err = syncfs.EnsureStage(id, c.ID); err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	s := &snapshot{ID: uid(), Root: root, Local: localNodes(entries), Cloud: map[string]Node{}, Skipped: skipped, At: stamp()}
	listings := map[string][]Node{}
	seen := map[string]bool{}
	var walk func(string, string) error
	walk = func(parent, rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if seen[parent] {
			return ErrRepair
		}
		seen[parent] = true
		children, err := b.List(ctx, parent)
		if err != nil {
			return err
		}
		listings[parent] = children
		for _, n := range children {
			if n.ParentID != parent || n.Name == "" || strings.ContainsAny(n.Name, "/\\") || n.Name == "." || n.Name == ".." || n.ID == "" {
				return ErrRepair
			}
			n.Path = path.Join(rel, n.Name)
			if old, exists := s.Cloud[n.Path]; exists && old.ID != n.ID {
				s.Issues = append(s.Issues, Issue{ID: digest([]string{n.Path, old.ID, n.ID})[:24], Path: n.Path, Kind: "name", Reason: i18n.F("云端存在重复名称，无法安全映射：%s", n.Path), Cloud: new(n)})
				continue
			}
			if m.reserved(c.ID, n.ID, n.Name) {
				continue
			}
			if !n.Directory && complete(n) {
				if previous != nil {
					old := previous.Cloud[n.Path]
					if old.ID == n.ID && n.Version != "" && old.Version == n.Version {
						n.Hash = old.Hash
					}
				}
				if n.Hash == "" {
					n.Hash, err = b.Hash(ctx, n)
					if err != nil {
						return err
					}
					if len(n.Hash) != 64 {
						return ErrRepair
					}
				}
			}
			s.Cloud[n.Path] = n
			if n.Directory {
				if err = walk(n.ID, n.Path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err = walk(c.CloudID, ""); err != nil {
		return nil, nil, err
	}
	// Closing every listing rejects changes during pagination, not only root changes.
	for parent, before := range listings {
		after, e := b.List(ctx, parent)
		if e != nil {
			return nil, nil, e
		}
		if listingProof(before) != listingProof(after) {
			return nil, nil, ErrChanged
		}
	}
	after, afterSkipped, err := syncfs.Scan(ctx, id, localCache(s))
	if err != nil {
		return nil, nil, err
	}
	if digest(localNodes(after)) != digest(s.Local) || digest(afterSkipped) != digest(skipped) {
		return nil, nil, ErrChanged
	}
	_, fresh, err := m.roots(ctx, c, id, owner, oldRoot)
	if err != nil {
		return nil, nil, err
	}
	if root.ParentID != fresh.ParentID || !equalStrings(root.Ancestors, fresh.Ancestors) {
		return nil, nil, ErrRepair
	}
	s.Root = fresh
	s.Complete = true
	return s, b, nil
}
func complete(n Node) bool {
	return n.Phase == "" || n.Phase == "PHASE_TYPE_COMPLETE" || n.Phase == "COMPLETE"
}
func listingProof(nodes []Node) string {
	copyNodes := append([]Node(nil), nodes...)
	for i := range copyNodes {
		copyNodes[i].Hash = ""
		copyNodes[i].Path = ""
	}
	sort.Slice(copyNodes, func(i, j int) bool { return copyNodes[i].ID < copyNodes[j].ID })
	return digest(copyNodes)
}
func (m *Manager) reserved(pair, id, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.pairs[pair]
	if r == nil {
		return false
	}
	for _, o := range r.record.Operations {
		if o.Temp == name || o.New != nil && o.New.ID == id || o.FileID != "" && o.FileID == id {
			return true
		}
	}
	return false
}
func classify(err error) string {
	switch {
	case errors.Is(err, ErrAuth):
		return "auth"
	case errors.Is(err, ErrOffline):
		return "offline"
	case errors.Is(err, ErrPermission), errors.Is(err, syncfs.ErrPermission):
		return "permission"
	case errors.Is(err, ErrUnknown):
		return "unknown"
	case errors.Is(err, ErrRepair), errors.Is(err, syncfs.ErrRootChanged):
		return "repair"
	case errors.Is(err, syncfs.ErrUnavailable):
		return "disk"
	default:
		return "failed"
	}
}
