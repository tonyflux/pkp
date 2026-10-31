package netproxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"time"
)

type Measurement struct {
	Reachable bool   `json:"reachable"`
	Status    int    `json:"status"`
	ElapsedMs int64  `json:"elapsedMs"`
	Node      string `json:"node"`
}

// Measure verifies TLS and measures time to response headers, not bandwidth.
// Credentials are never attached; HTTP errors still prove network reachability.
func Measure(ctx context.Context, cfg Config, target string) Measurement {
	t := &http.Transport{Proxy: func(r *http.Request) (*url.URL, error) { return cfg.ProxyFor(r.URL) }, DialContext: nodeDial((&net.Dialer{Timeout: 5 * time.Second}).DialContext), TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}
	installNodeTLS(t)
	defer t.CloseIdleConnections()
	result := Measurement{}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return result
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { result.Node = info.Conn.RemoteAddr().String() }}))
	start := time.Now()
	resp, err := (&http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	result.ElapsedMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Node = ""
		return result
	}
	resp.Body.Close()
	result.Reachable = true
	result.Status = resp.StatusCode
	return result
}
