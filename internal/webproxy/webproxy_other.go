//go:build !darwin

package webproxy

import (
	"unsafe"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// Supported is false away from macOS: the webview there is not WebKit's, and
// nothing in this package would reach it.
func Supported() bool { return false }

// UnsupportedNote is what Settings · Network & Proxy says: the webview (WebView2 on
// Windows) goes by the system's own network settings.
func UnsupportedNote() string {
	return i18n.T("这个版本暂时无法让网页内容走这里的代理，缩略图和头像按系统的网络设置连接。")
}

// Apply does nothing but say so, so callers need no build tags of their own.
func Apply(nsWindow unsafe.Pointer, cfg netproxy.Config) error {
	_, _ = nsWindow, cfg
	return ErrUnsupported
}
