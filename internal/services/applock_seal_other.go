//go:build !darwin && !windows

package services

// Without a credential store here, the verifier — salted and derived, never
// the password — rests in a file only this user may read.
func sealLockSecret(data []byte) ([]byte, error) { return data, nil }

func unsealLockSecret(data []byte) ([]byte, error) { return data, nil }
