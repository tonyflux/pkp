//go:build linux

package services

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tonyflux/pkp/internal/update"
)

func TestLinuxUpdateRevealsVerifiedPackageWithoutQuitting(t *testing.T) {
	u := isolatedUpdater(t)
	body := []byte("verified Linux installation package")
	digest := sha256.Sum256(body)
	platform := "linux-arm64-appimage"
	path := filepath.Join(t.TempDir(), "PKP.AppImage")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	u.platform = platform
	u.path = path
	u.manifest = &update.Manifest{Version: "0.7.2", LinuxPackages: map[string]update.Package{platform: {Size: int64(len(body)), SHA256: hex.EncodeToString(digest[:])}}}
	u.state.Phase = "ready"
	oldReveal := revealPath
	defer func() { revealPath = oldReveal }()
	calls := 0
	revealPath = func(got string, selectFile bool) error {
		if got != path || !selectFile {
			t.Fatalf("revealed %q, select=%v", got, selectFile)
		}
		calls++
		return nil
	}
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || u.State().Phase != "ready" || u.State().Error != "" || u.ctx.Err() != nil {
		t.Fatal("Linux update changed running app or lost ready state")
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(); !errors.Is(err, update.ErrIntegrity) {
		t.Fatalf("tampered handoff: %v", err)
	}
	if calls != 1 {
		t.Fatal("tampered download reached file manager")
	}
}

func TestExtractedAppImageKeepsAppImageUpdateFormat(t *testing.T) {
	t.Setenv("APPIMAGE", "")
	t.Setenv("PKP_APPIMAGE", "1")
	t.Setenv("PKP_LINUX_FORMAT", "")
	platform, libc := updatePlatform()
	if !update.SupportsOS(libc, "2.39") {
		t.Fatalf("unexpected Linux baseline %q", libc)
	}
	if platform != "linux-amd64-appimage" && platform != "linux-arm64-appimage" {
		t.Fatalf("AppImage chose system package: %s", platform)
	}
}
