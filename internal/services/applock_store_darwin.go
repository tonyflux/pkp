//go:build darwin

package services

import "errors"

// lockSecretAccount names the separate password verifier's item among PKP's in the
// login keychain; no PikPak login can take this form.
const lockSecretAccount = "pkp-applock-verifier"

// keychainLockStore keeps the verifier in the login keychain, beside the
// remembered sign-in passwords.
type keychainLockStore struct{ vault keychainVault }

func newLockSecretStore(string) lockSecretStore { return keychainLockStore{} }

func (k keychainLockStore) Load() (string, error) {
	v, err := k.vault.Password(lockSecretAccount)
	if errors.Is(err, ErrNoSavedPassword) {
		return "", errNoLockSecret
	}
	return v, err
}

func (k keychainLockStore) Save(verifier string) error {
	return k.vault.SetPassword(lockSecretAccount, verifier)
}

func (k keychainLockStore) Delete() error { return k.vault.DeletePassword(lockSecretAccount) }
