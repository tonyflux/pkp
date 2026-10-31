//go:build !darwin

package services

import (
	"errors"
	"os"
	"path/filepath"
)

// fileLockStore keeps the verifier in a file only this user may read, sealed
// with seal/unseal: DPAPI for this Windows user, nothing more elsewhere.
type fileLockStore struct{ path string }

func newLockSecretStore(configDir string) lockSecretStore {
	return fileLockStore{path: filepath.Join(configDir, "applock.secret")}
}

func (f fileLockStore) Load() (string, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errNoLockSecret
	}
	if err != nil {
		return "", err
	}
	plain, err := unsealLockSecret(data)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (f fileLockStore) Save(verifier string) error {
	sealed, err := sealLockSecret([]byte(verifier))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

func (f fileLockStore) Delete() error {
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
