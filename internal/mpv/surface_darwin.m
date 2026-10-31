#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#import <OpenGL/OpenGL.h>
#import <OpenGL/gl3.h>

#include <dlfcn.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

#include "surface_darwin.h"

// The video is a CAOpenGLLayer hosted by a plain NSView, not an NSOpenGLView.
// WKWebView forces the whole hierarchy into layer backing, and a GL view that
// is not itself a layer has no defined compositing order against its
// layer-backed siblings: it decodes and renders correctly and still never
// reaches the screen. As a layer it takes part in normal compositing, so the
// subview order below the webview is the order on screen.

static void *pkp_get_proc_address(void *ctx, const char *name) {
	(void)ctx;
	return dlsym(RTLD_DEFAULT, name);
}

@interface PkpVideoLayer : CAOpenGLLayer
@end

static NSView *gHost = nil;
static PkpVideoLayer *gLayer = nil;
static mpv_handle *gMpv = NULL;
static mpv_render_context *gRender = NULL;
static CGLContextObj gContext = NULL;

// gRender is created and used inside Core Animation's draws of the layer —
// on the main thread, measured — and freed by whoever shuts the player
// down, so every touch of it is serialised.
static pthread_mutex_t gLock = PTHREAD_MUTEX_INITIALIZER;

// 0 = the render context has not been built yet, 1 = ready, -1 = failed.
static _Atomic int gCreateState = 0;
static char gCreateError[256];

static _Atomic unsigned long long gFrames = 0;
static _Atomic unsigned long long gSkipped = 0;
static _Atomic int gLastStatus = 0;
static unsigned char gTopPixel[4] = {0, 0, 0, 0};
static unsigned char gBottomPixel[4] = {0, 0, 0, 0};

// Whether a page shows the picture. Core Animation keeps asking a hidden
// layer to draw every frame; while nobody sees it (the bar, any other page)
// those draws tell mpv the frame was shown without rendering it, so
// playback timing carries on and the main thread does no GL work for it.
// gRedraw makes the first draw once it is shown again paint the current
// frame: a paused video sends no new one, and the layer would otherwise
// keep the frame it had when it was hidden.
static _Atomic int gVisible = 1;
static _Atomic int gRedraw = 0;

// Capture: one request at a time. The drawing thread answers it from the
// framebuffer it just rendered, so the picture is exactly what is on screen,
// whatever the decoder hands mpv. (mpv's own screenshot command fails on
// direct videotoolbox frames: they never reach system memory.)
static pthread_mutex_t gCaptureLock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t gCaptureCond = PTHREAD_COND_INITIALIZER;
static _Atomic int gCaptureWanted = 0;
static unsigned char *gCaptureBuf = NULL;
static int gCaptureW = 0;
static int gCaptureH = 0;

static void pkp_main_sync(void (^block)(void)) {
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_sync(dispatch_get_main_queue(), block);
	}
}

// The window's content view is a plain NSView; Wails adds the WKWebView as a
// subview, and for translucent windows an NSVisualEffectView beneath it.
static NSView *pkp_find_webview(NSView *content) {
	for (NSView *sub in [content subviews]) {
		if ([sub isKindOfClass:NSClassFromString(@"WKWebView")]) {
			return sub;
		}
	}
	return nil;
}

@implementation PkpVideoLayer

- (CGLPixelFormatObj)copyCGLPixelFormatForDisplayMask:(uint32_t)mask {
	CGLPixelFormatAttribute attributes[] = {
	    kCGLPFAOpenGLProfile, (CGLPixelFormatAttribute)kCGLOGLPVersion_3_2_Core,
	    kCGLPFAAccelerated,
	    kCGLPFADoubleBuffer,
	    kCGLPFAColorSize, (CGLPixelFormatAttribute)24,
	    kCGLPFAAlphaSize, (CGLPixelFormatAttribute)8,
	    (CGLPixelFormatAttribute)0};

	CGLPixelFormatObj pixelFormat = NULL;
	GLint count = 0;
	if (CGLChoosePixelFormat(attributes, &pixelFormat, &count) == kCGLNoError && pixelFormat != NULL) {
		return pixelFormat;
	}
	return [super copyCGLPixelFormatForDisplayMask:mask];
}

// Remembered so shutdown can make the same context current: freeing the
// render context requires it, and the layer never hands it out again.
- (CGLContextObj)copyCGLContextForPixelFormat:(CGLPixelFormatObj)pixelFormat {
	CGLContextObj context = [super copyCGLContextForPixelFormat:pixelFormat];
	gContext = context;
	return context;
}

- (BOOL)canDrawInCGLContext:(CGLContextObj)ctx
                pixelFormat:(CGLPixelFormatObj)pixelFormat
               forLayerTime:(CFTimeInterval)timeInterval
                displayTime:(const CVTimeStamp *)timeStamp {
	// The render context can only be built where a GL context is current,
	// which is inside a draw, so the first draw always has to happen.
	if (atomic_load(&gCreateState) == 0) {
		return YES;
	}
	if (gRender == NULL) {
		return NO;
	}
	// A pending capture draws even without a new frame: mpv redraws the
	// current one, which is what a paused picture needs.
	int flags = mpv_render_context_update(gRender);
	if ((flags & MPV_RENDER_UPDATE_FRAME) != 0 && atomic_load(&gVisible) == 0 && atomic_load(&gCaptureWanted) == 0) {
		int skip = 1;
		mpv_render_param params[] = {{MPV_RENDER_PARAM_SKIP_RENDERING, &skip}, {0, NULL}};
		CGLSetCurrentContext(ctx);
		pthread_mutex_lock(&gLock);
		mpv_render_context_render(gRender, params);
		pthread_mutex_unlock(&gLock);
		return NO;
	}
	return (flags & MPV_RENDER_UPDATE_FRAME) != 0 || atomic_load(&gCaptureWanted) != 0 ||
	       (atomic_load(&gRedraw) != 0 && atomic_load(&gVisible) != 0);
}

- (void)drawInCGLContext:(CGLContextObj)ctx
             pixelFormat:(CGLPixelFormatObj)pixelFormat
            forLayerTime:(CFTimeInterval)timeInterval
             displayTime:(const CVTimeStamp *)timeStamp {
	CGLSetCurrentContext(ctx);
	pthread_mutex_lock(&gLock);

	if (gRender == NULL && atomic_load(&gCreateState) == 0 && gMpv != NULL) {
		mpv_opengl_init_params glInit = {.get_proc_address = pkp_get_proc_address,
		                                 .get_proc_address_ctx = NULL};
		int advanced = 0;
		mpv_render_param params[] = {
		    {MPV_RENDER_PARAM_API_TYPE, (void *)MPV_RENDER_API_TYPE_OPENGL},
		    {MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, &glInit},
		    {MPV_RENDER_PARAM_ADVANCED_CONTROL, &advanced},
		    {0, NULL}};

		int status = mpv_render_context_create(&gRender, gMpv, params);
		if (status < 0) {
			gRender = NULL;
			snprintf(gCreateError, sizeof(gCreateError), "%s", mpv_error_string(status));
			atomic_store(&gCreateState, -1);
		} else {
			atomic_store(&gCreateState, 1);
		}
	}

	int width = (int)(self.bounds.size.width * self.contentsScale);
	int height = (int)(self.bounds.size.height * self.contentsScale);
	if (width < 1) width = 1;
	if (height < 1) height = 1;

	// A layer renders into a framebuffer it owns, not into 0. Before the
	// layer has a drawable — the bootstrap draw that exists only to build
	// the render context — that framebuffer is undefined, and touching it
	// raises a GL error mpv then reports as its own.
	GLint target = 0;
	glGetIntegerv(GL_FRAMEBUFFER_BINDING, &target);
	GLenum status = glCheckFramebufferStatus(GL_FRAMEBUFFER);
	BOOL drawable = status == GL_FRAMEBUFFER_COMPLETE;
	if (!drawable) {
		atomic_store(&gLastStatus, (int)status);
		atomic_fetch_add(&gSkipped, 1);
	}

	if (drawable && gRender != NULL) {
		mpv_opengl_fbo fbo = {.fbo = (int)target, .w = width, .h = height, .internal_format = 0};
		int flip = 1;
		mpv_render_param params[] = {
		    {MPV_RENDER_PARAM_OPENGL_FBO, &fbo},
		    {MPV_RENDER_PARAM_FLIP_Y, &flip},
		    {0, NULL}};
		mpv_render_context_render(gRender, params);
		atomic_fetch_add(&gFrames, 1);
		atomic_store(&gRedraw, 0);

		// mpv does not promise to leave its internal framebuffer unbound.
		// Restoring the layer's target matters for the flush below, and
		// sampling the wrong one reports a black picture that is fine.
		glBindFramebuffer(GL_FRAMEBUFFER, (GLuint)target);
		glReadPixels(width / 2, (int)(height * 0.75), 1, 1, GL_RGBA, GL_UNSIGNED_BYTE, gTopPixel);
		glReadPixels(width / 2, (int)(height * 0.25), 1, 1, GL_RGBA, GL_UNSIGNED_BYTE, gBottomPixel);

		if (atomic_load(&gCaptureWanted) != 0) {
			pthread_mutex_lock(&gCaptureLock);
			free(gCaptureBuf);
			gCaptureBuf = malloc((size_t)width * (size_t)height * 4);
			if (gCaptureBuf != NULL) {
				glPixelStorei(GL_PACK_ALIGNMENT, 1);
				glReadPixels(0, 0, width, height, GL_RGBA, GL_UNSIGNED_BYTE, gCaptureBuf);
			}
			gCaptureW = width;
			gCaptureH = height;
			atomic_store(&gCaptureWanted, 0);
			pthread_cond_broadcast(&gCaptureCond);
			pthread_mutex_unlock(&gCaptureLock);
		}
	} else if (drawable) {
		glViewport(0, 0, width, height);
		glClearColor(0.0f, 0.0f, 0.0f, 1.0f);
		glClear(GL_COLOR_BUFFER_BIT);
	}

	pthread_mutex_unlock(&gLock);
	[super drawInCGLContext:ctx pixelFormat:pixelFormat forLayerTime:timeInterval displayTime:timeStamp];
}

@end

int pkp_surface_attach(void *nsWindow, mpv_handle *mpv, char **err) {
	__block int rc = 0;
	__block NSString *message = nil;

	pkp_main_sync(^{
	  if (gLayer != nil) {
		  return; // already attached
	  }
	  NSWindow *window = (NSWindow *)nsWindow;
	  NSView *content = [window contentView];
	  if (content == nil) {
		  rc = -1;
		  message = @"window has no content view yet";
		  return;
	  }

	  // Wails only switches the webview's background off when built with
	  // -tags private_mac_apis; its default build leaves WKWebView painting
	  // the window colour over everything beneath it, which is exactly where
	  // this layer lives. The video would render perfectly and never be seen.
	  // WebKit has honoured this key since WebKit1; a refusal is reported
	  // before anything is built, so a later attach starts clean.
	  NSView *webview = pkp_find_webview(content);
	  if (webview != nil) {
		  @try {
			  [webview setValue:@NO forKey:@"drawsBackground"];
		  } @catch (NSException *exception) {
			  rc = -1;
			  message = [NSString stringWithFormat:@"webview stays opaque: %@", exception.reason];
			  return;
		  }
	  }

	  gMpv = mpv;
	  atomic_store(&gCreateState, 0);

	  gLayer = [[PkpVideoLayer alloc] init];
	  gLayer.opaque = YES;
	  gLayer.needsDisplayOnBoundsChange = YES;
	  gLayer.contentsScale = window.backingScaleFactor;
	  // No zPosition override: sibling layers already composite in view
	  // order, and pushing this one back would also put it behind the
	  // backdrop effect view Wails inserts for translucent windows.
	  // Core Animation drives the draws off the display refresh; canDraw
	  // keeps that cheap when mpv has nothing new.
	  gLayer.asynchronous = YES;

	  // Layer-hosting, not layer-backed: assigning the layer before asking
	  // for wantsLayer is what makes AppKit leave it alone.
	  gHost = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 2, 2)];
	  [gHost setLayer:gLayer];
	  [gHost setWantsLayer:YES];

	  if (webview != nil) {
		  [content addSubview:gHost positioned:NSWindowBelow relativeTo:webview];
	  } else {
		  [content addSubview:gHost];
	  }
	});

	if (rc != 0) {
		if (err != NULL) {
			*err = strdup(message != nil ? [message UTF8String] : "surface attach failed");
		}
		return rc;
	}

	// The layer starts 2x2 and visible on purpose: it has to draw once to
	// build the render context, and a hidden layer never draws. Three
	// seconds is far longer than the frame or two this really takes.
	for (int i = 0; i < 300 && atomic_load(&gCreateState) == 0; i++) {
		usleep(10000);
	}
	if (atomic_load(&gCreateState) != 1) {
		if (err != NULL) {
			*err = strdup(atomic_load(&gCreateState) == -1 ? gCreateError
			                                              : "the video layer never drew a frame");
		}
		return -1;
	}
	return 0;
}

int pkp_surface_move(void *nsWindow, char **err) {
	__block int rc = 0;
	__block NSString *message = nil;

	pkp_main_sync(^{
	  if (gHost == nil) {
		  rc = -1;
		  message = @"no video surface to move";
		  return;
	  }
	  NSWindow *window = (NSWindow *)nsWindow;
	  NSView *content = [window contentView];
	  if (content == nil) {
		  rc = -1;
		  message = @"window has no content view yet";
		  return;
	  }
	  // Same reason as attach: the target window's webview paints the window
	  // colour over everything beneath it until told not to.
	  NSView *webview = pkp_find_webview(content);
	  if (webview != nil) {
		  @try {
			  [webview setValue:@NO forKey:@"drawsBackground"];
		  } @catch (NSException *exception) {
			  rc = -1;
			  message = [NSString stringWithFormat:@"webview stays opaque: %@", exception.reason];
			  return;
		  }
	  }

	  [gHost setHidden:YES];
	  atomic_store(&gVisible, 0);
	  if ([gHost superview] != content) {
		  // removeFromSuperview drops the old window's reference; the one
		  // taken at attach keeps the view, its layer and the GL context alive.
		  [gHost removeFromSuperview];
		  if (webview != nil) {
			  [content addSubview:gHost positioned:NSWindowBelow relativeTo:webview];
		  } else {
			  [content addSubview:gHost];
		  }
	  }
	  gLayer.contentsScale = window.backingScaleFactor;
	});

	if (rc != 0 && err != NULL) {
		*err = strdup(message != nil ? [message UTF8String] : "surface move failed");
	}
	return rc;
}

void pkp_surface_set_rect(double x, double y, double w, double h, int visible) {
	dispatch_async(dispatch_get_main_queue(), ^{
	  if (gHost == nil) {
		  return;
	  }
	  NSView *content = [gHost superview];
	  if (content == nil) {
		  return;
	  }
	  // Web coordinates grow downward from the top-left; AppKit's unflipped
	  // content view grows upward from the bottom-left.
	  CGFloat contentHeight = content.bounds.size.height;
	  [gHost setFrame:NSMakeRect(x, contentHeight - (y + h), w, h)];
	  [gHost setHidden:visible ? NO : YES];
	  if (atomic_exchange(&gVisible, visible ? 1 : 0) == 0 && visible) {
		  atomic_store(&gRedraw, 1);
	  }

	  NSWindow *window = [gHost window];
	  if (window != nil && gLayer != nil) {
		  gLayer.contentsScale = window.backingScaleFactor;
	  }
	});
}

void pkp_surface_detach(void) {
	// Fast path for a core that never had a surface: a headless run, or a
	// unit test. Hopping to the main queue there would block forever,
	// because nothing is draining it outside a real application.
	if (gLayer == nil && gRender == NULL) {
		return;
	}

	// Stop the draw loop before touching the render context, so no draw is
	// in flight while it goes away.
	pkp_main_sync(^{
	  if (gLayer != nil) {
		  gLayer.asynchronous = NO;
	  }
	});

	pthread_mutex_lock(&gLock);
	if (gRender != NULL) {
		if (gContext != NULL) {
			CGLSetCurrentContext(gContext);
		}
		mpv_render_context_free(gRender);
		gRender = NULL;
	}
	atomic_store(&gCreateState, 0);
	gMpv = NULL;
	gContext = NULL;
	pthread_mutex_unlock(&gLock);

	pkp_main_sync(^{
	  if (gHost != nil) {
		  [gHost removeFromSuperview];
		  [gHost release];
		  gHost = nil;
	  }
	  if (gLayer != nil) {
		  [gLayer release];
		  gLayer = nil;
	  }
	});
}

int pkp_surface_capture(int timeout_ms, unsigned char **rgba, int *width, int *height) {
	*rgba = NULL;
	*width = 0;
	*height = 0;
	if (gRender == NULL) {
		return -1;
	}

	struct timespec deadline;
	clock_gettime(CLOCK_REALTIME, &deadline);
	deadline.tv_sec += timeout_ms / 1000;
	deadline.tv_nsec += (long)(timeout_ms % 1000) * 1000000L;
	if (deadline.tv_nsec >= 1000000000L) {
		deadline.tv_sec += 1;
		deadline.tv_nsec -= 1000000000L;
	}

	pthread_mutex_lock(&gCaptureLock);
	free(gCaptureBuf);
	gCaptureBuf = NULL;
	atomic_store(&gCaptureWanted, 1);
	int rc = 0;
	while (atomic_load(&gCaptureWanted) != 0 && rc == 0) {
		rc = pthread_cond_timedwait(&gCaptureCond, &gCaptureLock, &deadline);
	}
	if (atomic_load(&gCaptureWanted) != 0 || gCaptureBuf == NULL) {
		// Timed out: the layer is not drawing (hidden, or no window on
		// screen). Withdraw the request so a later draw does not spend
		// time on it.
		atomic_store(&gCaptureWanted, 0);
		pthread_mutex_unlock(&gCaptureLock);
		return -2;
	}
	*rgba = gCaptureBuf;
	*width = gCaptureW;
	*height = gCaptureH;
	gCaptureBuf = NULL;
	pthread_mutex_unlock(&gCaptureLock);
	return 0;
}

char *pkp_surface_describe(void) {
	// Same reason as detach: with no surface there is nothing to ask the
	// main thread, and hopping there would hang in a process that is not
	// draining the main queue.
	if (gHost == nil) {
		return strdup("surface=none");
	}

	// Built and copied inside the block: an autoreleased NSString created on
	// the main thread can be drained before the calling thread reads it.
	__block char *copy = NULL;
	pkp_main_sync(^{
	  NSString *text = nil;
	  if (gHost == nil) {
		  text = @"surface=none";
	  } else {
		  NSView *content = [gHost superview];
		  NSInteger index = content != nil ? [[content subviews] indexOfObject:gHost] : -1;
		  NSInteger total = content != nil ? (NSInteger)[[content subviews] count] : 0;
		  NSRect frame = [gHost frame];
		  NSString *webview = @"missing";
		  NSView *sibling = pkp_find_webview(content);
		  if (sibling != nil) {
			  @try {
				  webview = [[sibling valueForKey:@"drawsBackground"] boolValue] ? @"opaque" : @"clear";
			  } @catch (NSException *exception) {
				  webview = @"unknown";
			  }
		  }
		  text = [NSString
		      stringWithFormat:
		          @"surface=live render=%d state=%d hidden=%d frame=%.0f,%.0f %.0fx%.0f "
		          @"window=%ld subview=%ld/%ld hosted=%d scale=%.1f async=%d frames=%llu "
		          @"skipped=%llu laststatus=0x%x webview=%@",
		          gRender != NULL, atomic_load(&gCreateState), [gHost isHidden],
		          frame.origin.x, frame.origin.y, frame.size.width, frame.size.height,
		          [gHost window] != nil ? (long)[[gHost window] windowNumber] : 0L, (long)index, (long)total,
		          [gHost layer] == gLayer, gLayer != nil ? gLayer.contentsScale : 0.0,
		          gLayer != nil ? gLayer.asynchronous : 0, atomic_load(&gFrames),
		          atomic_load(&gSkipped), atomic_load(&gLastStatus), webview];
	  }
	  copy = strdup([text UTF8String]);
	});
	return copy;
}

int pkp_surface_attached(void) { return gRender != NULL ? 1 : 0; }

unsigned long long pkp_surface_frames(void) { return atomic_load(&gFrames); }

void pkp_surface_probe(unsigned char *top_rgba, unsigned char *bottom_rgba) {
	if (top_rgba != NULL) {
		memcpy(top_rgba, gTopPixel, 4);
	}
	if (bottom_rgba != NULL) {
		memcpy(bottom_rgba, gBottomPixel, 4);
	}
}
