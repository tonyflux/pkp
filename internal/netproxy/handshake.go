package netproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// A Test Connection has to say which half failed, so the user is not sent to fix
// an address when the proxy is fine and the destination is not.
var (
	// ErrProxyAuth is the proxy turning down the user name or password.
	ErrProxyAuth = errors.New("netproxy: the proxy refused the credentials")
	// ErrDestination is the proxy answering, but saying it cannot reach
	// the destination: the proxy itself works.
	ErrDestination = errors.New("netproxy: the proxy could not reach the destination")
)

// handshakeTimeout bounds one Test Connection leg when the caller set no deadline.
const handshakeTimeout = 10 * time.Second

// Handshake talks to the proxy itself and asks it for target ("host:port"),
// then hangs up without sending a byte of the real conversation. It is the
// first half of Test Connection: ErrProxyAuth and ErrDestination separate "the
// proxy is wrong" from "the proxy is fine, the far end is not".
func Handshake(ctx context.Context, proxy *url.URL, target string) error {
	if proxy == nil {
		return ErrNoEndpoint
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return fmt.Errorf("netproxy: %q is not a host and port: %w", target, err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, handshakeTimeout)
		defer cancel()
	}
	conn, err := dialProxy(ctx, proxy)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	switch strings.ToLower(proxy.Scheme) {
	case SOCKS5, "socks5h":
		return socksHandshake(conn, proxy, target)
	default:
		return connectHandshake(conn, proxy, target)
	}
}

// dialProxy reaches the proxy, wrapping the connection in TLS for an https
// proxy (the hop to the proxy is encrypted, the tunnel inside is the
// caller's business).
func dialProxy(ctx context.Context, proxy *url.URL) (net.Conn, error) {
	address := proxy.Host
	if proxy.Port() == "" {
		port := "80"
		switch strings.ToLower(proxy.Scheme) {
		case HTTPS:
			port = "443"
		case SOCKS5, "socks5h":
			port = "1080"
		}
		address = net.JoinHostPort(proxy.Hostname(), port)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("netproxy: cannot reach the proxy: %w", err)
	}
	if strings.ToLower(proxy.Scheme) != HTTPS {
		return conn, nil
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: proxy.Hostname()})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("netproxy: the proxy's TLS handshake failed: %w", err)
	}
	return tlsConn, nil
}

// connectHandshake asks an HTTP proxy to open a tunnel. 407 is the proxy
// turning the credentials down; a 502/503/504 is the proxy reporting on the
// far end, which is the second half of the test, not this one.
func connectHandshake(conn net.Conn, proxy *url.URL, target string) error {
	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target},
		Host:   target,
		Header: http.Header{},
	}
	if user := proxy.User; user != nil {
		password, _ := user.Password()
		credentials := user.Username() + ":" + password
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}
	if err := request.Write(conn); err != nil {
		return fmt.Errorf("netproxy: the proxy closed the connection: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return fmt.Errorf("netproxy: the proxy gave no usable answer: %w", err)
	}
	// The body is never read: a refusal often comes with a keep-alive
	// connection and no length, and waiting for its end would cost the
	// whole deadline.
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusProxyAuthRequired:
		return ErrProxyAuth
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: the proxy answered %s", ErrDestination, resp.Status)
	default:
		return fmt.Errorf("netproxy: the proxy answered %s", resp.Status)
	}
}

// socksHandshake runs RFC 1928's greeting, RFC 1929's login where the proxy
// asks for one, and the CONNECT request. The reply code says whether the
// proxy or the far end is the problem.
func socksHandshake(conn net.Conn, proxy *url.URL, target string) error {
	user, password := "", ""
	if proxy.User != nil {
		user = proxy.User.Username()
		password, _ = proxy.User.Password()
	}
	methods := []byte{0x00}
	if user != "" {
		methods = []byte{0x00, 0x02}
	}
	greeting := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("netproxy: the proxy closed the connection: %w", err)
	}
	reader := bufio.NewReader(conn)
	answer := make([]byte, 2)
	if _, err := io.ReadFull(reader, answer); err != nil {
		return fmt.Errorf("netproxy: the proxy gave no usable answer: %w", err)
	}
	if answer[0] != 0x05 {
		return fmt.Errorf("netproxy: the proxy does not speak SOCKS5")
	}
	switch answer[1] {
	case 0x00:
	case 0x02:
		if user == "" {
			return fmt.Errorf("%w: the proxy wants a user name and password", ErrProxyAuth)
		}
		login := []byte{0x01, byte(len(user))}
		login = append(login, user...)
		login = append(login, byte(len(password)))
		login = append(login, password...)
		if _, err := conn.Write(login); err != nil {
			return fmt.Errorf("netproxy: the proxy closed the connection: %w", err)
		}
		status := make([]byte, 2)
		if _, err := io.ReadFull(reader, status); err != nil {
			return fmt.Errorf("netproxy: the proxy gave no usable answer: %w", err)
		}
		if status[1] != 0x00 {
			return ErrProxyAuth
		}
	case 0xff:
		return fmt.Errorf("%w: the proxy accepted none of the login methods", ErrProxyAuth)
	default:
		return fmt.Errorf("netproxy: the proxy asked for SOCKS5 method %d, which PKP does not speak", answer[1])
	}

	host, portText, _ := net.SplitHostPort(target)
	port, _ := strconv.Atoi(portText)
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			request = append(request, 0x01)
			request = append(request, v4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("netproxy: %q is too long for SOCKS5", host)
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err := conn.Write(request); err != nil {
		return fmt.Errorf("netproxy: the proxy closed the connection: %w", err)
	}

	reply := make([]byte, 4)
	if _, err := io.ReadFull(reader, reply); err != nil {
		return fmt.Errorf("netproxy: the proxy gave no usable answer: %w", err)
	}
	switch reply[1] {
	case 0x00:
	case 0x02:
		return fmt.Errorf("%w: the proxy does not allow this connection", ErrProxyAuth)
	case 0x03, 0x04, 0x05:
		return fmt.Errorf("%w: SOCKS5 reply %d", ErrDestination, reply[1])
	default:
		return fmt.Errorf("%w: SOCKS5 reply %d", ErrDestination, reply[1])
	}
	// Drain the bound address so the proxy sees a clean close.
	switch reply[3] {
	case 0x01:
		_, _ = io.CopyN(io.Discard, reader, 4+2)
	case 0x03:
		length, err := reader.ReadByte()
		if err == nil {
			_, _ = io.CopyN(io.Discard, reader, int64(length)+2)
		}
	case 0x04:
		_, _ = io.CopyN(io.Discard, reader, 16+2)
	}
	return nil
}
