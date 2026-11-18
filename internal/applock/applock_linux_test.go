//go:build linux

package applock

import (
	"bufio"
	"errors"
	"os/exec"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestAuthorizationOutcome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result polkitResult
		err    error
		want   Result
	}{
		{"verified", polkitResult{Authorized: true}, nil, Verified},
		{"cancelled", polkitResult{Details: map[string]string{"polkit.dismissed": "true"}}, nil, Cancelled},
		{"cancelled bus call", polkitResult{}, dbus.Error{Name: "org.freedesktop.PolicyKit1.Error.Cancelled"}, Cancelled},
		{"no agent", polkitResult{Challenge: true}, nil, Unavailable},
		{"rejected", polkitResult{}, nil, Failed},
		{"missing policy or daemon", polkitResult{}, errors.New("not available"), Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := authorizationOutcome(tc.result, tc.err)
			if got.Result != tc.want || (tc.want != Verified && got.Message == "") {
				t.Fatalf("outcome = %+v, want %v with an error explanation", got, tc.want)
			}
		})
	}
}

func TestLoginSignalLocksOnlyOurSession(t *testing.T) {
	path := dbus.ObjectPath("/org/freedesktop/login1/session/c1")
	other := dbus.ObjectPath("/org/freedesktop/login1/session/c2")
	for _, tc := range []struct {
		name   string
		signal *dbus.Signal
		want   bool
	}{
		{"nil", nil, false},
		{"lock", &dbus.Signal{Path: path, Name: loginSessionInterface + ".Lock"}, true},
		{"another user", &dbus.Signal{Path: other, Name: loginSessionInterface + ".Lock"}, false},
		{"unlock", &dbus.Signal{Path: path, Name: loginSessionInterface + ".Unlock"}, false},
		{"sleep", &dbus.Signal{Path: loginManager, Name: "org.freedesktop.login1.Manager.PrepareForSleep", Body: []any{true}}, true},
		{"wake", &dbus.Signal{Path: loginManager, Name: "org.freedesktop.login1.Manager.PrepareForSleep", Body: []any{false}}, false},
		{"locked hint", &dbus.Signal{Path: path, Name: "org.freedesktop.DBus.Properties.PropertiesChanged", Body: []any{loginSessionInterface, map[string]dbus.Variant{"LockedHint": dbus.MakeVariant(true)}}}, true},
		{"session inactive", &dbus.Signal{Path: path, Name: "org.freedesktop.DBus.Properties.PropertiesChanged", Body: []any{loginSessionInterface, map[string]dbus.Variant{"Active": dbus.MakeVariant(false)}}}, true},
		{"session active", &dbus.Signal{Path: path, Name: "org.freedesktop.DBus.Properties.PropertiesChanged", Body: []any{loginSessionInterface, map[string]dbus.Variant{"Active": dbus.MakeVariant(true)}}}, false},
		{"unrelated property", &dbus.Signal{Path: path, Name: "org.freedesktop.DBus.Properties.PropertiesChanged", Body: []any{"unrelated", map[string]dbus.Variant{"Active": dbus.MakeVariant(false)}}}, false},
		{"malformed properties", &dbus.Signal{Path: path, Name: "org.freedesktop.DBus.Properties.PropertiesChanged", Body: []any{loginSessionInterface, "bad"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := loginSignalLocks(tc.signal, path); got != tc.want {
				t.Fatalf("locks = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAvailableWithoutSystemBus(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+t.TempDir()+"/missing-bus")
	if available, reason := Available(); available || reason == "" {
		t.Fatalf("available = %v, reason = %q", available, reason)
	}
	if outcome := Authenticate(nil, "unlock PKP"); outcome.Result != Unavailable || outcome.Message == "" {
		t.Fatalf("authenticate = %+v", outcome)
	}
}

type testPolkitAuthority struct {
	mu          sync.Mutex
	action      polkitActionDescription
	result      polkitResult
	checks      int
	interactive int
}

func (a *testPolkitAuthority) EnumerateActions(string) ([]polkitActionDescription, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return []polkitActionDescription{a.action}, nil
}

func (a *testPolkitAuthority) CheckAuthorization(subject polkitSubject, action string, details map[string]string, flags uint32, cancellation string) (polkitResult, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if subject.Kind != "system-bus-name" || action != polkitAction || len(details) != 0 {
		return polkitResult{}, dbus.MakeFailedError(errors.New("invalid authorization request"))
	}
	a.checks++
	if flags == 0 {
		return polkitResult{Challenge: true}, nil
	}
	if flags != 1 || cancellation == "" {
		return polkitResult{}, dbus.MakeFailedError(errors.New("invalid interactive request"))
	}
	a.interactive++
	return a.result, nil
}

type testLoginSession struct{}

func (testLoginSession) GetSessionByPID(uint32) (dbus.ObjectPath, *dbus.Error) {
	return "/org/freedesktop/login1/session/test", nil
}

func (testLoginSession) GetAll(string) (map[string]dbus.Variant, *dbus.Error) {
	return map[string]dbus.Variant{"Active": dbus.MakeVariant(true), "Remote": dbus.MakeVariant(false)}, nil
}

func TestLinuxAuthenticationUsesFreshSystemOwnedChallenge(t *testing.T) {
	binary, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Fatal("polkit protocol tests require dbus-daemon: ", err)
	}
	command := exec.Command(binary, "--session", "--nofork", "--print-address=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("dbus-daemon did not print its address")
	}
	address := scanner.Text()
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", address)
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	authority := &testPolkitAuthority{action: polkitActionDescription{ID: polkitAction, Active: 1}, result: polkitResult{Authorized: true}}
	for _, export := range []struct {
		object any
		path   dbus.ObjectPath
		iface  string
	}{
		{authority, polkitPath, polkitInterface},
		{testLoginSession{}, loginManager, "org.freedesktop.login1.Manager"},
		{testLoginSession{}, "/org/freedesktop/login1/session/test", "org.freedesktop.DBus.Properties"},
	} {
		if err := conn.Export(export.object, export.path, export.iface); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{polkitDestination, loginDestination} {
		if reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("own %s: %v, %v", name, reply, err)
		}
	}
	if available, reason := Available(); !available {
		t.Fatal(reason)
	}
	authority.mu.Lock()
	checks, interactive := authority.checks, authority.interactive
	authority.mu.Unlock()
	if checks != 1 || interactive != 0 {
		t.Fatalf("Available prompted: checks=%d interactive=%d", checks, interactive)
	}
	if outcome := Authenticate(nil, "unlock PKP"); outcome.Result != Verified {
		t.Fatalf("authenticate = %+v", outcome)
	}
	authority.mu.Lock()
	authority.result = polkitResult{Challenge: true}
	authority.mu.Unlock()
	if outcome := Authenticate(nil, "unlock PKP"); outcome.Result != Unavailable {
		t.Fatalf("missing desktop agent = %+v", outcome)
	}
	authority.mu.Lock()
	authority.action.Active = 3 // auth_self_keep must never unlock from cached consent.
	authority.mu.Unlock()
	if available, reason := Available(); available || reason == "" {
		t.Fatalf("cached authorization policy accepted: %v %q", available, reason)
	}
}
