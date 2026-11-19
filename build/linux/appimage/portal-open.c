#include <gio/gio.h>
#include <gio/gunixfdlist.h>
#include <fcntl.h>
#include <unistd.h>

#ifndef PORTAL_TIMEOUT_SECONDS
#define PORTAL_TIMEOUT_SECONDS 30
#endif

typedef struct {
    GMainLoop *loop;
    const char *path;
    guint response;
    gboolean received;
    gboolean timed_out;
} Request;

static void response_received(GDBusConnection *bus, const char *sender, const char *path,
                              const char *interface, const char *signal, GVariant *parameters,
                              gpointer data) {
    Request *request = data;
    (void)bus; (void)sender; (void)interface; (void)signal;
    if (!request->path || !g_str_equal(path, request->path)) return;
    GVariant *results;
    g_variant_get(parameters, "(u@a{sv})", &request->response, &results);
    g_variant_unref(results);
    request->received = TRUE;
    g_main_loop_quit(request->loop);
}

static gboolean request_timeout(gpointer data) {
    Request *request = data;
    request->timed_out = TRUE;
    g_main_loop_quit(request->loop);
    return G_SOURCE_REMOVE;
}

/* Launch on the host desktop, never execute a host ELF against bundled /usr. */
int main(int argc, char **argv) {
    if (argc != 2) {
        g_printerr("Usage: xdg-open URL-or-file\n");
        return 2;
    }
    GError *error = NULL;
    GDBusConnection *bus = g_bus_get_sync(G_BUS_TYPE_SESSION, NULL, &error);
    if (!bus) goto failed;
    Request request = {0};
    request.loop = g_main_loop_new(NULL, FALSE);
    guint subscription = g_dbus_connection_signal_subscribe(bus, "org.freedesktop.portal.Desktop",
        "org.freedesktop.portal.Request", "Response", NULL, NULL, G_DBUS_SIGNAL_FLAGS_NONE,
        response_received, &request, NULL);
    GVariantBuilder options;
    g_variant_builder_init(&options, G_VARIANT_TYPE_VARDICT);
    GVariant *reply = NULL;
    char *path = g_filename_from_uri(argv[1], NULL, NULL);
    if (!path && (argv[1][0] == '/' || g_str_has_prefix(argv[1], "./") ||
                  g_str_has_prefix(argv[1], "../") || !g_uri_peek_scheme(argv[1]) ||
                  g_file_test(argv[1], G_FILE_TEST_EXISTS))) {
        path = g_canonicalize_filename(argv[1], NULL);
    }
    if (path) {
        int fd = open(path, O_RDONLY | O_CLOEXEC);
        g_free(path);
        if (fd < 0) {
            g_variant_builder_clear(&options);
            g_printerr("Cannot open file: %s\n", argv[1]);
            g_dbus_connection_signal_unsubscribe(bus, subscription);
            g_main_loop_unref(request.loop);
            g_object_unref(bus);
            return 1;
        }
        GUnixFDList *fds = g_unix_fd_list_new();
        int handle = g_unix_fd_list_append(fds, fd, &error);
        close(fd);
        if (handle >= 0) {
            reply = g_dbus_connection_call_with_unix_fd_list_sync(bus, "org.freedesktop.portal.Desktop",
                "/org/freedesktop/portal/desktop", "org.freedesktop.portal.OpenURI", "OpenFile",
                g_variant_new("(sh@a{sv})", "", handle, g_variant_builder_end(&options)),
                G_VARIANT_TYPE("(o)"), G_DBUS_CALL_FLAGS_NONE, 30000, fds, NULL, NULL, &error);
        } else {
            g_variant_builder_clear(&options);
        }
        g_object_unref(fds);
    } else {
        reply = g_dbus_connection_call_sync(bus, "org.freedesktop.portal.Desktop",
            "/org/freedesktop/portal/desktop", "org.freedesktop.portal.OpenURI", "OpenURI",
            g_variant_new("(ss@a{sv})", "", argv[1], g_variant_builder_end(&options)),
            G_VARIANT_TYPE("(o)"), G_DBUS_CALL_FLAGS_NONE, 30000, NULL, &error);
    }
    if (reply) {
        g_variant_get(reply, "(&o)", &request.path);
        guint timeout = g_timeout_add_seconds(PORTAL_TIMEOUT_SECONDS, request_timeout, &request);
        g_main_loop_run(request.loop);
        if (!request.timed_out) g_source_remove(timeout);
        if (!request.received) g_dbus_connection_call_sync(bus, "org.freedesktop.portal.Desktop", request.path,
            "org.freedesktop.portal.Request", "Close", NULL, NULL, G_DBUS_CALL_FLAGS_NONE,
            1000, NULL, NULL);
        g_variant_unref(reply);
    }
    g_dbus_connection_signal_unsubscribe(bus, subscription);
    g_main_loop_unref(request.loop);
    g_object_unref(bus);
    if (request.received && request.response == 0) return 0;
    if (request.received) {
        g_printerr("Desktop portal request %s.\n", request.response == 1 ? "cancelled" : "failed");
        return 1;
    }
    if (!error) g_set_error_literal(&error, G_IO_ERROR, G_IO_ERROR_TIMED_OUT, "portal request timed out");
failed:
    g_printerr("PKP needs a working desktop portal to open external files/URLs: %s\n", error ? error->message : "unknown error");
    g_clear_error(&error);
    return 1;
}
