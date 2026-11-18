//go:build linux && cgo

package netproxy

/*
#cgo pkg-config: gio-2.0
#include <gio/gio.h>
#include <stdlib.h>

static GSettings *pkp_proxy_settings(const char *name) {
    GSettingsSchemaSource *source = g_settings_schema_source_get_default();
    if (!source) return NULL;
    GSettingsSchema *schema = g_settings_schema_source_lookup(source, name, TRUE);
    if (!schema) return NULL;
    GSettings *settings = g_settings_new_full(schema, NULL, NULL);
    g_settings_schema_unref(schema);
    return settings;
}
static char *pkp_proxy_value(GSettings *settings, const char *key) {
    GSettingsSchema *schema = NULL;
    g_object_get(settings, "settings-schema", &schema, NULL);
    gboolean exists = g_settings_schema_has_key(schema, key);
    g_settings_schema_unref(schema);
    if (!exists) return NULL;
    GVariant *value = g_settings_get_value(settings, key);
    char *text = NULL;
    if (g_variant_is_of_type(value, G_VARIANT_TYPE_STRING)) text = g_variant_dup_string(value, NULL);
    else if (g_variant_is_of_type(value, G_VARIANT_TYPE_INT32)) text = g_strdup_printf("%d", g_variant_get_int32(value));
    else if (g_variant_is_of_type(value, G_VARIANT_TYPE_BOOLEAN)) text = g_strdup(g_variant_get_boolean(value) ? "true" : "false");
    else if (g_variant_is_of_type(value, G_VARIANT_TYPE_STRING_ARRAY)) {
        gchar **items = g_variant_dup_strv(value, NULL);
        text = g_strjoinv("\n", items);
        g_strfreev(items);
    }
    g_variant_unref(value);
    return text;
}
*/
import "C"

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"unsafe"
)

func linuxSettings(name string) *C.GSettings {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	return C.pkp_proxy_settings(cName)
}

func linuxProxyValue(settings *C.GSettings, key string) string {
	if settings == nil {
		return ""
	}
	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))
	value := C.pkp_proxy_value(settings, cKey)
	if value == nil {
		return ""
	}
	defer C.g_free(C.gpointer(value))
	return C.GoString(value)
}

func linuxProxyEndpoint(scheme, host, port string) *url.URL {
	host = strings.TrimSpace(host)
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || host == "" || strings.ContainsAny(host, "/@?#\r\n\x00") {
		return nil
	}
	return &url.URL{Scheme: scheme, Host: net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(n))}
}

func readSystemProxy() (SystemInfo, bool) {
	settings := linuxSettings("org.gnome.system.proxy")
	if settings == nil {
		return SystemInfo{}, false
	}
	defer C.g_object_unref(C.gpointer(settings))
	var info SystemInfo
	for _, host := range strings.Split(linuxProxyValue(settings, "ignore-hosts"), "\n") {
		if host != "" {
			info.Exceptions = append(info.Exceptions, host)
		}
	}
	switch linuxProxyValue(settings, "mode") {
	case "auto":
		info.PAC = linuxProxyValue(settings, "autoconfig-url")
		return info, true
	case "manual":
	default:
		return SystemInfo{}, false
	}
	for _, protocol := range []string{"http", "https", "socks"} {
		child := linuxSettings("org.gnome.system.proxy." + protocol)
		if child == nil {
			continue
		}
		scheme := "http"
		if protocol == "socks" {
			scheme = "socks5"
		}
		endpoint := linuxProxyEndpoint(scheme, linuxProxyValue(child, "host"), linuxProxyValue(child, "port"))
		if endpoint != nil && protocol == "http" && linuxProxyValue(child, "use-authentication") == "true" {
			endpoint.User = url.UserPassword(linuxProxyValue(child, "authentication-user"), linuxProxyValue(child, "authentication-password"))
		}
		C.g_object_unref(C.gpointer(child))
		switch protocol {
		case "http":
			info.HTTP = endpoint
		case "https":
			info.HTTPS = endpoint
		case "socks":
			info.SOCKS = endpoint
		}
	}
	if linuxProxyValue(settings, "use-same-proxy") == "true" {
		info.HTTPS = info.HTTP
	}
	return info, true
}
