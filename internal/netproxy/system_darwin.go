package netproxy

/*
#cgo LDFLAGS: -framework CoreFoundation -framework CFNetwork
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <CFNetwork/CFNetwork.h>

// appendProxy writes "<label>=<host>:<port>\n" for a proxy macOS has
// switched on, and nothing for one it has not.
static void pkp_append_proxy(CFMutableStringRef out, CFDictionaryRef settings,
                             CFStringRef enableKey, CFStringRef hostKey,
                             CFStringRef portKey, CFStringRef label) {
	CFNumberRef enabled = CFDictionaryGetValue(settings, enableKey);
	int on = 0;
	if (enabled == NULL || CFGetTypeID(enabled) != CFNumberGetTypeID() ||
	    !CFNumberGetValue(enabled, kCFNumberIntType, &on) || on == 0) {
		return;
	}
	CFStringRef host = CFDictionaryGetValue(settings, hostKey);
	if (host == NULL || CFGetTypeID(host) != CFStringGetTypeID() || CFStringGetLength(host) == 0) {
		return;
	}
	int port = 0;
	CFNumberRef portValue = CFDictionaryGetValue(settings, portKey);
	if (portValue != NULL && CFGetTypeID(portValue) == CFNumberGetTypeID()) {
		CFNumberGetValue(portValue, kCFNumberIntType, &port);
	}
	CFStringAppend(out, label);
	CFStringAppend(out, CFSTR("="));
	CFStringAppend(out, host);
	CFStringAppendFormat(out, NULL, CFSTR(":%d\n"), port);
}

// pkp_system_proxy renders the computer's proxy settings as lines the Go
// side parses: http=, https=, socks=, pac= and one ex= per exception. The
// caller frees the result. NULL means the settings could not be read.
static char *pkp_system_proxy(void) {
	CFDictionaryRef settings = CFNetworkCopySystemProxySettings();
	if (settings == NULL) {
		return NULL;
	}
	CFMutableStringRef out = CFStringCreateMutable(NULL, 0);
	pkp_append_proxy(out, settings, kCFNetworkProxiesHTTPEnable, kCFNetworkProxiesHTTPProxy,
	                 kCFNetworkProxiesHTTPPort, CFSTR("http"));
	pkp_append_proxy(out, settings, kCFNetworkProxiesHTTPSEnable, kCFNetworkProxiesHTTPSProxy,
	                 kCFNetworkProxiesHTTPSPort, CFSTR("https"));
	pkp_append_proxy(out, settings, kCFNetworkProxiesSOCKSEnable, kCFNetworkProxiesSOCKSProxy,
	                 kCFNetworkProxiesSOCKSPort, CFSTR("socks"));

	CFNumberRef pacEnabled = CFDictionaryGetValue(settings, kCFNetworkProxiesProxyAutoConfigEnable);
	int pacOn = 0;
	if (pacEnabled != NULL && CFGetTypeID(pacEnabled) == CFNumberGetTypeID()) {
		CFNumberGetValue(pacEnabled, kCFNumberIntType, &pacOn);
	}
	if (pacOn != 0) {
		CFStringRef pac = CFDictionaryGetValue(settings, kCFNetworkProxiesProxyAutoConfigURLString);
		CFStringAppend(out, CFSTR("pac="));
		if (pac != NULL && CFGetTypeID(pac) == CFStringGetTypeID()) {
			CFStringAppend(out, pac);
		}
		CFStringAppend(out, CFSTR("\n"));
	}

	CFArrayRef exceptions = CFDictionaryGetValue(settings, kCFNetworkProxiesExceptionsList);
	if (exceptions != NULL && CFGetTypeID(exceptions) == CFArrayGetTypeID()) {
		CFIndex count = CFArrayGetCount(exceptions);
		for (CFIndex i = 0; i < count; i++) {
			CFStringRef item = CFArrayGetValueAtIndex(exceptions, i);
			if (item == NULL || CFGetTypeID(item) != CFStringGetTypeID()) {
				continue;
			}
			CFStringAppend(out, CFSTR("ex="));
			CFStringAppend(out, item);
			CFStringAppend(out, CFSTR("\n"));
		}
	}

	CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength(out), kCFStringEncodingUTF8) + 1;
	char *buffer = malloc((size_t)size);
	if (buffer != NULL && !CFStringGetCString(out, buffer, size, kCFStringEncodingUTF8)) {
		free(buffer);
		buffer = NULL;
	}
	CFRelease(out);
	CFRelease(settings);
	return buffer;
}
*/
import "C"

import (
	"net/url"
	"strings"
	"unsafe"
)

// readSystemProxy reads macOS System Settings · Network · Proxies, which is what a
// VPN or a rule-based client such as Clash writes when it takes the system
// proxy over. Anything it cannot read reads as "nothing set".
func readSystemProxy() (SystemInfo, bool) {
	raw := C.pkp_system_proxy()
	if raw == nil {
		return SystemInfo{}, false
	}
	defer C.free(unsafe.Pointer(raw))

	var info SystemInfo
	for _, line := range strings.Split(C.GoString(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || value == "" && key != "pac" {
			continue
		}
		switch key {
		case "http":
			info.HTTP = endpointOrNil("http://" + value)
		case "https":
			// macOS names the proxy for TLS traffic; it is reached over
			// plain HTTP CONNECT unless it says otherwise.
			info.HTTPS = endpointOrNil("http://" + value)
		case "socks":
			info.SOCKS = endpointOrNil("socks5://" + value)
		case "pac":
			info.PAC = value
		case "ex":
			info.Exceptions = append(info.Exceptions, value)
		}
	}
	return info, true
}

// endpointOrNil parses a "scheme://host:port" line, dropping one without a
// usable port (macOS leaves 0 behind when a proxy is half-configured).
func endpointOrNil(raw string) *url.URL {
	u, err := parseEndpoint(raw)
	if err != nil || u.Port() == "" || u.Port() == "0" {
		return nil
	}
	return u
}
