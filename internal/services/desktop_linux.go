//go:build linux

package services

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

var linuxPortalSerial atomic.Uint64

func linuxFileURI(path string) (string, error) {
	if path == "" {
		return "", errors.New("no local path was supplied")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: absolute}).String(), nil
}

// revealLinuxPath asks the host desktop to reveal a path, never executing a
// host .desktop Exec entry inside the packaged private /usr runtime.
func revealLinuxPath(path string, selectFile bool) error {
	uri, err := linuxFileURI(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !selectFile && !info.IsDir() {
		return errors.New("the path to open in the file manager is not a directory")
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to host file manager: %w", err)
	}
	defer conn.Close()
	method := "ShowFolders"
	if selectFile {
		method = "ShowItems"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = conn.Object("org.freedesktop.FileManager1", "/org/freedesktop/FileManager1").CallWithContext(ctx,
		"org.freedesktop.FileManager1."+method, 0, []string{uri}, "").Err
	cancel()
	if err == nil {
		return nil
	}
	method = "OpenFile"
	if selectFile {
		method = "OpenDirectory"
	}
	return openLinuxPortalFile(conn, path, method)
}

func openLinuxPath(path string) error {
	if _, err := linuxFileURI(path); err != nil {
		return err
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to host desktop: %w", err)
	}
	defer conn.Close()
	return openLinuxPortalFile(conn, path, "OpenFile")
}

func openLinuxPortalFile(conn *dbus.Conn, path, method string) error {
	if !conn.SupportsUnixFDs() {
		return errors.New("the desktop portal requires D-Bus Unix FD passing")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	matches := []dbus.MatchOption{
		dbus.WithMatchSender("org.freedesktop.portal.Desktop"),
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if err := conn.AddMatchSignalContext(ctx, matches...); err != nil {
		return fmt.Errorf("watch desktop portal response: %w", err)
	}
	defer conn.RemoveMatchSignal(matches...)
	options := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(fmt.Sprintf("pkp_%d", linuxPortalSerial.Add(1))),
	}
	var handle dbus.ObjectPath
	if err := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop").CallWithContext(ctx,
		"org.freedesktop.portal.OpenURI."+method, 0, "", dbus.UnixFD(file.Fd()), options).Store(&handle); err != nil {
		return fmt.Errorf("open through host desktop portal: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			conn.Object("org.freedesktop.portal.Desktop", handle).Call("org.freedesktop.portal.Request.Close", dbus.FlagNoReplyExpected)
			return fmt.Errorf("waiting for host desktop portal: %w", ctx.Err())
		case signal, ok := <-signals:
			if !ok {
				return errors.New("desktop portal disconnected before responding")
			}
			if signal == nil || signal.Path != handle {
				continue
			}
			return linuxPortalResponse(signal.Body)
		}
	}
}

func linuxPortalResponse(body []any) error {
	if len(body) != 2 {
		return errors.New("desktop portal returned an invalid response")
	}
	response, ok := body[0].(uint32)
	if !ok {
		return errors.New("desktop portal returned an invalid response")
	}
	if _, ok := body[1].(map[string]dbus.Variant); !ok {
		return errors.New("desktop portal returned invalid results")
	}
	switch response {
	case 0:
		return nil
	case 1:
		return errors.New("opening through the host desktop was cancelled")
	default:
		return errors.New("the host desktop portal could not open the path")
	}
}
