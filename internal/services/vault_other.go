//go:build !darwin && !windows && !linux

package services

import "github.com/tonyflux/pkp/internal/i18n"

// Unsupported platforms do not store plaintext passwords as a fallback.
func newSystemVault() PasswordVault { return nil }

// keychainName is what errors call the vault that is missing here, in the
// language in force.
func keychainName() string { return i18n.T("钥匙串") }
