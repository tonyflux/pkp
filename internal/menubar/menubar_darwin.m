#import <Cocoa/Cocoa.h>
#import <CoreServices/CoreServices.h>

#include "menubar_darwin.h"

extern void pkpMenubarClickedElsewhere(void);

static void onMain(dispatch_block_t block) {
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_async(dispatch_get_main_queue(), block);
	}
}

void pkp_menubar_activate(void) {
	dispatch_block_t block = ^{
	  [NSApp activateIgnoringOtherApps:YES];
	};
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_sync(dispatch_get_main_queue(), block);
	}
}

/* ---------- the item: attention dot and readable name ---------- */

// Wails keeps the NSStatusItem to itself. Its button lives in one of the
// app's own windows (NSStatusBarWindow), and PKP has a single item, so the
// first status bar button among the app's windows is ours.
static NSStatusBarButton *findButton(NSView *view) {
	if ([view isKindOfClass:[NSStatusBarButton class]]) {
		return (NSStatusBarButton *)view;
	}
	for (NSView *sub in view.subviews) {
		NSStatusBarButton *found = findButton(sub);
		if (found != nil) {
			return found;
		}
	}
	return nil;
}

static NSStatusBarButton *statusButton(void) {
	for (NSWindow *window in NSApp.windows) {
		if (![NSStringFromClass([window class]) containsString:@"StatusBar"]) {
			continue;
		}
		NSStatusBarButton *button = findButton(window.contentView);
		if (button != nil) {
			return button;
		}
	}
	return nil;
}

static NSString *const dotID = @"pkp-attention";
static const CGFloat dotSize = 7;

// The dot is a plain subview over the button: the template image is tinted
// for the bar, the dot keeps its own orange. It is not the only signal —
// the name read out and the panel say the same in words.
static void showDot(NSStatusBarButton *button, BOOL on) {
	NSView *dot = nil;
	for (NSView *sub in button.subviews) {
		if ([sub.identifier isEqualToString:dotID]) {
			dot = sub;
		}
	}
	if (!on) {
		[dot removeFromSuperview];
		return;
	}
	if (dot != nil) {
		return;
	}
	NSRect bounds = button.bounds;
	CGFloat x = NSMaxX(bounds) - dotSize - 2;
	CGFloat y = button.isFlipped ? 3 : NSMaxY(bounds) - dotSize - 3;
	dot = [[NSView alloc] initWithFrame:NSMakeRect(x, y, dotSize, dotSize)];
	dot.identifier = dotID;
	dot.wantsLayer = YES;
	dot.layer.backgroundColor = NSColor.systemOrangeColor.CGColor;
	dot.layer.cornerRadius = dotSize / 2;
	dot.autoresizingMask = NSViewMinXMargin | (button.isFlipped ? NSViewMaxYMargin : NSViewMinYMargin);
	[button addSubview:dot];
	[dot release];
}

void pkp_menubar_badge(int on, const char *label) {
	NSString *name = [NSString stringWithUTF8String:label];
	onMain(^{
	  NSStatusBarButton *button = statusButton();
	  if (button == nil) {
		  return;
	  }
	  showDot(button, on != 0);
	  button.toolTip = name;
	  [button setAccessibilityLabel:name];
	});
}

/* ---------- quitting ---------- */

// pkp_menubar_quit_by_system tells whether the quit being decided — called
// from applicationShouldTerminate:, on the main thread — comes from logging
// out, restarting or shutting down: the quit Apple event then carries the
// reason. ⌘Q, the Dock's Quit and NSApp terminate: carry none.
int pkp_menubar_quit_by_system(void) {
	NSAppleEventDescriptor *event = [[NSAppleEventManager sharedAppleEventManager] currentAppleEvent];
	if (event == nil || event.eventClass != kCoreEventClass || event.eventID != kAEQuitApplication) {
		return 0;
	}
	NSAppleEventDescriptor *reason = [event attributeDescriptorForKeyword:kAEQuitReason];
	if (reason == nil) {
		return 0;
	}
	switch (reason.typeCodeValue) {
	case kAELogOut:
	case kAEReallyLogOut:
	case kAERestart:
	case kAEShowRestartDialog:
	case kAEShutDown:
	case kAEShowShutdownDialog:
		return 1;
	}
	return 0;
}

/* ---------- the panel ---------- */

static id clickMonitor;

// A click in another app never reaches PKP's windows, so the panel would not
// hear of it: a global monitor does (mouse clicks need no permission).
void pkp_menubar_watch_clicks(void) {
	onMain(^{
	  if (clickMonitor != nil) {
		  return;
	  }
	  NSEventMask mask = NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown | NSEventMaskOtherMouseDown;
	  clickMonitor = [[NSEvent addGlobalMonitorForEventsMatchingMask:mask
	                                                         handler:^(NSEvent *event) {
		                                                         pkpMenubarClickedElsewhere();
	                                                         }] retain];
	});
}

// pkp_menubar_fit gives the panel its content's height, keeping its top edge
// where it is (under the item) — Cocoa would keep the bottom — and never
// taller than the screen's free height less margin above and below.
void pkp_menubar_fit(void *nsWindow, int height, int margin) {
	NSWindow *window = (__bridge NSWindow *)nsWindow;
	onMain(^{
	  NSScreen *screen = window.screen ?: NSScreen.mainScreen;
	  CGFloat room = screen.visibleFrame.size.height - 2 * margin;
	  CGFloat h = fmin(height, fmax(room, 120));
	  NSRect frame = window.frame;
	  if (frame.size.height == h) {
		  return;
	  }
	  CGFloat top = NSMaxY(frame);
	  frame.size.height = h;
	  frame.origin.y = top - h;
	  [window setFrame:frame display:YES animate:NO];
	});
}
