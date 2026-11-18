//go:build darwin && pkp_surface_test

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdint.h>

extern void pkpDarwinSurfaceTestCall(uintptr_t handle);
static NSWindow *testWindows[2];

void pkp_darwin_test_prepare(void) {
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    for (int i = 0; i < 2; i++) {
        NSWindow *window = [[NSWindow alloc] initWithContentRect:NSMakeRect(100 + i * 40, 100 + i * 40, 320, 240)
                                                      styleMask:NSWindowStyleMaskTitled
                                                        backing:NSBackingStoreBuffered defer:NO];
        [window setReleasedWhenClosed:NO];
        WKWebView *webview = [[WKWebView alloc] initWithFrame:window.contentView.bounds];
        [webview setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];
        [window.contentView addSubview:webview];
        [webview loadHTMLString:@"<html><body style='background:transparent'></body></html>" baseURL:nil];
        [webview release];
        [window makeKeyAndOrderFront:nil];
        testWindows[i] = window;
    }
    [NSApp activateIgnoringOtherApps:YES];
}
void *pkp_darwin_test_window(int index) { return testWindows[index]; }
long pkp_darwin_test_window_number(int index) { return [testWindows[index] windowNumber]; }
void pkp_darwin_test_invoke(uintptr_t handle) {
    dispatch_async(dispatch_get_main_queue(), ^{ pkpDarwinSurfaceTestCall(handle); });
}
void pkp_darwin_test_run(void) {
    [NSApp run];
    for (int i = 0; i < 2; i++) {
        [testWindows[i] close];
        [testWindows[i] release];
        testWindows[i] = nil;
    }
}
void pkp_darwin_test_stop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp stop:nil];
        [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint
                                      modifierFlags:0 timestamp:0 windowNumber:0 context:nil
                                            subtype:0 data1:0 data2:0] atStart:NO];
    });
}
