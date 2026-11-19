//go:build linux

#include "surface_linux.h"
#include <gtk/gtk.h>
#include <webkit/webkit.h>
#include <epoxy/gl.h>
#include <epoxy/egl.h>
#include <epoxy/glx.h>
#include <mpv/render_gl.h>
#include <math.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>

// A single player, as on macOS. GTK/GL state belongs exclusively to the default
// main context. Only the cached observations and capture handshake cross threads.
static GtkWidget *area, *fixed;
static mpv_handle *core;
static pkp_surface_lifetime *surface_owner;
static mpv_render_context *render;
static GdkGLContext *gl_context;
static GLuint video_fbo, video_texture;
static int target_width, target_height;
static char *render_error;
static gint update_pending;
static gint surface_present;
static gint application_stopped;
static GMutex observations;
static GCond captured;
#ifdef PKP_SURFACE_TEST
static GMutex test_mutex;
static GCond test_cond;
static int test_queued;
static int test_armed;
static char test_gl_renderer[256];
static GLenum test_gl_before, test_gl_render, test_gl_read, test_fbo_status;
static GLint test_pack_skip_pixels, test_pack_skip_rows;
void pkp_surface_test_reset(void) {
    g_mutex_lock(&test_mutex);
    test_queued = 0;
    test_armed = 1;
    g_mutex_unlock(&test_mutex);
}
void pkp_surface_test_wait(void) {
    g_mutex_lock(&test_mutex);
    while (!test_queued) g_cond_wait(&test_cond, &test_mutex);
    g_mutex_unlock(&test_mutex);
}
#endif
#ifdef PKP_SURFACE_TEST
extern void pkpSurfaceTestClose(uintptr_t handle);
static gboolean pkp_test_close_main(gpointer data) {
    pkpSurfaceTestClose((uintptr_t)data);
    return G_SOURCE_REMOVE;
}
void pkp_test_queue_close(uintptr_t handle) {
    GSource *source = g_idle_source_new();
    g_source_set_callback(source, pkp_test_close_main, (void *)handle, NULL);
    g_source_attach(source, g_main_context_default());
    g_source_unref(source);
}
#endif
static int attached, visible, capture_busy, capture_pending, capture_status;
static int capture_width, capture_height;
static unsigned char *capture_pixels;
static unsigned long long frames;
static int drawn_width, drawn_height, drawn_scale;
static unsigned char top_pixel[4], bottom_pixel[4];

struct main_call {
    void (*fn)(void *);
    void *data;
    GMutex mutex;
    GCond cond;
    gboolean done, cancelled;
    gint refs;
};
static void release_call(gpointer data) {
    struct main_call *call = data;
    if (!g_atomic_int_dec_and_test(&call->refs)) return;
    g_cond_clear(&call->cond);
    g_mutex_clear(&call->mutex);
    g_free(call);
}
static gboolean run_call(gpointer data) {
    struct main_call *call = data;
    g_mutex_lock(&call->mutex);
    if (!call->cancelled) call->fn(call->data);
    call->done = TRUE;
    g_cond_signal(&call->cond);
    g_mutex_unlock(&call->mutex);
    return G_SOURCE_REMOVE;
}
static void on_main(void (*fn)(void *), void *data) {
    GMainContext *context = g_main_context_default();
    if (g_main_context_is_owner(context)) {
        fn(data);
        return;
    }
    if (g_atomic_int_get(&application_stopped)) return;
    // The source owns a reference too: shutdown may stop dispatch between
    // enqueueing this call and executing it, without leaving stack pointers.
    struct main_call *call = g_new0(struct main_call, 1);
    call->fn = fn;
    call->data = data;
    call->refs = 2;
    g_mutex_init(&call->mutex);
    g_cond_init(&call->cond);
    g_mutex_lock(&call->mutex);
    GSource *source = g_idle_source_new();
    g_source_set_callback(source, run_call, call, release_call);
    g_source_attach(source, context);
#ifdef PKP_SURFACE_TEST
    g_mutex_lock(&test_mutex);
    if (test_armed) {
        test_armed = 0;
        test_queued = 1;
        g_cond_broadcast(&test_cond);
    }
    g_mutex_unlock(&test_mutex);
#endif
    while (!call->done && !g_atomic_int_get(&application_stopped)) {
        g_cond_wait_until(&call->cond, &call->mutex, g_get_monotonic_time() + 100000);
    }
    call->cancelled = !call->done;
    g_mutex_unlock(&call->mutex);
    g_source_destroy(source);
    g_source_unref(source);
    release_call(call);
}
static gboolean draw(GtkGLArea *widget, GdkGLContext *context, gpointer unused);

static gboolean queue_draw(gpointer unused) {
    (void)unused;
    g_atomic_int_set(&update_pending, 0);
    if (area && render && visible && gtk_widget_get_mapped(area) &&
        gtk_gl_area_get_error(GTK_GL_AREA(area)) == NULL) {
        // The transparent WebKit overlay can occlude GtkGLArea's snapshot;
        // rendering must not depend on GTK choosing to snapshot the lower widget.
        gtk_gl_area_make_current(GTK_GL_AREA(area));
        if (gtk_gl_area_get_error(GTK_GL_AREA(area)) != NULL) {
            return G_SOURCE_REMOVE;
        }
        gtk_gl_area_attach_buffers(GTK_GL_AREA(area));
        draw(GTK_GL_AREA(area), gl_context, NULL);
        gtk_widget_queue_draw(area);
    }
    return G_SOURCE_REMOVE;
}
static void request_draw(void *unused) {
    (void)unused;
    if (!g_atomic_int_compare_and_exchange(&update_pending, 0, 1)) return;
    GSource *source = g_idle_source_new();
    g_source_set_callback(source, queue_draw, NULL, NULL);
    g_source_attach(source, g_main_context_default());
    g_source_unref(source);
}
static void *get_proc(void *unused, const char *name) {
    (void)unused;
    // GTK chooses EGL or GLX; use its current context, not the display protocol.
    if (eglGetCurrentContext() != EGL_NO_CONTEXT) return (void *)eglGetProcAddress(name);
    return (void *)glXGetProcAddressARB((const GLubyte *)name);
}
static void fail_capture(int status) {
    g_mutex_lock(&observations);
    if (capture_pending) {
        capture_pending = 0;
        capture_status = status;
    }
    g_cond_broadcast(&captured);
    g_mutex_unlock(&observations);
}
static GdkGLContext *create_context(GtkGLArea *widget, gpointer unused) {
    (void)unused;
    if (!gl_context) {
        GError *error = NULL;
        gl_context = gdk_display_create_gl_context(gtk_widget_get_display(GTK_WIDGET(widget)), &error);
        if (gl_context) {
            gdk_gl_context_set_allowed_apis(gl_context, GDK_GL_API_GL);
            gdk_gl_context_set_required_version(gl_context, 3, 2);
            if (!gdk_gl_context_realize(gl_context, &error)) g_clear_object(&gl_context);
        }
        if (error) {
            gtk_gl_area_set_error(widget, error);
            g_error_free(error);
        }
    }
    return gl_context ? g_object_ref(gl_context) : NULL;
}
static void realize(GtkGLArea *widget, gpointer unused) {
    (void)unused;
    gtk_gl_area_make_current(widget);
    g_clear_pointer(&render_error, g_free);
    GError *error = gtk_gl_area_get_error(widget);
    if (error) {
        render_error = g_strdup(error->message);
        return;
    }
    if (render) { request_draw(NULL); return; }
    mpv_opengl_init_params gl = {.get_proc_address = get_proc};
    mpv_render_param params[] = {
        {MPV_RENDER_PARAM_API_TYPE, MPV_RENDER_API_TYPE_OPENGL},
        {MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, &gl},
        {MPV_RENDER_PARAM_INVALID, NULL}
    };
    int status = mpv_render_context_create(&render, core, params);
    if (status < 0) {
        render_error = g_strdup(mpv_error_string(status));
        return;
    }
    g_mutex_lock(&observations);
    attached = 1;
    g_mutex_unlock(&observations);
    mpv_render_context_set_update_callback(render, request_draw, NULL);
    request_draw(NULL);
}
static gboolean draw(GtkGLArea *widget, GdkGLContext *context, gpointer unused) {
    (void)context;
    (void)unused;
    if (!render || !visible) return TRUE;
    int scale = gtk_widget_get_scale_factor(GTK_WIDGET(widget));
    int w = gtk_widget_get_width(GTK_WIDGET(widget)) * scale;
    int h = gtk_widget_get_height(GTK_WIDGET(widget)) * scale;
    if (w <= 0 || h <= 0) return TRUE;
    GLint framebuffer;
    glGetIntegerv(GL_FRAMEBUFFER_BINDING, &framebuffer);
    // GtkGLArea owns the framebuffer; libmpv requires otherwise-default GL
    // pixel transfer state, including after GTK has composited its texture.
    glPixelStorei(GL_UNPACK_ALIGNMENT, 4);
    glPixelStorei(GL_UNPACK_ROW_LENGTH, 0);
    glPixelStorei(GL_UNPACK_SKIP_PIXELS, 0);
    glPixelStorei(GL_UNPACK_SKIP_ROWS, 0);
    glBindBuffer(GL_PIXEL_UNPACK_BUFFER, 0);
    // GTK may use a multisampled target; reading it directly is invalid.
    // A single-sample video target makes probes/captures deterministic and
    // survives GTK recreating its own buffers during cross-window migration.
    if (!video_fbo) glGenFramebuffers(1, &video_fbo);
    if (!video_texture) glGenTextures(1, &video_texture);
    if (target_width != w || target_height != h) {
        glBindTexture(GL_TEXTURE_2D, video_texture);
        glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA8, w, h, 0, GL_RGBA, GL_UNSIGNED_BYTE, NULL);
        glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR);
        glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_LINEAR);
        glBindFramebuffer(GL_FRAMEBUFFER, video_fbo);
        glFramebufferTexture2D(GL_FRAMEBUFFER, GL_COLOR_ATTACHMENT0, GL_TEXTURE_2D, video_texture, 0);
        glBindTexture(GL_TEXTURE_2D, 0);
        target_width = w;
        target_height = h;
    }
    mpv_opengl_fbo fbo = {.fbo = video_fbo, .w = w, .h = h, .internal_format = GL_RGBA8};
    int flip = 1, block = 0;
    mpv_render_param params[] = {
        {MPV_RENDER_PARAM_OPENGL_FBO, &fbo},
        {MPV_RENDER_PARAM_FLIP_Y, &flip},
        {MPV_RENDER_PARAM_BLOCK_FOR_TARGET_TIME, &block},
        {MPV_RENDER_PARAM_INVALID, NULL}
    };
#ifdef PKP_SURFACE_TEST
    GLenum gl_before = glGetError();
#endif
    mpv_render_context_update(render);
    if (mpv_render_context_render(render, params) < 0) {
        fail_capture(-1);
        return TRUE;
    }
#ifdef PKP_SURFACE_TEST
    GLenum gl_render = glGetError();
#endif
    glPixelStorei(GL_PACK_ALIGNMENT, 1);
    glBindFramebuffer(GL_FRAMEBUFFER, video_fbo);
    glPixelStorei(GL_PACK_ROW_LENGTH, 0);
    glBindBuffer(GL_PIXEL_PACK_BUFFER, 0);
#ifdef PKP_SURFACE_TEST
    GLenum fbo_status = glCheckFramebufferStatus(GL_FRAMEBUFFER);
    GLint pack_skip_pixels, pack_skip_rows;
    glGetIntegerv(GL_PACK_SKIP_PIXELS, &pack_skip_pixels);
    glGetIntegerv(GL_PACK_SKIP_ROWS, &pack_skip_rows);
#endif
    unsigned char top[4], bottom[4];
    glReadPixels(w / 2, h * 2 / 3, 1, 1, GL_RGBA, GL_UNSIGNED_BYTE, top);
    glReadPixels(w / 2, h / 3, 1, 1, GL_RGBA, GL_UNSIGNED_BYTE, bottom);
#ifdef PKP_SURFACE_TEST
    GLenum gl_read = glGetError();
    const GLubyte *gl_renderer = glGetString(GL_RENDERER);
#endif
    g_mutex_lock(&observations);
#ifdef PKP_SURFACE_TEST
    if (gl_renderer) g_strlcpy(test_gl_renderer, (const char *)gl_renderer, sizeof(test_gl_renderer));
    if (gl_before) test_gl_before = gl_before;
    if (gl_render) test_gl_render = gl_render;
    if (gl_read) test_gl_read = gl_read;
    test_fbo_status = fbo_status;
    test_pack_skip_pixels = pack_skip_pixels;
    test_pack_skip_rows = pack_skip_rows;
#endif
    drawn_width = w;
    drawn_height = h;
    drawn_scale = scale;
    frames++;
    memcpy(top_pixel, top, 4);
    memcpy(bottom_pixel, bottom, 4);
    if (capture_pending) {
        // glReadPixels is intentionally limited to explicit captures, not every frame.
        free(capture_pixels);
        capture_pixels = NULL;
        size_t bytes = (size_t)w * h * 4;
        unsigned char *pixels = malloc(bytes);
        if (pixels) {
            glReadPixels(0, 0, w, h, GL_RGBA, GL_UNSIGNED_BYTE, pixels);
            capture_pixels = pixels;
            capture_width = w;
            capture_height = h;
            capture_status = 0;
        } else {
            capture_status = -1;
        }
        capture_pending = 0;
        g_cond_broadcast(&captured);
    }
    glBindFramebuffer(GL_READ_FRAMEBUFFER, video_fbo);
    glBindFramebuffer(GL_DRAW_FRAMEBUFFER, framebuffer);
    glBlitFramebuffer(0, 0, w, h, 0, 0, w, h, GL_COLOR_BUFFER_BIT, GL_NEAREST);
    glBindFramebuffer(GL_FRAMEBUFFER, framebuffer);
    g_mutex_unlock(&observations);
    mpv_render_context_report_swap(render);
    return TRUE;
}

static GtkWidget *find_webview(GtkWidget *widget) {
    if (WEBKIT_IS_WEB_VIEW(widget)) return widget;
    for (GtkWidget *child = gtk_widget_get_first_child(widget); child;
         child = gtk_widget_get_next_sibling(child)) {
        GtkWidget *found = find_webview(child);
        if (found) return found;
    }
    return NULL;
}
static GtkWidget *window_fixed(GtkWindow *window, char **err) {
    GtkWidget *webview = find_webview(GTK_WIDGET(window));
    if (!webview) {
        *err = g_strdup("window has no WebKitWebView");
        return NULL;
    }
    GtkWidget *parent = gtk_widget_get_parent(webview);
    if (GTK_IS_OVERLAY(parent)) {
        GtkWidget *child = g_object_get_data(G_OBJECT(parent), "pkp-video-fixed");
        if (child) return child;
    }
    if (!GTK_IS_BOX(parent)) {
        *err = g_strdup("WebKitWebView must be a child of the Wails GtkBox");
        return NULL;
    }
    // Replace only the webview slot, leaving Wails' box, menu and titlebar intact.
    GtkWidget *previous = gtk_widget_get_prev_sibling(webview);
    GtkWidget *overlay = gtk_overlay_new();
    GtkWidget *base = gtk_fixed_new();
    gtk_widget_set_hexpand(overlay, TRUE);
    gtk_widget_set_vexpand(overlay, TRUE);
    gtk_widget_set_hexpand(base, TRUE);
    gtk_widget_set_vexpand(base, TRUE);
    g_object_ref(webview);
    gtk_box_remove(GTK_BOX(parent), webview);
    // Neither the previous video size nor its offset may become a toplevel
    // minimum size: otherwise shrinking the window feeds the old rect back.
    gtk_overlay_set_child(GTK_OVERLAY(overlay), gtk_drawing_area_new());
    gtk_overlay_add_overlay(GTK_OVERLAY(overlay), base);
    gtk_overlay_set_measure_overlay(GTK_OVERLAY(overlay), base, FALSE);
    gtk_overlay_add_overlay(GTK_OVERLAY(overlay), webview);
    gtk_overlay_set_measure_overlay(GTK_OVERLAY(overlay), webview, TRUE);
    gtk_widget_set_halign(webview, GTK_ALIGN_FILL);
    gtk_widget_set_valign(webview, GTK_ALIGN_FILL);
    gtk_box_insert_child_after(GTK_BOX(parent), overlay, previous);
    g_object_unref(webview);
    g_object_set_data(G_OBJECT(overlay), "pkp-video-fixed", base);
    GdkRGBA background = {0, 0, 0, 0};
    webkit_web_view_set_background_color(WEBKIT_WEB_VIEW(webview), &background);
    return base;
}
struct attach_args { GtkWindow *window; mpv_handle *mpv; pkp_surface_lifetime *lifetime; char **err; int status; };
static void detach_main(void *unused);
static void application_shutdown(GApplication *application, gpointer unused) {
    (void)application;
    // GApplication emits shutdown before g_application_run returns, including
    // exits that bypass Wails cleanup. This is the last safe GTK/GL teardown.
    detach_main(unused);
    g_atomic_int_set(&application_stopped, 1);
}
static void attach_active(void *data) {
    struct attach_args *args = data;
    if (area) {
        if (surface_owner == args->lifetime) args->status = 0;
        else *args->err = g_strdup("video surface belongs to another client");
        return;
    }
    fixed = window_fixed(args->window, args->err);
    if (!fixed) return;
    GtkApplication *application = gtk_window_get_application(args->window);
    if (application && !g_object_get_data(G_OBJECT(application), "pkp-surface-shutdown")) {
        g_signal_connect(application, "shutdown", G_CALLBACK(application_shutdown), NULL);
        g_object_set_data(G_OBJECT(application), "pkp-surface-shutdown", GINT_TO_POINTER(1));
    }
    g_mutex_lock(&observations);
    frames = 0;
    memset(top_pixel, 0, sizeof(top_pixel));
    memset(bottom_pixel, 0, sizeof(bottom_pixel));
    drawn_width = drawn_height = drawn_scale = 0;
#ifdef PKP_SURFACE_TEST
    test_gl_renderer[0] = '\0';
    test_gl_before = test_gl_render = test_gl_read = test_fbo_status = 0;
    test_pack_skip_pixels = test_pack_skip_rows = 0;
#endif
    g_mutex_unlock(&observations);
    surface_owner = args->lifetime;
    core = args->mpv;
    area = gtk_gl_area_new();
    gtk_widget_set_opacity(area, 0);
    g_object_ref_sink(area); // survives removal/reparenting and window destruction
    gtk_gl_area_set_allowed_apis(GTK_GL_AREA(area), GDK_GL_API_GL);
    gtk_gl_area_set_required_version(GTK_GL_AREA(area), 3, 2);
    gtk_gl_area_set_auto_render(GTK_GL_AREA(area), FALSE);
    gtk_widget_set_can_target(area, FALSE);
    gtk_widget_set_size_request(area, 1, 1);
    g_signal_connect(area, "realize", G_CALLBACK(realize), NULL);
    g_signal_connect(area, "create-context", G_CALLBACK(create_context), NULL);
    g_signal_connect(area, "render", G_CALLBACK(draw), NULL);
    gtk_fixed_put(GTK_FIXED(fixed), area, 0, 0);
    gtk_widget_realize(area);
    if (!render) {
        *args->err = g_strdup(render_error ? render_error : "GTK could not realize the GL surface");
        gtk_fixed_remove(GTK_FIXED(fixed), area);
        g_clear_object(&gl_context);
        g_object_unref(area);
        area = fixed = NULL;
        surface_owner = NULL;
        core = NULL;
        return;
    }
    g_atomic_int_set(&surface_present, 1);
    args->status = 0;
}
static void attach_main(void *data) {
    struct attach_args *args = data;
    if (!pkp_surface_lifetime_enter(args->lifetime)) return;
    attach_active(data);
    pkp_surface_lifetime_leave(args->lifetime);
}
int pkp_surface_attach(void *window, mpv_handle *mpv, pkp_surface_lifetime *lifetime, char **err) {
    struct attach_args args = {.window = window, .mpv = mpv, .lifetime = lifetime, .err = err, .status = -1};
    on_main(attach_main, &args);
    return args.status;
}
int pkp_surface_wait_ready(pkp_surface_lifetime *lifetime, char **err) {
    (void)err;
    return pkp_surface_lifetime_active(lifetime) ? 0 : -1;
}
static void detach_main(void *unused) {
    (void)unused;
    visible = 0;
    if (area) {
        // The display-owned context survives GtkGLArea's unrealize/reparent.
        if (gl_context) gdk_gl_context_make_current(gl_context);
        if (render) {
            mpv_render_context_set_update_callback(render, NULL, NULL);
            mpv_render_context_free(render);
            render = NULL;
        }
        if (video_fbo) glDeleteFramebuffers(1, &video_fbo);
        if (video_texture) glDeleteTextures(1, &video_texture);
        video_fbo = video_texture = 0;
        target_width = target_height = 0;
        GtkWidget *parent = gtk_widget_get_parent(area);
        if (parent) gtk_fixed_remove(GTK_FIXED(parent), area);
        g_clear_object(&gl_context);
        g_mutex_lock(&observations);
        attached = 0;
        g_mutex_unlock(&observations);
        g_object_unref(area);
        area = NULL;
    }
    fixed = NULL;
    surface_owner = NULL;
    core = NULL;
    g_clear_pointer(&render_error, g_free);
    g_atomic_int_set(&surface_present, 0);
    g_mutex_lock(&observations);
    attached = 0;
    free(capture_pixels);
    capture_pixels = NULL;
    capture_pending = 0;
    capture_status = -1;
    g_cond_broadcast(&captured);
    g_mutex_unlock(&observations);
}
static void detach_owned(void *data) {
    if (surface_owner == data) detach_main(NULL);
}
void pkp_surface_detach(pkp_surface_lifetime *lifetime) {
    if (g_atomic_int_get(&surface_present)) on_main(detach_owned, lifetime);
}
static void move_active(void *data) {
    struct attach_args *args = data;
    if (!area) { *args->err = g_strdup("no video surface"); return; }
    GtkWidget *target = window_fixed(args->window, args->err);
    if (!target) return;
    visible = 0;
    gtk_widget_set_opacity(area, 0);
    if (target != gtk_widget_get_parent(area)) {
        GtkWidget *parent = gtk_widget_get_parent(area);
        if (parent) gtk_fixed_remove(GTK_FIXED(parent), area);
        fixed = target;
        gtk_fixed_put(GTK_FIXED(fixed), area, 0, 0);
        gtk_widget_realize(area);
    }
    GError *error = gtk_gl_area_get_error(GTK_GL_AREA(area));
    if (!render || render_error != NULL || error != NULL) {
        const char *message = render_error ? render_error
                            : (error && error->message ? error->message : "GTK could not re-realize the GL surface");
        *args->err = g_strdup(message);
        return;
    }
    args->status = 0;
}
static void move_main(void *data) {
    struct attach_args *args = data;
    if (!pkp_surface_lifetime_enter(args->lifetime)) return;
    if (surface_owner == args->lifetime) move_active(data);
    pkp_surface_lifetime_leave(args->lifetime);
}
int pkp_surface_move(void *window, pkp_surface_lifetime *lifetime, char **err) {
    if (!g_atomic_int_get(&surface_present)) {
        *err = g_strdup("no video surface");
        return -1;
    }
    struct attach_args args = {.window = window, .lifetime = lifetime, .err = err, .status = -1};
    on_main(move_main, &args);
    return args.status;
}
struct rect_args { pkp_surface_lifetime *lifetime; double x, y, w, h; int visible; };
static void rect_active(void *data) {
    struct rect_args *rect = data;
    if (!area) return;
    GtkWidget *parent = gtk_widget_get_parent(area);
    if (!parent) return;
    visible = rect->visible;
    gtk_fixed_move(GTK_FIXED(parent), area, rect->x, rect->y);
    gtk_widget_set_size_request(area, (int)ceil(rect->w), (int)ceil(rect->h));
    gtk_widget_set_opacity(area, visible ? 1 : 0);
    if (!visible) fail_capture(-2);
    else request_draw(NULL);
}
static void rect_main(void *data) {
    struct rect_args *rect = data;
    if (!pkp_surface_lifetime_enter(rect->lifetime)) return;
    if (surface_owner == rect->lifetime) rect_active(data);
    pkp_surface_lifetime_leave(rect->lifetime);
}
void pkp_surface_set_rect(pkp_surface_lifetime *lifetime, double x, double y, double w, double h, int show) {
    if (!g_atomic_int_get(&surface_present)) return;
    struct rect_args rect = {lifetime, x, y, w, h, show};
    on_main(rect_main, &rect);
}
int pkp_surface_attached(void) {
    g_mutex_lock(&observations);
    int result = attached;
    g_mutex_unlock(&observations);
    return result;
}
unsigned long long pkp_surface_frames(void) {
    g_mutex_lock(&observations);
    unsigned long long result = frames;
    g_mutex_unlock(&observations);
    return result;
}
void pkp_surface_probe(unsigned char *top, unsigned char *bottom) {
    g_mutex_lock(&observations);
    memcpy(top, top_pixel, 4);
    memcpy(bottom, bottom_pixel, 4);
    g_mutex_unlock(&observations);
}
int pkp_surface_capture(int timeout_ms, unsigned char **rgba, int *width, int *height) {
    // Waiting on the GTK thread would prevent the requested draw from happening.
    if (g_main_context_is_owner(g_main_context_default())) return -2;
    gint64 until = g_get_monotonic_time() + (gint64)MAX(0, timeout_ms) * 1000;
    g_mutex_lock(&observations);
    if (!attached) { g_mutex_unlock(&observations); return -1; }
    // Serialize captures, without allowing one caller to steal another's buffer.
    while (capture_busy) {
        if (!g_cond_wait_until(&captured, &observations, until)) {
            if (!capture_busy) break;
            g_mutex_unlock(&observations);
            return -2;
        }
    }
    if (!attached) { g_mutex_unlock(&observations); return -1; }
    free(capture_pixels);
    capture_pixels = NULL;
    capture_busy = 1;
    capture_pending = 1;
    capture_status = -2;
    request_draw(NULL);
    while (capture_pending) {
        if (!g_cond_wait_until(&captured, &observations, until)) {
            if (!capture_pending) break;
            capture_pending = 0;
            capture_busy = 0;
            g_cond_broadcast(&captured);
            g_mutex_unlock(&observations);
            return -2;
        }
    }
    int result = capture_status;
    if (!result && !capture_pixels) {
        result = -1;
    }
    if (!result) {
        *rgba = capture_pixels;
        *width = capture_width;
        *height = capture_height;
        capture_pixels = NULL;
    }
    capture_busy = 0;
    g_cond_broadcast(&captured);
    g_mutex_unlock(&observations);
    return result;
}
char *pkp_surface_describe(void) {
    g_mutex_lock(&observations);
#ifdef PKP_SURFACE_TEST
    char *description = g_strdup_printf("GTK4 GtkGLArea attached=%d frames=%llu backing=%dx%d scale=%d capture=%d GL renderer=%s fbo_status=0x%x errors(before/render/read)=0x%x/0x%x/0x%x pack_skip=%d,%d", attached, frames, drawn_width, drawn_height, drawn_scale, capture_pending, test_gl_renderer, test_fbo_status, test_gl_before, test_gl_render, test_gl_read, test_pack_skip_pixels, test_pack_skip_rows);
#else
    char *description = g_strdup_printf("GTK4 GtkGLArea attached=%d frames=%llu backing=%dx%d scale=%d capture=%d", attached, frames, drawn_width, drawn_height, drawn_scale, capture_pending);
#endif
    g_mutex_unlock(&observations);
    return description;
}
