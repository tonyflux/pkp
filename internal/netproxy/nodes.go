package netproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Only these service hosts share the automatic DNS-node selection policy.
// Hostnames, TLS SNI, certificate checks and HTTP Host remain unchanged.
var serviceHosts = map[string]bool{
	"api-drive.mypikpak.com": true,
	"user.mypikpak.com":      true,
	"api-dav.mypikpak.com":   true,
}

type nodeChoice struct {
	address string
	until   time.Time
}

var nodes = struct {
	sync.Mutex
	chosen map[string]nodeChoice
}{chosen: make(map[string]nodeChoice)}

func resetNodes() {
	nodes.Lock()
	clear(nodes.chosen)
	nodes.Unlock()
}

type dialFunc func(context.Context, string, string) (net.Conn, error)

// nodeDial races TCP handshakes, never HTTP requests: even writes are sent once.
// Proxy endpoints and all unrelated hosts retain the transport's original dialer.
func nodeDial(dial dialFunc) dialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !serviceHosts[strings.ToLower(host)] || network != "tcp" {
			return dial(ctx, network, address)
		}
		nodes.Lock()
		choice := nodes.chosen[address]
		nodes.Unlock()
		if choice.address != "" && time.Now().Before(choice.until) {
			// A dead cached node must not consume the caller's entire deadline.
			attempt, cancel := context.WithTimeout(ctx, time.Second)
			conn, err := dial(attempt, network, choice.address)
			cancel()
			if err == nil {
				return conn, nil
			}
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		addresses := make([]string, 0, len(ips))
		for _, ip := range ips {
			addresses = append(addresses, net.JoinHostPort(ip.String(), port))
		}
		conn, best, err := raceNodes(ctx, network, addresses, dial)
		if err == nil {
			nodes.Lock()
			nodes.chosen[address] = nodeChoice{address: best, until: time.Now().Add(5 * time.Minute)}
			nodes.Unlock()
		}
		return conn, err
	}
}

// HTTPS races include certificate validation: a fast but broken TLS endpoint loses.
func installNodeTLS(t *http.Transport) {
	if t.DialTLSContext != nil {
		return
	}
	t.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		config := t.TLSClientConfig
		if config == nil {
			config = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			config = config.Clone()
		}
		if config.ServerName == "" {
			config.ServerName = host
		}
		connect := func(ctx context.Context, network, address string) (net.Conn, error) {
			raw, err := t.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			conn := tls.Client(raw, config)
			if err := conn.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, err
			}
			return conn, nil
		}
		deadline := t.TLSHandshakeTimeout
		if deadline <= 0 {
			deadline = 10 * time.Second
		}
		ctx, cancel := context.WithTimeout(ctx, deadline)
		defer cancel()
		return nodeDial(connect)(ctx, network, address)
	}
}

func raceNodes(ctx context.Context, network string, addresses []string, dial dialFunc) (net.Conn, string, error) {
	if len(addresses) == 0 {
		return nil, "", errors.New("no DNS addresses")
	}
	// Bound parallel connections without discarding fallback addresses.
	slots := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(ctx)
	type result struct {
		conn    net.Conn
		address string
		err     error
	}
	results := make(chan result)
	for _, address := range addresses {
		go func() {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			conn, err := dial(ctx, network, address)
			select {
			case results <- result{conn: conn, address: address, err: err}:
			case <-ctx.Done():
				if conn != nil {
					conn.Close()
				}
			}
		}()
	}
	defer cancel()
	var last error
	for range addresses {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case r := <-results:
			if r.err == nil {
				return r.conn, r.address, nil
			}
			last = r.err
		}
	}
	return nil, "", last
}
