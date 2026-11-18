//go:build linux && pkp_surface_test

package mpv

/*
#cgo CFLAGS: -DPKP_SURFACE_TEST
#include <stdint.h>
extern void pkp_surface_test_reset(void);
#include <gtk/gtk.h>
#include <webkit/webkit.h>
#include <stdlib.h>
extern void pkp_test_queue_close(uintptr_t handle);

// Isolated integration-test host. No Wails application or user account required.
struct pkp_test_host {
    GMainLoop *loop;
    GtkWindow *windows[2];
};
static struct pkp_test_host *pkp_test_host_new(void) {
    if (!gtk_init_check()) return NULL;
    struct pkp_test_host *host = g_new0(struct pkp_test_host, 1);
    host->loop = g_main_loop_new(NULL, FALSE);
    for (int i = 0; i < 2; i++) {
        host->windows[i] = GTK_WINDOW(gtk_window_new());
        gtk_window_set_decorated(host->windows[i], FALSE);
        gtk_window_set_default_size(host->windows[i], 320, 240);
        GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
        GtkWidget *webview = webkit_web_view_new();
        GdkRGBA clear = {0, 0, 0, 0};
        webkit_web_view_set_background_color(WEBKIT_WEB_VIEW(webview), &clear);
        webkit_web_view_load_html(WEBKIT_WEB_VIEW(webview),
            "<html style='background:transparent'><body style='background:transparent;margin:0'></body></html>", NULL);
        gtk_widget_set_vexpand(webview, TRUE);
        gtk_widget_set_hexpand(webview, TRUE);
        gtk_box_append(GTK_BOX(box), webview);
        gtk_window_set_child(host->windows[i], box);
        gtk_window_present(host->windows[i]);
    }
    return host;
}
static void *pkp_test_host_window(struct pkp_test_host *host, int index) {
    return host->windows[index];
}
static void pkp_test_host_run(struct pkp_test_host *host) {
    g_main_loop_run(host->loop);
    for (int i = 0; i < 2; i++) gtk_window_destroy(host->windows[i]);
    g_main_loop_unref(host->loop);
    g_free(host);
}
static gboolean pkp_test_host_quit_main(gpointer data) {
    struct pkp_test_host *host = data;
    g_main_loop_quit(host->loop);
    return G_SOURCE_REMOVE;
}
static void pkp_test_host_quit(struct pkp_test_host *host) {
    GSource *source = g_idle_source_new();
    g_source_set_callback(source, pkp_test_host_quit_main, host, NULL);
    g_source_attach(source, g_main_context_default());
    g_source_unref(source);
}
*/
import "C"

import (
	"runtime"
	"runtime/cgo"
	"unsafe"
)

func surfaceTestHost() ([2]unsafe.Pointer, func(), bool) {
	type ready struct {
		host    *C.struct_pkp_test_host
		windows [2]unsafe.Pointer
	}
	started := make(chan ready, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		host := C.pkp_test_host_new()
		if host == nil {
			started <- ready{}
			return
		}
		started <- ready{host, [2]unsafe.Pointer{C.pkp_test_host_window(host, 0), C.pkp_test_host_window(host, 1)}}
		C.pkp_test_host_run(host)
	}()
	r := <-started
	if r.host == nil {
		return r.windows, func() {}, false
	}
	return r.windows, func() { C.pkp_test_host_quit(r.host); <-done }, true
}

type surfaceCloseTest struct {
	client  *Client
	entered chan struct{}
	closed  chan error
}

func surfaceCloseOnMain(c *Client) (<-chan struct{}, <-chan error) {
	C.pkp_surface_test_reset()
	test := &surfaceCloseTest{client: c, entered: make(chan struct{}), closed: make(chan error, 1)}
	C.pkp_test_queue_close(C.uintptr_t(cgo.NewHandle(test)))
	return test.entered, test.closed
}
