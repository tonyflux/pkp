#import <Cocoa/Cocoa.h>

#include <math.h>

#include "videowin_darwin.h"

// The controls need 480 points across; below 270 high the picture is a
// strip. A portrait picture would make that floor taller than a laptop
// screen, so the floor never exceeds four fifths of the work area.
static NSSize floorFor(double ratio, NSSize work) {
	double w = fmax(480, 270 * ratio);
	double h = w / ratio;
	double s = fmin(1, fmin(0.8 * work.width / w, 0.8 * work.height / h));
	return NSMakeSize(w * s, h * s);
}

static void fit(NSWindow *window, int w, int h) {
	double ratio = (double)w / h;
	NSScreen *screen = window.screen ?: NSScreen.mainScreen;
	NSRect work = screen.visibleFrame;
	NSSize floor = floorFor(ratio, work.size);

	window.contentAspectRatio = NSMakeSize(w, h);
	window.contentMinSize = floor;
	window.minSize = [window frameRectForContentRect:NSMakeRect(0, 0, floor.width, floor.height)].size;
	if (window.styleMask & NSWindowStyleMaskFullScreen) {
		return;
	}

	// About the area it had, so a new video neither shrinks nor balloons the
	// window, at most nine tenths of the work area, centred where it was.
	NSRect content = [window contentRectForFrameRect:window.frame];
	double width = sqrt(content.size.width * content.size.height * ratio);
	double height = width / ratio;
	double s = fmin(1, fmin(0.9 * work.size.width / width, 0.9 * work.size.height / height));
	width *= s;
	height *= s;
	if (width < floor.width) {
		width = floor.width;
		height = floor.height;
	}
	NSRect frame = [window frameRectForContentRect:NSMakeRect(0, 0, round(width), round(height))];
	frame.origin.x = NSMidX(window.frame) - frame.size.width / 2;
	frame.origin.y = NSMidY(window.frame) - frame.size.height / 2;
	frame.origin.x = fmax(NSMinX(work), fmin(frame.origin.x, NSMaxX(work) - frame.size.width));
	frame.origin.y = fmax(NSMinY(work), fmin(frame.origin.y, NSMaxY(work) - frame.size.height));
	if (NSEqualRects(NSIntegralRect(frame), NSIntegralRect(window.frame))) {
		return;
	}
	[window setFrame:frame display:YES animate:window.visible];
}

void pkp_videowin_fit(void *nsWindow, int w, int h) {
	NSWindow *window = (__bridge NSWindow *)nsWindow;
	if ([NSThread isMainThread]) {
		fit(window, w, h);
		return;
	}
	dispatch_sync(dispatch_get_main_queue(), ^{
		fit(window, w, h);
	});
}

static void showButtons(NSWindow *window, BOOL show) {
	if (window.styleMask & NSWindowStyleMaskFullScreen) {
		show = YES;
	}
	NSWindowButton kinds[] = {NSWindowCloseButton, NSWindowMiniaturizeButton, NSWindowZoomButton};
	for (int i = 0; i < 3; i++) {
		NSButton *button = [window standardWindowButton:kinds[i]];
		if (button == nil) {
			continue;
		}
		if (show) {
			button.hidden = NO;
		}
		[NSAnimationContext
		    runAnimationGroup:^(NSAnimationContext *context) {
			  context.duration = 0.25;
			  button.animator.alphaValue = show ? 1 : 0;
		    }
		    completionHandler:^{
			  // Faded out but there, it would still take the clicks meant
			  // for the picture. A show since then has set it back to 1.
			  if (button.alphaValue == 0) {
				  button.hidden = YES;
			  }
		    }];
	}
}

void pkp_videowin_show_buttons(void *nsWindow, int show) {
	NSWindow *window = (__bridge NSWindow *)nsWindow;
	dispatch_async(dispatch_get_main_queue(), ^{
		showButtons(window, show != 0);
	});
}

void pkp_videowin_free_aspect(void *nsWindow) {
	NSWindow *window = (__bridge NSWindow *)nsWindow;
	void (^apply)(void) = ^{
		window.contentAspectRatio = NSZeroSize;
	};
	if ([NSThread isMainThread]) {
		apply();
	} else {
		dispatch_sync(dispatch_get_main_queue(), apply);
	}
}
