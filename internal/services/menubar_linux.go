//go:build linux

package services

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

func (m *MenuBarService) canHideToTray() bool { return m.trayHost.Load() }

func (m *MenuBarService) startTrayHostMonitor(ctx context.Context) {
	go func() {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return
		}
		defer conn.Close()
		// The context owns this connection; shutdown interrupts a pending call.
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		defer m.trayHost.Store(false)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			callCtx, cancel := context.WithTimeout(ctx, time.Second)
			var present bool
			err := conn.BusObject().CallWithContext(callCtx, "org.freedesktop.DBus.NameHasOwner", 0,
				"org.kde.StatusNotifierWatcher").Store(&present)
			available := false
			if err == nil && present {
				var host dbus.Variant
				err = conn.Object("org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher").CallWithContext(callCtx,
					"org.freedesktop.DBus.Properties.Get", 0, "org.kde.StatusNotifierWatcher", "IsStatusNotifierHostRegistered").Store(&host)
				registered, ok := host.Value().(bool)
				available = err == nil && ok && registered
			}
			cancel()
			m.trayHost.Store(available)
			if !available && m.hidden.Load() {
				// A panel disappearing must not strand an already-hidden window.
				m.Open("app", "", "")
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}
