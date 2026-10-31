//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include <sys/attr.h>
#include <sys/mount.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>
#include "platform_darwin.h"

int pkp_sync_volume(const char *path, unsigned char *uuid, int *case_sensitive) {
 struct statfs mount;
 if (statfs(path, &mount) != 0) return errno;
 // APFS and HFS+ have documented Unicode canonical-equivalence semantics.
 // Unknown/network drivers cannot safely be guessed from the OS default.
 if (strcmp(mount.f_fstypename, "apfs") && strcmp(mount.f_fstypename, "hfs")) return ENOTSUP;
 struct attrlist attrs = {0};
 attrs.bitmapcount = ATTR_BIT_MAP_COUNT;
 attrs.volattr = ATTR_VOL_INFO | ATTR_VOL_CAPABILITIES | ATTR_VOL_UUID;
 struct __attribute__((packed, aligned(4))) {
  uint32_t length;
  vol_capabilities_attr_t caps;
  unsigned char uuid[16];
 } result = {0};
 if (getattrlist(mount.f_mntonname, &attrs, &result, sizeof(result), FSOPT_NOFOLLOW) != 0) return errno;
 if (!(result.caps.valid[VOL_CAPABILITIES_FORMAT] & VOL_CAP_FMT_CASE_SENSITIVE)) return ENOTSUP;
 unsigned char zero[16] = {0};
 if (!memcmp(result.uuid, zero, 16)) return ENOTSUP;
 memcpy(uuid, result.uuid, 16);
 *case_sensitive = !!(result.caps.capabilities[VOL_CAPABILITIES_FORMAT] & VOL_CAP_FMT_CASE_SENSITIVE);
 return 0;
}

int pkp_sync_rename(int fromfd, const char *from, int tofd, const char *to) {
 return renameatx_np(fromfd, from, tofd, to, RENAME_EXCL) == 0 ? 0 : errno;
}

int pkp_sync_trash(const char *path) {
 @autoreleasepool {
  NSURL *url = [NSURL fileURLWithFileSystemRepresentation:path isDirectory:NO relativeToURL:nil];
  NSURL *result = nil;
  NSError *error = nil;
  if (![[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:&result error:&error]) {
   if ([error.domain isEqualToString:NSPOSIXErrorDomain]) return (int)error.code;
   if ([error.domain isEqualToString:NSCocoaErrorDomain] && (error.code == NSFileWriteNoPermissionError || error.code == NSFileReadNoPermissionError)) return EACCES;
   return ENOTSUP;
  }
  // A recoverable location is mandatory. There is no removeItem fallback.
  return result ? 0 : ENOTSUP;
 }
}

struct pkp_sync_watcher {
 FSEventStreamRef stream;
 dispatch_queue_t queue;
 pthread_mutex_t mutex;
 pthread_cond_t condition;
 int pending;
 int stopped;
};

static void sync_events(ConstFSEventStreamRef stream, void *context, size_t count,
                        void *paths, const FSEventStreamEventFlags flags[],
                        const FSEventStreamEventId ids[]) {
 pkp_sync_watcher *watcher = context;
 pthread_mutex_lock(&watcher->mutex);
 watcher->pending = 1;
 pthread_cond_signal(&watcher->condition);
 pthread_mutex_unlock(&watcher->mutex);
}

pkp_sync_watcher *pkp_sync_watch(const char *path) {
 @autoreleasepool {
  pkp_sync_watcher *watcher = calloc(1, sizeof(*watcher));
  if (!watcher) return NULL;
  pthread_mutex_init(&watcher->mutex, NULL);
  pthread_cond_init(&watcher->condition, NULL);
  FSEventStreamContext context = {0, watcher, NULL, NULL, NULL};
  NSString *root = [[NSFileManager defaultManager] stringWithFileSystemRepresentation:path length:strlen(path)];
  NSArray *paths = @[root];
  watcher->stream = FSEventStreamCreate(NULL, sync_events, &context, (__bridge CFArrayRef)paths,
   kFSEventStreamEventIdSinceNow, 1.0,
   kFSEventStreamCreateFlagFileEvents | kFSEventStreamCreateFlagWatchRoot | kFSEventStreamCreateFlagNoDefer);
  if (!watcher->stream) { pkp_sync_watch_free(watcher); return NULL; }
  watcher->queue = dispatch_queue_create("com.pkp.syncfs", DISPATCH_QUEUE_SERIAL);
  FSEventStreamSetDispatchQueue(watcher->stream, watcher->queue);
  if (!FSEventStreamStart(watcher->stream)) {
   FSEventStreamInvalidate(watcher->stream);
   FSEventStreamRelease(watcher->stream);
   watcher->stream = NULL;
   pkp_sync_watch_free(watcher);
   return NULL;
  }
  return watcher;
 }
}

int pkp_sync_watch_wait(pkp_sync_watcher *watcher) {
 pthread_mutex_lock(&watcher->mutex);
 while (!watcher->pending && !watcher->stopped) pthread_cond_wait(&watcher->condition, &watcher->mutex);
 int result = watcher->stopped ? -1 : 1;
 watcher->pending = 0;
 pthread_mutex_unlock(&watcher->mutex);
 return result;
}

void pkp_sync_watch_stop(pkp_sync_watcher *watcher) {
 // Drain callbacks before waking Go's waiter; only the waiter frees this object.
 if (watcher->stream) {
  FSEventStreamStop(watcher->stream);
  FSEventStreamInvalidate(watcher->stream);
  dispatch_sync(watcher->queue, ^{});
 }
 pthread_mutex_lock(&watcher->mutex);
 watcher->stopped = 1;
 pthread_cond_broadcast(&watcher->condition);
 pthread_mutex_unlock(&watcher->mutex);
}

void pkp_sync_watch_free(pkp_sync_watcher *watcher) {
 if (watcher->stream) FSEventStreamRelease(watcher->stream);
#if !OS_OBJECT_USE_OBJC
 if (watcher->queue) dispatch_release(watcher->queue);
#endif
 pthread_cond_destroy(&watcher->condition);
 pthread_mutex_destroy(&watcher->mutex);
 free(watcher);
}
