//go:build darwin && cgo

package pcm

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/darwin/libmpv/include
#cgo LDFLAGS: -L${SRCDIR}/../../build/darwin/libmpv/lib -lavformat -lavcodec -lavutil -lswresample
#include <stdlib.h>
#include "decoder_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"unsafe"
)

// Supported reports whether this build includes the native audio decoder.
const Supported = true

// Decoder reads one audio stream. Its calls belong to one goroutine. Each
// call takes a context that interrupts it from anywhere; an interrupted call
// leaves the decoder fit for the next one, so a single connection, opened
// once, serves every position asked for.
type Decoder struct {
	// mu keeps an interrupt from reaching a decoder Close has freed, or a
	// call other than the one it was meant for.
	mu sync.Mutex
	p  *C.pkp_pcm
	// calls counts the calls begun and ended; an interrupt carries the
	// count its call began at.
	calls uint64
}

// Open connects to url and starts at fromMS of media time. stream is the
// container's index of the audio stream to decode (mpv's ff-index); any
// other value takes the file's best audio stream. Cancelling ctx makes a
// pending Open return ctx's error.
func Open(ctx context.Context, url string, stream int, fromMS int64) (*Decoder, error) {
	p := C.pkp_pcm_new()
	if p == nil {
		return nil, errors.New("pcm: out of memory")
	}
	d := &Decoder{p: p}
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	if ret := d.call(ctx, func() C.int { return C.pkp_pcm_open(p, cURL, C.int(stream), C.int64_t(max(fromMS, 0))) }); ret < 0 {
		d.Close()
		return nil, fail(ctx, "open", ret)
	}
	return d, nil
}

// Seek moves to fromMS of media time: the next Next starts there. It works
// at the end of the stream and after an interrupted call alike; it costs
// one request at the new position, not the file's header again.
func (d *Decoder) Seek(ctx context.Context, fromMS int64) error {
	if ret := d.call(ctx, func() C.int { return C.pkp_pcm_seek(d.p, C.int64_t(max(fromMS, 0))) }); ret < 0 {
		return fail(ctx, "seek", ret)
	}
	return nil
}

// Next returns the next samples and the media position, in milliseconds, of
// the first one; io.EOF after the last. The slice is only valid until the
// next call. Cancelling ctx makes it return ctx's error.
func (d *Decoder) Next(ctx context.Context) ([]byte, int64, error) {
	var data *C.uint8_t
	var n C.int
	var pos C.int64_t
	switch ret := d.call(ctx, func() C.int { return C.pkp_pcm_next(d.p, &data, &n, &pos) }); {
	case ret == 1:
		return nil, 0, io.EOF
	case ret < 0:
		return nil, 0, fail(ctx, "decode", ret)
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(data)), int(n)), int64(pos), nil
}

// Close frees the decoder and its connection.
func (d *Decoder) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.p != nil {
		C.pkp_pcm_close(d.p)
		d.p = nil
	}
}

// call runs one C call that cancelling ctx interrupts. Once it has returned,
// an interrupt meant for it can no longer land on the next call.
func (d *Decoder) call(ctx context.Context, f func() C.int) C.int {
	if ctx.Err() != nil {
		return C.int(-1)
	}
	d.mu.Lock()
	d.calls++
	began := d.calls
	d.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.calls == began && d.p != nil {
			C.pkp_pcm_interrupt(d.p)
		}
	})
	ret := f()
	stop()
	d.mu.Lock()
	d.calls++
	if d.p != nil {
		C.pkp_pcm_resume(d.p)
	}
	d.mu.Unlock()
	return ret
}

// fail names an FFmpeg error, or the cancellation that caused it.
func fail(ctx context.Context, op string, ret C.int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var buf [128]C.char
	C.pkp_pcm_strerror(ret, &buf[0], C.int(len(buf)))
	return fmt.Errorf("pcm: %s: %s", op, C.GoString(&buf[0]))
}
