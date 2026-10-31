#import <Cocoa/Cocoa.h>
#import <LocalAuthentication/LocalAuthentication.h>
#include <stdlib.h>
#include <string.h>

#include "applock_darwin.h"

extern void pkpApplockScreenLocked(void);

static char *copyText(NSString *text) {
	return strdup(text.UTF8String ?: "");
}

// The system's own words where it has them (they follow the Mac's language);
// for the cases the lock screen and Settings explain, a code Go words in the
// language PKP shows (applock_darwin.go).
static int describe(NSError *error, char **message) {
	switch (error.code) {
	case LAErrorPasscodeNotSet:
		return PKP_APPLOCK_NO_PASSCODE;
	case LAErrorUserCancel:
	case LAErrorSystemCancel:
	case LAErrorAppCancel:
		return PKP_APPLOCK_CANCELLED;
	case LAErrorAuthenticationFailed:
		return PKP_APPLOCK_NOT_PASSED;
	}
	if (error.localizedDescription != nil) {
		*message = copyText(error.localizedDescription);
		return PKP_APPLOCK_SYSTEM;
	}
	return PKP_APPLOCK_UNAVAILABLE;
}

int pkp_applock_available(char **message, int *reason) {
	@autoreleasepool {
		LAContext *context = [[LAContext alloc] init];
		NSError *error = nil;
		// Device owner, not biometrics only: Touch ID, an Apple Watch or the
		// login password, whichever this Mac has.
		if ([context canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:&error]) {
			return 1;
		}
		*reason = describe(error, message);
		return 0;
	}
}

int pkp_applock_authenticate(const char *reason, char **message, int *why) {
	@autoreleasepool {
		LAContext *context = [[LAContext alloc] init];
		NSError *error = nil;
		if (![context canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:&error]) {
			*why = describe(error, message);
			return 3;
		}
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		__block int result = 3;
		__block NSError *failed = nil;
		[context evaluatePolicy:LAPolicyDeviceOwnerAuthentication
		        localizedReason:[NSString stringWithUTF8String:reason]
		                  reply:^(BOOL success, NSError *failure) {
			if (success) {
				result = 0;
			} else {
				switch (failure.code) {
				case LAErrorUserCancel:
				case LAErrorSystemCancel:
				case LAErrorAppCancel:
				case LAErrorUserFallback:
					result = 1;
					break;
				case LAErrorAuthenticationFailed:
				case LAErrorBiometryLockout:
					result = 2;
					break;
				default:
					result = 3;
				}
				failed = failure;
			}
			dispatch_semaphore_signal(done);
		}];
		dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
		if (result != 0) {
			*why = describe(failed, message);
		}
		return result;
	}
}

static BOOL watching;

void pkp_applock_watch_screen(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (watching) {
			return;
		}
		watching = YES;
		// The login window's lock (⌃⌘Q, the screen saver or sleep asking
		// for the password, the lid closing) is posted by loginwindow to
		// every app; fast user switching is the workspace's.
		[[NSDistributedNotificationCenter defaultCenter] addObserverForName:@"com.apple.screenIsLocked"
		                                                             object:nil
		                                                              queue:nil
		                                                         usingBlock:^(NSNotification *note) {
			pkpApplockScreenLocked();
		}];
		[[[NSWorkspace sharedWorkspace] notificationCenter] addObserverForName:NSWorkspaceSessionDidResignActiveNotification
		                                                                object:nil
		                                                                 queue:nil
		                                                            usingBlock:^(NSNotification *note) {
			pkpApplockScreenLocked();
		}];
	});
}

void pkp_applock_shield(void *nsWindow, int on) {
	if (nsWindow == NULL) {
		return;
	}
	NSWindow *window = (__bridge NSWindow *)nsWindow;
	dispatch_async(dispatch_get_main_queue(), ^{
		window.sharingType = on ? NSWindowSharingNone : NSWindowSharingReadOnly;
	});
}
