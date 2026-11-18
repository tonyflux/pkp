// GTK4 native surface; window arguments are GtkWindow*, never X11 window IDs.
#ifndef PKP_SURFACE_LINUX_H
#define PKP_SURFACE_LINUX_H
#include <mpv/client.h>
int pkp_surface_attach(void *window, mpv_handle *mpv, char **err);
int pkp_surface_move(void *window, char **err);
void pkp_surface_detach(void);
void pkp_surface_set_rect(double x, double y, double w, double h, int visible);
int pkp_surface_attached(void);
unsigned long long pkp_surface_frames(void);
void pkp_surface_probe(unsigned char *top, unsigned char *bottom);
int pkp_surface_capture(int timeout_ms, unsigned char **rgba, int *width, int *height);
char *pkp_surface_describe(void);
#endif
