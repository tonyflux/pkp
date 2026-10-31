// Native video surface for macOS.
//
// The surface is an NSOpenGLView inserted into the window's content view
// *below* the WKWebView. Wails makes the webview non-opaque, so wherever the
// page paints nothing the view below shows through: that is the hole the
// player page leaves for the video.
//
// One surface for the whole application. It lives in one window at a time
// and can be moved to another (the separate player window and back) without
// touching the render context, so playback never notices. The globals are
// deliberate — this application has a single player — and every entry point
// marshals to the main thread, which is where the GL context lives.
#ifndef PKP_SURFACE_DARWIN_H
#define PKP_SURFACE_DARWIN_H

#include <mpv/client.h>
#include <mpv/render.h>
#include <mpv/render_gl.h>

// pkp_surface_attach builds the view and binds a libmpv render context to its
// GL context. Returns 0 on success; otherwise it returns the mpv error code
// (or -1) and stores a malloc'd message in *err for the caller to free.
int pkp_surface_attach(void *nsWindow, mpv_handle *mpv, char **err);

// pkp_surface_set_rect positions the view in web coordinates: origin at the
// top-left of the content area, in points rather than pixels.
void pkp_surface_set_rect(double x, double y, double w, double h, int visible);

// pkp_surface_move re-parents the attached view into another window, below
// that window's webview, and hides it until the next pkp_surface_set_rect:
// the old rectangle means nothing there. The layer and its render context
// stay as they are. Returns 0 on success; otherwise -1 with a malloc'd
// message in *err for the caller to free.
int pkp_surface_move(void *nsWindow, char **err);

// pkp_surface_detach frees the render context and removes the view. It must
// run before the mpv handle is destroyed.
void pkp_surface_detach(void);

// pkp_surface_attached reports whether a render context is bound.
int pkp_surface_attached(void);

// pkp_surface_frames counts frames rendered into the view.
unsigned long long pkp_surface_frames(void);

// pkp_surface_probe copies the last sampled pixels out: two RGBA quadruples,
// taken from the centre of the upper and lower thirds of the view as the user
// sees them. They exist so a test can prove that real frames reached the
// surface, and in which orientation.
void pkp_surface_probe(unsigned char *top_rgba, unsigned char *bottom_rgba);

// pkp_surface_capture asks the drawing thread for the next rendered frame
// and waits up to timeout_ms for it. On success it returns 0 and hands over
// a malloc'd bottom-up RGBA buffer of width × height pixels (the layer's
// backing size), which the caller frees. -1: no render context; -2: the
// layer did not draw in time (hidden, or its window is off screen).
int pkp_surface_capture(int timeout_ms, unsigned char **rgba, int *width, int *height);

// pkp_surface_describe returns a malloc'd one-line description of the live
// view and layer state. "No picture" has several possible causes — hidden
// view, wrong frame, wrong sibling order — and they are indistinguishable
// from outside the process. The caller frees the result.
char *pkp_surface_describe(void);

#endif
