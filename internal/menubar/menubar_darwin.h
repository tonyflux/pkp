#ifndef PKP_MENUBAR_DARWIN_H
#define PKP_MENUBAR_DARWIN_H

void pkp_menubar_activate(void);
void pkp_menubar_badge(int on, const char *label);
int pkp_menubar_quit_by_system(void);
void pkp_menubar_watch_clicks(void);
void pkp_menubar_fit(void *nsWindow, int height, int margin);

#endif
