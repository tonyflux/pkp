//go:build !linux

package services

import (
	"context"
	"errors"
)

func (m *MenuBarService) canHideToTray() bool { return true }

func (m *MenuBarService) startTrayHostMonitor(context.Context) {}

func openLinuxNotificationSettings() error {
	return errors.New("Linux notification settings are unavailable on this platform")
}

func checkNotificationHost() error {
	return errors.New("freedesktop notifications are unavailable on this platform")
}

func revealLinuxPath(string, bool) error {
	return errors.New("Linux file manager activation is unavailable on this platform")
}

func openLinuxPath(string) error {
	return errors.New("Linux desktop activation is unavailable on this platform")
}
