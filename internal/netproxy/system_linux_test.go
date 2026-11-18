//go:build linux && cgo

package netproxy

import (
	"net/url"
	"testing"
	"time"
)

func TestLinuxProxyEndpointValidatesDesktopValues(t *testing.T) {
	for _, tc := range []struct {
		host, port, want string
	}{
		{"proxy.example", "8080", "http://proxy.example:8080"},
		{"[::1]", "1080", "http://[::1]:1080"},
		{"", "80", ""},
		{"proxy.example", "0", ""},
		{"proxy.example", "65536", ""},
		{"user:password@proxy.example", "8080", ""},
		{"proxy.example/path", "8080", ""},
		{"proxy.example\n", "80", "http://proxy.example:80"},
	} {
		u := linuxProxyEndpoint("http", tc.host, tc.port)
		got := ""
		if u != nil {
			got = u.String()
			if u.User != nil {
				t.Fatal("desktop host unexpectedly became URL credentials")
			}
		}
		if got != tc.want {
			t.Errorf("endpoint(%q, %q) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestLinuxProxyFallbackAndRouting(t *testing.T) {
	httpProxy := linuxProxyEndpoint("http", "http.proxy", "8080")
	httpProxy.User = url.UserPassword("user", "password")
	httpsProxy := linuxProxyEndpoint("http", "https.proxy", "8443")
	socksProxy := linuxProxyEndpoint("socks5", "socks.proxy", "1080")

	systemMu.Lock()
	oldInfo, oldAt := systemInfo, systemAt
	systemMu.Unlock()
	t.Cleanup(func() {
		systemMu.Lock()
		systemInfo, systemAt = oldInfo, oldAt
		systemMu.Unlock()
	})

	for _, tc := range []struct {
		name                string
		http, https, socks  *url.URL
		wantHTTP, wantHTTPS *url.URL
	}{
		{"HTTPS only", nil, httpsProxy, nil, nil, httpsProxy},
		{"HTTP only", httpProxy, nil, nil, httpProxy, httpProxy},
		{"separate proxies", httpProxy, httpsProxy, nil, httpProxy, httpsProxy},
		{"SOCKS only", nil, nil, socksProxy, socksProxy, socksProxy},
		{"HTTPS and SOCKS", nil, httpsProxy, socksProxy, socksProxy, httpsProxy},
		{"HTTP and SOCKS", httpProxy, nil, socksProxy, httpProxy, httpProxy},
		{"all proxies", httpProxy, httpsProxy, socksProxy, httpProxy, httpsProxy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := SystemInfo{HTTP: tc.http, HTTPS: tc.https, SOCKS: tc.socks, Exceptions: []string{"*.internal"}}
			linuxProxyFallback(&info)
			if info.HTTP != tc.http || info.SOCKS != tc.socks || (tc.https != nil && info.HTTPS != tc.https) {
				t.Fatalf("fallback changed configured proxies: %+v", info)
			}
			systemMu.Lock()
			systemInfo, systemAt = info, time.Now()
			systemMu.Unlock()
			for _, route := range []struct {
				scheme string
				want   *url.URL
			}{{"http", tc.wantHTTP}, {"https", tc.wantHTTPS}} {
				// An unconfigured protocol falls back to the process environment.
				if route.want == nil {
					continue
				}
				got, err := systemProxyFor(&url.URL{Scheme: route.scheme, Host: "example.com"})
				if err != nil || got != route.want {
					t.Errorf("%s proxy = %v, %v; want %v", route.scheme, got, err, route.want)
				}
				got, err = systemProxyFor(&url.URL{Scheme: route.scheme, Host: "host.internal"})
				if err != nil || got != nil {
					t.Errorf("%s exception = %v, %v; want direct", route.scheme, got, err)
				}
			}
		})
	}
}
