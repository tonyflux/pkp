package foldersync

import (
	"strings"

	"github.com/tonyflux/pkp/internal/syncfs"
)

// CompletedFile identifies a published original in a committed sync baseline.
// Callers must validate Root, securely open RelativePath, and verify Hash before use.
type CompletedFile struct {
	Root         syncfs.Identity
	RelativePath string
	Hash         string
	Size         int64
}

func reusableOriginal(path string, b baseline) bool {
	return b.Preserved == "" && b.Local != nil && b.Cloud != nil &&
		!b.Local.Directory && !b.Cloud.Directory && b.Local.Path == path && b.Cloud.Path == path &&
		b.Cloud.ID != "" && b.Cloud.Version != "" && complete(*b.Cloud) &&
		b.Local.Size == b.Cloud.Size && len(b.Cloud.Hash) == 64 && b.Local.Hash == b.Cloud.Hash
}

// CompletedFiles only reads existing relationships; it never scans, starts,
// resumes, or cancels synchronization. Paused pairs can still supply originals.
func (m *Manager) CompletedFiles(account, owner string, cloud Node) []CompletedFile {
	if account == "" || owner == "" || cloud.ID == "" || cloud.Version == "" || cloud.Directory || !complete(cloud) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.storeErr != nil {
		return nil
	}
	var files []CompletedFile
	for _, r := range m.pairs {
		if r.record.Pair.Account != account || r.record.Owner != owner || r.record.Removing || r.record.Repair || r.broken || syncfs.IsStagePath(r.record.Identity.Path, r.record.Identity) {
			continue
		}
		// Baseline generations change on every committed update. Index once per
		// generation instead of scanning all synced files for each shared row.
		if r.completedPaths == nil || r.completedVersion != r.record.Version {
			r.completedPaths = make(map[string]string)
			r.completedPending = make(map[string]struct{}, len(r.record.Operations))
			for _, op := range r.record.Operations {
				if !op.Committed {
					r.completedPending[op.Path] = struct{}{}
					if op.From != "" {
						r.completedPending[op.From] = struct{}{}
					}
				}
			}
			for path, b := range r.record.Baseline {
				if !reusableOriginal(path, b) || syncfs.IsStagePath(path, r.record.Identity) {
					continue
				}
				if _, duplicate := r.completedPaths[b.Cloud.ID]; duplicate {
					r.completedPaths[b.Cloud.ID] = ""
				} else {
					r.completedPaths[b.Cloud.ID] = path
				}
			}
			r.completedVersion = r.record.Version
		}
		path := r.completedPaths[cloud.ID]
		b := r.record.Baseline[path]
		if path == "" || !reusableOriginal(path, b) || b.Cloud.Version != cloud.Version || b.Cloud.Size != cloud.Size || b.Cloud.ParentID != cloud.ParentID || b.Cloud.Name != cloud.Name {
			continue
		}
		pending := false
		for parent := path; parent != ""; {
			if _, blocked := r.completedPending[parent]; blocked {
				pending = true
				break
			}
			slash := strings.LastIndexByte(parent, '/')
			if slash < 0 {
				break
			}
			parent = parent[:slash]
		}
		if pending {
			continue
		}
		files = append(files, CompletedFile{Root: r.record.Identity, RelativePath: path, Hash: b.Cloud.Hash, Size: b.Cloud.Size})
	}
	return files
}
