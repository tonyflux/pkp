package lanshare

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	if record == nil || !validCachePath(record.Path, false) {
		return nil, nil
	}
	f, token, err := OpenLocal(filepath.Join(m.dir, record.Path))
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

// Only manifest-owned, unchanged, unopened compatibility objects can be removed.
func (m *Manager) removeCacheLocked(key string) (int64, bool) {
	record := m.cache[key]
	if record == nil || !validCachePath(record.Path, false) {
		return 0, false
	}
	if record.Readers != 0 {
		return 0, false
	}
	f, token, err := OpenLocal(filepath.Join(m.dir, record.Path))
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
	f.Close()
	if err := m.storage.Remove(record.Path); err != nil {
		return 0, false
	}
	_ = m.storage.Remove(filepath.Dir(record.Path))
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
	if bytes < 0 || bytes > m.cacheLimit {
		return errDiskFull
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) > 0 && m.reserved == 0 {
		return errUnavailable
	}
	keys := make([]string, 0, len(m.cache))
	for key := range m.cache {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return m.cache[keys[i]].Used.Before(m.cache[keys[j]].Used) })
	for _, key := range keys {
		if m.cacheBytesLocked()+m.reserved+bytes <= m.cacheLimit {
			break
		}
		m.removeCacheLocked(key)
	}
	if m.cacheBytesLocked()+m.reserved+bytes > m.cacheLimit {
		return errDiskFull
	}
	free, err := freeBytes(m.dir)
	if err != nil || free < bytes+m.minFree {
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

func (m *Manager) openCompatible(ctx context.Context, sid, id string) (*os.File, Entry, func(), error) {
	src, err := m.resolve(ctx, sid, id)
	if err != nil {
		return nil, Entry{}, nil, err
	}
	m.mu.Lock()
	key := resourceKey(sid, id, src.Entry.Version)
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
	name := "cache/" + randomID()
	if err := m.storage.Mkdir(name, 0700); err != nil {
		return "", err
	}
	f, err := openAbsolute(filepath.Join(m.dir, name), true)
	if err != nil {
		return "", err
	}
	key, _, _, err := handleIdentity(f)
	f.Close()
	if err != nil {
		return "", err
	}
	m.pending[name] = key
	if err = m.saveLocked(); err != nil {
		delete(m.pending, name)
		_ = m.storage.Remove(name)
		return "", err
	}
	return name, nil
}

func (m *Manager) removePendingLocked(name string) bool {
	key, owned := m.pending[name]
	if !owned || !validCachePath(name, true) {
		return false
	}
	f, err := openAbsolute(filepath.Join(m.dir, name), true)
	if errors.Is(err, os.ErrNotExist) {
		delete(m.pending, name)
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
	if err = m.storage.RemoveAll(name); err != nil {
		return false
	}
	delete(m.pending, name)
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
		} else {
			m.removePendingLocked(name)
		}
	}
}

func (m *Manager) pendingBytesLocked() int64 {
	var total int64
	for name, key := range m.pending {
		f, err := openAbsolute(filepath.Join(m.dir, name), true)
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

func (m *Manager) outputBudget(headroom, desired int64) int64 {
	m.mu.Lock()
	available := m.cacheLimit - m.reserved - headroom
	unused := available - m.cacheBytesLocked()
	for _, record := range m.cache {
		if record.Readers > 0 {
			available -= record.Size
		}
	}
	// Use unoccupied capacity first; do not evict the entire cache merely to
	// reserve the largest output the quota could theoretically accommodate.
	available = min(available, max(unused, desired))
	m.mu.Unlock()
	free, err := freeBytes(m.dir)
	if err != nil {
		return 0
	}
	return min(available, free-m.minFree-headroom)
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
