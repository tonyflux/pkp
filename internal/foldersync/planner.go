package foldersync

import (
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/syncfs"
)

type plan struct {
	preview     Preview
	snapshot    *snapshot
	operations  []*operation
	observed    map[string]baseline
	issues      []Issue
	fingerprint string
}

func nodeAt(nodes map[string]Node, p string) *Node {
	n, ok := nodes[p]
	if !ok {
		return nil
	}
	return new(n)
}
func same(a, b *Node) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Directory != b.Directory {
		return false
	}
	if a.Directory {
		return true
	}
	return a.Hash != "" && b.Hash != "" && a.Size == b.Size && a.Hash == b.Hash
}
func versionSame(a, b *Node) bool {
	if !same(a, b) {
		return false
	}
	if a == nil {
		return true
	}
	return a.ID == b.ID && a.Modified == b.Modified && a.Version == b.Version
}
func under(p, parent string) bool { return p == parent || strings.HasPrefix(p, parent+"/") }
func orderedKeys(a, b map[string]Node, base map[string]baseline) []string {
	set := map[string]bool{}
	for p := range a {
		set[p] = true
	}
	for p := range b {
		set[p] = true
	}
	for p := range base {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func makePlan(c Config, id syncfs.Identity, s *snapshot, base map[string]baseline) *plan {
	p := &plan{preview: Preview{Config: c, Changes: []Change{}}, snapshot: s, observed: map[string]baseline{}, issues: []Issue{}}
	for _, b := range base {
		if (b.Local != nil && !b.Local.Directory) || (b.Cloud != nil && !b.Cloud.Directory) {
			p.preview.Tracked++
		}
	}
	blocked := map[string]bool{}
	issue := func(rel, kind, reason string, l, r *Node) {
		i := Issue{ID: digest([]string{rel, kind, nodeProof(l), nodeProof(r)})[:24], Path: rel, Kind: kind, Reason: reason, Local: l, Cloud: r}
		if kind == "conflict" || kind == "target-edited" {
			i.KeepName = keepName(rel, i.ID, s, id)
		}
		p.issues = append(p.issues, i)
		blocked[rel] = true
		p.preview.Changes = append(p.preview.Changes, Change{ID: i.ID, Path: rel, Action: map[bool]string{true: "conflict", false: "blocked"}[kind == "conflict" || kind == "target-edited"], Reason: reason, KeepName: i.KeepName})
		if kind == "conflict" || kind == "target-edited" {
			p.preview.Conflicts++
		} else {
			p.preview.Blocked = true
		}
	}
	for _, i := range s.Issues {
		p.issues = append(p.issues, i)
		blocked[i.Path] = true
		p.preview.Blocked = true
		p.preview.Changes = append(p.preview.Changes, Change{ID: i.ID, Path: i.Path, Action: "blocked", Reason: i.Reason})
	}
	// Collision checks use actual destination volume semantics without rewriting names.
	keys := map[string]string{}
	for _, rel := range orderedKeys(s.Local, s.Cloud, nil) {
		for _, n := range []*Node{nodeAt(s.Local, rel), nodeAt(s.Cloud, rel)} {
			if n == nil {
				continue
			}
			if err := syncfs.NameError(n.Name, runtime.GOOS); err != nil {
				issue(rel, "name", err.Error(), nodeAt(s.Local, rel), nodeAt(s.Cloud, rel))
				break
			}
			if n.Phase != "" && n.Phase != "PHASE_TYPE_COMPLETE" && n.Phase != "COMPLETE" {
				issue(rel, "blocked", i18n.T("云端文件尚未完成，暂不参与同步"), nodeAt(s.Local, rel), nodeAt(s.Cloud, rel))
				break
			}
		}
		key := collisionPath(rel, id)
		if old, ok := keys[key]; ok && old != rel {
			if !caseRenameProof(old, rel, s, base) && !caseRenameProof(rel, old, s, base) {
				issue(old, "name", i18n.T("文件名在目标磁盘上发生大小写或 Unicode 冲突"), nodeAt(s.Local, old), nodeAt(s.Cloud, old))
				issue(rel, "name", i18n.T("文件名在目标磁盘上发生大小写或 Unicode 冲突"), nodeAt(s.Local, rel), nodeAt(s.Cloud, rel))
			}
		} else {
			keys[key] = rel
		}
	}
	for _, sk := range s.Skipped {
		if sk.Reason == syncfs.ErrCollision.Error() || syncfs.NameError(path.Base(sk.Path), runtime.GOOS) != nil {
			issue(sk.Path, "name", sk.Reason, nodeAt(s.Local, sk.Path), nodeAt(s.Cloud, sk.Path))
		} else {
			p.preview.Changes = append(p.preview.Changes, Change{Path: sk.Path, Action: "skip", Reason: sk.Reason})
			p.preview.Skipped++
		}
		blocked[sk.Path] = true
	}
	add := func(rel, action, direction, from string, l, r *Node) {
		o := &operation{ID: digest([]string{c.ID, rel, action, direction, from, nodeProof(l), nodeProof(r)})[:32], Action: action, Direction: direction, Path: rel, From: from, Local: l, Cloud: r, Stage: "planned"}
		p.operations = append(p.operations, o)
		ch := Change{ID: o.ID, Path: rel, Action: action, Direction: direction}
		if l != nil {
			ch.Directory = l.Directory
		} else if r != nil {
			ch.Directory = r.Directory
		}
		if action == "upload" {
			p.preview.Uploads++
			if l != nil {
				ch.Size = l.Size
			}
		}
		if action == "download" {
			p.preview.Downloads++
			if r != nil {
				ch.Size = r.Size
			}
		}
		if action == "mkdir" {
			p.preview.Directories++
		}
		p.preview.Bytes += ch.Size
		p.preview.Changes = append(p.preview.Changes, ch)
	}
	handled := map[string]bool{}
	// Identity alone is only a hint: require a unique identity AND unchanged content/subtree.
	for _, old := range orderedKeys(nil, nil, base) {
		b := base[old]
		for _, direction := range []string{"upload", "download"} {
			if c.Mode == Backup && direction == "download" || c.Mode == Download && direction == "upload" {
				continue
			}
			before, current, other := b.Local, s.Local, s.Cloud
			target := b.Cloud
			if direction == "download" {
				before, current, other, target = b.Cloud, s.Cloud, s.Local, b.Local
			}
			if before == nil || nodeAt(current, old) != nil || before.ID == "" {
				continue
			}
			found := ""
			count := 0
			for rel, n := range current {
				if n.ID == before.ID {
					found = rel
					count++
				}
			}
			if count != 1 || found == old || !same(before, nodeAt(current, found)) || !same(target, nodeAt(other, old)) || nodeAt(other, found) != nil || handled[old] {
				continue
			}
			if before.Directory && !renameTreeProof(old, found, current, base, direction) {
				continue
			}
			l, r := nodeAt(s.Local, found), nodeAt(s.Cloud, old)
			if direction == "download" {
				l, r = nodeAt(s.Local, old), nodeAt(s.Cloud, found)
			}
			add(found, "rename", direction, old, l, r)
			for _, rel := range orderedKeys(s.Local, s.Cloud, base) {
				if under(rel, old) || under(rel, found) {
					handled[rel] = true
				}
			}
		}
	}
	for _, rel := range orderedKeys(s.Local, s.Cloud, base) {
		if handled[rel] {
			continue
		}
		isBlocked := false
		for b := range blocked {
			if under(rel, b) {
				isBlocked = true
				break
			}
		}
		if isBlocked {
			continue
		}
		l, r := nodeAt(s.Local, rel), nodeAt(s.Cloud, rel)
		b, tracked := base[rel]
		if l != nil && r != nil && l.Directory != r.Directory {
			issue(rel, "name", i18n.T("同一路径同时存在文件和文件夹"), l, r)
			continue
		}
		if tracked && l == nil && r != nil && r.Directory && subtreeChanged(rel, s.Cloud, base, "cloud") || tracked && r == nil && l != nil && l.Directory && subtreeChanged(rel, s.Local, base, "local") {
			issue(rel, "conflict", i18n.T("两端均有变化，或删除与编辑冲突"), l, r)
			continue
		}
		if same(l, r) {
			if l != nil {
				p.observed[rel] = baseline{Local: l, Cloud: r}
			} else if tracked {
				p.observed[rel] = baseline{}
			}
			continue
		}
		if !tracked {
			if l != nil && r != nil {
				issue(rel, "conflict", i18n.T("两端内容不同，保留双方后才能继续"), l, r)
				continue
			}
			if l != nil && c.Mode != Download {
				a := "upload"
				if l.Directory {
					a = "mkdir"
				}
				add(rel, a, "upload", "", l, r)
			} else if r != nil && c.Mode != Backup {
				a := "download"
				if r.Directory {
					a = "mkdir"
				}
				add(rel, a, "download", "", l, r)
			} else {
				p.preview.Changes = append(p.preview.Changes, Change{Path: rel, Action: "keep", Reason: i18n.T("保留单向同步目标端的额外项目")})
			}
			continue
		}
		if b.Preserved != "" {
			if b.Preserved == "local" && l == nil || b.Preserved == "cloud" && r == nil {
				changed := b.Preserved == "local" && !same(r, b.Cloud) || b.Preserved == "cloud" && !same(l, b.Local)
				if changed {
					issue(rel, "conflict", i18n.T("删除后保留的另一端又有编辑，请确认保留结果"), l, r)
				} else {
					p.observed[rel] = baseline{Local: b.Local, Cloud: b.Cloud, Preserved: b.Preserved}
				}
				continue
			}
			// A restored/new entry is a new event; never reuse old deletion approval.
			if l != nil && r != nil {
				issue(rel, "conflict", i18n.T("删除后重新出现的项目需要核对双方内容"), l, r)
				continue
			}
		}
		lc, rc := !same(l, b.Local), !same(r, b.Cloud)
		if c.Mode == Backup {
			rc = !versionSame(r, b.Cloud)
		}
		if c.Mode == Download {
			lc = !versionSame(l, b.Local)
		}
		if c.Mode == Backup && !lc || c.Mode == Download && !rc {
			issue(rel, "target-edited", i18n.T("目标端已被修改，保留目标且不写回源端"), l, r)
			continue
		}
		if lc && rc {
			issue(rel, "conflict", i18n.T("两端均有变化，或删除与编辑冲突"), l, r)
			continue
		}
		direction := "upload"
		source, target := l, r
		if rc {
			direction = "download"
			source, target = r, l
		}
		if c.Mode == Backup && direction != "upload" || c.Mode == Download && direction != "download" {
			continue
		}
		if source == nil {
			if target == nil {
				p.observed[rel] = baseline{}
				continue
			}
			if c.Deletes[c.Mode] {
				add(rel, "trash", direction, "", l, r)
			} else {
				preserved := "local"
				if direction == "download" {
					preserved = "cloud"
				}
				p.observed[rel] = baseline{Local: b.Local, Cloud: b.Cloud, Preserved: preserved}
				p.preview.Changes = append(p.preview.Changes, Change{Path: rel, Action: "keep", Direction: direction, Reason: i18n.T("已记录删除并保留另一端，不自动恢复源端")})
			}
		} else if target == nil || !source.Directory {
			a := direction
			if source.Directory {
				a = "mkdir"
			}
			add(rel, a, direction, "", l, r)
		}
	}
	// A blocked child must not be removed by a parent's directory operation.
	kept := p.operations[:0]
	for _, o := range p.operations {
		bad := false
		for b := range blocked {
			if under(o.Path, b) || o.Action == "trash" && under(b, o.Path) || o.Action == "rename" && (under(b, o.From) || under(o.From, b)) {
				bad = true
				break
			}
		}
		if !bad {
			kept = append(kept, o)
		}
	}
	p.operations = kept
	validOps := map[string]bool{}
	for _, o := range p.operations {
		validOps[o.ID] = true
	}
	changes := p.preview.Changes[:0]
	p.preview.Uploads = 0
	p.preview.Downloads = 0
	p.preview.Directories = 0
	p.preview.Bytes = 0
	for _, ch := range p.preview.Changes {
		if ch.Action == "upload" || ch.Action == "download" || ch.Action == "mkdir" || ch.Action == "rename" || ch.Action == "trash" {
			if !validOps[ch.ID] {
				continue
			}
			switch ch.Action {
			case "upload":
				p.preview.Uploads++
			case "download":
				p.preview.Downloads++
			case "mkdir":
				p.preview.Directories++
			}
			p.preview.Bytes += ch.Size
		}
		changes = append(changes, ch)
	}
	p.preview.Changes = changes
	sort.SliceStable(p.operations, func(i, j int) bool {
		a, b := p.operations[i], p.operations[j]
		rank := func(o *operation) int {
			if o.Action == "mkdir" {
				return 0
			}
			if o.Action == "rename" {
				return 1
			}
			if o.Action == "trash" {
				return 3
			}
			return 2
		}
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if a.Action == "trash" {
			return strings.Count(a.Path, "/") > strings.Count(b.Path, "/")
		}
		return strings.Count(a.Path, "/") < strings.Count(b.Path, "/")
	})
	deleted := map[string]bool{}
	deletedDirs := map[string]bool{}
	for _, o := range p.operations {
		if o.Action != "trash" {
			continue
		}
		for rel, b := range base {
			if !under(rel, o.Path) {
				continue
			}
			n := b.Cloud
			if o.Direction == "download" {
				n = b.Local
			}
			if n != nil {
				if n.Directory {
					deletedDirs[rel] = true
				} else {
					deleted[rel] = true
				}
			}
		}
	}
	p.preview.Deletes = len(deleted)
	p.preview.DeleteDirectories = len(deletedDirs)
	rootEmpty := false
	for _, direction := range []string{"upload", "download"} {
		total, removed := 0, 0
		for rel, b := range base {
			n := b.Cloud
			if direction == "download" {
				n = b.Local
			}
			if n == nil {
				continue
			}
			total++
			for _, o := range p.operations {
				if o.Action == "trash" && o.Direction == direction && under(rel, o.Path) {
					removed++
					break
				}
			}
		}
		if total > 0 && removed == total {
			rootEmpty = true
		}
	}
	if deleteGuard(len(deleted), p.preview.Tracked, rootEmpty) {
		p.preview.Blocked = true
		p.preview.Error = i18n.T("本批删除触发保护，请审阅并确认当前版本")
	}
	p.fingerprint = digest(struct {
		Config       Config
		Local, Cloud map[string]Node
		Base         map[string]baseline
		Issues       []Issue
	}{c, s.Local, s.Cloud, base, p.issues})
	p.preview.ID = p.fingerprint[:32]
	return p
}

func deleteGuard(d, n int, rootEmpty bool) bool {
	return rootEmpty || d >= 100 || d >= 20 && n > 0 && d*5 >= n
}
func nodeProof(n *Node) string {
	if n == nil {
		return "absent"
	}
	return digest(n)
}
func collisionPath(rel string, id syncfs.Identity) string {
	parts := strings.Split(rel, "/")
	for i := range parts {
		parts[i] = syncfs.CollisionKey(parts[i], id)
	}
	return strings.Join(parts, "/")
}
func keepName(rel, id string, s *snapshot, root syncfs.Identity) string {
	ext := path.Ext(rel)
	stem := strings.TrimSuffix(path.Base(rel), ext)
	if len([]rune(stem)) > 100 {
		stem = string([]rune(stem)[:100])
	}
	if len([]rune(ext)) > 40 {
		ext = ""
	}
	for n := 0; ; n++ {
		suffix := ""
		if n > 0 {
			suffix = "-" + strconv.Itoa(n)
		}
		name := stem + " (PKP conflict " + id[:10] + suffix + ")" + ext
		candidate := path.Join(path.Dir(rel), name)
		taken := false
		for p := range s.Local {
			if collisionPath(p, root) == collisionPath(candidate, root) {
				taken = true
			}
		}
		for p := range s.Cloud {
			if collisionPath(p, root) == collisionPath(candidate, root) {
				taken = true
			}
		}
		if !taken && syncfs.NameError(name, runtime.GOOS) == nil {
			return name
		}
	}
}
func renameTreeProof(old, new string, current map[string]Node, base map[string]baseline, direction string) bool {
	count := 0
	for rel, b := range base {
		if !under(rel, old) {
			continue
		}
		n := b.Local
		if direction == "download" {
			n = b.Cloud
		}
		if n == nil {
			return false
		}
		now := nodeAt(current, new+strings.TrimPrefix(rel, old))
		if !same(n, now) || now == nil || n.ID != now.ID {
			return false
		}
		count++
	}
	actual := 0
	for rel := range current {
		if under(rel, new) {
			actual++
		}
	}
	return count == actual
}

func caseRenameProof(old, newPath string, s *snapshot, base map[string]baseline) bool {
	b, ok := base[old]
	if !ok {
		return false
	}
	if b.Local != nil && b.Cloud != nil && nodeAt(s.Local, old) == nil && nodeAt(s.Cloud, newPath) == nil {
		n := nodeAt(s.Local, newPath)
		if n != nil && n.ID == b.Local.ID && same(n, b.Local) && same(nodeAt(s.Cloud, old), b.Cloud) {
			return true
		}
	}
	if b.Local != nil && b.Cloud != nil && nodeAt(s.Cloud, old) == nil && nodeAt(s.Local, newPath) == nil {
		n := nodeAt(s.Cloud, newPath)
		if n != nil && n.ID == b.Cloud.ID && same(n, b.Cloud) && same(nodeAt(s.Local, old), b.Local) {
			return true
		}
	}
	return false
}

func subtreeChanged(root string, current map[string]Node, base map[string]baseline, side string) bool {
	for rel, n := range current {
		if !under(rel, root) {
			continue
		}
		b, ok := base[rel]
		before := b.Cloud
		if side == "local" {
			before = b.Local
		}
		if !ok || !same(new(n), before) {
			return true
		}
	}
	for rel, b := range base {
		if !under(rel, root) {
			continue
		}
		before := b.Cloud
		if side == "local" {
			before = b.Local
		}
		if before != nil && nodeAt(current, rel) == nil {
			return true
		}
	}
	return false
}
