#pragma once

// Why the system did not verify, for Go to put in words (applock_darwin.go):
// PKP_APPLOCK_SYSTEM comes with the system's own words in *message.
enum {
	PKP_APPLOCK_SYSTEM = 0,
	PKP_APPLOCK_UNAVAILABLE = 1,
	PKP_APPLOCK_NO_PASSCODE = 2,
	PKP_APPLOCK_CANCELLED = 3,
	PKP_APPLOCK_NOT_PASSED = 4,
};

// pkp_applock_available is 1 when this Mac can authenticate its owner; 0
// otherwise, with *reason set to why and, for PKP_APPLOCK_SYSTEM, *message
// to the system's words (malloc'ed UTF-8, the caller frees).
int pkp_applock_available(char **message, int *reason);

// pkp_applock_authenticate shows the system's owner-verification dialog
// (Touch ID, Apple Watch or the login password) and blocks until it ends.
// It returns 0 verified, 1 cancelled, 2 failed, 3 unavailable; for anything
// but 0, *why is set as pkp_applock_available's *reason, with *message
// (malloc'ed UTF-8, the caller frees) for PKP_APPLOCK_SYSTEM. Never call it
// on the main thread: the reply comes through it.
int pkp_applock_authenticate(const char *reason, char **message, int *why);

// pkp_applock_watch_screen installs, once, the observers of the screen
// locking and of the session being switched away.
void pkp_applock_watch_screen(void);

// pkp_applock_shield keeps nsWindow (an NSWindow) out of screenshots, screen
// recording and sharing while on is 1, and lets it back in at 0.
void pkp_applock_shield(void *nsWindow, int on);
