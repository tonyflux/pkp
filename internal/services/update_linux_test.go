//go:build linux

package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/update"
)

func TestLinuxUpdateRevealsVerifiedPackageWithoutQuitting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	existing := filepath.Join(home, "PKP Updates", "existing.deb")
	if err := os.MkdirAll(filepath.Dir(existing), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
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
	var saved string
	revealPath = func(got string, selectFile bool) error {
		if !selectFile || filepath.Dir(filepath.Dir(got)) != filepath.Join(home, "PKP Updates") {
			t.Fatalf("revealed %q, select=%v", got, selectFile)
		}
		if saved != "" && saved != got {
			t.Fatalf("repeat created another package: %q", got)
		}
		saved = got
		calls++
		return nil
	}
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || u.State().Phase != "ready" || u.State().Error != "" || u.ctx.Err() != nil {
		t.Fatal("Linux update changed running app or lost ready state")
	}
	info, err := os.Stat(saved)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("AppImage permissions: %v, %v", info, err)
	}
	if err := u.Install(); err != nil || calls != 2 {
		t.Fatalf("repeat reveal: calls=%d err=%v", calls, err)
	}
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("export lost on shutdown: %v", err)
	}
	if data, err := os.ReadFile(existing); err != nil || string(data) != "user file" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
	restarted := isolatedUpdater(t)
	restarted.cacheDir = u.cacheDir
	if err := restarted.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("export lost on startup: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary download survived shutdown: %v", err)
	}
}

func TestLinuxUpdateRejectsTamperingAndFailedReveal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	u := isolatedUpdater(t)
	body := []byte("verified package")
	digest := sha256.Sum256(body)
	path := filepath.Join(t.TempDir(), "package.deb")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	u.platform, u.path = "linux-amd64-deb", path
	u.manifest = &update.Manifest{Version: "0.7.2", LinuxPackages: map[string]update.Package{u.platform: {Size: int64(len(body)), SHA256: hex.EncodeToString(digest[:])}}}
	u.state.Phase = "ready"
	oldReveal := revealPath
	defer func() { revealPath = oldReveal }()
	revealPath = func(string, bool) error { return errors.New("no file manager") }
	if err := u.Install(); err == nil {
		t.Fatal("expected reveal failure")
	}
	entries, err := os.ReadDir(filepath.Join(home, "PKP Updates"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed export remains: %v %v", entries, err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(); !errors.Is(err, update.ErrIntegrity) {
		t.Fatalf("tampered handoff: %v", err)
	}
	if u.State().Error != "integrity" {
		t.Fatalf("missing integrity error: %+v", u.State())
	}
}

func TestLinuxSavedPackageTamperingAllowsRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	u := isolatedUpdater(t)
	body := []byte("verified package")
	digest := sha256.Sum256(body)
	path := filepath.Join(t.TempDir(), "package.rpm")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	u.platform, u.path = "linux-amd64-rpm", path
	u.manifest = &update.Manifest{Version: "0.7.2", LinuxPackages: map[string]update.Package{u.platform: {Size: int64(len(body)), SHA256: hex.EncodeToString(digest[:])}}}
	u.state.Phase = "ready"
	oldReveal := revealPath
	defer func() { revealPath = oldReveal }()
	var saved string
	revealPath = func(path string, _ bool) error { saved = path; return nil }
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	first := saved
	if err := os.WriteFile(first, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(); !errors.Is(err, update.ErrIntegrity) {
		t.Fatalf("tampered export: %v", err)
	}
	if u.State().Phase != "ready" || u.State().Error != "integrity" {
		t.Fatalf("retry state: %+v", u.State())
	}
	if err := u.Install(); err != nil || saved == first {
		t.Fatalf("retry: %q, %v", saved, err)
	}
	if data, err := os.ReadFile(first); err != nil || string(data) != "tampered" {
		t.Fatalf("previous export changed: %q, %v", data, err)
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
