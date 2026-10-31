package netproxy

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// SystemInfo is what this computer's own settings say about proxies: the
// macOS network settings a VPN or a rule-based client writes, or — when
// those name none — the HTTP_PROXY family in the environment.
type SystemInfo struct {
	// HTTP, HTTPS and SOCKS are the endpoints for plain, TLS and SOCKS5
	// traffic; nil for the ones that are off.
	HTTP  *url.URL
	HTTPS *url.URL
	SOCKS *url.URL
	// PAC is the address of an automatic proxy configuration script. PKP
	// cannot run one (it is JavaScript evaluated per request), so a
	// computer that only has a PAC reads as having no proxy here.
	PAC string
	// Exceptions are the hosts macOS keeps off the proxy.
	Exceptions []string
	// FromEnv says the settings came from the environment, which is all
	// there is to read outside macOS.
	FromEnv bool
}

// Any reports whether the computer names a proxy at all.
func (s SystemInfo) Any() bool { return s.HTTP != nil || s.HTTPS != nil || s.SOCKS != nil }

// systemTTL keeps the settings for a moment: they are read per connection
// and a user changing them is in no hurry.
const systemTTL = 3 * time.Second

var (
	systemMu   sync.Mutex
	systemAt   time.Time
	systemInfo SystemInfo
)

// System reports the computer's proxy settings, re-reading them at most
// every few seconds.
func System() SystemInfo {
	systemMu.Lock()
	defer systemMu.Unlock()
	if !systemAt.IsZero() && time.Since(systemAt) < systemTTL {
		return systemInfo
	}
	info, ok := readSystemProxy()
	if !ok || (!info.Any() && info.PAC == "") {
		info = envInfo()
	}
	systemInfo, systemAt = info, time.Now()
	return info
}

// forgetSystem drops the cache, so the next read is fresh.
func forgetSystem() {
	systemMu.Lock()
	systemAt = time.Time{}
	systemMu.Unlock()
}

// envInfo describes the environment variables for the settings page; the
// routing itself stays with net/http, which also honours NO_PROXY.
func envInfo() SystemInfo {
	info := SystemInfo{FromEnv: true}
	pick := func(names ...string) *url.URL {
		for _, name := range names {
			for _, key := range []string{name, strings.ToLower(name)} {
				if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
					if u, err := parseEndpoint(raw); err == nil {
						return u
					}
				}
			}
		}
		return nil
	}
	info.HTTP = pick("HTTP_PROXY", "ALL_PROXY")
	info.HTTPS = pick("HTTPS_PROXY", "ALL_PROXY")
	if raw := strings.TrimSpace(os.Getenv("NO_PROXY")); raw != "" {
		info.Exceptions = strings.Split(raw, ",")
	}
	return info
}

// parseEndpoint reads a proxy address the way net/http does: a bare
// host:port means an HTTP proxy.
func parseEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		u, err = url.Parse("http://" + raw)
		if err != nil {
			return nil, err
		}
	}
	return u, nil
}

func systemProxyFor(u *url.URL) (*url.URL, error) {
	info := System()
	if !info.FromEnv {
		if matchesException(info.Exceptions, u.Hostname()) {
			return nil, nil
		}
		switch u.Scheme {
		case "https", "wss":
			if info.HTTPS != nil {
				return info.HTTPS, nil
			}
		default:
			if info.HTTP != nil {
				return info.HTTP, nil
			}
		}
		if info.SOCKS != nil {
			return info.SOCKS, nil
		}
		// A PAC-only computer, or one whose settings cover another
		// protocol: fall through to the environment rather than guess.
	}
	return http.ProxyFromEnvironment(&http.Request{URL: u})
}

// matchesException applies the macOS exception list: an exact host, a
// suffix such as "*.local" or ".local", or a network such as "192.168/16".
func matchesException(list []string, host string) bool {
	if host == "" {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	ip := net.ParseIP(host)
	for _, raw := range list {
		pattern := strings.ToLower(strings.TrimSpace(raw))
		if pattern == "" {
			continue
		}
		if pattern == "*" {
			return true
		}
		if strings.Contains(pattern, "/") {
			if ip == nil {
				continue
			}
			if _, network, err := net.ParseCIDR(normalizeCIDR(pattern)); err == nil && network.Contains(ip) {
				return true
			}
			continue
		}
		// "*.x", ".x" and a bare "x" all mean x and anything under it.
		// WebKit's excluded domains (the webview follows the same list)
		// match by suffix too, so the two stay in step.
		pattern = strings.TrimPrefix(strings.TrimPrefix(pattern, "*"), ".")
		if host == pattern || strings.HasSuffix(host, "."+pattern) {
			return true
		}
	}
	return false
}

// normalizeCIDR fills in the octets macOS leaves out ("192.168/16").
func normalizeCIDR(pattern string) string {
	address, bits, found := strings.Cut(pattern, "/")
	if !found || strings.Contains(address, ":") {
		return pattern
	}
	parts := strings.Split(address, ".")
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	return strings.Join(parts, ".") + "/" + bits
}
