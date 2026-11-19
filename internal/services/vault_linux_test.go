//go:build linux

package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	testSecretSession    = dbus.ObjectPath("/org/freedesktop/secrets/session/test")
	testSecretCollection = dbus.ObjectPath("/org/freedesktop/secrets/collection/test")
	testSecretItem       = dbus.ObjectPath("/org/freedesktop/secrets/item/test")
	testSecretPrompt     = dbus.ObjectPath("/org/freedesktop/secrets/prompt/test")
)

type testSecretService struct {
	conn         *dbus.Conn
	mu           sync.Mutex
	account      string
	password     []byte
	exists       bool
	locked       bool
	cancelPrompt bool
	hangPrompt   bool
	prompted     chan struct{}
	dismissed    int
	closed       int
}

func (s *testSecretService) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	if algorithm != "plain" || input.Value() != "" {
		return dbus.Variant{}, "/", dbus.MakeFailedError(errors.New("unexpected session algorithm"))
	}
	return dbus.MakeVariant(""), testSecretSession, nil
}

func (s *testSecretService) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if attributes["application"] != "com.pkp.desktop" || attributes["account"] != s.account || !s.exists {
		return []dbus.ObjectPath{}, []dbus.ObjectPath{}, nil
	}
	if s.locked {
		return []dbus.ObjectPath{}, []dbus.ObjectPath{testSecretItem}, nil
	}
	return []dbus.ObjectPath{testSecretItem}, []dbus.ObjectPath{}, nil
}

func (s *testSecretService) Unlock(paths []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return []dbus.ObjectPath{}, testSecretPrompt, nil
	}
	return paths, "/", nil
}

func (s *testSecretService) ReadAlias(alias string) (dbus.ObjectPath, *dbus.Error) {
	if alias != "default" {
		return "/", dbus.MakeFailedError(errors.New("unexpected alias"))
	}
	return testSecretCollection, nil
}

func (s *testSecretService) CreateItem(properties map[string]dbus.Variant, secret serviceSecret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !replace || secret.Session != testSecretSession || len(secret.Parameters) != 0 {
		return "/", "/", dbus.MakeFailedError(errors.New("invalid item creation"))
	}
	attributes, ok := properties[secretItemInterface+".Attributes"].Value().(map[string]string)
	if !ok || attributes["application"] != "com.pkp.desktop" {
		return "/", "/", dbus.MakeFailedError(errors.New("invalid attributes"))
	}
	s.account = attributes["account"]
	s.password = append([]byte(nil), secret.Value...)
	s.exists = true
	return testSecretItem, "/", nil
}

func (s *testSecretService) GetSecret(session dbus.ObjectPath) (serviceSecret, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session != testSecretSession || s.locked || !s.exists {
		return serviceSecret{}, dbus.MakeFailedError(errors.New("item unavailable"))
	}
	return serviceSecret{session, []byte{}, append([]byte(nil), s.password...), "text/plain; charset=utf-8"}, nil
}

func (s *testSecretService) SetSecret(secret serviceSecret) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked || secret.Session != testSecretSession {
		return dbus.MakeFailedError(errors.New("locked or wrong session"))
	}
	s.password = append([]byte(nil), secret.Value...)
	return nil
}

func (s *testSecretService) Delete() (dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return "/", dbus.MakeFailedError(errors.New("item locked"))
	}
	s.exists = false
	s.password = nil
	return "/", nil
}

func (s *testSecretService) Prompt(string) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prompted != nil {
		select {
		case s.prompted <- struct{}{}:
		default:
		}
	}
	if s.hangPrompt {
		return nil
	}
	if !s.cancelPrompt {
		s.locked = false
	}
	// Complete before replying: clients must subscribe before calling Prompt.
	if err := s.conn.Emit(testSecretPrompt, secretPromptInterface+".Completed", s.cancelPrompt, dbus.MakeVariant([]dbus.ObjectPath{testSecretItem})); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

func (s *testSecretService) Dismiss() *dbus.Error {
	s.mu.Lock()
	s.dismissed++
	s.mu.Unlock()
	return nil
}

func (s *testSecretService) Close() *dbus.Error {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
	return nil
}

func startTestSecretService(t *testing.T) *testSecretService {
	t.Helper()
	binary, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Fatal("Secret Service protocol tests require dbus-daemon: ", err)
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
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := &testSecretService{conn: conn}
	for _, export := range []struct {
		path  dbus.ObjectPath
		iface string
	}{
		{secretRoot, secretServiceInterface},
		{testSecretCollection, "org.freedesktop.Secret.Collection"},
		{testSecretItem, secretItemInterface},
		{testSecretPrompt, secretPromptInterface},
		{testSecretSession, "org.freedesktop.Secret.Session"},
	} {
		if err := conn.Export(s, export.path, export.iface); err != nil {
			t.Fatal(err)
		}
	}
	if reply, err := conn.RequestName(secretDestination, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own Secret Service name: %v, %v", reply, err)
	}
	return s
}

func TestSecretServiceVaultLifecycle(t *testing.T) {
	s := startTestSecretService(t)
	vault := secretServiceVault{}
	account := "a@example.com"
	if _, err := vault.Password(account); !errors.Is(err, ErrNoSavedPassword) {
		t.Fatalf("missing password = %v", err)
	}
	if err := vault.DeletePassword(account); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"密钥\n\"\\\x00", "replacement", ""} {
		if err := vault.SetPassword(account, password); err != nil {
			t.Fatal(err)
		}
		if got, err := vault.Password(account); err != nil || got != password {
			t.Fatalf("password = %q, %v; want %q", got, err, password)
		}
	}
	s.mu.Lock()
	s.locked = true
	s.mu.Unlock()
	if _, err := vault.Password(account); err != nil {
		t.Fatalf("unlock prompt: %v", err)
	}
	s.mu.Lock()
	s.locked, s.cancelPrompt = true, true
	s.mu.Unlock()
	if _, err := vault.Password(account); err == nil || errors.Is(err, ErrNoSavedPassword) {
		t.Fatalf("cancelled prompt must not masquerade as missing password: %v", err)
	}
	s.mu.Lock()
	s.locked, s.cancelPrompt = false, false
	s.mu.Unlock()
	if err := vault.DeletePassword(account); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Password(account); !errors.Is(err, ErrNoSavedPassword) {
		t.Fatalf("deleted password = %v", err)
	}
}

func TestSecretServiceUnavailableDoesNotPretendPasswordMissing(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+t.TempDir()+"/missing-bus")
	vault := secretServiceVault{}
	if _, err := vault.Password("account"); err == nil || errors.Is(err, ErrNoSavedPassword) {
		t.Fatalf("missing service must remain a service error: %v", err)
	}
	if err := vault.SetPassword("account", "never write to a fallback file"); err == nil {
		t.Fatal("missing service accepted a password")
	}
}

func TestSecretServiceExpiredOperation(t *testing.T) {
	s := startTestSecretService(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	client := &secretService{conn: s.conn, ctx: ctx, session: testSecretSession}
	if _, err := client.search("account"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired request = %v", err)
	}
}

func TestSecretServicePromptTimeoutDismissesAndClosesSession(t *testing.T) {
	s := startTestSecretService(t)
	s.mu.Lock()
	s.hangPrompt = true
	s.mu.Unlock()
	err := withSecretServiceTimeout(50*time.Millisecond, func(client *secretService) error {
		_, err := client.prompt(testSecretPrompt)
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("prompt timeout = %v, want DeadlineExceeded", err)
	}
	s.mu.Lock()
	dismissed, closed := s.dismissed, s.closed
	s.mu.Unlock()
	if dismissed != 1 || closed != 1 {
		t.Fatalf("cleanup on timeout: dismissed=%d closed=%d, want 1, 1", dismissed, closed)
	}
}

func TestSecretServicePromptStopsOnClosedConnection(t *testing.T) {
	s := startTestSecretService(t)
	prompted := make(chan struct{}, 1)
	s.mu.Lock()
	s.hangPrompt = true
	s.prompted = prompted
	s.mu.Unlock()
	clientConn, err := dbus.Connect(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &secretService{conn: clientConn, ctx: ctx, session: testSecretSession}
	errCh := make(chan error, 1)
	go func() {
		_, err := client.prompt(testSecretPrompt)
		errCh <- err
	}()
	select {
	case <-prompted:
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt was not called")
	}
	if err := clientConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err == nil || errors.Is(err, ErrNoSavedPassword) || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("prompt after connection close = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt spun or blocked after connection close")
	}
}

// Run only inside a throwaway HOME and a private dbus-run-session containing
// a foreground, unlocked Secret Service provider. XDG_RUNTIME_DIR must be a
// private mode-0700 directory below HOME; otherwise the provider can exit or
// reconnect to another daemon. Inspect its environment to protect real keys.
func TestLinuxRealKeyring(t *testing.T) {
	if os.Getenv("PKP_TEST_SECRET_SERVICE") != "1" {
		t.Skip("set PKP_TEST_SECRET_SERVICE=1 inside an isolated keyring session")
	}
	home, err := filepath.EvalSymlinks(os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(home, temporary+string(os.PathSeparator)) {
		t.Fatal("real-provider test requires a throwaway HOME beneath the temporary directory")
	}
	runtimeDirectory, err := filepath.EvalSymlinks(os.Getenv("XDG_RUNTIME_DIR"))
	if err != nil || !strings.HasPrefix(runtimeDirectory, home+string(os.PathSeparator)) {
		t.Fatal("real-provider test requires XDG_RUNTIME_DIR beneath its throwaway HOME")
	}
	runtimeInfo, err := os.Stat(runtimeDirectory)
	if err != nil || !runtimeInfo.IsDir() || runtimeInfo.Mode().Perm() != 0o700 {
		t.Fatal("real-provider test requires a private mode-0700 XDG_RUNTIME_DIR")
	}
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if !strings.HasPrefix(address, "unix:") || strings.Contains(address, "/run/user/") {
		t.Fatal("real-provider test requires an explicit private Unix session bus")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var pid uint32
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, secretDestination).Store(&pid); err != nil {
		t.Fatalf("start the isolated Secret Service provider first: %v", err)
	}
	environment, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		t.Fatalf("cannot verify keyring provider isolation: %v", err)
	}
	providerHome := ""
	for _, entry := range strings.Split(string(environment), "\x00") {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			providerHome, err = filepath.EvalSymlinks(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		if value, ok := strings.CutPrefix(entry, "XDG_DATA_HOME="); ok && value != "" {
			if !filepath.IsAbs(value) || !strings.HasPrefix(filepath.Clean(value), home+string(os.PathSeparator)) {
				t.Fatal("keyring provider XDG_DATA_HOME must be beneath its throwaway HOME")
			}
		}
	}
	if providerHome != home {
		t.Fatal("Secret Service provider is not running in the throwaway HOME; refusing to use it")
	}
	vault := secretServiceVault{}
	account := fmt.Sprintf("pkp-linux-integration-%d-%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() {
		if err := vault.DeletePassword(account); err != nil {
			t.Errorf("remove test keyring item: %v", err)
		}
	})
	if _, err := vault.Password(account); !errors.Is(err, ErrNoSavedPassword) {
		t.Fatalf("new unique account must not exist: %v", err)
	}
	for _, password := range []string{"PKP 密码 — Καλημέρα\nquotes: \"'\\", "replacement — 新密码"} {
		if err := vault.SetPassword(account, password); err != nil {
			t.Fatalf("store in real keyring: %v", err)
		}
		if got, err := vault.Password(account); err != nil || got != password {
			t.Fatalf("real keyring roundtrip mismatch (read error: %v)", err)
		}
	}
	if err := vault.DeletePassword(account); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Password(account); !errors.Is(err, ErrNoSavedPassword) {
		t.Fatalf("real keyring deletion: %v", err)
	}
}
