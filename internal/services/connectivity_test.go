package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/tonyflux/pkp/internal/netproxy/proxytest"
)

func TestConnectivityUsesAppliedProxyAndKeepsServiceFailuresDistinct(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Errorf("unexpected method %s", r.Method)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("probe sent account credentials")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer origin.Close()
	proxy := proxytest.HTTP(t)
	for _, host := range []string{"api-drive.mypikpak.com:80", "user.mypikpak.com:80", "api-dav.mypikpak.com:443"} {
		proxy.Route(host, strings.TrimPrefix(origin.URL, "http://"))
	}
	svc := newProxyService(t, &memoryVault{}, "http://api-drive.mypikpak.com")
	svc.opts.AuthBaseURL = "http://user.mypikpak.com"
	if err := netproxy.Set(netproxy.Config{Mode: netproxy.ModeManual, Scheme: netproxy.HTTP, Host: proxy.Host(), Port: proxy.Port()}); err != nil {
		t.Fatal(err)
	}
	result := svc.checkConnectivity(context.Background())
	if len(result.Targets) != 3 || !result.Targets[0].Reachable || result.Targets[0].Status != 401 {
		t.Fatalf("API rejection is not a network failure: %+v", result)
	}
	if !proxy.Saw("api-drive.mypikpak.com") || !proxy.Saw("user.mypikpak.com") {
		t.Fatal("probe bypassed configured proxy")
	}
	// The TLS service cannot be served by this HTTP origin and must fail securely.
	if result.Targets[2].Reachable || !result.Poor {
		t.Fatalf("TLS failure hidden: %+v", result)
	}
	saved := svc.Connectivity()
	saved.Targets[0].Host = "changed"
	if svc.Connectivity().Targets[0].Host == "changed" {
		t.Fatal("snapshot aliases internal state")
	}
}
