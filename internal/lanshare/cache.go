package lanshare

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
	"github.com/tonyflux/pkp/internal/syncfs"
)

func resourceKey(space, file, version string) string { return space + "/" + file + "/" + version }

func (m *Manager) cacheBytesLocked() int64 {
	var size int64
	for _, file := range m.cache {
		size += file.Size
	}
	return size
}

func (m *Manager) cachedLocked(key string) (*cacheFile, *os.File) {
	record := m.cache[key]
	if record == nil {
		return nil, nil
	}
	path, ok := m.cacheLocation(record.Path, record.Directory, false)
	if !ok {
		return nil, nil
	}
	f, token, err := OpenLocal(path)
	if err != nil {
		return nil, nil
	}
	info, err := f.Stat()
	if err != nil || token != record.Token || info.Size() != record.Size {
		f.Close()
		return nil, nil
	}
	return record, f
}

func (m *Manager) decorate(entry *Entry, sid string) {
	if entry.IsDir || entry.Version == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, f := m.cachedLocked(resourceKey(sid, entry.ID, entry.Version))
	if f != nil {
		f.Close()
		entry.CompatibleReady, entry.CompatibleKind = true, record.Kind
	}
}

// desktopCompatibleReady only reports results proved by the current task and object.
func (m *Manager) desktopCompatibleReady(sid string, entry Entry) bool {
	if entry.IsDir || entry.Version == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	space := m.spaces[sid]
	if space == nil || !space.Enabled {
		return false
	}
	key := resourceKey(sid, entry.ID, entry.Version)
	caps := lanmedia.Capabilities{H264: true, AAC: true, MP4: true}
	for _, task := range m.tasks {
		if task.SpaceID != sid || task.FileID != entry.ID || task.Version != entry.Version || task.Kind != "compatible" || task.State != "ready" || task.Capabilities != caps {
			continue
		}
		if task.RequestKey == key+"/compatible/"+planKey(lanmedia.Plan{Kind: "direct", Container: "mp4"}) && task.OutputToken == "" {
			// Cloud versions do not bind the local original's token across restarts.
			if space.Kind == "local" && entry.Ready && !entry.NeedsVerification {
				return true
			}
			continue
		}
		if task.OutputToken == "" {
			continue
		}
		cached, f := m.cachedLocked(key)
		if f == nil {
			continue
		}
		f.Close()
		if cached.Token == task.OutputToken && task.RequestKey == key+"/compatible/"+cached.PlanKey {
			return true
		}
	}
	return false
}

// Only manifest-owned, unchanged, unopened compatibility objects can be removed.
func (m *Manager) removeCacheLocked(key string) (int64, bool) {
	record := m.cache[key]
	if record == nil || record.Readers != 0 {
		return 0, false
	}
	path, ok := m.cacheLocation(record.Path, record.Directory, false)
	if !ok {
		return 0, false
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return 0, false
	}
	defer root.Close()
	f, token, err := OpenLocal(path)
	if errors.Is(err, os.ErrNotExist) {
		delete(m.cache, key)
		return 0, true
	}
	if err != nil || token != record.Token {
		if f != nil {
			f.Close()
		}
		return 0, false
	}
	info, statErr := f.Stat()
	f.Close()
	anchored, anchorErr := root.Stat(filepath.Base(path))
	if statErr != nil || anchorErr != nil || !os.SameFile(info, anchored) {
		return 0, false
	}
	if err := root.Remove(filepath.Base(path)); err != nil {
		return 0, false
	}
	delete(m.cache, key)
	return record.Size, true
}

func (m *Manager) ClearCache() (ClearResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := ClearResult{}
	for key := range m.cache {
		removed, ok := m.removeCacheLocked(key)
		if ok {
			result.RemovedBytes += removed
		} else {
			result.Skipped++
		}
	}
	if m.reserved != 0 {
		result.Skipped++
	}
	return result, m.saveLocked()
}

func (m *Manager) reserve(bytes int64) error {
	if bytes < 0 {
		return errDiskFull
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if syncfs.Validate(m.copyIdentity) != nil {
		return errStorageUnavailable
	}
	if len(m.pending) > 0 && m.reserved == 0 {
		return errUnavailable
	}
	free, err := freeBytes(m.copyIdentity.Path)
	if err != nil || free < m.minFree {
		return errDiskFull
	}
	available := free - m.minFree
	if m.reserved > available || bytes > available-m.reserved {
		return errDiskFull
	}
	m.reserved += bytes
	if err := m.saveLocked(); err != nil {
		m.reserved -= bytes
		return err
	}
	return nil
}

func (m *Manager) release(bytes int64) {
	m.mu.Lock()
	m.reserved -= bytes
	m.mu.Unlock()
}

func (m *Manager) openCompatible(sid string, src Source) (*os.File, Entry, func(), error) {
	if _, err := m.space(sid, false); err != nil {
		return nil, Entry{}, nil, err
	}
	m.mu.Lock()
	key := resourceKey(sid, src.Entry.ID, src.Entry.Version)
	record, f := m.cachedLocked(key)
	if f == nil {
		m.mu.Unlock()
		return nil, Entry{}, nil, errNotReady
	}
	record.Readers++
	record.Used = time.Now()
	src.Entry.Size, src.Entry.MIME = record.Size, record.MIME
	src.Entry.localToken = record.Token
	m.mu.Unlock()
	release := func() { f.Close(); m.mu.Lock(); record.Readers--; m.mu.Unlock() }
	return f, src.Entry, release, nil
}

func (m *Manager) newOutputDirectory() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := openStorageRoot(m.copyIdentity)
	if err != nil {
		return "", errStorageUnavailable
	}
	defer root.Close()
	id := randomID()
	if err := root.Mkdir(id, 0700); err != nil {
		return "", errStorageUnavailable
	}
	path := filepath.Join(m.copyIdentity.Path, id)
	identity, err := syncfs.Identify(path)
	if err != nil || syncfs.Validate(m.copyIdentity) != nil {
		return "", errStorageUnavailable
	}
	f, err := openAbsolute(path, true)
	if err != nil {
		return "", errStorageUnavailable
	}
	key, _, _, err := handleIdentity(f)
	f.Close()
	if err != nil {
		return "", errStorageUnavailable
	}
	name := path
	if m.copyIdentity.Path == filepath.Join(m.dir, "cache") {
		name = "cache/" + id
	}
	m.pending[name], m.pendingIdentities[name] = key, identity
	if err = m.saveLocked(); err != nil {
		delete(m.pending, name)
		delete(m.pendingIdentities, name)
		_ = root.Remove(id)
		return "", err
	}
	return name, nil
}

func (m *Manager) removePendingLocked(name string) bool {
	key, owned := m.pending[name]
	if !owned {
		return false
	}
	path, ok := m.cacheLocation(name, m.pendingIdentities[name], true)
	if !ok {
		return false
	}
	f, err := openAbsolute(path, true)
	if errors.Is(err, os.ErrNotExist) {
		delete(m.pending, name)
		delete(m.pendingIdentities, name)
		return true
	}
	if err != nil {
		return false
	}
	actual, _, _, err := handleIdentity(f)
	f.Close()
	if err != nil || actual != key {
		return false
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false
	}
	defer root.Close()
	anchored, err := root.Open(filepath.Base(path))
	if err != nil {
		return false
	}
	actual, _, _, err = handleIdentity(anchored)
	anchored.Close()
	if err != nil || actual != key {
		return false
	}
	if err = root.RemoveAll(filepath.Base(path)); err != nil {
		return false
	}
	delete(m.pending, name)
	delete(m.pendingIdentities, name)
	return true
}

func (m *Manager) recoverPending() {
	for name := range m.pending {
		published := false
		for _, record := range m.cache {
			if filepath.Dir(record.Path) == name {
				published = true
				break
			}
		}
		if published {
			delete(m.pending, name)
			delete(m.pendingIdentities, name)
		} else {
			m.removePendingLocked(name)
		}
	}
}

func (m *Manager) pendingBytesLocked() int64 {
	var total int64
	for name, key := range m.pending {
		path, ok := m.cacheLocation(name, m.pendingIdentities[name], true)
		if !ok {
			continue
		}
		f, err := openAbsolute(path, true)
		if err != nil {
			continue
		}
		actual, _, _, err := handleIdentity(f)
		if err == nil && actual == key {
			entries, _ := f.ReadDir(-1)
			for _, entry := range entries {
				child, e := openRelative(f, entry.Name(), false)
				if e != nil {
					continue
				}
				info, e := child.Stat()
				child.Close()
				if e == nil && info.Mode().IsRegular() {
					total += info.Size()
				}
			}
		}
		f.Close()
	}
	return total
}

func (m *Manager) pinCurrentCopy(key string) func() {
	m.mu.Lock()
	record, f := m.cachedLocked(key)
	if f != nil {
		f.Close()
		record.Readers++
	}
	m.mu.Unlock()
	return func() {
		if record != nil {
			m.mu.Lock()
			record.Readers--
			m.mu.Unlock()
		}
	}
}

func (m *Manager) outputBudget(headroom int64) int64 {
	if headroom < 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	free, err := freeBytes(m.copyIdentity.Path)
	if err != nil || syncfs.Validate(m.copyIdentity) != nil || free < m.minFree {
		return 0
	}
	available := free - m.minFree
	if m.reserved > available {
		return 0
	}
	available -= m.reserved
	if headroom > available {
		return 0
	}
	return available - headroom
}

func validCachePath(name string, directory bool) bool {
	parts := strings.Split(name, "/")
	if directory && len(parts) != 2 || !directory && len(parts) != 3 {
		return false
	}
	if parts[0] != "cache" || len(parts[1]) != 48 {
		return false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return false
	}
	return directory || parts[2] == "compatible.mp4" || parts[2] == "compatible.webm"
}
