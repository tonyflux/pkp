package lanshare

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/tonyflux/pkp/internal/syncfs"
)

func (m *Manager) preparedReady(sid string, entry Entry) bool {
	if entry.Version == "" {
		return false
	}
	m.mu.Lock()
	record := m.originals[resourceKey(sid, entry.ID, entry.Version)]
	m.mu.Unlock()
	if record == nil {
		return false
	}
	f, token, err := OpenLocal(record.Path)
	if err != nil {
		return false
	}
	defer f.Close()
	stat, err := f.Stat()
	return err == nil && token == record.Token && stat.Size() == entry.Size
}

// Allocation is deferred until explicit cloud preparation; a missing user-selected disk is never recreated.
func (m *Manager) originalStorage() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.originalIdentity.Path != "" {
		if err := syncfs.Validate(m.originalIdentity); err != nil {
			return "", errUnavailable
		}
		return m.originalIdentity.Path, nil
	}
	base, err := syncfs.Identify(filepath.Dir(m.originalsDir))
	if err != nil {
		return "", errUnavailable
	}
	dir := filepath.Join(base.Path, filepath.Base(m.originalsDir))
	parent, err := os.OpenRoot(base.Path)
	if err != nil {
		return "", errUnavailable
	}
	defer parent.Close()
	if err = parent.Mkdir(filepath.Base(dir), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", errUnavailable
	}
	identity, err := syncfs.Identify(dir)
	if err != nil {
		return "", errUnavailable
	}
	if err = syncfs.Validate(base); err != nil {
		return "", errUnavailable
	}
	m.originalIdentity, m.originalsDir = identity, identity.Path
	if err = m.saveLocked(); err != nil {
		return "", err
	}
	return m.originalsDir, nil
}
