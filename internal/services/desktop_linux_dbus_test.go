//go:build linux

package services

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type testDesktopCall struct {
	method   string
	uris     []string
	contents string
}

type testDesktopService struct {
	conn     *dbus.Conn
	calls    chan testDesktopCall
	response atomic.Uint32
}

func (s *testDesktopService) ShowItems(uris []string, startup string) *dbus.Error {
	s.calls <- testDesktopCall{method: "ShowItems", uris: uris}
	return nil
}

func (s *testDesktopService) ShowFolders(uris []string, startup string) *dbus.Error {
	s.calls <- testDesktopCall{method: "ShowFolders", uris: uris}
	return nil
}

func (s *testDesktopService) OpenFile(parent string, fd dbus.UnixFD, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return s.portal("OpenFile", fd, options)
}

func (s *testDesktopService) OpenDirectory(parent string, fd dbus.UnixFD, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return s.portal("OpenDirectory", fd, options)
}

func (s *testDesktopService) portal(method string, fd dbus.UnixFD, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	file := os.NewFile(uintptr(fd), "received-desktop-file")
	if file == nil {
		return "", dbus.MakeFailedError(fmt.Errorf("missing file descriptor"))
	}
	defer file.Close()
	data := make([]byte, 64)
	n, _ := file.Read(data)
	s.calls <- testDesktopCall{method: method, contents: string(data[:n])}
	token, ok := options["handle_token"].Value().(string)
	if !ok {
		return "", dbus.MakeFailedError(fmt.Errorf("missing handle token"))
	}
	handle := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/test/" + token)
	// Emitting before the method reply exercises the response subscription race.
	if err := s.conn.Emit(handle, "org.freedesktop.portal.Request.Response", s.response.Load(), map[string]dbus.Variant{}); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return handle, nil
}

func startTestDesktopService(t *testing.T, name, iface string) *testDesktopService {
	t.Helper()
	if os.Getenv("PKP_TEST_DESKTOP_DBUS") != "1" {
		t.Skip("set PKP_TEST_DESKTOP_DBUS=1 to exercise desktop protocols on an isolated D-Bus daemon")
	}
	// Reuse the existing private-daemon fixture; never claim host service names.
	bus := startTestSecretService(t)
	service := &testDesktopService{conn: bus.conn, calls: make(chan testDesktopCall, 8)}
	path := dbus.ObjectPath("/org/freedesktop/FileManager1")
	if name == "org.freedesktop.portal.Desktop" {
		path = "/org/freedesktop/portal/desktop"
	}
	if err := bus.conn.Export(service, path, iface); err != nil {
		t.Fatal(err)
	}
	reply, err := bus.conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register private desktop service: %v, %v", reply, err)
	}
	return service
}

func desktopTestCall(t *testing.T, service *testDesktopService) testDesktopCall {
	t.Helper()
	select {
	case call := <-service.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("desktop service received no call")
		return testDesktopCall{}
	}
}

func TestLinuxFileManagerDBusProtocol(t *testing.T) {
	service := startTestDesktopService(t, "org.freedesktop.FileManager1", "org.freedesktop.FileManager1")
	dir := t.TempDir()
	path := filepath.Join(dir, "照片 #1?.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path       string
		selectFile bool
		method     string
	}{{path, true, "ShowItems"}, {dir, false, "ShowFolders"}} {
		if err := revealLinuxPath(tc.path, tc.selectFile); err != nil {
			t.Fatal(err)
		}
		call := desktopTestCall(t, service)
		uri, err := linuxFileURI(tc.path)
		if err != nil || call.method != tc.method || len(call.uris) != 1 || call.uris[0] != uri {
			t.Fatalf("unexpected file manager call: %+v, %v", call, err)
		}
	}
}

func TestLinuxPortalDBusProtocol(t *testing.T) {
	service := startTestDesktopService(t, "org.freedesktop.portal.Desktop", "org.freedesktop.portal.OpenURI")
	path := filepath.Join(t.TempDir(), "sample.mp4")
	if err := os.WriteFile(path, []byte("descriptor payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := openLinuxPath(path); err != nil {
		t.Fatal(err)
	}
	call := desktopTestCall(t, service)
	if call.method != "OpenFile" || call.contents != "descriptor payload" {
		t.Fatalf("portal did not receive the file descriptor: %+v", call)
	}
	// No FileManager1 owner exists on this private bus: reveal must use the portal.
	if err := revealLinuxPath(path, true); err != nil {
		t.Fatal(err)
	}
	if call := desktopTestCall(t, service); call.method != "OpenDirectory" {
		t.Fatalf("reveal fallback used %q", call.method)
	}
	service.response.Store(1)
	if err := openLinuxPath(path); err == nil {
		t.Fatal("portal cancellation reported success")
	}
}
