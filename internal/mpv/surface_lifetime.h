#ifndef PKP_SURFACE_LIFETIME_H
#define PKP_SURFACE_LIFETIME_H

#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>

// Only UI callbacks enter this lock; Close can cancel queued calls without
// waiting for the UI thread, then dispatches teardown before freeing the core.
typedef struct {
    _Atomic int refs;
    pthread_mutex_t mutex;
    _Atomic int active;
} pkp_surface_lifetime;

static inline pkp_surface_lifetime *pkp_surface_lifetime_new(void) {
    pkp_surface_lifetime *lifetime = malloc(sizeof(*lifetime));
    if (lifetime) {
        atomic_init(&lifetime->refs, 1);
        pthread_mutex_init(&lifetime->mutex, NULL);
        atomic_init(&lifetime->active, 1);
    }
    return lifetime;
}
static inline void pkp_surface_lifetime_retain(pkp_surface_lifetime *lifetime) {
    atomic_fetch_add(&lifetime->refs, 1);
}
static inline void pkp_surface_lifetime_release(pkp_surface_lifetime *lifetime) {
    if (atomic_fetch_sub(&lifetime->refs, 1) == 1) {
        pthread_mutex_destroy(&lifetime->mutex);
        free(lifetime);
    }
}
static inline void pkp_surface_lifetime_stop(pkp_surface_lifetime *lifetime) {
    pthread_mutex_lock(&lifetime->mutex);
    atomic_store(&lifetime->active, 0);
    pthread_mutex_unlock(&lifetime->mutex);
}
static inline int pkp_surface_lifetime_enter(pkp_surface_lifetime *lifetime) {
    pthread_mutex_lock(&lifetime->mutex);
    if (atomic_load(&lifetime->active)) return 1;
    pthread_mutex_unlock(&lifetime->mutex);
    return 0;
}
static inline void pkp_surface_lifetime_leave(pkp_surface_lifetime *lifetime) {
    pthread_mutex_unlock(&lifetime->mutex);
}
static inline int pkp_surface_lifetime_active(pkp_surface_lifetime *lifetime) {
    return atomic_load(&lifetime->active);
}
#endif
