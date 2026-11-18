//go:build linux

package videowin

/*
#cgo pkg-config: gtk4
#cgo LDFLAGS: -lm
#include <gtk/gtk.h>
#include <math.h>

struct pkp_fit {
    GtkWindow *window;
    int width, height;
};
static gboolean pkp_fit_main(gpointer data) {
    struct pkp_fit *fit = data;
    GtkWidget *widget = GTK_WIDGET(fit->window);
    // GTK4 intentionally has no fixed-aspect toplevel hints. Wayland also
    // reserves positioning and the final window size to the compositor.
    // Request the initial fit, but leave interactive resizing to Wails/GTK.
    if (!gtk_window_is_fullscreen(fit->window) && !gtk_window_is_maximized(fit->window)) {
        int w = gtk_widget_get_width(widget), h = gtk_widget_get_height(widget);
        if (w <= 0 || h <= 0) gtk_window_get_default_size(fit->window, &w, &h);
        double area = (double)MAX(w, 320) * MAX(h, 180);
        double ratio = (double)fit->width / fit->height;
        int width = MAX(1, (int)sqrt(area * ratio));
        int height = MAX(1, (int)(width / ratio));
        GdkSurface *surface = gtk_native_get_surface(GTK_NATIVE(fit->window));
        GdkMonitor *monitor = surface ? gdk_display_get_monitor_at_surface(gtk_widget_get_display(widget), surface) : NULL;
        if (monitor) {
            GdkRectangle bounds;
            gdk_monitor_get_geometry(monitor, &bounds);
            double shrink = MIN(1.0, MIN((double)bounds.width * .9 / width, (double)bounds.height * .9 / height));
            width *= shrink;
            height *= shrink;
        }
        gtk_window_set_default_size(fit->window, MAX(width, 1), MAX(height, 1));
    }
    g_object_unref(fit->window);
    g_free(fit);
    return G_SOURCE_REMOVE;
}
static void pkp_fit_window(void *window, int width, int height) {
    struct pkp_fit *fit = g_new(struct pkp_fit, 1);
    *fit = (struct pkp_fit){g_object_ref(window), width, height};
    GSource *source = g_idle_source_new();
    g_source_set_callback(source, pkp_fit_main, fit, NULL);
    g_source_attach(source, g_main_context_default());
    g_source_unref(source);
}
*/
import "C"

import "unsafe"

// Fit requests a picture-shaped content size. GTK4 has no fixed-aspect hints;
// the compositor controls placement, bounds, and subsequent interactive resizing.
func Fit(window unsafe.Pointer, w, h int) {
	if window != nil && w > 0 && h > 0 {
		C.pkp_fit_window(window, C.int(w), C.int(h))
	}
}

// ShowButtons leaves native decorations to Wails and the compositor. Hiding
// server-side titlebar buttons independently is not available in GTK4/Wayland.
func ShowButtons(unsafe.Pointer, bool) {}

// FreeAspect is unnecessary: GTK4 does not enforce an aspect ratio.
func FreeAspect(unsafe.Pointer) {}
