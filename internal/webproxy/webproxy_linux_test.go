//go:build linux && cgo && !gtk3

package webproxy

import (
	"slices"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/netproxy"
)

func TestLinuxBypassAlwaysPreservesLoopbackAndNetworks(t *testing.T) {
	got := linuxBypass([]string{"*.example.com", "10.0.0.0/8", "::1", "LOCALHOST"})
	for _, want := range []string{"localhost", "127.0.0.0/8", "::1", "*.example.com", "10.0.0.0/8"} {
		if !slices.Contains(got, want) {
			t.Errorf("bypass %v is missing %s", got, want)
		}
	}
	if len(got) != 5 {
		t.Errorf("bypass contains duplicate entries: %v", got)
	}
}

func TestLinuxApplyRejectsInvalidSettingsBeforeGTKAccess(t *testing.T) {
	for _, cfg := range []netproxy.Config{
		{Mode: "unknown"},
		{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: "proxy.example", Port: 0},
		{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: "proxy.example", Port: 8080, Bypass: []string{"good\x00bad"}},
	} {
		if err := Apply(nil, cfg); err == nil {
			t.Fatalf("Apply accepted invalid configuration: mode %q", cfg.Mode)
		}
	}
}

func TestLinuxApplyFailureDoesNotRevealCredentials(t *testing.T) {
	cfg := netproxy.Config{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: "proxy.example", Port: 8080, Username: "secret-user", Password: "secret-password"}
	err := Apply(nil, cfg)
	if err == nil {
		t.Fatal("Apply without a GTK window succeeded")
	}
	if strings.Contains(err.Error(), cfg.Username) || strings.Contains(err.Error(), cfg.Password) {
		t.Fatalf("Apply exposed credentials: %s", err)
	}
}
