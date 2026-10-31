package services

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/netproxy/proxytest"
)

// newProxyService is a service with a stand-in keychain and an API base the
// test chooses; nothing is signed in. The process-wide proxy is put back the
// way it was when the test ends — NewDriveService installs the saved one.
func newProxyService(t *testing.T, vault PasswordVault, apiBase string) *DriveService {
	t.Helper()
	t.Cleanup(func() {
		if err := netproxy.Set(netproxy.Config{Mode: netproxy.ModeSystem}); err != nil {
			t.Fatalf("restore proxy: %v", err)
		}
	})
	return NewDriveService(DriveOptions{
		ConfigDir:   t.TempDir(),
		APIBaseURL:  apiBase,
		NoIndexSync: true,
		Passwords:   vault,
		Logger:      slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)),
	}, nil)
}

// closedPort is an address on this computer that nothing listens on: a
// connection to it is refused at once, which a name that does not resolve no
// longer guarantees on a machine running a rule-based proxy client.
func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

func TestApplyProxySavesAndInstalls(t *testing.T) {
	svc := newProxyService(t, &memoryVault{}, "")

	out, err := svc.ApplyProxy(ProxyInput{
		Mode: "custom", Scheme: "socks5", Host: "127.0.0.1", Port: 7890,
		Username: "alice", Password: "s3cret", Bypass: "*.local, 192.168.0.0/16",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.Mode != "custom" || out.Host != "127.0.0.1" || out.Port != 7890 || out.Username != "alice" {
		t.Fatalf("settings came back as %+v", out)
	}
	if !out.Auth || out.PasswordHint == "" || strings.Contains(out.PasswordHint, "s3cret") {
		t.Fatalf("password hint is %q, auth %v", out.PasswordHint, out.Auth)
	}
	if !strings.Contains(out.Active, "127.0.0.1:7890") {
		t.Fatalf("active reads %q", out.Active)
	}

	cfg := netproxy.Current()
	if cfg.Mode != netproxy.ModeManual || cfg.Address() != "socks5://127.0.0.1:7890" {
		t.Fatalf("installed %+v", cfg)
	}
	if cfg.Username != "alice" || cfg.Password != "s3cret" {
		t.Fatalf("the keychain password did not reach netproxy: %q/%q", cfg.Username, cfg.Password)
	}
	if len(cfg.Bypass) != 2 {
		t.Fatalf("bypass installed as %v", cfg.Bypass)
	}

	// The file keeps the choice and nothing of the password.
	data, err := os.ReadFile(filepath.Join(svc.configDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(data), "s3cret") {
		t.Fatal("the password was written to settings.json")
	}
	var saved LocalSettings
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if saved.ProxyMode != "custom" || saved.ProxyPort != 7890 || saved.ProxyPasswordFor == "" {
		t.Fatalf("saved %+v", saved)
	}

	// What was applied is what reads back.
	again := svc.ProxySettings()
	if again.Mode != out.Mode || again.Host != out.Host || again.Port != out.Port ||
		again.Username != out.Username || again.Bypass != out.Bypass || again.PasswordHint != out.PasswordHint {
		t.Fatalf("read back %+v, applied %+v", again, out)
	}
}

func TestApplyProxyRefusesInvalidAndKeepsTheOldOne(t *testing.T) {
	svc := newProxyService(t, &memoryVault{}, "")
	if _, err := svc.ApplyProxy(ProxyInput{Mode: "custom", Scheme: "http", Host: "127.0.0.1", Port: 8080}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	for _, broken := range []ProxyInput{
		{Mode: "custom", Scheme: "http", Host: "127.0.0.1", Port: 99999},
		{Mode: "custom", Scheme: "ftp", Host: "127.0.0.1", Port: 8080},
		{Mode: "custom", Scheme: "http", Host: "  ", Port: 8080},
		{Mode: "nonsense"},
	} {
		out, err := svc.ApplyProxy(broken)
		if err == nil {
			t.Fatalf("%+v was accepted", broken)
		}
		if !hasChinese(err.Error()) {
			t.Fatalf("error is not for the user: %q", err)
		}
		if out.Port != 8080 || out.Scheme != "http" {
			t.Fatalf("the old setting was disturbed: %+v", out)
		}
		if cfg := netproxy.Current(); cfg.Address() != "http://127.0.0.1:8080" {
			t.Fatalf("the installed proxy was disturbed: %+v", cfg)
		}
	}
}

func TestApplyProxyForgetsThePasswordWhenTheEndpointChanges(t *testing.T) {
	vault := &memoryVault{}
	svc := newProxyService(t, vault, "")
	if _, err := svc.ApplyProxy(ProxyInput{
		Mode: "custom", Scheme: "socks5", Host: "127.0.0.1", Port: 7890, Username: "alice", Password: "s3cret",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := vault.Password(proxyPasswordAccount); err != nil {
		t.Fatalf("the password was not stored: %v", err)
	}

	// A new address with no password and no Keep forgets the old one.
	out, err := svc.ApplyProxy(ProxyInput{
		Mode: "custom", Scheme: "socks5", Host: "10.0.0.2", Port: 1080, Username: "alice",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.PasswordHint != "" {
		t.Fatalf("the password survived the move: %q", out.PasswordHint)
	}
	if _, err := vault.Password(proxyPasswordAccount); err == nil {
		t.Fatal("the keychain still holds the password")
	}
	if cfg := netproxy.Current(); cfg.Password != "" {
		t.Fatal("netproxy still carries a password")
	}

	// Keep saved password moves it with the endpoint instead.
	if _, err := svc.ApplyProxy(ProxyInput{
		Mode: "custom", Scheme: "socks5", Host: "10.0.0.2", Port: 1080, Username: "alice", Password: "other",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	kept, err := svc.ApplyProxy(ProxyInput{
		Mode: "custom", Scheme: "socks5", Host: "10.0.0.3", Port: 1080, Username: "alice", KeepPassword: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if kept.PasswordHint == "" {
		t.Fatal("Keep saved password lost it")
	}
	if cfg := netproxy.Current(); cfg.Password != "other" {
		t.Fatalf("netproxy carries %q", cfg.Password)
	}
}

func TestForgetProxyPassword(t *testing.T) {
	vault := &memoryVault{}
	svc := newProxyService(t, vault, "")
	if _, err := svc.ApplyProxy(ProxyInput{
		Mode: "custom", Scheme: "http", Host: "127.0.0.1", Port: 8080, Username: "alice", Password: "s3cret",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, err := svc.ForgetProxyPassword()
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if out.PasswordHint != "" || out.Host != "127.0.0.1" {
		t.Fatalf("forget left %+v", out)
	}
	if _, err := vault.Password(proxyPasswordAccount); err == nil {
		t.Fatal("the keychain still holds the password")
	}
	if cfg := netproxy.Current(); cfg.Password != "" || cfg.Address() != "http://127.0.0.1:8080" {
		t.Fatalf("installed %+v", cfg)
	}
}

func TestSettledRepairsAHandEditedProxyBlock(t *testing.T) {
	svc := newProxyService(t, &memoryVault{}, "")
	broken := `{"proxyMode":"custom","proxyScheme":"socks5","proxyHost":"127.0.0.1","proxyPort":99999,"proxyUser":"alice","proxyPasswordFor":"socks5://127.0.0.1:99999 alice"}`
	if err := os.MkdirAll(svc.configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(svc.configDir, "settings.json"), []byte(broken), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := svc.LocalSettings()
	if s.ProxyMode != proxyModeSystem || s.ProxyHost != "" || s.ProxyPort != 0 || s.ProxyPasswordFor != "" {
		t.Fatalf("settled left %+v", s)
	}
	if out := svc.ProxySettings(); out.Mode != proxyModeSystem || out.PasswordHint != "" {
		t.Fatalf("the page would show %+v", out)
	}
}

func TestTestProxyThroughAProxyThatWorks(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)
	_, apiPort, err := net.SplitHostPort(strings.TrimPrefix(api.URL, "http://"))
	if err != nil {
		t.Fatalf("api address: %v", err)
	}
	// A name that is not loopback, so the request is proxied, routed at the
	// proxy to the stand-in service: no DNS is involved either way.
	alias := net.JoinHostPort("pkp-api.test", apiPort)
	proxy := proxytest.HTTP(t)
	proxy.Route(alias, api.Listener.Addr().String())

	svc := newProxyService(t, &memoryVault{}, "http://"+alias)
	draft := ProxyInput{Mode: "custom", Scheme: "http", Host: proxy.Host(), Port: proxy.Port()}
	result := svc.TestProxy(draft)
	if !result.OK || result.Kind != "" {
		t.Fatalf("test said %+v", result)
	}
	if !result.ProxyOK || !result.ServiceOK {
		t.Fatalf("halves came back %+v", result)
	}
	if !hasChinese(result.ProxyMessage) || !hasChinese(result.ServiceMessage) {
		t.Fatalf("messages are not for the user: %+v", result)
	}
	if !proxy.Saw("pkp-api.test") {
		t.Fatalf("the proxy was not used: %v", proxy.Seen())
	}
	// Testing a draft changes nothing: the service still follows the
	// settings in force, which no one applied.
	if cfg := netproxy.Current(); cfg.Mode == netproxy.ModeManual {
		t.Fatalf("the test applied itself: %+v", cfg)
	}
	if out := svc.ProxySettings(); out.Mode != proxyModeSystem || out.Host != "" {
		t.Fatalf("the test saved the draft: %+v", out)
	}
}

func TestTestProxyReportsAProxyThatIsNotListening(t *testing.T) {
	svc := newProxyService(t, &memoryVault{}, "https://api-drive.mypikpak.com")
	port := closedPort(t)
	result := svc.TestProxy(ProxyInput{Mode: "custom", Scheme: "http", Host: "127.0.0.1", Port: port})
	if result.OK || result.Kind != proxyKindProxy {
		t.Fatalf("test said %+v", result)
	}
	if result.ProxyOK || result.ServiceOK {
		t.Fatalf("halves came back %+v", result)
	}
	if !strings.Contains(result.ProxyMessage, strconv.Itoa(port)) || !hasChinese(result.ProxyMessage) {
		t.Fatalf("message reads %q", result.ProxyMessage)
	}
	if !strings.Contains(result.Via, "127.0.0.1") {
		t.Fatalf("via reads %q", result.Via)
	}
}

func TestTestProxyInDirectAndSystemModes(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)
	svc := newProxyService(t, &memoryVault{}, api.URL)
	// Loopback never goes through a proxy, so both modes reach the service
	// the same way and say that no proxy was needed.
	for _, mode := range []string{"direct", "system"} {
		result := svc.TestProxy(ProxyInput{Mode: mode})
		if !result.OK || result.Kind != "" {
			t.Fatalf("%s said %+v", mode, result)
		}
		if !strings.Contains(result.ProxyMessage, "无需代理") {
			t.Fatalf("%s reported the proxy half as %q", mode, result.ProxyMessage)
		}
		if !hasChinese(result.Via) {
			t.Fatalf("%s via reads %q", mode, result.Via)
		}
	}
}

func TestTestProxyRefusesABrokenDraft(t *testing.T) {
	svc := newProxyService(t, &memoryVault{}, "https://api-drive.mypikpak.com")
	result := svc.TestProxy(ProxyInput{Mode: "custom", Scheme: "socks5", Port: 1080})
	if result.OK || result.Kind != proxyKindConfig {
		t.Fatalf("test said %+v", result)
	}
	if !hasChinese(result.ProxyMessage) {
		t.Fatalf("message reads %q", result.ProxyMessage)
	}
}

// hasChinese reports whether a message was written for the user.
func hasChinese(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

func TestTestProxyTellsARefusedPasswordApart(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)
	_, apiPort, err := net.SplitHostPort(strings.TrimPrefix(api.URL, "http://"))
	if err != nil {
		t.Fatalf("api address: %v", err)
	}
	alias := net.JoinHostPort("pkp-api.test", apiPort)
	proxy := proxytest.SOCKS5WithAuth(t, "alice", "s3cret")
	proxy.Route(alias, api.Listener.Addr().String())
	svc := newProxyService(t, &memoryVault{}, "http://"+alias)

	draft := ProxyInput{
		Mode: "custom", Scheme: "socks5", Host: proxy.Host(), Port: proxy.Port(),
		Username: "alice", Password: "wrong",
	}
	result := svc.TestProxy(draft)
	// A refused password is the proxy's doing, not PKP's: the page says so
	// instead of sending the user to check the address.
	if result.OK || result.Kind != proxyKindAuth {
		t.Fatalf("a wrong password came back as %+v", result)
	}
	if strings.Contains(result.ProxyMessage, "wrong") || strings.Contains(result.Via, "wrong") {
		t.Fatalf("the password leaked into %+v", result)
	}

	draft.Password = "s3cret"
	if result := svc.TestProxy(draft); !result.OK {
		t.Fatalf("the right password came back as %+v", result)
	}
}
