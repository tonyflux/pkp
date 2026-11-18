//go:build linux

package services

import (
	"os"
	"strings"
	"testing"
)

func TestLinuxStandaloneVerifierDoesNotRequireKeyring(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+t.TempDir()+"/missing-bus")
	password := "standalone PKP password — not the login password"
	verifier, err := newLockVerifier(password)
	if err != nil {
		t.Fatal(err)
	}
	store := newLockSecretStore(t.TempDir())
	if err := store.Save(verifier); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded != verifier {
		t.Fatalf("load = %q, %v", loaded, err)
	}
	file := store.(fileLockStore).path
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("verifier mode = %o, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), password) || !strings.HasPrefix(string(data), "$argon2id$") {
		t.Fatal("standalone password storage must contain only its salted Argon2id verifier")
	}
	if matches, err := lockVerifierMatches(loaded, password); err != nil || !matches {
		t.Fatalf("stored verifier does not unlock: %v, %v", matches, err)
	}
}
