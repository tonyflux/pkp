// GTK4 native surface; window arguments are GtkWindow*, never X11 window IDs.
#ifndef PKP_SURFACE_LINUX_H
#define PKP_SURFACE_LINUX_H
#include <mpv/client.h>
#include "surface_lifetime.h"
// Window pointers must remain alive until their main-thread operation runs.
int pkp_surface_attach(void *window, mpv_handle *mpv, pkp_surface_lifetime *lifetime, char **err);
int pkp_surface_wait_ready(pkp_surface_lifetime *lifetime, char **err);
int pkp_surface_move(void *window, pkp_surface_lifetime *lifetime, char **err);
void pkp_surface_detach(pkp_surface_lifetime *lifetime);
void pkp_surface_set_rect(pkp_surface_lifetime *lifetime, double x, double y, double w, double h, int visible);
int pkp_surface_attached(void);
unsigned long long pkp_surface_frames(void);
void pkp_surface_probe(unsigned char *top, unsigned char *bottom);
int pkp_surface_capture(int timeout_ms, unsigned char **rgba, int *width, int *height);
char *pkp_surface_describe(void);
#endif
