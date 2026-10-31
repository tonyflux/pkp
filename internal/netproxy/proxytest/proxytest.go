// Package proxytest runs throwaway HTTP and SOCKS5 proxies for tests that
// have to prove a request really went through the proxy and not around it.
// Every proxy records the hosts it was asked for.
package proxytest

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Proxy is a running proxy: Addr is its 127.0.0.1 address and Seen lists the
// "host:port" targets it was asked to reach, oldest first.
type Proxy struct {
	Addr string

	mu     sync.Mutex
	seen   []string
	routes map[string]string
	closed bool
	ln     net.Listener
}

// Route makes the proxy dial addr whenever it is asked for target, so a
// test can use a host name that is not loopback (and so is proxied) without
// needing it in DNS.
func (p *Proxy) Route(target, addr string) {
	p.mu.Lock()
	if p.routes == nil {
		p.routes = map[string]string{}
	}
	p.routes[target] = addr
	p.mu.Unlock()
}

// dial reaches target, honouring the routes a test set.
func (p *Proxy) dial(ctx context.Context, target string) (net.Conn, error) {
	p.mu.Lock()
	if addr, ok := p.routes[target]; ok {
		target = addr
	}
	p.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, "tcp", target)
}

func (p *Proxy) note(target string) {
	p.mu.Lock()
	p.seen = append(p.seen, target)
	p.mu.Unlock()
}

// Seen is the targets so far.
func (p *Proxy) Seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// Saw reports whether the proxy was asked for a target with this host.
func (p *Proxy) Saw(host string) bool {
	for _, target := range p.Seen() {
		if target == host {
			return true
		}
		if h, _, err := net.SplitHostPort(target); err == nil && h == host {
			return true
		}
	}
	return false
}

// URL is the proxy as a settings value, e.g. "http://127.0.0.1:52001".
func (p *Proxy) URL(scheme string) string { return scheme + "://" + p.Addr }

// Host and Port split Addr for a netproxy.Config.
func (p *Proxy) Host() string {
	host, _, _ := net.SplitHostPort(p.Addr)
	return host
}

func (p *Proxy) Port() int {
	_, port, _ := net.SplitHostPort(p.Addr)
	n, _ := strconv.Atoi(port)
	return n
}

func (p *Proxy) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	_ = p.ln.Close()
}

func (p *Proxy) stopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// HTTP starts an HTTP proxy that serves both CONNECT tunnels and
// absolute-form plain requests. It stops when the test ends.
func HTTP(t *testing.T) *Proxy {
	t.Helper()
	return HTTPWithAuth(t, "", "")
}

// HTTPWithAuth is HTTP, demanding Basic credentials when user is not empty.
func HTTPWithAuth(t *testing.T, user, password string) *Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxytest: listen: %v", err)
	}
	p := &Proxy{Addr: ln.Addr().String(), ln: ln}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != "" {
			gotUser, gotPassword, ok := parseProxyAuth(r.Header.Get("Proxy-Authorization"))
			if !ok || gotUser != user || gotPassword != password {
				w.Header().Set("Proxy-Authenticate", `Basic realm="proxytest"`)
				w.WriteHeader(http.StatusProxyAuthRequired)
				return
			}
		}
		if r.Method == http.MethodConnect {
			p.note(r.Host)
			tunnel(p, w, r)
			return
		}
		if !r.URL.IsAbs() {
			http.Error(w, "proxytest: not a proxy request", http.StatusBadRequest)
			return
		}
		p.note(r.URL.Host)
		forward(p, w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); p.close() })
	return p
}

func parseProxyAuth(header string) (string, string, bool) {
	request := &http.Request{Header: http.Header{"Authorization": {header}}}
	return request.BasicAuth()
}

func tunnel(p *Proxy, w http.ResponseWriter, r *http.Request) {
	upstream, err := p.dial(r.Context(), r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxytest: no hijacker", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(upstream, buffered) }()
	go func() { defer wg.Done(); _, _ = io.Copy(client, upstream) }()
	wg.Wait()
}

func forward(p *Proxy, w http.ResponseWriter, r *http.Request) {
	outbound, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for name, values := range r.Header {
		if strings.EqualFold(name, "Proxy-Authorization") || strings.EqualFold(name, "Proxy-Connection") {
			continue
		}
		for _, value := range values {
			outbound.Header.Add(name, value)
		}
	}
	resp, err := (&http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return p.dial(ctx, addr) }}}).Do(outbound)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// SOCKS5 starts a SOCKS5 proxy. It stops when the test ends.
func SOCKS5(t *testing.T) *Proxy {
	t.Helper()
	return SOCKS5WithAuth(t, "", "")
}

// SOCKS5WithAuth is SOCKS5, demanding the username/password method (RFC
// 1929) when user is not empty.
func SOCKS5WithAuth(t *testing.T, user, password string) *Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxytest: listen: %v", err)
	}
	p := &Proxy{Addr: ln.Addr().String(), ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := socksServe(p, conn, user, password); err != nil && !p.stopped() {
					t.Logf("proxytest: socks5: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(p.close)
	return p
}

func socksServe(p *Proxy, conn net.Conn, user, password string) error {
	reader := bufio.NewReader(conn)
	version, err := reader.ReadByte()
	if err != nil {
		return err
	}
	if version != 5 {
		return fmt.Errorf("version %d", version)
	}
	count, err := reader.ReadByte()
	if err != nil {
		return err
	}
	methods := make([]byte, count)
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	wanted := byte(0x00)
	if user != "" {
		wanted = 0x02
	}
	if !strings.ContainsRune(string(methods), rune(wanted)) {
		_, _ = conn.Write([]byte{5, 0xff})
		return errors.New("no acceptable method")
	}
	if _, err := conn.Write([]byte{5, wanted}); err != nil {
		return err
	}
	if wanted == 0x02 {
		if err := socksAuth(reader, conn, user, password); err != nil {
			return err
		}
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != 5 || header[1] != 1 {
		return fmt.Errorf("command %d", header[1])
	}
	var host string
	switch header[3] {
	case 1:
		address := make([]byte, 4)
		if _, err := io.ReadFull(reader, address); err != nil {
			return err
		}
		host = net.IP(address).String()
	case 3:
		length, err := reader.ReadByte()
		if err != nil {
			return err
		}
		name := make([]byte, length)
		if _, err := io.ReadFull(reader, name); err != nil {
			return err
		}
		host = string(name)
	case 4:
		address := make([]byte, 16)
		if _, err := io.ReadFull(reader, address); err != nil {
			return err
		}
		host = net.IP(address).String()
	default:
		return fmt.Errorf("address type %d", header[3])
	}
	var port uint16
	if err := binary.Read(reader, binary.BigEndian, &port); err != nil {
		return err
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	p.note(target)

	upstream, err := p.dial(context.Background(), target)
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return err
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(upstream, reader) }()
	go func() { defer wg.Done(); _, _ = io.Copy(conn, upstream) }()
	wg.Wait()
	return nil
}

func socksAuth(reader *bufio.Reader, conn net.Conn, user, password string) error {
	version, err := reader.ReadByte()
	if err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("auth version %d", version)
	}
	read := func() (string, error) {
		length, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		value := make([]byte, length)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", err
		}
		return string(value), nil
	}
	gotUser, err := read()
	if err != nil {
		return err
	}
	gotPassword, err := read()
	if err != nil {
		return err
	}
	if gotUser != user || gotPassword != password {
		_, _ = conn.Write([]byte{1, 1})
		return errors.New("bad credentials")
	}
	_, err = conn.Write([]byte{1, 0})
	return err
}
