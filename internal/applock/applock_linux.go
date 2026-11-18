//go:build linux

package applock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/godbus/dbus/v5"
)

const (
	polkitDestination     = "org.freedesktop.PolicyKit1"
	polkitPath            = dbus.ObjectPath("/org/freedesktop/PolicyKit1/Authority")
	polkitInterface       = "org.freedesktop.PolicyKit1.Authority"
	polkitAction          = "com.pkp.desktop.authenticate"
	loginDestination      = "org.freedesktop.login1"
	loginManager          = dbus.ObjectPath("/org/freedesktop/login1")
	loginSessionInterface = "org.freedesktop.login1.Session"
)

type polkitSubject struct {
	Kind    string
	Details map[string]dbus.Variant
}

type polkitResult struct {
	Authorized bool
	Challenge  bool
	Details    map[string]string
}

type polkitActionDescription struct {
	ID          string
	Description string
	Message     string
	Vendor      string
	VendorURL   string
	Icon        string
	Any         uint32
	Inactive    uint32
	Active      uint32
	Annotations map[string]string
}

func sessionForProcess(ctx context.Context, conn *dbus.Conn) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	err := conn.Object(loginDestination, loginManager).CallWithContext(ctx, "org.freedesktop.login1.Manager.GetSessionByPID", 0, uint32(os.Getpid())).Store(&path)
	if err != nil {
		return "", fmt.Errorf("Linux login session unavailable: %w", err)
	}
	if !path.IsValid() || path == "/" {
		return "", errors.New("Linux login session unavailable")
	}
	return path, nil
}

func activeLocalSession(ctx context.Context, conn *dbus.Conn) error {
	path, err := sessionForProcess(ctx, conn)
	if err != nil {
		return err
	}
	var properties map[string]dbus.Variant
	if err := conn.Object(loginDestination, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, loginSessionInterface).Store(&properties); err != nil {
		return fmt.Errorf("Linux login session unavailable: %w", err)
	}
	active, ok := properties["Active"].Value().(bool)
	remote, remoteOK := properties["Remote"].Value().(bool)
	if !ok || !active || !remoteOK || remote {
		return errors.New("System authentication requires an active local Linux login session")
	}
	return nil
}

func subjectForConnection(conn *dbus.Conn) (polkitSubject, error) {
	for _, name := range conn.Names() {
		if len(name) > 0 && name[0] == ':' {
			// The system bus supplies PID/UID/session, avoiding PID reuse races.
			return polkitSubject{"system-bus-name", map[string]dbus.Variant{"name": dbus.MakeVariant(name)}}, nil
		}
	}
	return polkitSubject{}, errors.New("System authentication has no D-Bus identity")
}

func checkPolicy(ctx context.Context, conn *dbus.Conn) error {
	var actions []polkitActionDescription
	if err := conn.Object(polkitDestination, polkitPath).CallWithContext(ctx, polkitInterface+".EnumerateActions", 0, "").Store(&actions); err != nil {
		return fmt.Errorf("Linux polkit unavailable: %w", err)
	}
	for _, action := range actions {
		if action.ID == polkitAction {
			// auth_self (1), not auth_admin or auth_self_keep: each unlock must
			// verify this user, not another administrator or cached consent.
			if action.Any != 0 || action.Inactive != 0 || action.Active != 1 {
				return errors.New("PKP system authentication policy must require auth_self without cached authorization")
			}
			return nil
		}
	}
	return errors.New("PKP system authentication policy is not installed (/usr/share/polkit-1/actions/com.pkp.desktop.policy); use a standalone PKP password instead")
}

func checkAuthorization(ctx context.Context, conn *dbus.Conn, subject polkitSubject, flags uint32, id string) (polkitResult, error) {
	// Unprivileged callers cannot override polkit.message; use the root-owned
	// action's message instead of attempting to supply PKP's dynamic reason.
	details := map[string]string{}
	var result polkitResult
	err := conn.Object(polkitDestination, polkitPath).CallWithContext(ctx, polkitInterface+".CheckAuthorization", 0, subject, polkitAction, details, flags, id).Store(&result)
	return result, err
}

// Available never starts authentication or a password dialog. Polkit does not
// expose registered agents; an absent desktop agent is detected truthfully by
// Authenticate, not inferred from desktop names or processes.
func Available() (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return false, fmt.Sprintf("Linux system bus unavailable: %v", err)
	}
	defer conn.Close()
	_, err = authenticationReady(ctx, conn)
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

func authenticationReady(ctx context.Context, conn *dbus.Conn) (polkitSubject, error) {
	if err := activeLocalSession(ctx, conn); err != nil {
		return polkitSubject{}, err
	}
	if err := checkPolicy(ctx, conn); err != nil {
		return polkitSubject{}, err
	}
	subject, err := subjectForConnection(conn)
	if err != nil {
		return subject, err
	}
	result, err := checkAuthorization(ctx, conn, subject, 0, "")
	if err != nil {
		return subject, fmt.Errorf("Linux system authentication unavailable: %w", err)
	}
	if result.Authorized || !result.Challenge {
		return subject, errors.New("Linux system authentication must require a fresh user authentication; check polkit policy")
	}
	return subject, nil
}

// Authenticate delegates all credential collection to the desktop's polkit
// authentication agent. PKP never reads the operating-system password.
func Authenticate(_ unsafe.Pointer, _ string) Outcome {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return Outcome{Unavailable, fmt.Sprintf("Linux system bus unavailable: %v", err)}
	}
	defer conn.Close()
	readyCtx, readyCancel := context.WithTimeout(ctx, 3*time.Second)
	subject, err := authenticationReady(readyCtx, conn)
	readyCancel()
	if err != nil {
		return Outcome{Unavailable, err.Error()}
	}
	id := fmt.Sprintf("pkp-%d-%d", os.Getpid(), time.Now().UnixNano())
	result, err := checkAuthorization(ctx, conn, subject, 1, id)
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		cancelCtx, cancelCheck := context.WithTimeout(context.Background(), time.Second)
		defer cancelCheck()
		conn.Object(polkitDestination, polkitPath).CallWithContext(cancelCtx, polkitInterface+".CancelCheckAuthorization", 0, id)
		return Outcome{Cancelled, "System authentication timed out"}
	}
	return authorizationOutcome(result, err)
}

func authorizationOutcome(result polkitResult, err error) Outcome {
	if err != nil {
		var busError dbus.Error
		if errors.As(err, &busError) && busError.Name == "org.freedesktop.PolicyKit1.Error.Cancelled" {
			return Outcome{Cancelled, "System authentication cancelled"}
		}
		return Outcome{Unavailable, fmt.Sprintf("Linux system authentication unavailable: %v", err)}
	}
	if result.Authorized {
		return Outcome{Result: Verified}
	}
	if result.Details["polkit.dismissed"] != "" {
		return Outcome{Cancelled, "System authentication cancelled"}
	}
	if result.Challenge {
		return Outcome{Unavailable, "No desktop polkit authentication agent is available for this login session"}
	}
	return Outcome{Failed, "System authentication did not verify this user"}
}

var linuxLockCallback atomic.Pointer[func()]
var linuxWatchOnce sync.Once

// WatchScreenLock observes only this process's logind session, plus the desktop
// screensaver service on its private session bus. Repeated calls replace fn.
func WatchScreenLock(fn func()) {
	linuxLockCallback.Store(&fn)
	linuxWatchOnce.Do(func() {
		go watchLoginLock()
		go watchScreenSaverLock()
	})
}

func notifyLinuxLock() {
	if fn := linuxLockCallback.Load(); fn != nil && *fn != nil {
		go (*fn)()
	}
}

func watchLoginLock() {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	path, err := sessionForProcess(ctx, conn)
	if err != nil {
		return
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	matches := [][]dbus.MatchOption{
		{dbus.WithMatchSender(loginDestination), dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(loginSessionInterface), dbus.WithMatchMember("Lock")},
		{dbus.WithMatchSender(loginDestination), dbus.WithMatchObjectPath(path), dbus.WithMatchInterface("org.freedesktop.DBus.Properties"), dbus.WithMatchMember("PropertiesChanged")},
		{dbus.WithMatchSender(loginDestination), dbus.WithMatchObjectPath(loginManager), dbus.WithMatchInterface("org.freedesktop.login1.Manager"), dbus.WithMatchMember("PrepareForSleep")},
	}
	for _, match := range matches {
		if err := conn.AddMatchSignalContext(ctx, match...); err != nil {
			return
		}
	}
	for {
		select {
		case signal := <-signals:
			if loginSignalLocks(signal, path) {
				notifyLinuxLock()
			}
		case <-conn.Context().Done():
			return
		}
	}
}

func loginSignalLocks(signal *dbus.Signal, session dbus.ObjectPath) bool {
	if signal == nil {
		return false
	}
	if signal.Path == session && signal.Name == loginSessionInterface+".Lock" {
		return true
	}
	if signal.Path == loginManager && signal.Name == "org.freedesktop.login1.Manager.PrepareForSleep" {
		return len(signal.Body) == 1 && signal.Body[0] == true
	}
	if signal.Path != session || signal.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" || len(signal.Body) < 2 || signal.Body[0] != loginSessionInterface {
		return false
	}
	properties, ok := signal.Body[1].(map[string]dbus.Variant)
	if !ok {
		return false
	}
	if locked, ok := properties["LockedHint"].Value().(bool); ok && locked {
		return true
	}
	active, ok := properties["Active"].Value().(bool)
	return ok && !active
}

func watchScreenSaverLock() {
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" || address == "autolaunch:" {
		address = fmt.Sprintf("unix:path=/run/user/%d/bus", os.Getuid())
	}
	conn, err := dbus.Connect(address)
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	// KDE/freedesktop and GNOME publish different historical interfaces.
	for _, iface := range []string{"org.freedesktop.ScreenSaver", "org.gnome.ScreenSaver"} {
		if err := conn.AddMatchSignalContext(ctx, dbus.WithMatchSender(iface), dbus.WithMatchInterface(iface), dbus.WithMatchMember("ActiveChanged")); err != nil {
			return
		}
	}
	for {
		select {
		case signal := <-signals:
			if signal != nil && (signal.Name == "org.freedesktop.ScreenSaver.ActiveChanged" || signal.Name == "org.gnome.ScreenSaver.ActiveChanged") && len(signal.Body) == 1 && signal.Body[0] == true {
				notifyLinuxLock()
			}
		case <-conn.Context().Done():
			return
		}
	}
}

// ShieldWindow cannot exclude a GTK window from screenshots or recording on
// both X11 and Wayland. There is no portable desktop protocol for this; the
// compositor controls capture. App Lock still conceals content in PKP itself.
func ShieldWindow(unsafe.Pointer, bool) {}
