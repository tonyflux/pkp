package services

// Settings · Network & Proxy: which proxy everything this app sends goes through.
// The page edits a draft and nothing changes until Apply Settings, so SetProxy's
// work is split in two — TestProxy tries a draft and saves nothing,
// ApplyProxy validates it, saves it and puts it in force. The choice lives
// in settings.json, the password only in the keychain, and both the Go side
// (internal/netproxy: the drive API, uploads, downloads, TMDB, the model
// services) and the webview's own page loads (internal/webproxy: thumbnails,
// avatars) follow it.

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/webproxy"
)

const (
	// proxyPasswordAccount names the proxy password's keychain item, beside
	// the AI key, the TMDB credential and the Remember account passwords passwords (which
	// are named by login, never like this).
	proxyPasswordAccount = "pkp-proxy-password"
	// maxProxyPasswordBytes bounds a pasted password; SOCKS5 itself takes
	// 255 bytes at most (RFC 1929), HTTP Basic rather more.
	maxProxyPasswordBytes = 1024
	// proxyHandshakeTimeout bounds the first leg (reaching the proxy itself) and
	// proxyTestTimeout the test as a whole.
	proxyHandshakeTimeout = 10 * time.Second
	proxyTestTimeout      = 30 * time.Second
)

// The three Connection Methods as the page and settings.json name them. The file
// keeps these words, not netproxy's, so the page and the file agree;
// proxyMode maps Custom Proxy onto netproxy.ModeManual.
const (
	proxyModeDirect = "direct"
	proxyModeSystem = "system"
	proxyModeCustom = "custom"
)

// The kinds Test Connection reports, which decide the page's advice.
const (
	proxyKindConfig  = "config"
	proxyKindProxy   = "proxy"
	proxyKindAuth    = "auth"
	proxyKindService = "service"
)

var (
	errProxyMode           = i18n.New("请选择直连、跟随系统或自定义代理")
	errProxyScheme         = i18n.New("请选择 SOCKS5、HTTP 或 HTTPS 代理")
	errProxyHostMissing    = i18n.New("请填写代理服务器地址")
	errProxyHost           = i18n.New("代理服务器地址不是有效的主机名或 IP")
	errProxyPort           = i18n.New("端口需在 1 到 65535 之间")
	errProxyUser           = i18n.New("代理用户名不能包含空格或冒号")
	errProxyCredTooLong    = i18n.New("SOCKS5 的用户名和密码都不能超过 255 字节")
	errProxyPasswordFormat = i18n.New("代理密码不能包含换行等控制字符")
	errProxyPasswordLong   = i18n.New("代理密码太长")
)

// The keychain's errors name it as this platform does, in the language in
// force.
func errProxyNoKeychain() error {
	return i18n.Errorf("这台电脑没有可用的%s，无法保存代理密码", keychainName())
}

func errProxyPasswordStore() error {
	return i18n.Errorf("无法把代理密码存进%s", keychainName())
}

func errProxyPasswordForget() error {
	return i18n.Errorf("无法从%s删除代理密码", keychainName())
}

// ProxyInput is Network & Proxy as the page submits it: a draft the user has not
// applied yet.
type ProxyInput struct {
	Mode         string `json:"mode"`   // direct | system | custom
	Scheme       string `json:"scheme"` // socks5 | http | https
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Password     string `json:"password"`     // only ever reaches the keychain
	KeepPassword bool   `json:"keepPassword"` // an empty Password keeps the stored one
	Bypass       string `json:"bypass"`       // User-entered addresses that bypass the proxy.
}

// ProxySettings is Network & Proxy as it stands: what was applied, what the
// computer's own settings say, and what requests take right now.
type ProxySettings struct {
	Mode     string `json:"mode"`
	Scheme   string `json:"scheme"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// Auth is Use Authentication: a user name is saved, so the page opens that
	// section. PasswordHint is "••••abcd", "" when no password is stored
	// for this endpoint; Keychain is false where there is none to store in.
	Auth         bool   `json:"auth"`
	PasswordHint string `json:"passwordHint"`
	Keychain     bool   `json:"keychain"`
	// Bypass is Addresses Not Using Proxy as the user typed it, one per line.
	Bypass string `json:"bypass"`
	// System is what Follow System resolves to now, in Chinese; SystemPAC says
	// the computer only names a PAC script, which PKP cannot run.
	System    string `json:"system"`
	SystemPAC bool   `json:"systemPac"`
	// Webview is "" when page images follow the proxy too, else the reason
	// in Chinese.
	Webview string `json:"webview"`
	// Active is Currently in Use, one Chinese line.
	Active string `json:"active"`
}

// ProxyTestResult is what Test Connection found. It keeps the two halves apart —
// reaching the proxy server and reaching PKP's own service through it — so
// an upstream outage is never reported as a wrong proxy setting. Kind is ""
// when both worked, else "config" (the draft cannot be used), "proxy" (the
// proxy server did not take the connection), "auth" (it took it but turned
// the user name or password down) or "service" (the proxy is fine, PKP's
// service is not). No message ever holds the password.
type ProxyTestResult struct {
	OK             bool   `json:"ok"`
	Kind           string `json:"kind"`
	ProxyOK        bool   `json:"proxyOk"`
	ProxyMessage   string `json:"proxyMessage"`
	ServiceOK      bool   `json:"serviceOk"`
	ServiceMessage string `json:"serviceMessage"`
	Via            string `json:"via"`
	ElapsedMs      int    `json:"elapsedMs"`
}

// ProxySettings reads Settings · Network & Proxy, together with what the computer's
// own settings say right now and what a request takes.
func (d *DriveService) ProxySettings() ProxySettings {
	d.settingsMu.Lock()
	s := d.readSettings()
	d.settingsMu.Unlock()
	return d.proxySettings(s)
}

// ApplyProxy is Apply Settings: it validates the draft, saves it and puts it in
// force at once — every later request, and the webview's own page loads,
// follow it. A password that came with the draft goes to the keychain only;
// one kept there for another endpoint is deleted unless the draft asks to
// keep it. A draft that cannot be used changes nothing.
func (d *DriveService) ApplyProxy(in ProxyInput) (ProxySettings, error) {
	draft := settledInput(in)
	cfg := proxyConfigOfInput(draft)
	if err := checkProxy(cfg); err != nil {
		return d.ProxySettings(), err
	}
	d.settingsMu.Lock()
	s := d.readSettings()
	bound := proxyPasswordFor(cfg)
	switch {
	case draft.Password != "":
		if d.vault == nil {
			d.settingsMu.Unlock()
			return d.proxySettings(s), errProxyNoKeychain()
		}
		// The keychain tool's complaint may quote what it was given, so its
		// error is not passed on: neither the page nor the log may see it.
		if err := d.vault.SetPassword(proxyPasswordAccount, draft.Password); err != nil {
			d.settingsMu.Unlock()
			d.log.Warn("proxy: the keychain did not take the proxy password")
			return d.proxySettings(s), errProxyPasswordStore()
		}
		s.ProxyPasswordFor = bound
	case draft.KeepPassword && s.ProxyPasswordFor != "" && draft.Username != "":
		// Keep the saved password: the user edited the address but not the
		// credentials, so the stored password follows the endpoint over.
		s.ProxyPasswordFor = bound
	default:
		if s.ProxyPasswordFor != "" {
			if err := d.forgetProxyPassword(); err != nil {
				d.settingsMu.Unlock()
				return d.proxySettings(s), err
			}
			s.ProxyPasswordFor = ""
		}
	}
	s.ProxyMode = draft.Mode
	s.ProxyScheme, s.ProxyHost, s.ProxyPort = draft.Scheme, draft.Host, draft.Port
	s.ProxyUser, s.ProxyBypass = draft.Username, draft.Bypass
	if err := d.writeSettings(s); err != nil {
		saved := d.readSettings()
		d.settingsMu.Unlock()
		return d.proxySettings(saved), i18n.Errorf("无法保存代理设置：%w", err)
	}
	d.settingsMu.Unlock()
	d.installProxy(s)
	return d.proxySettings(s), nil
}

// TestProxy tries the draft the page is showing and saves nothing: the
// configuration in force is untouched, whatever it finds. It answers in two
// halves — the proxy server itself, then PKP's own service through it — so a
// service outage is never read as a wrong proxy. It never fails: the outcome
// is the answer.
func (d *DriveService) TestProxy(in ProxyInput) ProxyTestResult {
	draft := settledInput(in)
	cfg := proxyConfigOfInput(draft)
	d.settingsMu.Lock()
	s := d.readSettings()
	d.settingsMu.Unlock()
	if draft.Password != "" {
		cfg.Password = draft.Password
	} else if draft.KeepPassword {
		cfg.Password = d.storedProxyPassword(s)
	}
	result := ProxyTestResult{Via: proxyWhere(cfg, netproxy.System())}
	if err := checkProxy(cfg); err != nil {
		result.Kind = proxyKindConfig
		result.ProxyMessage = err.Error()
		result.ServiceMessage = i18n.T("尚未测试")
		return result
	}
	target, err := url.Parse(d.apiBaseURL())
	if err != nil || target.Host == "" {
		result.Kind = proxyKindConfig
		result.ProxyMessage = i18n.T("PKP 服务地址无效，无法测试")
		result.ServiceMessage = i18n.T("尚未测试")
		return result
	}
	started := time.Now()
	var proxyKind string
	result.ProxyOK, proxyKind, result.ProxyMessage = d.testProxyServer(cfg, target)
	if !result.ProxyOK {
		// A refused user name or password is worth its own headline: the
		// address is right and only the credentials are not.
		result.Kind = cmp.Or(proxyKind, proxyKindProxy)
		result.ServiceMessage = i18n.T("代理服务器不可用，未测试 PKP 服务")
		result.ElapsedMs = int(time.Since(started).Milliseconds())
		return result
	}
	result.ServiceOK, result.ServiceMessage = d.testProxyService(cfg)
	result.ElapsedMs = int(time.Since(started).Milliseconds())
	if !result.ServiceOK {
		result.Kind = proxyKindService
		return result
	}
	result.OK = true
	return result
}

// ForgetProxyPassword Forget Password: the keychain item goes, the endpoint stays,
// and the proxy is reinstalled without credentials.
func (d *DriveService) ForgetProxyPassword() (ProxySettings, error) {
	d.settingsMu.Lock()
	s := d.readSettings()
	if err := d.forgetProxyPassword(); err != nil {
		d.settingsMu.Unlock()
		return d.proxySettings(s), err
	}
	s.ProxyPasswordFor = ""
	err := d.writeSettings(s)
	d.settingsMu.Unlock()
	d.installProxy(s)
	if err != nil {
		return d.proxySettings(s), i18n.Errorf("无法保存代理设置：%w", err)
	}
	return d.proxySettings(s), nil
}

// AttachWebview remembers the main window and puts its page loads behind the
// proxy in force. The webview's own requests — thumbnails, avatars and anything else
// the page fetches — do not go through Go's transports, so they have to be
// told separately, and only once the native window exists.
func (d *DriveService) AttachWebview(window *application.WebviewWindow) {
	d.webviewMu.Lock()
	d.webview = window
	d.webviewMu.Unlock()
	d.settingsMu.Lock()
	s := d.readSettings()
	d.settingsMu.Unlock()
	d.applyWebviewProxy(d.proxyConfig(s))
}

/* ---------------- the two halves of a test ---------------- */

// testProxyServer is Proxy Server Reachable: a handshake with the proxy alone,
// asking it for the API host. Direct, or Follow System where the computer names
// no proxy, needs no proxy at all, which counts as passing.
// The kind it returns beside the message is "" unless the failure deserves
// its own headline on the page ("auth": the credentials were refused).
func (d *DriveService) testProxyServer(cfg netproxy.Config, target *url.URL) (bool, string, string) {
	endpoint, err := cfg.ProxyFor(target)
	if err != nil {
		return false, "", i18n.F("无法确定要使用的代理：%s", d.proxyFailure(cfg, err, i18n.T("代理设置无法使用")))
	}
	if endpoint == nil {
		switch cfg.Mode {
		case netproxy.ModeDirect:
			return true, "", i18n.T("直连，无需代理")
		default:
			return true, "", i18n.T("这台电脑没有设置代理，无需代理")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxyHandshakeTimeout)
	defer cancel()
	if err := netproxy.Handshake(ctx, endpoint, proxyTarget(target)); err != nil {
		if errors.Is(err, netproxy.ErrDestination) {
			// The proxy answered, it simply could not reach PKP; that is the
			// second half's news, not the first's.
			return true, "", i18n.F("代理服务器可连接（%s），但它连不到 PKP 服务", endpointWhere(endpoint))
		}
		if errors.Is(err, netproxy.ErrProxyAuth) {
			return false, proxyKindAuth, i18n.T("代理服务器拒绝了用户名或密码")
		}
		return false, "", i18n.F("连不上代理服务器 %s：%s", endpointWhere(endpoint), d.proxyFailure(cfg, err, i18n.T("代理服务器没有正常回应")))
	}
	return true, "", i18n.F("代理服务器可连接（%s）", endpointWhere(endpoint))
}

// testProxyService is PKP Service Accessible: one real request to the account API
// through the draft. Whatever answer comes back, the path carried it.
func (d *DriveService) testProxyService(cfg netproxy.Config) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), proxyTestTimeout)
	defer cancel()
	elapsed, err := netproxy.Probe(ctx, cfg, d.apiBaseURL())
	if err != nil {
		return false, i18n.F("PKP 服务没有响应：%s", d.proxyFailure(cfg, err, i18n.T("请求没有成功")))
	}
	return true, i18n.F("PKP 服务可访问，用时 %d 毫秒", elapsed.Milliseconds())
}

// proxyTarget is the host:port a handshake asks the proxy for; an API base
// URL names no port of its own.
func proxyTarget(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

/* ---------------- applying ---------------- */

// installProxy puts s in force for everything this process sends, the
// webview included. A configuration netproxy refuses — a file edited by
// hand, or one from a newer build — warns and falls back to Follow System rather
// than leaving the app with no way out.
func (d *DriveService) installProxy(s LocalSettings) {
	cfg := d.proxyConfig(s)
	if err := netproxy.Set(cfg); err != nil {
		d.log.Warn("proxy: the saved proxy cannot be used, following the system settings instead", "error", err)
		cfg = netproxy.Config{Mode: netproxy.ModeSystem}
		_ = netproxy.Set(cfg)
	}
	d.applyWebviewProxy(cfg)
	d.connectivityMu.Lock()
	d.connectivity = ConnectivityResult{Targets: []ConnectivityTarget{}}
	if d.connectivityWake != nil {
		select {
		case d.connectivityWake <- struct{}{}:
		default:
		}
	}
	d.connectivityMu.Unlock()
}

// applyWebviewProxy points the window's web content at cfg. It is a no-op
// before AttachWebview and where WebKit cannot be told (macOS 13 and older),
// in which case ProxySettings().Webview says so.
func (d *DriveService) applyWebviewProxy(cfg netproxy.Config) {
	d.webviewMu.Lock()
	window := d.webview
	d.webviewMu.Unlock()
	if !webproxy.Supported() {
		return
	}
	windows := []*application.WebviewWindow{}
	if window != nil {
		windows = append(windows, window)
	}
	d.viewerMu.Lock()
	for _, view := range d.viewerWindows {
		windows = append(windows, view.window)
	}
	d.viewerMu.Unlock()
	if d.player != nil {
		d.player.popoutMu.Lock()
		if d.player.popout != nil {
			windows = append(windows, d.player.popout)
		}
		d.player.popoutMu.Unlock()
	}
	if len(windows) == 0 {
		return
	}
	application.InvokeAsync(func() {
		for _, window := range windows {
			if err := webproxy.Apply(window.NativeWindow(), cfg); err != nil && !errors.Is(err, webproxy.ErrUnsupported) {
				d.log.Warn("proxy: the webview could not be put behind the proxy", "error", err)
			}
		}
	})
}

/* ---------------- settings and keychain ---------------- */

// proxyConfig is s together with the password kept for it, which is read
// from the keychain on every install rather than held anywhere.
func (d *DriveService) proxyConfig(s LocalSettings) netproxy.Config {
	cfg := proxyConfigOf(s)
	cfg.Password = d.storedProxyPassword(s)
	return cfg
}

// proxyConfigOf is the saved choice without the password: all settled() and
// the settings page need.
func proxyConfigOf(s LocalSettings) netproxy.Config {
	return netproxy.Config{
		Mode:     proxyMode(s.ProxyMode),
		Scheme:   s.ProxyScheme,
		Host:     s.ProxyHost,
		Port:     s.ProxyPort,
		Username: s.ProxyUser,
		Bypass:   netproxy.ParseBypass(s.ProxyBypass),
	}
}

// proxyConfigOfInput is a draft as netproxy sees it, password apart.
func proxyConfigOfInput(in ProxyInput) netproxy.Config {
	return netproxy.Config{
		Mode:     proxyMode(in.Mode),
		Scheme:   in.Scheme,
		Host:     in.Host,
		Port:     in.Port,
		Username: in.Username,
		Bypass:   netproxy.ParseBypass(in.Bypass),
	}
}

// proxyMode maps the page's word onto netproxy's. An unknown one is passed
// through so that Check refuses it rather than it quietly meaning Follow System.
func proxyMode(mode string) netproxy.Mode {
	switch mode {
	case proxyModeCustom:
		return netproxy.ModeManual
	case proxyModeDirect:
		return netproxy.ModeDirect
	case proxyModeSystem, "":
		return netproxy.ModeSystem
	}
	return netproxy.Mode(mode)
}

// settledInput trims a draft the way it will be saved, so that what the page
// submits, what is tested and what is stored are the same thing.
func settledInput(in ProxyInput) ProxyInput {
	in.Mode = strings.TrimSpace(in.Mode)
	if in.Mode == "" {
		in.Mode = proxyModeSystem
	}
	in.Scheme = strings.ToLower(strings.TrimSpace(in.Scheme))
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.Password = strings.TrimSpace(in.Password)
	in.Bypass = netproxy.FormatBypass(netproxy.ParseBypass(in.Bypass))
	if in.Username == "" {
		// Authentication off: a password without a user name is sent nowhere.
		in.Password, in.KeepPassword = "", false
	}
	return in
}

// storedProxyPassword is the password kept for s's endpoint and user name,
// "" when there is none or it was entered for another proxy.
func (d *DriveService) storedProxyPassword(s LocalSettings) string {
	if d.vault == nil || s.ProxyPasswordFor == "" || s.ProxyPasswordFor != proxyPasswordFor(proxyConfigOf(s)) {
		return ""
	}
	password, err := d.vault.Password(proxyPasswordAccount)
	if err != nil {
		if !errors.Is(err, ErrNoSavedPassword) {
			d.log.Warn("proxy: the proxy password could not be read from the keychain", "error", err)
		}
		return ""
	}
	return password
}

// forgetProxyPassword deletes the stored password; having none is not an
// error.
func (d *DriveService) forgetProxyPassword() error {
	if d.vault == nil {
		return nil
	}
	if err := d.vault.DeletePassword(proxyPasswordAccount); err != nil {
		d.log.Warn("proxy: the proxy password could not be deleted", "error", err)
		return errProxyPasswordForget()
	}
	return nil
}

// proxyPasswordFor names the proxy a password is bound to. The mode is left
// out on purpose: turning the proxy off for a while and back on keeps the
// password, while a changed address, port, protocol or user name drops it.
func proxyPasswordFor(cfg netproxy.Config) string {
	if cfg.Host == "" || cfg.Username == "" {
		return ""
	}
	return cfg.Scheme + "://" + net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)) + " " + cfg.Username
}

// proxySettings is s as the settings page shows it, the password hint read
// from the keychain (it is never stored here).
func (d *DriveService) proxySettings(s LocalSettings) ProxySettings {
	out := ProxySettings{
		Mode:     s.ProxyMode,
		Scheme:   s.ProxyScheme,
		Host:     s.ProxyHost,
		Port:     s.ProxyPort,
		Username: s.ProxyUser,
		Auth:     s.ProxyUser != "",
		Keychain: d.vault != nil,
		Bypass:   s.ProxyBypass,
	}
	if password := d.storedProxyPassword(s); password != "" {
		out.PasswordHint = keyHint(password)
	}
	info := netproxy.System()
	out.System = systemWhere(info)
	out.SystemPAC = info.PAC != ""
	if !webproxy.Supported() {
		out.Webview = webproxy.UnsupportedNote()
	}
	out.Active = proxyWhere(proxyConfigOf(s), info)
	return out
}

/* ---------------- words for the page ---------------- */

// checkProxy is netproxy's own check, worded for the user. The package has
// the last word: anything it refuses that is not named here is refused too.
func checkProxy(cfg netproxy.Config) error {
	switch cfg.Mode {
	case netproxy.ModeSystem, netproxy.ModeDirect:
		return nil
	case netproxy.ModeManual:
	default:
		return errProxyMode
	}
	switch cfg.Scheme {
	case netproxy.HTTP, netproxy.HTTPS, netproxy.SOCKS5:
	default:
		return errProxyScheme
	}
	if strings.TrimSpace(cfg.Host) == "" {
		return errProxyHostMissing
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errProxyPort
	}
	if strings.ContainsAny(cfg.Username, " \t\r\n:") {
		return errProxyUser
	}
	if cfg.Scheme == netproxy.SOCKS5 && (len(cfg.Username) > 255 || len(cfg.Password) > 255) {
		return errProxyCredTooLong
	}
	if len(cfg.Password) > maxProxyPasswordBytes {
		return errProxyPasswordLong
	}
	if strings.IndexFunc(cfg.Password, unicode.IsControl) >= 0 {
		return errProxyPasswordFormat
	}
	if err := cfg.Check(); err != nil {
		if errors.Is(err, netproxy.ErrNoEndpoint) {
			return errProxyHostMissing
		}
		// The package's own words are English and may quote the address, so
		// the page gets the one message that fits every remaining case.
		return errProxyHost
	}
	return nil
}

// scrub keeps the password out of a message built from a transport error,
// which may have quoted the endpoint it dialled.
// proxyFailure words why a leg of Test Connection failed for the page; the
// library's English, password scrubbed, goes to the log.
func (d *DriveService) proxyFailure(cfg netproxy.Config, err error, fallback string) string {
	d.log.Warn("proxy: test failed", "proxy", cfg.Address(), "error", scrub(cfg, err.Error()))
	text := strings.ToLower(err.Error())
	var netErr net.Error
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	switch {
	case errors.Is(err, netproxy.ErrProxyAuth):
		return i18n.T("代理服务器拒绝了用户名或密码")
	case errors.Is(err, netproxy.ErrNoEndpoint):
		return i18n.T("没有填写代理地址")
	case errors.As(err, &dnsErr) || strings.Contains(text, "no such host"):
		return i18n.T("找不到这个主机名，请检查地址")
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) || strings.Contains(text, "timeout"):
		return i18n.T("连接超时")
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(text, "connection refused") || strings.Contains(text, "actively refused"):
		return i18n.T("连接被拒绝，这个地址和端口上没有服务在运行")
	case errors.As(err, &certErr) || strings.Contains(text, "tls") || strings.Contains(text, "x509") || strings.Contains(text, "certificate"):
		return i18n.T("TLS 握手失败，证书或协议不匹配")
	case errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(text, "connection reset") || strings.Contains(text, "forcibly closed"):
		return i18n.T("连接被中断")
	}
	return fallback
}

func scrub(cfg netproxy.Config, detail string) string {
	if cfg.Password == "" {
		return detail
	}
	detail = strings.ReplaceAll(detail, cfg.Password, "***")
	if quoted := url.QueryEscape(cfg.Password); quoted != cfg.Password {
		detail = strings.ReplaceAll(detail, quoted, "***")
	}
	return detail
}

// proxyWhere words what a request takes under cfg: Currently in Use for the
// settings in force, ready to apply for a draft under test.
func proxyWhere(cfg netproxy.Config, info netproxy.SystemInfo) string {
	switch cfg.Mode {
	case netproxy.ModeDirect:
		return i18n.T("直连，不额外指定代理")
	case netproxy.ModeManual:
		where := strings.ToUpper(cfg.Scheme) + " · " + net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
		if cfg.Username != "" {
			where += i18n.F("（用户名 %s）", cfg.Username)
		}
		return where
	default:
		return i18n.F("跟随系统：%s", systemWhere(info))
	}
}

// systemWhere words this computer's own proxy settings, credentials left
// out: an environment variable may carry them.
func systemWhere(info netproxy.SystemInfo) string {
	var parts []string
	if info.HTTP != nil {
		parts = append(parts, "HTTP "+endpointWhere(info.HTTP))
	}
	if info.HTTPS != nil {
		parts = append(parts, "HTTPS "+endpointWhere(info.HTTPS))
	}
	if info.SOCKS != nil {
		parts = append(parts, "SOCKS5 "+endpointWhere(info.SOCKS))
	}
	source := ""
	if info.FromEnv {
		source = i18n.T("（来自环境变量）")
	}
	switch {
	case len(parts) > 0:
		return strings.Join(parts, i18n.T("list|、")) + source
	case info.PAC != "":
		return i18n.T("系统只设置了 PAC 自动代理脚本，PKP 无法执行它，请求按直连发出")
	default:
		return i18n.T("系统没有设置代理，请求按直连发出")
	}
}

// endpointWhere is a proxy endpoint without its credentials.
func endpointWhere(u *url.URL) string {
	if u.Scheme == "" {
		return u.Host
	}
	return u.Scheme + "://" + u.Host
}
