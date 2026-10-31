package download

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/tonyflux/pkp/internal/netproxy"
)

// newHTTPClient is the client downloads use. HTTP/1.1 only: HTTP/2 would
// multiplex a file's ranges over one TCP connection, which is what the
// threads are there to avoid. No transparent gzip either, or byte offsets
// would count compressed bytes. No overall timeout: a long range may take
// hours, and the stall watchdog catches a dead one. Every range goes
// through the app's Proxy setting, like the rest of the app's traffic.
func newHTTPClient() *http.Client {
	return &http.Client{Transport: netproxy.Install(&http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
	})}
}
