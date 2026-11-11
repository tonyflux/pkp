//go:build windows

package pcm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supported reports whether this build includes the native audio decoder.
const Supported = true

// The installer places the statically linked decoder beside pkp.exe. Keep its
// module loaded for the process lifetime, including while cancellation runs.
var lib = sync.OnceValues(loadLibrary)

type library struct {
	module                       *windows.DLL
	new, open, seek, next, close *windows.Proc
	interrupt, resume, strerror  *windows.Proc
}

func loadLibrary() (*library, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("pcm: executable path: %w", err)
	}
	path := filepath.Join(filepath.Dir(exe), "pkp-pcm.dll")
	// Never search the working directory or PATH for native dependencies.
	module, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("pcm: load %s: %w", path, err)
	}
	dll := &windows.DLL{Name: path, Handle: module}
	l := &library{module: dll}
	for name, proc := range map[string]**windows.Proc{
		"pkp_pcm_new":       &l.new,
		"pkp_pcm_open":      &l.open,
		"pkp_pcm_seek":      &l.seek,
		"pkp_pcm_next":      &l.next,
		"pkp_pcm_close":     &l.close,
		"pkp_pcm_interrupt": &l.interrupt,
		"pkp_pcm_resume":    &l.resume,
		"pkp_pcm_strerror":  &l.strerror,
	} {
		if *proc, err = dll.FindProc(name); err != nil {
			dll.Release()
			return nil, fmt.Errorf("pcm: load %s: %w", path, err)
		}
	}
	return l, nil
}

// Decoder reads one audio stream. Its calls belong to one goroutine. Each
// call takes a context that interrupts it from anywhere; an interrupted call
// leaves the decoder fit for the next one, so a single connection, opened
// once, serves every position asked for.
type Decoder struct {
	// mu keeps an interrupt from reaching a decoder Close has freed, or a
	// call other than the one it was meant for.
	mu sync.Mutex
	l  *library
	p  uintptr
	// calls counts the calls begun and ended; an interrupt carries the
	// count its call began at.
	calls uint64
}

// Open connects to url and starts at fromMS of media time. stream is the
// container's index of the audio stream to decode (mpv's ff-index); any
// other value takes the file's best audio stream. Cancelling ctx makes a
// pending Open return ctx's error.
func Open(ctx context.Context, url string, stream int, fromMS int64) (*Decoder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cURL, err := windows.BytePtrFromString(url)
	if err != nil {
		return nil, fmt.Errorf("pcm: open URL: %w", err)
	}
	l, err := lib()
	if err != nil {
		return nil, err
	}
	p, _, _ := l.new.Call()
	if p == 0 {
		return nil, errors.New("pcm: out of memory")
	}
	d := &Decoder{l: l, p: p}
	ret := d.call(ctx, func() int32 {
		r, _, _ := l.open.Call(p, uintptr(unsafe.Pointer(cURL)), uintptr(int32(stream)), uintptr(max(fromMS, 0)))
		return int32(r)
	})
	runtime.KeepAlive(cURL)
	if ret < 0 {
		d.Close()
		return nil, l.fail(ctx, "open", ret)
	}
	return d, nil
}

// Seek moves to fromMS of media time: the next Next starts there. It works
// at the end of the stream and after an interrupted call alike; it costs
// one request at the new position, not the file's header again.
func (d *Decoder) Seek(ctx context.Context, fromMS int64) error {
	ret := d.call(ctx, func() int32 {
		r, _, _ := d.l.seek.Call(d.p, uintptr(max(fromMS, 0)))
		return int32(r)
	})
	if ret < 0 {
		return d.l.fail(ctx, "seek", ret)
	}
	return nil
}

// Next returns the next samples and the media position, in milliseconds, of
// the first one; io.EOF after the last. The slice is only valid until the
// next call. Cancelling ctx makes it return ctx's error.
func (d *Decoder) Next(ctx context.Context) ([]byte, int64, error) {
	var data *byte
	var n int32
	var pos int64
	ret := d.call(ctx, func() int32 {
		r, _, _ := d.l.next.Call(d.p, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&pos)))
		return int32(r)
	})
	switch {
	case ret == 1:
		return nil, 0, io.EOF
	case ret < 0:
		return nil, 0, d.l.fail(ctx, "decode", ret)
	}
	return unsafe.Slice(data, int(n)), pos, nil
}

// Close frees the decoder and its connection.
func (d *Decoder) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.p != 0 {
		d.l.close.Call(d.p)
		d.p = 0
	}
}

// call runs one native call that cancelling ctx interrupts. Once it has
// returned, an interrupt meant for it can no longer land on the next call.
func (d *Decoder) call(ctx context.Context, f func() int32) int32 {
	if ctx.Err() != nil {
		return -1
	}
	d.mu.Lock()
	d.calls++
	began := d.calls
	d.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.calls == began && d.p != 0 {
			d.l.interrupt.Call(d.p)
		}
	})
	ret := f()
	stop()
	d.mu.Lock()
	d.calls++
	if d.p != 0 {
		d.l.resume.Call(d.p)
	}
	d.mu.Unlock()
	return ret
}

// fail names an FFmpeg error, or the cancellation that caused it. Native
// return values are C ints: only their low 32 bits are defined on Windows.
func (l *library) fail(ctx context.Context, op string, ret int32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var buf [128]byte
	l.strerror.Call(uintptr(ret), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return fmt.Errorf("pcm: %s: %s", op, windows.ByteSliceToString(buf[:]))
}
