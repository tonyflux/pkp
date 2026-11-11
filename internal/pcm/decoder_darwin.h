// Audio decoding for speech services: one audio stream of a media URL,
// resampled to 16 kHz mono signed 16-bit PCM, with the media position of
// every run of samples. FFmpeg does the work; it is already in the bundle as
// libmpv's own dependency.
#ifndef PKP_PCM_DECODER_DARWIN_H
#define PKP_PCM_DECODER_DARWIN_H

#include <stdint.h>

#define PKP_PCM_RATE 16000

typedef struct pkp_pcm pkp_pcm;

// pkp_pcm_new returns a decoder that has not opened anything yet, or NULL.
// It exists before pkp_pcm_open so that an open in progress can be
// interrupted.
pkp_pcm *pkp_pcm_new(void);

// pkp_pcm_open connects to url and selects audio stream `stream` (an index
// into the container's streams; any other value picks the best audio
// stream). from_ms > 0 seeks there first, in media time counted from the
// container's start, the way mpv counts it. Returns 0 or an AVERROR.
int pkp_pcm_open(pkp_pcm *p, const char *url, int stream, int64_t from_ms);

// pkp_pcm_seek moves to from_ms: the next pkp_pcm_next starts there. It
// works at any point, the end of the stream and after an interrupted call
// included, so one open decoder serves any number of positions. Returns 0
// or an AVERROR.
int pkp_pcm_seek(pkp_pcm *p, int64_t from_ms);

// pkp_pcm_next decodes until it has samples: *data and *len (bytes) are
// valid until the next call, *pos_ms is the media position of the first
// sample. Nothing before from_ms is returned. Returns 0, 1 at the end of the
// stream, or an AVERROR.
int pkp_pcm_next(pkp_pcm *p, const uint8_t **data, int *len, int64_t *pos_ms);

// pkp_pcm_interrupt makes a blocking open, seek or next return soon, and
// every one after it until pkp_pcm_resume. Safe from any thread while the
// decoder exists.
void pkp_pcm_interrupt(pkp_pcm *p);

// pkp_pcm_resume lets calls run again after pkp_pcm_interrupt.
void pkp_pcm_resume(pkp_pcm *p);

// pkp_pcm_close frees the decoder. Not concurrently with open, seek or next.
void pkp_pcm_close(pkp_pcm *p);

// pkp_pcm_strerror describes an AVERROR.
void pkp_pcm_strerror(int err, char *buf, int size);

#endif
