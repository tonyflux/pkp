package netproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestTLSNodeSelectionPreservesHostnameAndCustomDialer(t *testing.T) {
	var sawHost atomic.Bool
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost.Store(r.Host == "example.com")
		w.WriteHeader(204)
	}))
	defer origin.Close()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	transport := Install(&http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	}})
	defer transport.CloseIdleConnections()
	original := Current()
	defer Set(original)
	if err := Set(Config{Mode: ModeDirect}); err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 || !sawHost.Load() {
		t.Fatal("TLS selection changed HTTP authority")
	}
}

func TestTLSNodeSelectionRejectsWrongCertificate(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("request sent despite wrong certificate") }))
	defer origin.Close()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	transport := Install(&http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	}})
	defer transport.CloseIdleConnections()
	original := Current()
	defer Set(original)
	Set(Config{Mode: ModeDirect})
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get("https://wrong.invalid")
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("hostname validation bypassed")
	}
}
