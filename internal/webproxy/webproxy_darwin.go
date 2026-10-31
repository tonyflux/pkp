//go:build darwin

package webproxy

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit -framework Network -framework Security
#include <stdlib.h>
#include "webproxy_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// Supported reports whether this macOS can give the webview a proxy at all:
// the knob is macOS 14 and later, while the app itself still runs on 12.
// A false answer is not an error, it is a feature the machine lacks.
func Supported() bool { return C.pkp_webproxy_supported() != 0 }

// UnsupportedNote is what Settings · Network & Proxy says when Supported is false.
func UnsupportedNote() string {
	return i18n.T("这台电脑的系统版本无法让网页内容走代理，缩略图和头像会继续直连；需要 macOS 14 或更高版本。")
}

// Apply points the WKWebView inside nsWindow (an NSWindow, as Wails hands it
// over) at cfg, and may be called again whenever the user changes Settings: the
// new configuration replaces the old one. Loads already running keep their
// connection, everything after follows the new proxy.
//
// Both System and Direct clear the webview's own proxy: WebKit then does what it
// did before this package existed, which for System is to follow the computer's
// proxy settings by itself, and for Direct is to leave the few loads that this
// app sends through a proxy alone. Loopback — this process's stream server —
// is excluded in every manual mode, so the preview sheet keeps working when
// the proxy is down.
//
// It changes nothing and returns an error when the window has no webview
// yet, which is what the caller sees if it runs before the UI is up.
func Apply(nsWindow unsafe.Pointer, cfg netproxy.Config) error {
	if nsWindow == nil {
		return errors.New("webproxy: no window")
	}
	if !Supported() {
		return ErrUnsupported
	}
	cfg = cfg.Normalized()
	if err := cfg.Check(); err != nil {
		return err
	}

	var scheme, host, username, password string
	var port int
	if cfg.Mode == netproxy.ModeManual {
		// IPv6 is written [::1] in a URL, but the endpoint wants the address.
		scheme, host = cfg.Scheme, strings.Trim(cfg.Host, "[]")
		port, username, password = cfg.Port, cfg.Username, cfg.Password
	}

	cScheme, cHost := C.CString(scheme), C.CString(host)
	cUser, cPassword := C.CString(username), C.CString(password)
	defer func() {
		C.free(unsafe.Pointer(cScheme))
		C.free(unsafe.Pointer(cHost))
		C.free(unsafe.Pointer(cUser))
		C.free(unsafe.Pointer(cPassword))
	}()

	// The list is empty in every non-manual mode: WebKit then has no proxy
	// configuration of ours to hang exclusions on.
	var bypass []*C.char
	if cfg.Mode == netproxy.ModeManual {
		for _, entry := range suffixes(cfg.Bypass) {
			bypass = append(bypass, C.CString(entry))
		}
	}
	defer func() {
		for _, entry := range bypass {
			C.free(unsafe.Pointer(entry))
		}
	}()
	var first **C.char
	if len(bypass) > 0 {
		first = (**C.char)(unsafe.Pointer(&bypass[0]))
	}

	failure := C.pkp_webproxy_apply(nsWindow, cScheme, cHost, C.int(port), cUser, cPassword,
		first, C.int(len(bypass)))
	if failure == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(failure))
	return fmt.Errorf("webproxy: %s", C.GoString(failure))
}

// suffixes turns the user's Addresses That Bypass the Proxy list into what WebKit takes:
// host-name suffixes. "*.example.com" and ".example.com" are the same suffix
// to it, and a bare host name becomes one too — an exception for a host
// therefore also covers its subdomains, which is wider than the Go side's
// matching but never narrower, so nothing the user excluded can slip into
// the proxy. A network in CIDR form is dropped: WebKit matches names, not
// addresses, and a half-understood "192.168.0.0/16" would exclude nothing
// while looking as if it had.
func suffixes(bypass []string) []string {
	var out []string
	seen := make(map[string]bool, len(bypass))
	for _, entry := range bypass {
		entry = strings.ToLower(strings.TrimSpace(entry))
		entry = strings.TrimPrefix(entry, "*")
		entry = strings.TrimPrefix(entry, ".")
		if entry == "" || strings.Contains(entry, "/") || seen[entry] {
			continue
		}
		seen[entry] = true
		out = append(out, entry)
	}
	return out
}
