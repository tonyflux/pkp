//go:build linux

package services

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/adrg/xdg"
)

func TestLinuxTrayRequiresKnownHost(t *testing.T) {
	m := &MenuBarService{}
	if m.canHideToTray() {
		t.Fatal("an unknown tray host allowed the main window to hide")
	}
	// closeMain must choose quit before touching a native window or drive.
	m.closeMain()
	if m.hidden.Load() {
		t.Fatal("close without a tray host hid the main window")
	}
	m.trayHost.Store(true)
	if !m.canHideToTray() {
		t.Fatal("registered tray host did not allow residency")
	}
}

func TestLinuxNotificationSettingsCommands(t *testing.T) {
	for _, tc := range []struct {
		desktop, command, panel string
	}{
		{"ubuntu:GNOME", "gnome-control-center", "notifications"},
		{"KDE", "systemsettings", "kcm_notifications"},
		{"plasma", "systemsettings", "kcm_notifications"},
	} {
		path, args, err := notificationSettingsCommand(tc.desktop, func(name string) (string, error) {
			if name != tc.command {
				t.Errorf("desktop %s chose %s", tc.desktop, name)
			}
			return "/usr/bin/" + name, nil
		})
		if err != nil || path != "/usr/bin/"+tc.command || !reflect.DeepEqual(args, []string{tc.panel}) {
			t.Errorf("desktop %s: %q %v, %v", tc.desktop, path, args, err)
		}
	}
	if _, _, err := notificationSettingsCommand("sway", func(string) (string, error) {
		t.Fatal("unsupported desktop tried an unrelated settings application")
		return "", nil
	}); err == nil {
		t.Fatal("unsupported desktop reported settings opened")
	}
	if _, _, err := notificationSettingsCommand("GNOME", func(string) (string, error) {
		return "", errors.New("not found")
	}); err == nil {
		t.Fatal("missing control center reported settings opened")
	}
}

func TestLinuxScreenshotUsesXDGPictures(t *testing.T) {
	previous := xdg.UserDirs.Pictures
	t.Cleanup(func() { xdg.UserDirs.Pictures = previous })
	xdg.UserDirs.Pictures = filepath.Join(t.TempDir(), "照片")
	got, err := screenshotDir()
	if err != nil || got != filepath.Join(xdg.UserDirs.Pictures, "PKP") {
		t.Fatalf("screenshotDir = %q, %v", got, err)
	}
	xdg.UserDirs.Pictures = ""
	if _, err := screenshotDir(); err == nil {
		t.Fatal("empty XDG directory silently used a different folder")
	}
}
