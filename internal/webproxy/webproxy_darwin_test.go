//go:build darwin

package webproxy

import (
	"slices"
	"testing"

	"github.com/tonyflux/pkp/internal/netproxy"
)

// Only the parts that hold without a window server are tested here. That a
// WKWebView really follows the configuration — the page's pictures through
// the proxy, 127.0.0.1 around it — was proven with an offscreen window in a
// throwaway program, because `go test` has no window server to host one.

func TestSuffixes(t *testing.T) {
	cases := []struct {
		name  string
		given []string
		want  []string
	}{
		{"wildcard and leading dot are the same suffix", []string{"*.example.com", ".example.com"}, []string{"example.com"}},
		{"a bare host stays itself", []string{"cdn.pikpak.com"}, []string{"cdn.pikpak.com"}},
		{"case and spaces do not make a second entry", []string{" CDN.Example.COM ", "cdn.example.com"}, []string{"cdn.example.com"}},
		{"a network is dropped: WebKit matches names, not addresses", []string{"192.168.0.0/16", "10.0.0.0/8"}, nil},
		{"a plain address is kept: WebKit does honour IP literals", []string{"203.0.113.9"}, []string{"203.0.113.9"}},
		{"nothing in, nothing out", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := suffixes(c.given); !slices.Equal(got, c.want) {
				t.Fatalf("suffixes(%q) = %q, want %q", c.given, got, c.want)
			}
		})
	}
}

// Apply is called from the settings page, which may run before the window is
// up; it has to say so rather than reach for a window that is not there.
func TestApplyWithoutWindow(t *testing.T) {
	if err := Apply(nil, netproxy.Config{Mode: netproxy.ModeDirect}); err == nil {
		t.Fatal("Apply(nil, ...) must return an error")
	}
}
