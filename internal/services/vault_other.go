//go:build !darwin && !windows

package services

import "github.com/tonyflux/pkp/internal/i18n"

// newSystemVault is nil outside macOS and Windows, like the player's placeholder there:
// no keychain backend exists yet, and keeping the password in a file beside
// the credentials is not an option. Remember Account Password and Auto Login are not offered.
func newSystemVault() PasswordVault { return nil }

// keychainName is what errors call the vault that is missing here, in the
// language in force.
func keychainName() string { return i18n.T("钥匙串") }
