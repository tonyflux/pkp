package services

import (
	"strings"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/index"
)

// A plan that does not cover a whole folder — a selection, an arrival, a
// model's proposal — still needs the tree around what it touches: where
// each entry sits, what already lies beside it (name conflicts), what a
// folder holds (moving it moves that) and what the target folders already
// hold. PlanTree reads exactly that from the index, never the whole scope.

// planTreeDepth bounds an ancestor walk against a parent cycle in a
// corrupt index, as index.Path does.
const planTreeDepth = 64

var (
	errNotIndexed = i18n.New("新文件还没进入本地索引，请稍后再试")
	errOutOfScope = i18n.New("所选的文件不在这个文件夹里，可能已被移动或删除")
)

// PlanTree is the part of account's index a plan below scopeID ("" = the
// root) works on, every entry live and with its ParentID, each once:
//   - the scope's direct children;
//   - for each of ids: the entry, its ancestors up to (excluding) the
//     scope and every ancestor's direct children;
//   - for each folder among ids: everything below it (all of those
//     subtrees together at most maxTree entries, else errTreeTooLarge);
//   - for each of paths (folder names from the scope down): every folder
//     on it that exists — names compared as nameKey does, every folder of
//     a matching name followed — and each one's direct children.
//
// An id the index does not know (yet) is errNotIndexed, for the webview to
// try again after the next sync; one trashed, or not below the scope, is
// errOutOfScope. A level with more than maxTree entries is errTreeTooLarge.
func (d *DriveService) PlanTree(account, scopeID string, ids []string, paths [][]string) ([]Entry, error) {
	_, store, err := d.readyIndex(account)
	if err != nil {
		return nil, err
	}
	if scopeID != "" {
		row, found, err := store.Get(scopeID)
		if err != nil {
			return nil, err
		}
		if !found || row.Trashed || row.Kind != drive.KindFolder {
			return nil, errNoSuchFolder
		}
	}
	t := planTree{store: store, seen: map[string]bool{}, listed: map[string]bool{}, out: []Entry{}}
	if err := t.children(scopeID); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := t.entry(scopeID, id); err != nil {
			return nil, err
		}
	}
	for _, path := range paths {
		if err := t.layers(scopeID, path); err != nil {
			return nil, err
		}
	}
	return t.out, nil
}

// planTree collects PlanTree's answer.
type planTree struct {
	store *index.Store
	seen  map[string]bool // ids in out
	// listed are the folders whose direct children are in out already.
	listed   map[string]bool
	subtrees int // entries the subtrees brought
	out      []Entry
}

func (t *planTree) add(r index.Row) bool {
	if t.seen[r.ID] {
		return false
	}
	t.seen[r.ID] = true
	t.out = append(t.out, toEntry(rowToFile(r)))
	return true
}

// children adds the live entries directly under parentID.
func (t *planTree) children(parentID string) error {
	if t.listed[parentID] {
		return nil
	}
	t.listed[parentID] = true
	rows, err := t.store.Children(parentID, maxTree)
	if err != nil {
		return err
	}
	if len(rows) > maxTree {
		return errTreeTooLarge
	}
	for _, r := range rows {
		t.add(r)
	}
	return nil
}

// entry adds id, its ancestors below scopeID with their children, and the
// subtree of a folder.
func (t *planTree) entry(scopeID, id string) error {
	if id == "" || id == scopeID {
		return errOutOfScope
	}
	row, found, err := t.store.Get(id)
	if err != nil {
		return err
	}
	if !found {
		return errNotIndexed
	}
	if row.Trashed {
		return errOutOfScope
	}
	// Walk up to the scope first: nothing is added for an id that turns
	// out to lie elsewhere.
	var chain []index.Row
	parent := row.ParentID
	for hops := 0; parent != scopeID; hops++ {
		if parent == "" || hops >= planTreeDepth {
			return errOutOfScope
		}
		up, found, err := t.store.Get(parent)
		if err != nil {
			return err
		}
		if !found {
			return errNotIndexed
		}
		if up.Trashed {
			return errOutOfScope
		}
		chain = append(chain, up)
		parent = up.ParentID
	}
	for i := len(chain) - 1; i >= 0; i-- {
		t.add(chain[i])
		if err := t.children(chain[i].ID); err != nil {
			return err
		}
	}
	t.add(row)
	if row.Kind != drive.KindFolder {
		return nil
	}
	rows, err := t.store.Subtree(row.ID, maxTree)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if t.add(r) {
			t.subtrees++
		}
	}
	if len(rows) > maxTree || t.subtrees > maxTree {
		return errTreeTooLarge
	}
	return nil
}

// layers follows path's folder names down from scopeID, adding every
// folder that matches at each level and what it holds directly. A level
// nothing matches ends the walk: what is below does not exist yet.
func (t *planTree) layers(scopeID string, path []string) error {
	parents := []string{scopeID}
	for _, name := range path {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		want := nameKey(name)
		var next []string
		for _, p := range parents {
			folders, err := t.store.Folders(p)
			if err != nil {
				return err
			}
			for _, f := range folders {
				if nameKey(f.Name) != want {
					continue
				}
				t.add(f)
				if err := t.children(f.ID); err != nil {
					return err
				}
				next = append(next, f.ID)
			}
		}
		if len(next) == 0 {
			return nil
		}
		parents = next
	}
	return nil
}
