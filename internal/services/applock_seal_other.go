//go:build !darwin && !windows

package services

// The standalone verifier remains usable even without a desktop keyring (for
// example after logging in without unlocking GNOME Keyring). It is salted and
// derived, never a password; the existing mode-0600 file is not encrypted here.
func sealLockSecret(data []byte) ([]byte, error) { return data, nil }

func unsealLockSecret(data []byte) ([]byte, error) { return data, nil }
