#define PORTAL_TIMEOUT_SECONDS 1
#define main portal_open_main
#include "portal-open.c"
#include <signal.h>
#include <sys/wait.h>
#undef main

static const char *test_executable;

static GVariant *response(guint code) {
    GVariantBuilder results;
    g_variant_builder_init(&results, G_VARIANT_TYPE_VARDICT);
    return g_variant_ref_sink(g_variant_new("(u@a{sv})", code, g_variant_builder_end(&results)));
}

static void test_response(void) {
    Request request = { .loop = g_main_loop_new(NULL, FALSE), .path = "/request/one" };
    GVariant *success = response(0);
    response_received(NULL, NULL, "/request/other", NULL, NULL, success, &request);
    g_assert_false(request.received);
    response_received(NULL, NULL, request.path, NULL, NULL, success, &request);
    g_assert_true(request.received);
    g_assert_cmpuint(request.response, ==, 0);
    g_variant_unref(success);
    request.received = FALSE;
    GVariant *cancelled = response(1);
    response_received(NULL, NULL, request.path, NULL, NULL, cancelled, &request);
    g_assert_true(request.received);
    g_assert_cmpuint(request.response, ==, 1);
    g_variant_unref(cancelled);
    g_main_loop_unref(request.loop);
}

static void test_timeout(void) {
    Request request = { .loop = g_main_loop_new(NULL, FALSE) };
    g_timeout_add(1, request_timeout, &request);
    g_main_loop_run(request.loop);
    g_assert_false(request.received);
    g_main_loop_unref(request.loop);
}

#define REQUEST_PATH "/org/freedesktop/portal/desktop/request/test/request"
static const char portal_xml[] =
    "<node><interface name='org.freedesktop.portal.OpenURI'>"
    "<method name='OpenURI'><arg type='s' direction='in'/><arg type='s' direction='in'/>"
    "<arg type='a{sv}' direction='in'/><arg type='o' direction='out'/></method>"
    "</interface></node>";

typedef struct {
    GDBusConnection *connection;
    guint code;
    gboolean before_reply;
    gboolean send_response;
} FakePortal;

static gboolean emit_response(gpointer data) {
    FakePortal *portal = data;
    GError *error = NULL;
    g_dbus_connection_emit_signal(portal->connection, NULL, REQUEST_PATH,
        "org.freedesktop.portal.Request", "Response", response(portal->code), &error);
    g_assert_no_error(error);
    return G_SOURCE_REMOVE;
}

static void open_uri(GDBusConnection *connection, const char *sender, const char *object,
                     const char *interface, const char *method, GVariant *parameters,
                     GDBusMethodInvocation *invocation, gpointer data) {
    FakePortal *portal = data;
    (void)connection; (void)sender; (void)object; (void)interface; (void)method; (void)parameters;
    if (portal->send_response && portal->before_reply) emit_response(portal);
    g_dbus_method_invocation_return_value(invocation, g_variant_new("(o)", REQUEST_PATH));
    if (portal->send_response && !portal->before_reply) g_timeout_add(25, emit_response, portal);
}

typedef struct { GMainLoop *loop; int status; gboolean exited; } Child;

static void child_exited(GPid pid, gint status, gpointer data) {
    Child *child = data;
    child->status = status;
    child->exited = TRUE;
    g_spawn_close_pid(pid);
    g_main_loop_quit(child->loop);
}

static gboolean child_timeout(gpointer data) {
    g_main_loop_quit(((Child *)data)->loop);
    return G_SOURCE_REMOVE;
}
static void test_dbus(gconstpointer data) {
    guint scenario = GPOINTER_TO_UINT(data);
    GTestDBus *test_bus = g_test_dbus_new(G_TEST_DBUS_NONE);
    g_test_dbus_up(test_bus);
    GError *error = NULL;
    GDBusConnection *connection = g_dbus_connection_new_for_address_sync(
        g_test_dbus_get_bus_address(test_bus),
        G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_CLIENT | G_DBUS_CONNECTION_FLAGS_MESSAGE_BUS_CONNECTION,
        NULL, NULL, &error);
    g_assert_no_error(error);
    GVariant *name = g_dbus_connection_call_sync(connection, "org.freedesktop.DBus",
        "/org/freedesktop/DBus", "org.freedesktop.DBus", "RequestName",
        g_variant_new("(su)", "org.freedesktop.portal.Desktop", 0), G_VARIANT_TYPE("(u)"),
        G_DBUS_CALL_FLAGS_NONE, -1, NULL, &error);
    g_assert_no_error(error);
    guint result;
    g_variant_get(name, "(u)", &result);
    g_assert_cmpuint(result, ==, 1);
    g_variant_unref(name);
    GDBusNodeInfo *info = g_dbus_node_info_new_for_xml(portal_xml, &error);
    g_assert_no_error(error);
    FakePortal portal = { connection, scenario == 2 ? 1 : 0, scenario == 0, scenario != 3 };
    static const GDBusInterfaceVTable vtable = { .method_call = open_uri };
    guint registration = g_dbus_connection_register_object(connection,
        "/org/freedesktop/portal/desktop", info->interfaces[0], &vtable, &portal, NULL, &error);
    g_assert_no_error(error);
    g_assert_cmpuint(registration, >, 0);
    char *args[] = { (char *)test_executable, (char *)"--portal-client", NULL };
    GPid pid;
    g_spawn_async(NULL, args, NULL, G_SPAWN_DO_NOT_REAP_CHILD | G_SPAWN_SEARCH_PATH,
                  NULL, NULL, &pid, &error);
    g_assert_no_error(error);
    Child child = { .loop = g_main_loop_new(NULL, FALSE) };
    guint watch = g_child_watch_add(pid, child_exited, &child);
    guint timeout = g_timeout_add_seconds(5, child_timeout, &child);
    g_main_loop_run(child.loop);
    if (child.exited) g_source_remove(timeout);
    else { g_source_remove(watch); kill(pid, SIGKILL); waitpid(pid, NULL, 0); g_spawn_close_pid(pid); }
    g_assert_true(child.exited);
    g_assert_true(WIFEXITED(child.status));
    g_assert_cmpint(WEXITSTATUS(child.status), ==, scenario < 2 ? 0 : 1);
    g_main_loop_unref(child.loop);
    g_dbus_connection_unregister_object(connection, registration);
    g_dbus_node_info_unref(info);
    g_object_unref(connection);
    g_test_dbus_down(test_bus);
    g_object_unref(test_bus);
}

int main(int argc, char **argv) {
    if (argc == 2 && g_str_equal(argv[1], "--portal-client")) {
        char *args[] = { argv[0], (char *)"https://example.org/", NULL };
        return portal_open_main(2, args);
    }
    test_executable = argv[0];
    g_test_init(&argc, &argv, NULL);
    g_test_add_func("/portal/response", test_response);
    g_test_add_func("/portal/timeout", test_timeout);
    g_test_add_data_func("/portal/dbus/early", GUINT_TO_POINTER(0), test_dbus);
    g_test_add_data_func("/portal/dbus/delayed", GUINT_TO_POINTER(1), test_dbus);
    g_test_add_data_func("/portal/dbus/cancel", GUINT_TO_POINTER(2), test_dbus);
    g_test_add_data_func("/portal/dbus/timeout", GUINT_TO_POINTER(3), test_dbus);
    return g_test_run();
}
