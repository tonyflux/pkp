//go:build linux && cgo && !gtk3

package webproxy

/*
#cgo pkg-config: gtk4 webkitgtk-6.0
#include <gtk/gtk.h>
#include <webkit/webkit.h>
#include <stdlib.h>

static WebKitWebView *pkp_find_webview(GtkWidget *widget) {
    if (WEBKIT_IS_WEB_VIEW(widget)) return WEBKIT_WEB_VIEW(widget);
    for (GtkWidget *child = gtk_widget_get_first_child(widget); child; child = gtk_widget_get_next_sibling(child)) {
        WebKitWebView *view = pkp_find_webview(child);
        if (view) return view;
    }
    return NULL;
}
static const char *pkp_webproxy_apply(void *window, int mode, const char *endpoint, const char *bypass) {
    // Do not inspect GTK objects off the UI thread, even to check their type.
    if (!g_main_context_is_owner(g_main_context_default())) return "GTK main thread is required";
    if (!window || !GTK_IS_WINDOW(window)) return "no GTK window";
    WebKitWebView *view = pkp_find_webview(GTK_WIDGET(window));
    if (!view) return "the GTK window has no WebKitWebView yet";
    WebKitNetworkSession *session = webkit_web_view_get_network_session(view);
    if (!session) return "the WebKitWebView has no network session";
    if (mode == 0) {
        webkit_network_session_set_proxy_settings(session, WEBKIT_NETWORK_PROXY_MODE_DEFAULT, NULL);
    } else if (mode == 1) {
        webkit_network_session_set_proxy_settings(session, WEBKIT_NETWORK_PROXY_MODE_NO_PROXY, NULL);
    } else {
        gchar **ignore = g_strsplit(bypass, "\n", -1);
        WebKitNetworkProxySettings *settings = webkit_network_proxy_settings_new(endpoint, (const gchar * const *)ignore);
		if (!settings) {
			g_strfreev(ignore);
			return "WebKit rejected the proxy settings";
		}
        webkit_network_session_set_proxy_settings(session, WEBKIT_NETWORK_PROXY_MODE_CUSTOM, settings);
        webkit_network_proxy_settings_free(settings);
        g_strfreev(ignore);
    }
    return NULL;
}
*/
import "C"

import (
	"errors"
	"strings"
	"unsafe"

	"github.com/tonyflux/pkp/internal/netproxy"
)

func Supported() bool { return true }

func UnsupportedNote() string { return "" }

// Apply must run on GTK's main thread; callers use application.InvokeAsync.
// WebKit applies settings per network session, which may be shared by windows.
func Apply(window unsafe.Pointer, cfg netproxy.Config) error {
	cfg = cfg.Normalized()
	if err := cfg.Check(); err != nil {
		return err
	}
	for _, entry := range cfg.Bypass {
		if strings.IndexByte(entry, 0) >= 0 {
			return errors.New("webproxy: bypass entries cannot contain NUL")
		}
	}
	mode := C.int(0)
	endpoint := ""
	if cfg.Mode == netproxy.ModeDirect {
		mode = 1
	} else if cfg.Mode == netproxy.ModeManual {
		mode = 2
		endpoint = cfg.Endpoint().String()
	}
	cEndpoint := C.CString(endpoint)
	cBypass := C.CString(strings.Join(linuxBypass(cfg.Bypass), "\n"))
	defer C.free(unsafe.Pointer(cEndpoint))
	defer C.free(unsafe.Pointer(cBypass))
	if failure := C.pkp_webproxy_apply(window, mode, cEndpoint, cBypass); failure != nil {
		// Native failures never contain endpoint URLs or proxy credentials.
		return errors.New("webproxy: " + C.GoString(failure))
	}
	return nil
}

func linuxBypass(entries []string) []string {
	// Include the entire loopback ranges, not just the local stream's current IP.
	return netproxy.ParseBypass("localhost,127.0.0.0/8,::1," + strings.Join(entries, ","))
}
