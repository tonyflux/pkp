package services

import "errors"

// PasswordVault keeps remembered sign-in passwords, one per account, outside
// the config directory: on macOS the login keychain, on Windows the
// Credential Manager. DriveService uses the system's when
// DriveOptions.Passwords is nil.
type PasswordVault interface {
	// Password returns the account's password, or ErrNoSavedPassword.
	Password(account string) (string, error)
	// SetPassword stores the account's password, replacing an earlier one.
	SetPassword(account, password string) error
	// DeletePassword removes it; having none is not an error.
	DeletePassword(account string) error
}

// ErrNoSavedPassword means the vault holds nothing for the account.
var ErrNoSavedPassword = errors.New("vault: no password saved for this account")
