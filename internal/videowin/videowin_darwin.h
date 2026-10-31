#ifndef PKP_VIDEOWIN_DARWIN_H
#define PKP_VIDEOWIN_DARWIN_H

// pkp_videowin_fit shapes an NSWindow for a w×h picture on the main thread
// and returns once it has. See Fit.
void pkp_videowin_fit(void *nsWindow, int w, int h);

// pkp_videowin_show_buttons fades the traffic lights in or out. See
// ShowButtons.
void pkp_videowin_show_buttons(void *nsWindow, int show);

void pkp_videowin_free_aspect(void *nsWindow);

#endif
