//go:build linux

package lanshare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const sleepPreventionSupported = true

type linuxSleepBlock struct {
	once sync.Once
	file *os.File
	err  error
}

func acquireSleepBlock() (io.Closer, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to login1: %w", err)
	}
	defer conn.Close()
	if !conn.SupportsUnixFDs() {
		return nil, errors.New("login1 sleep inhibition requires D-Bus Unix FD passing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var fd dbus.UnixFD
	err = conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").CallWithContext(ctx,
		"org.freedesktop.login1.Manager.Inhibit", 0,
		"idle", "PKP", "LAN sharing is active", "block").Store(&fd)
	if err != nil {
		return nil, fmt.Errorf("inhibit idle sleep: %w", err)
	}
	// Received descriptors outlive the bus connection and belong to this process.
	if fd < 0 {
		return nil, errors.New("login1 returned an invalid inhibitor FD")
	}
	file := os.NewFile(uintptr(fd), "pkp-idle-inhibitor")
	if file == nil {
		return nil, errors.New("login1 returned an invalid inhibitor FD")
	}
	return &linuxSleepBlock{file: file}, nil
}

func (b *linuxSleepBlock) Close() error {
	b.once.Do(func() { b.err = b.file.Close() })
	return b.err
}
