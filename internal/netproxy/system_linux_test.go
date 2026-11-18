//go:build linux && cgo

package netproxy

import "testing"

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
