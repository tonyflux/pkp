package i18n

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

// pkp_preferred_languages is the user's language list (System Settings ·
// Language & Region), one tag per line; the caller frees it. It reads the
// global domain: the app's own AppleLanguages (pkp_set_app_language) would
// otherwise answer for it.
static char *pkp_preferred_languages(void) {
	CFPropertyListRef value = CFPreferencesCopyValue(CFSTR("AppleLanguages"), kCFPreferencesAnyApplication, kCFPreferencesCurrentUser, kCFPreferencesAnyHost);
	CFArrayRef langs = NULL;
	if (value != NULL && CFGetTypeID(value) == CFArrayGetTypeID()) {
		langs = (CFArrayRef)value;
	} else {
		if (value != NULL) CFRelease(value);
		langs = CFLocaleCopyPreferredLanguages();
	}
	if (langs == NULL) return NULL;
	CFIndex n = CFArrayGetCount(langs);
	char *out = calloc((size_t)n * 64 + 1, 1);
	size_t used = 0;
	for (CFIndex i = 0; i < n && out != NULL; i++) {
		CFTypeRef item = CFArrayGetValueAtIndex(langs, i);
		char tag[64];
		if (CFGetTypeID(item) != CFStringGetTypeID() || !CFStringGetCString(item, tag, sizeof tag, kCFStringEncodingUTF8)) continue;
		size_t len = strlen(tag);
		memcpy(out + used, tag, len);
		used += len;
		out[used++] = '\n';
	}
	CFRelease(langs);
	return out;
}

// pkp_set_app_language makes the system's own UI in this app (open panels,
// the authentication prompt, the app menu) use tag from the next launch;
// NULL goes back to the user's list.
static void pkp_set_app_language(const char *tag) {
	CFArrayRef value = NULL;
	if (tag != NULL) {
		CFStringRef s = CFStringCreateWithCString(NULL, tag, kCFStringEncodingUTF8);
		value = CFArrayCreate(NULL, (const void **)&s, 1, &kCFTypeArrayCallBacks);
		CFRelease(s);
	}
	CFPreferencesSetAppValue(CFSTR("AppleLanguages"), value, kCFPreferencesCurrentApplication);
	CFPreferencesAppSynchronize(kCFPreferencesCurrentApplication);
	if (value != NULL) CFRelease(value);
}
*/
import "C"

import (
	"strings"
	"unsafe"
)

func preferred() []string {
	p := C.pkp_preferred_languages()
	if p == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(p))
	return strings.Fields(C.GoString(p))
}

// SetAppLanguage makes the system's own text in PKP (open panels, the
// authentication prompt) follow l from the next launch; "" follows the
// user's system list again.
func SetAppLanguage(l Lang) {
	if l == "" {
		C.pkp_set_app_language(nil)
		return
	}
	tag := C.CString(string(l))
	defer C.free(unsafe.Pointer(tag))
	C.pkp_set_app_language(tag)
}
