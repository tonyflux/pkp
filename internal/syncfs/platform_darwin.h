#include <errno.h>
#include <stdint.h>
typedef struct pkp_sync_watcher pkp_sync_watcher;
int pkp_sync_volume(const char *path, unsigned char *uuid, int *case_sensitive);
int pkp_sync_rename(int fromfd, const char *from, int tofd, const char *to);
int pkp_sync_trash(const char *path);
pkp_sync_watcher *pkp_sync_watch(const char *path);
int pkp_sync_watch_wait(pkp_sync_watcher *watcher);
void pkp_sync_watch_stop(pkp_sync_watcher *watcher);
void pkp_sync_watch_free(pkp_sync_watcher *watcher);
