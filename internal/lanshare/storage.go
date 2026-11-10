package lanshare

import (
	"os"
	"path/filepath"

	"github.com/tonyflux/pkp/internal/syncfs"
)

func (m *Manager) storageBusyLocked() bool {
	if m.reserved != 0 {
		return true
	}
	for _, task := range m.tasks {
		if task.cancel != nil || task.State == "queued" || task.State == "downloading" || task.State == "converting" {
			return true
		}
	}
	return false
}

// SetStorageDirectory changes only future allocations. Existing resources and held
// download checkpoints retain their original paths and identities.
func (m *Manager) SetStorageDirectory(kind, path string) error {
	if kind != "original" && kind != "copies" {
		return errInvalid
	}
	if path == "" || !filepath.IsAbs(path) {
		return errStorageUnavailable
	}
	identity, err := syncfs.Identify(path)
	if err != nil {
		return errStorageUnavailable
	}
	root, err := openStorageRoot(identity)
	if err != nil {
		return errStorageUnavailable
	}
	defer root.Close()
	probe := ".pkp-lan-write-" + randomID()
	f, err := root.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errStorageUnavailable
	}
	closeErr := f.Close()
	removeErr := root.Remove(probe)
	if closeErr != nil || removeErr != nil || syncfs.Validate(identity) != nil {
		return errStorageUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storageBusyLocked() {
		return errStorageBusy
	}
	previous, previousDir := m.originalIdentity, m.originalsDir
	previousCopy := m.copyIdentity
	if kind == "original" {
		m.originalIdentity, m.originalsDir = identity, identity.Path
	} else {
		m.copyIdentity = identity
	}
	if err = m.saveLocked(); err != nil {
		m.originalIdentity, m.originalsDir, m.copyIdentity = previous, previousDir, previousCopy
		return errStorageUnavailable
	}
	return nil
}

func openStorageRoot(identity syncfs.Identity) (*os.Root, error) {
	if err := syncfs.Validate(identity); err != nil {
		return nil, errStorageUnavailable
	}
	f, err := openAbsolute(identity.Path, true)
	if err != nil {
		return nil, errStorageUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, errStorageUnavailable
	}
	root, err := os.OpenRoot(identity.Path)
	if err != nil {
		return nil, errStorageUnavailable
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) || syncfs.Validate(identity) != nil {
		root.Close()
		return nil, errStorageUnavailable
	}
	return root, nil
}

// Relative v1 cache paths always resolve against the fixed configuration root.
// Absolute paths require the allocation directory identity persisted with them.
func (m *Manager) cacheLocation(path string, identity syncfs.Identity, directory bool) (string, bool) {
	if !filepath.IsAbs(path) {
		if !validCachePath(filepath.ToSlash(path), directory) {
			return "", false
		}
		path = filepath.Join(m.dir, path)
	} else if identity.Path == "" {
		return "", false
	}
	allocation := path
	if !directory {
		allocation = filepath.Dir(path)
	}
	if identity.Path != "" && (allocation != identity.Path || syncfs.Validate(identity) != nil) {
		return "", false
	}
	if !validCachePath("cache/"+filepath.Base(allocation), true) {
		return "", false
	}
	if !directory && filepath.Base(path) != "compatible.mp4" && filepath.Base(path) != "compatible.webm" {
		return "", false
	}
	return path, true
}
