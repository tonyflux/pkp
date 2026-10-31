#include "tags_darwin.h"

#include <libavformat/avformat.h>
#include <libavutil/error.h>

#include <errno.h>
#include <stdatomic.h>
#include <stdlib.h>

struct pkp_tags {
	_Atomic int stop;
	AVFormatContext *fmt;
};

static int interrupted(void *opaque) {
	return atomic_load(&((pkp_tags *)opaque)->stop);
}

pkp_tags *pkp_tags_new(void) {
	return calloc(1, sizeof(pkp_tags));
}

void pkp_tags_interrupt(pkp_tags *t) {
	atomic_store(&t->stop, 1);
}

int pkp_tags_open(pkp_tags *t, const char *url) {
	t->fmt = avformat_alloc_context();
	if (t->fmt == NULL) {
		return AVERROR(ENOMEM);
	}
	t->fmt->interrupt_callback.callback = interrupted;
	t->fmt->interrupt_callback.opaque = t;
	// A connection that stops delivering must fail, not hang the caller.
	AVDictionary *opts = NULL;
	av_dict_set(&opts, "rw_timeout", "15000000", 0);
	// Opening is enough: the demuxers read tags and the attached picture
	// while parsing the header; avformat_find_stream_info would go on to
	// decode packets for nothing.
	int ret = avformat_open_input(&t->fmt, url, NULL, &opts);
	av_dict_free(&opts);
	return ret;
}

const char *pkp_tags_get(pkp_tags *t, const char *key) {
	if (t->fmt == NULL) {
		return NULL;
	}
	AVDictionaryEntry *e = av_dict_get(t->fmt->metadata, key, NULL, 0);
	if (e != NULL && e->value[0] != '\0') {
		return e->value;
	}
	for (unsigned i = 0; i < t->fmt->nb_streams; i++) {
		AVStream *st = t->fmt->streams[i];
		if (st->codecpar->codec_type != AVMEDIA_TYPE_AUDIO) {
			continue;
		}
		e = av_dict_get(st->metadata, key, NULL, 0);
		if (e != NULL && e->value[0] != '\0') {
			return e->value;
		}
	}
	return NULL;
}

int pkp_tags_cover(pkp_tags *t, const uint8_t **data, int *size) {
	if (t->fmt == NULL) {
		return 0;
	}
	for (unsigned i = 0; i < t->fmt->nb_streams; i++) {
		AVStream *st = t->fmt->streams[i];
		if (!(st->disposition & AV_DISPOSITION_ATTACHED_PIC) || st->attached_pic.size <= 0) {
			continue;
		}
		int kind = 0;
		switch (st->codecpar->codec_id) {
		case AV_CODEC_ID_MJPEG:
			kind = 1;
			break;
		case AV_CODEC_ID_PNG:
			kind = 2;
			break;
		case AV_CODEC_ID_WEBP:
			kind = 3;
			break;
		case AV_CODEC_ID_GIF:
			kind = 4;
			break;
		case AV_CODEC_ID_BMP:
			kind = 5;
			break;
		default:
			continue;
		}
		*data = st->attached_pic.data;
		*size = st->attached_pic.size;
		return kind;
	}
	return 0;
}

void pkp_tags_close(pkp_tags *t) {
	if (t == NULL) {
		return;
	}
	avformat_close_input(&t->fmt);
	free(t);
}

void pkp_tags_strerror(int err, char *buf, int size) {
	av_strerror(err, buf, (size_t)size);
}
