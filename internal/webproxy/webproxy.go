// Package webproxy points the window's WKWebView at the proxy the user
// chose in Settings · Proxy. The Go side's netproxy only reaches connections this
// process makes itself; the webview does its own networking for every image
// the page asks for — PikPak's thumbnail and avatar CDNs — so without
// this the pictures are the one thing a VPN-less user still cannot load.
//
// WebKit takes a proxy per WKWebsiteDataStore (macOS 14 and later), built
// from Network.framework's proxy configuration objects. Older systems have
// no such knob at all, which is what Supported reports.
package webproxy

import "errors"

// ErrUnsupported says this build, or this macOS, has no way to give the
// webview a proxy of its own. The caller treats it as "images keep going
// direct", not as a failure of the user's setting.
var ErrUnsupported = errors.New("webproxy: this system cannot give the webview its own proxy (macOS 14 or later is required)")
