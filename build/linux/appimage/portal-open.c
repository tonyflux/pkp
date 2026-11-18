#include <gio/gio.h>
#include <gio/gunixfdlist.h>
#include <fcntl.h>
#include <unistd.h>

/* Launch on the host desktop, never execute a host ELF against bundled /usr. */
int main(int argc, char **argv) {
    if (argc != 2) {
        g_printerr("Usage: xdg-open URL-or-file\n");
        return 2;
    }
    GError *error = NULL;
    GDBusConnection *bus = g_bus_get_sync(G_BUS_TYPE_SESSION, NULL, &error);
    if (!bus) goto failed;
    GVariantBuilder options;
    g_variant_builder_init(&options, G_VARIANT_TYPE_VARDICT);
    GVariant *reply = NULL;
    char *path = g_filename_from_uri(argv[1], NULL, NULL);
    if (!path && !g_uri_peek_scheme(argv[1])) path = g_canonicalize_filename(argv[1], NULL);
    if (path) {
        int fd = open(path, O_RDONLY | O_CLOEXEC);
        g_free(path);
        if (fd < 0) {
            g_printerr("Cannot open file: %s\n", argv[1]);
            g_object_unref(bus);
            return 1;
        }
        GUnixFDList *fds = g_unix_fd_list_new();
        int handle = g_unix_fd_list_append(fds, fd, &error);
        close(fd);
        if (handle >= 0)
            reply = g_dbus_connection_call_with_unix_fd_list_sync(bus, "org.freedesktop.portal.Desktop",
                "/org/freedesktop/portal/desktop", "org.freedesktop.portal.OpenURI", "OpenFile",
                g_variant_new("(sh@a{sv})", "", handle, g_variant_builder_end(&options)),
                G_VARIANT_TYPE("(o)"), G_DBUS_CALL_FLAGS_NONE, 30000, fds, NULL, NULL, &error);
        g_object_unref(fds);
    } else {
        reply = g_dbus_connection_call_sync(bus, "org.freedesktop.portal.Desktop",
            "/org/freedesktop/portal/desktop", "org.freedesktop.portal.OpenURI", "OpenURI",
            g_variant_new("(ss@a{sv})", "", argv[1], g_variant_builder_end(&options)),
            G_VARIANT_TYPE("(o)"), G_DBUS_CALL_FLAGS_NONE, 30000, NULL, &error);
    }
    g_object_unref(bus);
    if (reply) {
        g_variant_unref(reply);
        return 0;
    }
failed:
    g_printerr("PKP needs a working desktop portal to open external files/URLs: %s\n", error ? error->message : "unknown error");
    g_clear_error(&error);
    return 1;
}
