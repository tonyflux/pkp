// Tags and the embedded cover of a music file, from FFmpeg's demuxer.
#ifndef PKP_AUDIOTAGS_TAGS_DARWIN_H
#define PKP_AUDIOTAGS_TAGS_DARWIN_H

#include <stdint.h>

typedef struct pkp_tags pkp_tags;

// pkp_tags_new returns a reader that has not opened anything yet, or NULL.
// It exists before pkp_tags_open so that an open in progress can be
// interrupted.
pkp_tags *pkp_tags_new(void);

// pkp_tags_open reads url's header. Returns 0 or an AVERROR.
int pkp_tags_open(pkp_tags *t, const char *url);

// pkp_tags_get is the value of key (case-insensitive) in the container's
// metadata, else in an audio stream's (Ogg keeps its comments there); NULL
// when neither has it. Valid until pkp_tags_close.
const char *pkp_tags_get(pkp_tags *t, const char *key);

// pkp_tags_cover finds the attached picture: *data and *size describe it
// (valid until pkp_tags_close) and the result is 1 for JPEG, 2 for PNG,
// 3 for WebP, 4 for GIF, 5 for BMP, 0 when there is none of these.
int pkp_tags_cover(pkp_tags *t, const uint8_t **data, int *size);

// pkp_tags_interrupt makes a blocking open return soon. Safe from any thread
// while the reader exists.
void pkp_tags_interrupt(pkp_tags *t);

// pkp_tags_close frees the reader. Not concurrently with open.
void pkp_tags_close(pkp_tags *t);

// pkp_tags_strerror describes an AVERROR.
void pkp_tags_strerror(int err, char *buf, int size);

#endif
