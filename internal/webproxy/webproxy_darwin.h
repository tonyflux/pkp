#ifndef PKP_WEBPROXY_DARWIN_H
#define PKP_WEBPROXY_DARWIN_H

// pkp_webproxy_supported reports whether this machine has both halves of the
// feature: Network.framework's proxy configuration objects and
// WKWebsiteDataStore.proxyConfigurations, which arrived together in macOS 14.
int pkp_webproxy_supported(void);

// pkp_webproxy_apply gives the WKWebView inside nsWindow one proxy, or none
// when scheme is empty. scheme is "http", "https" or "socks5"; username and
// password may be empty; bypass holds bypassCount host-name suffixes that
// must not go through it. It returns NULL when the proxy is in place, or a
// malloc'd English message the caller frees, having changed nothing.
char *pkp_webproxy_apply(void *nsWindow, const char *scheme, const char *host,
                         int port, const char *username, const char *password,
                         const char *const *bypass, int bypassCount);

#endif
