#import <Cocoa/Cocoa.h>
#import <Network/Network.h>
#import <Security/SecProtocolOptions.h>
#import <WebKit/WebKit.h>

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "webproxy_darwin.h"

// The app is built for macOS 12, where neither nw_proxy_config_* nor
// -[WKWebsiteDataStore setProxyConfigurations:] exists; both arrived in
// macOS 14. The Network.framework calls are weak-imported at that deployment
// target, so on an older system they are simply null and calling one would
// kill the process. dlsym answers for the machine actually running this copy,
// and the selector check covers the WebKit half separately — a system could
// in principle have one and not the other.
static int pkp_webproxy_available(void) {
	static int available = 0;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
	  if (@available(macOS 14.0, *)) {
		  if (dlsym(RTLD_DEFAULT, "nw_proxy_config_create_http_connect") != NULL &&
		      dlsym(RTLD_DEFAULT, "nw_proxy_config_create_socksv5") != NULL &&
		      dlsym(RTLD_DEFAULT, "nw_proxy_config_add_excluded_domain") != NULL &&
		      [WKWebsiteDataStore instancesRespondToSelector:@selector(setProxyConfigurations:)]) {
			  available = 1;
		  }
	  }
	});
	return available;
}

int pkp_webproxy_supported(void) { return pkp_webproxy_available(); }

// Wails hangs the WKWebView off the window's content view, but a translucent
// window puts an NSVisualEffectView in between, and a future layout could add
// another wrapper; the search goes all the way down rather than depending on
// the depth. The class is looked up by name so this file does not care which
// WebKit the host app linked against.
static WKWebView *pkp_find_webview(NSView *view) {
	Class wk = NSClassFromString(@"WKWebView");
	if (wk == nil) {
		return nil;
	}
	for (NSView *sub in view.subviews) {
		if ([sub isKindOfClass:wk]) {
			return (WKWebView *)sub;
		}
		WKWebView *found = pkp_find_webview(sub);
		if (found != nil) {
			return found;
		}
	}
	return nil;
}

// pkp_build turns the user's settings into one proxy configuration, or
// leaves *failure with the reason it could not. Callers have already checked
// that this macOS has the symbols.
API_AVAILABLE(macos(14.0))
static nw_proxy_config_t pkp_build(const char *scheme, const char *host, int port,
                                   const char *username, const char *password,
                                   const char *const *bypass, int bypassCount,
                                   NSString **failure) {
	char service[8];
	snprintf(service, sizeof(service), "%d", port);
	nw_endpoint_t endpoint = nw_endpoint_create_host(host, service);
	if (endpoint == NULL) {
		*failure = [NSString stringWithFormat:@"%s:%d is not a usable proxy address", host, port];
		return NULL;
	}

	nw_proxy_config_t config = NULL;
	if (strcmp(scheme, "socks5") == 0) {
		config = nw_proxy_config_create_socksv5(endpoint);
	} else if (strcmp(scheme, "http") == 0) {
		config = nw_proxy_config_create_http_connect(endpoint, NULL);
	} else if (strcmp(scheme, "https") == 0) {
		// An https proxy is the same HTTP CONNECT inside TLS, which this call
		// takes as protocol options. The certificate is checked against the
		// proxy's own host name: the endpoint carries it, but saying so
		// explicitly keeps the check right even when the user typed an
		// address that resolves elsewhere.
		nw_protocol_options_t tls = nw_tls_create_options();
		if (tls == NULL) {
			*failure = @"TLS options for the https proxy could not be built";
			return NULL;
		}
		sec_protocol_options_t security = nw_tls_copy_sec_protocol_options(tls);
		if (security != NULL) {
			sec_protocol_options_set_tls_server_name(security, host);
		}
		config = nw_proxy_config_create_http_connect(endpoint, tls);
	} else {
		*failure = [NSString stringWithFormat:@"unknown proxy protocol %s", scheme];
		return NULL;
	}
	if (config == NULL) {
		*failure = @"the system refused this proxy configuration";
		return NULL;
	}

	if (username != NULL && username[0] != '\0') {
		nw_proxy_config_set_username_and_password(config, username,
		                                          password == NULL ? "" : password);
	}

	// Loopback never goes to the proxy. The preview sheet loads its images
	// and PDFs from this process's own stream server on 127.0.0.1, and a
	// proxy that is down — or simply does not forward to the machine it runs
	// on — would turn every preview into a blank sheet. The in-process
	// wails:// scheme never reaches the network layer at all.
	nw_proxy_config_add_excluded_domain(config, "localhost");
	nw_proxy_config_add_excluded_domain(config, "127.0.0.1");
	nw_proxy_config_add_excluded_domain(config, "::1");

	// The user's Addresses that bypass the proxy list. WebKit matches these as host-name
	// suffixes, which is what the Go side has already reduced the wildcard
	// and leading-dot forms to; a network in CIDR form has no suffix to
	// match and was dropped there.
	for (int i = 0; i < bypassCount; i++) {
		if (bypass[i] != NULL && bypass[i][0] != '\0') {
			nw_proxy_config_add_excluded_domain(config, bypass[i]);
		}
	}
	return config;
}

// pkp_apply runs on the main thread: a WKWebView and its data store are
// AppKit-side objects.
static NSString *pkp_apply(NSWindow *window, const char *scheme, const char *host,
                           int port, const char *username, const char *password,
                           const char *const *bypass, int bypassCount) {
	NSView *content = window.contentView;
	if (content == nil) {
		return @"the window has no content view yet";
	}
	WKWebView *webview = pkp_find_webview(content);
	if (webview == nil) {
		return @"the window has no WKWebView yet";
	}
	WKWebsiteDataStore *store = webview.configuration.websiteDataStore;
	if (store == nil || ![store respondsToSelector:@selector(setProxyConfigurations:)]) {
		return @"this WebKit cannot take a proxy";
	}

	if (@available(macOS 14.0, *)) {
		if (scheme == NULL || scheme[0] == '\0') {
			// Direct and System proxy both end here. Nothing of our own means
			// WebKit falls back on what it always did: the computer's own
			// proxy settings for System, and for Direct there is nothing to
			// undo beyond this, since the app never asked the system to
			// proxy anything in the first place.
			store.proxyConfigurations = nil;
			return nil;
		}
		NSString *failure = nil;
		nw_proxy_config_t config =
		    pkp_build(scheme, host, port, username, password, bypass, bypassCount, &failure);
		if (config == NULL) {
			return failure == nil ? @"the proxy could not be built" : failure;
		}
		store.proxyConfigurations = @[config];
		return nil;
	}
	return @"this macOS cannot give the webview a proxy";
}

char *pkp_webproxy_apply(void *nsWindow, const char *scheme, const char *host,
                         int port, const char *username, const char *password,
                         const char *const *bypass, int bypassCount) {
	if (nsWindow == NULL) {
		return strdup("no window");
	}
	if (!pkp_webproxy_available()) {
		return strdup("this macOS cannot give the webview a proxy");
	}

	NSWindow *window = (__bridge NSWindow *)nsWindow;
	__block NSString *failure = nil;
	void (^work)(void) = ^{
	  @autoreleasepool {
		  @try {
			  failure = pkp_apply(window, scheme, host, port, username, password,
			                      bypass, bypassCount);
		  } @catch (NSException *exception) {
			  failure = [NSString stringWithFormat:@"WebKit refused the proxy: %@", exception.reason];
		  }
	  }
	};
	if ([NSThread isMainThread]) {
		work();
	} else {
		dispatch_sync(dispatch_get_main_queue(), work);
	}
	if (failure == nil) {
		return NULL;
	}
	return strdup(failure.UTF8String);
}
