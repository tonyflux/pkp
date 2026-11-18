//go:build linux

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

func notificationSettingsCommand(desktop string, lookPath func(string) (string, error)) (string, []string, error) {
	desktop = strings.ToLower(desktop)
	var name, panel string
	switch {
	case strings.Contains(desktop, "gnome"), strings.Contains(desktop, "unity"), strings.Contains(desktop, "ubuntu"):
		name, panel = "gnome-control-center", "notifications"
	case strings.Contains(desktop, "kde"), strings.Contains(desktop, "plasma"):
		name, panel = "systemsettings", "kcm_notifications"
	default:
		return "", nil, errors.New("this Linux desktop has no supported notification settings command; use its system settings")
	}
	path, err := lookPath(name)
	if err != nil {
		return "", nil, fmt.Errorf("notification settings command %s is not installed", name)
	}
	return path, []string{panel}, nil
}

func openLinuxNotificationSettings() error {
	desktop := os.Getenv("XDG_CURRENT_DESKTOP")
	if isGNOMEDesktop(desktop) {
		if err := activateGNOMENotificationSettings(); err == nil {
			return nil
		} else if os.Getenv("PKP_APPIMAGE") == "1" {
			return fmt.Errorf("host GNOME notification settings are unavailable over D-Bus: %w", err)
		}
	}
	if os.Getenv("PKP_APPIMAGE") == "1" {
		return errors.New("notification settings cannot be launched safely from this private runtime on this desktop; open the host system settings")
	}
	path, args, err := notificationSettingsCommand(os.Getenv("XDG_CURRENT_DESKTOP"), exec.LookPath)
	if err != nil {
		return err
	}
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open notification settings: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func isGNOMEDesktop(desktop string) bool {
	for _, name := range strings.Split(strings.ToLower(desktop), ":") {
		if name == "gnome" || name == "unity" || name == "ubuntu" {
			return true
		}
	}
	return false
}

func activateGNOMENotificationSettings() error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// GNOME Settings exposes launch-panel with the GAction parameter type (sav).
	panel := struct {
		Name      string
		Arguments []dbus.Variant
	}{"notifications", []dbus.Variant{}}
	return conn.Object("org.gnome.Settings", "/org/gnome/Settings").CallWithContext(ctx,
		"org.freedesktop.Application.ActivateAction", 0, "launch-panel",
		[]dbus.Variant{dbus.MakeVariant(panel)}, map[string]dbus.Variant{}).Err
}

func checkNotificationHost() error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to notification bus: %w", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Call the daemon instead of NameHasOwner so D-Bus activation is honoured.
	var name, vendor, version, spec string
	if err := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").CallWithContext(ctx,
		"org.freedesktop.Notifications.GetServerInformation", 0).Store(&name, &vendor, &version, &spec); err != nil {
		return fmt.Errorf("freedesktop notifications are unavailable: %w", err)
	}
	return nil
}
