package lanshare

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tonyflux/pkp/internal/syncfs"
)

type localReference struct {
	Relative string `json:"relative"`
	Key      string `json:"key"`
}

type spaceRecord struct {
	Space
	Identity        syncfs.Identity           `json:"identity"`
	HandleKey       string                    `json:"handleKey"`
	Volume          string                    `json:"volume"`
	AccountIdentity string                    `json:"accountIdentity"`
	References      map[string]localReference `json:"references"`
}

// OpenLocal opens a regular file without following any link or directory reparse.
// The token fingerprints the opened object, not a subsequent path lookup.
func OpenLocal(path string) (*os.File, string, error) {
	f, err := openAbsolute(path, false)
	if err != nil {
		return nil, "", err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, "", errUnavailable
	}
	_, version, _, err := handleIdentity(f)
	if err != nil {
		f.Close()
		return nil, "", err
	}
	return f, version, nil
}

func LocalVersion(path string) (string, error) {
	f, version, err := OpenLocal(path)
	if err != nil {
		return "", err
	}
	f.Close()
	return version, nil
}

func sourceVersion(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) localID(space, key, relative string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(space))
	mac.Write([]byte{0})
	mac.Write([]byte(key))
	mac.Write([]byte{0})
	mac.Write([]byte(relative))
	return hex.EncodeToString(mac.Sum(nil)[:20])
}

func validateLocalHandle(f *os.File, identity syncfs.Identity) error {
	// Windows opens remain case-insensitive with per-directory case-sensitive roots.
	if runtime.GOOS == "windows" {
		identity.CaseSensitive = false
	}
	// The live handle path also catches aliases and directories renamed after opening.
	path, err := handlePath(f)
	if err != nil || syncfs.IsStagePath(path, identity) {
		return errUnavailable
	}
	return nil
}

func openRoot(s spaceRecord) (*os.File, error) {
	if syncfs.IsStagePath(s.Identity.Path, s.Identity) {
		return nil, errUnavailable
	}
	if err := syncfs.Validate(s.Identity); err != nil {
		return nil, errUnavailable
	}
	f, err := openAbsolute(s.Identity.Path, true)
	if err != nil {
		return nil, errUnavailable
	}
	key, _, volume, err := handleIdentity(f)
	if err != nil || key != s.HandleKey || volume != s.Volume || validateLocalHandle(f, s.Identity) != nil {
		f.Close()
		return nil, errUnavailable
	}
	return f, nil
}

func (m *Manager) space(id string, prepare bool) (spaceRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[id]
	if m.closed || s == nil || !s.Enabled {
		return spaceRecord{}, errForbidden
	}
	if prepare && !s.AllowPrepare {
		return spaceRecord{}, errPrepareDisabled
	}
	copy := *s
	copy.References = nil
	return copy, nil
}

func (m *Manager) validateSpace(ctx context.Context, s spaceRecord) error {
	if _, err := m.space(s.ID, false); err != nil {
		return err
	}
	if s.Kind == "local" {
		f, err := openRoot(s)
		if err == nil {
			f.Close()
		}
		return err
	}
	if m.cloud == nil {
		return errUnavailable
	}
	identity, err := m.cloud.AccountIdentity(ctx, s.AccountID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || identity == "" || identity != s.AccountIdentity {
		return errUnavailable
	}
	return sourceError(m.cloud.ValidateRoot(ctx, s.AccountID, s.RootID, s.AccountIdentity))
}

func (m *Manager) localOpen(s spaceRecord, id string, directory bool) (*os.File, Entry, error) {
	root, err := openRoot(s)
	if err != nil {
		return nil, Entry{}, err
	}
	defer root.Close()
	rel, expected := "", s.HandleKey
	if id != "" {
		m.mu.Lock()
		current := m.spaces[s.ID]
		var ref localReference
		if current != nil {
			ref = current.References[id]
		}
		m.mu.Unlock()
		if ref.Key == "" {
			return nil, Entry{}, errForbidden
		}
		rel, expected = ref.Relative, ref.Key
	}
	if syncfs.IsStagePath(rel, s.Identity) {
		return nil, Entry{}, errUnavailable
	}
	f, err := openRelative(root, rel, directory)
	if err != nil {
		return nil, Entry{}, errUnavailable
	}
	key, version, volume, err := handleIdentity(f)
	info, statErr := f.Stat()
	if err != nil || statErr != nil || key != expected || volume != s.Volume || (!info.IsDir() && !info.Mode().IsRegular()) || validateLocalHandle(f, s.Identity) != nil {
		f.Close()
		return nil, Entry{}, errUnavailable
	}
	parent := ""
	if rel != "" && strings.Contains(rel, "/") {
		p, e := openRelative(root, filepath.ToSlash(filepath.Dir(rel)), true)
		if e != nil {
			f.Close()
			return nil, Entry{}, errUnavailable
		}
		pkey, _, _, e := handleIdentity(p)
		p.Close()
		if e != nil {
			f.Close()
			return nil, Entry{}, errUnavailable
		}
		parent = m.localID(s.ID, pkey, filepath.ToSlash(filepath.Dir(rel)))
	}
	entry := Entry{ID: id, Name: filepath.Base(rel), ParentID: parent, Version: sourceVersion(version), MIME: mime.TypeByExtension(filepath.Ext(rel)), IsDir: info.IsDir(), Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339Nano), Ready: !info.IsDir()}
	entry.localToken = version
	return f, entry, nil
}

func (m *Manager) ListPreparationEntries(ctx context.Context, spaceID, parentID string) ([]PreparationEntry, error) {
	ctx = m.sourceProofContext(ctx)
	entries, err := m.list(ctx, spaceID, parentID)
	if err != nil {
		return nil, err
	}
	result := make([]PreparationEntry, len(entries))
	for i, entry := range entries {
		result[i] = PreparationEntry{Entry: entry, Thumbnail: entry.Thumbnail, DesktopCompatibleReady: m.desktopCompatibleReady(spaceID, entry)}
	}
	return result, nil
}

func (m *Manager) list(ctx context.Context, sid, parent string) ([]Entry, error) {
	s, err := m.space(sid, false)
	if err != nil {
		return nil, err
	}
	if err = m.validateSpace(ctx, s); err != nil {
		return nil, err
	}
	var entries []Entry
	if s.Kind == "cloud" {
		entries, err = m.cloud.List(ctx, s.AccountID, s.RootID, parent, s.AccountIdentity)
		if err != nil {
			return nil, sourceError(err)
		}
		if len(entries) > 10000 {
			return nil, errInvalid
		}
		for i := range entries {
			if entries[i].IsDir {
				continue
			}
			entries[i].HasThumbnail = entries[i].HasThumbnail || entries[i].Thumbnail != ""
			m.decorate(&entries[i], sid)
			if !entries[i].Ready {
				entries[i].Ready = m.preparedReady(sid, entries[i])
			}
			if entries[i].Ready {
				entries[i].NeedsVerification = false
			}
		}
	} else {
		dir, _, e := m.localOpen(s, parent, true)
		if e != nil {
			return nil, e
		}
		defer dir.Close()
		names, e := dir.Readdirnames(10001)
		if e != nil && e != io.EOF {
			return nil, errUnavailable
		}
		if len(names) > 10000 {
			return nil, errInvalid
		}
		m.mu.Lock()
		rel := ""
		if parent != "" {
			current := m.spaces[sid]
			if current == nil {
				m.mu.Unlock()
				return nil, errForbidden
			}
			rel = current.References[parent].Relative
		}
		m.mu.Unlock()
		refs := make(map[string]localReference, len(names))
		for _, name := range names {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if syncfs.IsStagePath(name, s.Identity) {
				continue
			}
			f, e := openRelative(dir, name, false)
			if e != nil {
				continue
			} // Links and unreadable special entries are deliberately not exposed.
			info, e := f.Stat()
			key, version, volume, identityErr := handleIdentity(f)
			pathErr := validateLocalHandle(f, s.Identity)
			f.Close()
			if e != nil || identityErr != nil || pathErr != nil || volume != s.Volume || (!info.IsDir() && !info.Mode().IsRegular()) {
				continue
			}
			relative := filepath.ToSlash(filepath.Join(rel, name))
			id := m.localID(sid, key, relative)
			refs[id] = localReference{Relative: relative, Key: key}
			entry := Entry{ID: id, Name: name, ParentID: parent, Version: sourceVersion(version), MIME: mime.TypeByExtension(filepath.Ext(name)), IsDir: info.IsDir(), Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339Nano), Ready: !info.IsDir()}
			m.decorate(&entry, sid)
			entries = append(entries, entry)
		}
		m.mu.Lock()
		if current := m.spaces[sid]; current != nil && current.Enabled {
			count := 0
			for _, space := range m.spaces {
				count += len(space.References)
			}
			for id := range refs {
				if _, exists := current.References[id]; !exists {
					count++
				}
			}
			if count > 50000 {
				err = errInvalid
			} else {
				changed := false
				for id, ref := range refs {
					if current.References[id] != ref {
						current.References[id] = ref
						changed = true
					}
				}
				if changed {
					err = m.saveLocked()
				}
			}
		} else {
			err = errForbidden
		}
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := m.space(sid, false); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []Entry{}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func (m *Manager) resolve(ctx context.Context, sid, id string) (Source, error) {
	if id == "" {
		return Source{}, errInvalid
	}
	s, err := m.space(sid, false)
	if err != nil {
		return Source{}, err
	}
	if err = m.validateSpace(ctx, s); err != nil {
		return Source{}, err
	}
	if s.Kind == "local" {
		f, entry, err := m.localOpen(s, id, false)
		if err != nil {
			return Source{}, err
		}
		f.Close()
		return Source{Entry: entry}, nil
	}
	src, err := m.cloud.Resolve(ctx, s.AccountID, s.RootID, id, s.AccountIdentity)
	if err != nil {
		return Source{}, sourceError(err)
	}
	if src.Entry.ID != id || src.Entry.Version == "" {
		return Source{}, errUnavailable
	}
	src.Entry.Ready = false
	src.Entry.NeedsVerification = false
	if src.LocalPath != "" && src.LocalVersion != "" {
		f, token, e := OpenLocal(src.LocalPath)
		if e == nil {
			stat, statErr := f.Stat()
			f.Close()
			src.Entry.Ready = token == src.LocalVersion && statErr == nil && stat.Size() == src.Entry.Size
		}
	}
	if !src.Entry.Ready {
		m.mu.Lock()
		prepared := m.originals[resourceKey(sid, id, src.Entry.Version)]
		m.mu.Unlock()
		if prepared != nil && (prepared.Root.Path == "" || (prepared.Root.Path == filepath.Dir(prepared.Path) && syncfs.Validate(prepared.Root) == nil)) {
			f, token, e := OpenLocal(prepared.Path)
			if e == nil {
				stat, statErr := f.Stat()
				f.Close()
				if token == prepared.Token && statErr == nil && stat.Size() == src.Entry.Size {
					src.LocalPath, src.LocalVersion, src.Entry.Ready = prepared.Path, token, true
				}
			}
		}
	}
	if _, err = m.space(sid, false); err != nil {
		return Source{}, err
	}
	return src, nil
}

func (m *Manager) original(ctx context.Context, sid, id, version string) (*os.File, Entry, error) {
	src, err := m.resolve(ctx, sid, id)
	if err != nil {
		return nil, Entry{}, err
	}
	if src.Entry.IsDir || !src.Entry.Ready {
		return nil, src.Entry, errNotReady
	}
	if version != "" && src.Entry.Version != version {
		return nil, Entry{}, errUnavailable
	}
	return m.openOriginal(sid, src)
}

// openOriginal reuses a source proof only within the operation that obtained it.
func (m *Manager) openOriginal(sid string, src Source) (*os.File, Entry, error) {
	if src.Entry.IsDir || !src.Entry.Ready {
		return nil, src.Entry, errNotReady
	}
	s, err := m.space(sid, false)
	if err != nil {
		return nil, Entry{}, err
	}
	if s.Kind == "local" {
		f, entry, err := m.localOpen(s, src.Entry.ID, false)
		if err != nil {
			return nil, Entry{}, err
		}
		if entry.Version != src.Entry.Version {
			f.Close()
			return nil, Entry{}, errUnavailable
		}
		return f, entry, nil
	}
	f, token, err := OpenLocal(src.LocalPath)
	if err != nil {
		return nil, Entry{}, errUnavailable
	}
	if token != src.LocalVersion {
		f.Close()
		return nil, Entry{}, errUnavailable
	}
	src.Entry.localToken = token
	return f, src.Entry, nil
}
