//go:build darwin && cgo

#include "decoder.h"

#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/channel_layout.h>
#include <libavutil/error.h>
#include <libavutil/mathematics.h>
#include <libswresample/swresample.h>

#include <errno.h>
#include <stdatomic.h>
#include <stdlib.h>

struct pkp_pcm {
	_Atomic int stop;
	AVFormatContext *fmt;
	AVCodecContext *dec;
	SwrContext *swr;
	AVPacket *pkt;
	AVFrame *frame;
	int stream;
	// origin_ms is the container's start time: positions count from it,
	// which is what mpv's time-pos does (rebase-start-time). MPEG-TS files
	// rarely start at 0.
	int64_t origin_ms;
	int64_t from_ms;
	// next_ms is the position right after the last samples handed out; it
	// places a frame that comes without a timestamp.
	int64_t next_ms;
	int draining;
	int flushed;
	// read is set once decoding has read from the stream: from then on a
	// failed seek cannot fall back to decoding from the start.
	int read;
	uint8_t *out;
	int out_cap;
	// What the resampler was built for; a stream may change mid-way.
	int in_rate;
	int in_format;
	AVChannelLayout in_layout;
};

static int interrupted(void *opaque) {
	return atomic_load(&((pkp_pcm *)opaque)->stop);
}

pkp_pcm *pkp_pcm_new(void) {
	return calloc(1, sizeof(pkp_pcm));
}

void pkp_pcm_interrupt(pkp_pcm *p) {
	atomic_store(&p->stop, 1);
}

void pkp_pcm_resume(pkp_pcm *p) {
	atomic_store(&p->stop, 0);
}

int pkp_pcm_open(pkp_pcm *p, const char *url, int stream, int64_t from_ms) {
	p->fmt = avformat_alloc_context();
	if (p->fmt == NULL) {
		return AVERROR(ENOMEM);
	}
	p->fmt->interrupt_callback.callback = interrupted;
	p->fmt->interrupt_callback.opaque = p;

	// A connection that stops delivering must fail, not hang the caller.
	AVDictionary *opts = NULL;
	av_dict_set(&opts, "rw_timeout", "20000000", 0);
	int ret = avformat_open_input(&p->fmt, url, NULL, &opts);
	av_dict_free(&opts);
	if (ret < 0) {
		return ret;
	}
	if ((ret = avformat_find_stream_info(p->fmt, NULL)) < 0) {
		return ret;
	}

	if (stream < 0 || stream >= (int)p->fmt->nb_streams ||
	    p->fmt->streams[stream]->codecpar->codec_type != AVMEDIA_TYPE_AUDIO) {
		stream = av_find_best_stream(p->fmt, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);
		if (stream < 0) {
			return stream;
		}
	}
	p->stream = stream;
	// The demuxer still reads past the other streams' packets, but no
	// decoder ever sees them.
	for (unsigned i = 0; i < p->fmt->nb_streams; i++) {
		p->fmt->streams[i]->discard = (int)i == stream ? AVDISCARD_DEFAULT : AVDISCARD_ALL;
	}

	AVStream *st = p->fmt->streams[stream];
	const AVCodec *codec = avcodec_find_decoder(st->codecpar->codec_id);
	if (codec == NULL) {
		return AVERROR_DECODER_NOT_FOUND;
	}
	p->dec = avcodec_alloc_context3(codec);
	p->pkt = av_packet_alloc();
	p->frame = av_frame_alloc();
	if (p->dec == NULL || p->pkt == NULL || p->frame == NULL) {
		return AVERROR(ENOMEM);
	}
	if ((ret = avcodec_parameters_to_context(p->dec, st->codecpar)) < 0) {
		return ret;
	}
	p->dec->pkt_timebase = st->time_base;
	if ((ret = avcodec_open2(p->dec, codec, NULL)) < 0) {
		return ret;
	}

	p->origin_ms = p->fmt->start_time == AV_NOPTS_VALUE ? 0 : av_rescale(p->fmt->start_time, 1000, AV_TIME_BASE);
	return from_ms > 0 ? pkp_pcm_seek(p, from_ms) : 0;
}

int pkp_pcm_seek(pkp_pcm *p, int64_t from_ms) {
	AVIOContext *pb = p->fmt->pb;
	if (pb != NULL) {
		// An interrupted or failed read leaves the I/O context marked at its
		// end, and it reads nothing more while marked: a short seek forward
		// would report the end of the file.
		pb->eof_reached = 0;
		pb->error = 0;
	}
	int64_t ts = av_rescale(from_ms + p->origin_ms, AV_TIME_BASE, 1000);
	int ret = avformat_seek_file(p->fmt, -1, INT64_MIN, ts, ts, 0);
	// Before anything was read a failed seek is not fatal: decoding runs
	// from the start and drops everything before from_ms. Later it would
	// run on from wherever the last read stopped.
	if (ret < 0 && p->read) {
		return ret;
	}
	// What the decoder and the resampler still hold is from before.
	avcodec_flush_buffers(p->dec);
	swr_free(&p->swr);
	p->from_ms = from_ms;
	p->next_ms = from_ms;
	p->draining = 0;
	p->flushed = 0;
	return 0;
}

static int reserve(pkp_pcm *p, int bytes) {
	if (bytes <= p->out_cap) {
		return 0;
	}
	uint8_t *out = realloc(p->out, bytes);
	if (out == NULL) {
		return AVERROR(ENOMEM);
	}
	p->out = out;
	p->out_cap = bytes;
	return 0;
}

static int configure(pkp_pcm *p, const AVFrame *f) {
	if (p->swr != NULL && f->sample_rate == p->in_rate && f->format == p->in_format &&
	    av_channel_layout_compare(&f->ch_layout, &p->in_layout) == 0) {
		return 0;
	}
	swr_free(&p->swr);
	AVChannelLayout mono = AV_CHANNEL_LAYOUT_MONO;
	int ret = swr_alloc_set_opts2(&p->swr, &mono, AV_SAMPLE_FMT_S16, PKP_PCM_RATE, &f->ch_layout, f->format,
	                              f->sample_rate, 0, NULL);
	if (ret < 0 || (ret = swr_init(p->swr)) < 0) {
		return ret;
	}
	av_channel_layout_uninit(&p->in_layout);
	if ((ret = av_channel_layout_copy(&p->in_layout, &f->ch_layout)) < 0) {
		return ret;
	}
	p->in_rate = f->sample_rate;
	p->in_format = f->format;
	return 0;
}

// emit hands out `got` converted samples starting at ms, minus whatever lies
// before from_ms. Returns 1 when something is handed out.
static int emit(pkp_pcm *p, int got, int64_t ms, const uint8_t **data, int *len, int64_t *pos_ms) {
	const uint8_t *start = p->out;
	if (ms < p->from_ms) {
		int64_t skip = (p->from_ms - ms) * PKP_PCM_RATE / 1000;
		if (skip >= got) {
			p->next_ms = ms + (int64_t)got * 1000 / PKP_PCM_RATE;
			return 0;
		}
		start += skip * 2;
		got -= (int)skip;
		ms = p->from_ms;
	}
	if (got <= 0) {
		return 0;
	}
	*data = start;
	*len = got * 2;
	*pos_ms = ms;
	p->next_ms = ms + (int64_t)got * 1000 / PKP_PCM_RATE;
	return 1;
}

static int convert(pkp_pcm *p, const AVFrame *f, const uint8_t **data, int *len, int64_t *pos_ms) {
	if (f->nb_samples <= 0 || f->sample_rate <= 0) {
		return 0;
	}
	int ret = configure(p, f);
	if (ret < 0) {
		return ret;
	}
	int64_t ms = p->next_ms;
	if (f->best_effort_timestamp != AV_NOPTS_VALUE) {
		ms = av_rescale_q(f->best_effort_timestamp, p->fmt->streams[p->stream]->time_base, (AVRational){1, 1000}) -
		     p->origin_ms;
	}
	int max = swr_get_out_samples(p->swr, f->nb_samples);
	if (max < 0) {
		return max;
	}
	if ((ret = reserve(p, max * 2)) < 0) {
		return ret;
	}
	uint8_t *out[1] = {p->out};
	int got = swr_convert(p->swr, out, max, (const uint8_t **)f->extended_data, f->nb_samples);
	if (got < 0) {
		return got;
	}
	return emit(p, got, ms, data, len, pos_ms);
}

// flush drains what the resampler still holds once the decoder is done.
static int flush(pkp_pcm *p, const uint8_t **data, int *len, int64_t *pos_ms) {
	if (p->swr == NULL) {
		return 0;
	}
	int max = swr_get_out_samples(p->swr, 0);
	if (max <= 0) {
		return 0;
	}
	int ret = reserve(p, max * 2);
	if (ret < 0) {
		return ret;
	}
	uint8_t *out[1] = {p->out};
	int got = swr_convert(p->swr, out, max, NULL, 0);
	if (got < 0) {
		return got;
	}
	return emit(p, got, p->next_ms, data, len, pos_ms);
}

int pkp_pcm_next(pkp_pcm *p, const uint8_t **data, int *len, int64_t *pos_ms) {
	for (;;) {
		int ret = avcodec_receive_frame(p->dec, p->frame);
		if (ret == 0) {
			ret = convert(p, p->frame, data, len, pos_ms);
			av_frame_unref(p->frame);
			if (ret != 0) {
				return ret < 0 ? ret : 0;
			}
			continue;
		}
		if (ret == AVERROR_EOF) {
			if (!p->flushed) {
				p->flushed = 1;
				if ((ret = flush(p, data, len, pos_ms)) != 0) {
					return ret < 0 ? ret : 0;
				}
			}
			return 1;
		}
		if (ret != AVERROR(EAGAIN)) {
			return ret;
		}

		p->read = 1;
		ret = av_read_frame(p->fmt, p->pkt);
		if (ret == AVERROR_EOF) {
			if (p->draining) {
				return 1;
			}
			p->draining = 1;
			avcodec_send_packet(p->dec, NULL);
			continue;
		}
		if (ret < 0) {
			return ret;
		}
		if (p->pkt->stream_index != p->stream) {
			av_packet_unref(p->pkt);
			continue;
		}
		ret = avcodec_send_packet(p->dec, p->pkt);
		av_packet_unref(p->pkt);
		// A damaged packet costs its own samples, not the stream.
		if (ret < 0 && ret != AVERROR(EAGAIN) && ret != AVERROR_INVALIDDATA) {
			return ret;
		}
	}
}

void pkp_pcm_close(pkp_pcm *p) {
	if (p == NULL) {
		return;
	}
	swr_free(&p->swr);
	av_channel_layout_uninit(&p->in_layout);
	av_frame_free(&p->frame);
	av_packet_free(&p->pkt);
	avcodec_free_context(&p->dec);
	avformat_close_input(&p->fmt);
	free(p->out);
	free(p);
}

void pkp_pcm_strerror(int err, char *buf, int size) {
	av_strerror(err, buf, size);
}
