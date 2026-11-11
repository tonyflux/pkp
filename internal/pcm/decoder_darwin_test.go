//go:build darwin && cgo

package pcm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// render makes an ffmpeg command's output, once per test.
func render(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("ffmpeg", append(append([]string{"-v", "error"}, args...), out)...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fixture renders an ffmpeg command's output and serves it over HTTP, which
// is how the app reads media (through the loopback proxy).
func fixture(t *testing.T, name string, args ...string) string {
	t.Helper()
	data := render(t, name, args...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, name, time.Unix(0, 0), bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/" + name
}

// tones is 3 s of 1000 Hz then 3 s of 440 Hz, 48 kHz stereo: rate and
// channels both need converting, and the switch marks a known position. The
// audio is the filter's [a]; more inputs follow the two sines.
func tones(more ...string) []string {
	return append([]string{
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=3",
		"-filter_complex", "[0][1]concat=n=2:v=0:a=1,pan=stereo|c0=c0|c1=c0[a]",
	}, more...)
}

// decoded is a run of samples placed on the media timeline.
type decoded struct {
	first, end int64 // ms
	samples    []int16
	gaps       int // places where a run did not continue the previous one
}

func decodeAll(t *testing.T, url string, stream int, from int64) decoded {
	t.Helper()
	d, err := Open(context.Background(), url, stream, from)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	return read(t, d, 0)
}

// read decodes on to the end, or, with ms > 0, until it has that much.
func read(t *testing.T, d *Decoder, ms int64) decoded {
	t.Helper()
	out := decoded{first: -1}
	for ms <= 0 || out.first < 0 || out.end-out.first < ms {
		data, pos, err := d.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		if len(data)%2 != 0 || len(data) == 0 {
			t.Fatalf("got %d bytes, want whole samples", len(data))
		}
		if out.first < 0 {
			out.first = pos
		} else if d := pos - out.end; d < -2 || d > 2 {
			out.gaps++
		}
		for i := 0; i < len(data); i += 2 {
			out.samples = append(out.samples, int16(binary.LittleEndian.Uint16(data[i:])))
		}
		out.end = pos + int64(len(data)/2)*1000/SampleRate
	}
	return out
}

// pitch is the frequency of the 100 ms that start at media time ms, from
// zero crossings.
func (d decoded) pitch(ms int64) float64 {
	from := int((ms - d.first) * SampleRate / 1000)
	window := d.samples[from : from+SampleRate/10]
	crossings := 0
	for i := 1; i < len(window); i++ {
		if (window[i-1] < 0) != (window[i] < 0) {
			crossings++
		}
	}
	return float64(crossings) / 2 / 0.1
}

func near(got, want, tolerance float64) bool { return math.Abs(got-want) <= tolerance }

func TestDecodesWholeFileOnTheMediaTimeline(t *testing.T) {
	if !Supported {
		t.Fatal("native decoder must report that it is supported")
	}
	url := fixture(t, "tones.mp4", tones("-map", "[a]", "-c:a", "aac")...)
	d := decodeAll(t, url, -1, 0)
	if d.first > 25 || d.gaps != 0 {
		t.Fatalf("first sample at %d ms with %d gaps, want 0 ms and none", d.first, d.gaps)
	}
	if !near(float64(d.end), 6000, 50) {
		t.Errorf("audio ends at %d ms, want 6000", d.end)
	}
	if got := len(d.samples); !near(float64(got), 6*SampleRate, SampleRate/20) {
		t.Errorf("%d samples, want 6 s at 16 kHz", got)
	}
	if p := d.pitch(1000); !near(p, 1000, 30) {
		t.Errorf("pitch at 1 s = %.0f Hz, want 1000", p)
	}
	if p := d.pitch(4500); !near(p, 440, 30) {
		t.Errorf("pitch at 4.5 s = %.0f Hz, want 440", p)
	}
}

// A start position skips to it: the first sample is the one at that media
// time (an AAC decoder opened mid-stream drops its first frame, ~21 ms), not
// the keyframe before it.
func TestStartsAtThePositionAsked(t *testing.T) {
	url := fixture(t, "tones.mp4", tones("-map", "[a]", "-c:a", "aac")...)
	d := decodeAll(t, url, -1, 3500)
	if d.first < 3500 || d.first > 3525 {
		t.Fatalf("first sample at %d ms, want 3500", d.first)
	}
	if p := d.pitch(3550); !near(p, 440, 30) {
		t.Errorf("pitch at the start = %.0f Hz, want the 440 Hz half", p)
	}
}

// PikPak's transcodes are MPEG-TS, whose timestamps do not start at 0; mpv
// counts from the container's start, and so must the positions here.
func TestTransportStreamPositionsCountFromItsStart(t *testing.T) {
	url := fixture(t, "tones.ts", tones("-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=6",
		"-map", "2:v", "-map", "[a]", "-c:v", "libx264", "-c:a", "aac", "-f", "mpegts")...)
	whole := decodeAll(t, url, -1, 0)
	if whole.first > 80 {
		t.Fatalf("first sample at %d ms, want the stream's start at 0", whole.first)
	}
	if p := whole.pitch(2800); !near(p, 1000, 30) {
		t.Errorf("pitch at 2.8 s = %.0f Hz, want 1000", p)
	}
	if p := whole.pitch(3100); !near(p, 440, 30) {
		t.Errorf("pitch at 3.1 s = %.0f Hz, want 440", p)
	}
	late := decodeAll(t, url, -1, 4000)
	if late.first < 4000 || late.first > 4030 {
		t.Errorf("seeked first sample at %d ms, want 4000", late.first)
	}
}

// With two audio tracks the one the user listens to is the one decoded.
func TestDecodesTheChosenStream(t *testing.T) {
	url := fixture(t, "two.mkv",
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=44100:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=2",
		"-map", "0", "-map", "1", "-c:a", "flac")
	for _, c := range []struct {
		stream int
		want   float64
	}{{0, 1000}, {1, 440}, {7, 1000}} {
		if p := decodeAll(t, url, c.stream, 0).pitch(1000); !near(p, c.want, 30) {
			t.Errorf("stream %d: pitch %.0f Hz, want %.0f", c.stream, p, c.want)
		}
	}
}

// A connection that never answers must not hold the caller: cancelling the
// context ends the open.
func TestCancelInterruptsAStalledOpen(t *testing.T) {
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hold:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(hold)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := Open(ctx, srv.URL+"/x.mp4", -1, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("open = %v, want context.Canceled", err)
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Errorf("open returned %v after the cancel, want promptly", waited)
	}
}

// One open decoder goes anywhere: back once it has reached the end, and
// forward in the middle of the stream. Each time the first sample is the one
// at the position asked, with the sound that is there.
func TestSeeksTheOpenDecoder(t *testing.T) {
	url := fixture(t, "tones.mp4", tones("-map", "[a]", "-c:a", "aac")...)
	d, err := Open(context.Background(), url, -1, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	read(t, d, 0)
	for _, c := range []struct {
		at, probe int64
		want      float64
	}{{1000, 1500, 1000}, {4500, 4600, 440}} {
		if err := d.Seek(context.Background(), c.at); err != nil {
			t.Fatalf("seek to %d ms: %v", c.at, err)
		}
		got := read(t, d, 1000)
		if got.first < c.at || got.first > c.at+25 || got.gaps != 0 {
			t.Errorf("after a seek to %d ms: first sample at %d ms with %d gaps", c.at, got.first, got.gaps)
		}
		if p := got.pitch(c.probe); !near(p, c.want, 30) {
			t.Errorf("after a seek to %d ms: pitch at %d ms = %.0f Hz, want %.0f", c.at, c.probe, p, c.want)
		}
	}
}

// A cancelled call ends promptly and leaves the decoder as it was: the next
// call, on another context, seeks and decodes as if nothing had happened.
func TestCancelLeavesTheDecoderUsable(t *testing.T) {
	data := render(t, "tones.mp4", tones("-map", "[a]", "-c:a", "aac")...)
	var held atomic.Bool
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if held.Load() {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "tones.mp4", time.Unix(0, 0), bytes.NewReader(data))
	}))
	defer srv.Close()
	defer close(release)

	d, err := Open(context.Background(), srv.URL+"/tones.mp4", -1, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	read(t, d, 0) // at the end: going back takes a new request
	held.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	err = d.Seek(ctx, 500)
	if err == nil {
		_, _, err = d.Next(ctx)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("seek and decode with the source holding = %v, want context.Canceled", err)
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Errorf("returned %v after the cancel, want promptly", waited)
	}

	held.Store(false)
	if err := d.Seek(context.Background(), 1500); err != nil {
		t.Fatalf("seek after the cancel: %v", err)
	}
	got := read(t, d, 500)
	if got.first < 1500 || got.first > 1525 {
		t.Errorf("first sample after the cancel at %d ms, want 1500", got.first)
	}
	if p := got.pitch(1600); !near(p, 1000, 30) {
		t.Errorf("pitch after the cancel = %.0f Hz, want 1000", p)
	}
}

// A read cancelled in the middle of a body, while the source has stopped
// sending, leaves the decoder fit to go on: a short seek forward reads on
// from the same connection, with the sound that is there. FFmpeg marks its
// I/O as ended after such a read and would never read again.
func TestReadsOnAfterACancelMidBody(t *testing.T) {
	data := render(t, "long.mp4", "-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:duration=30",
		"-ac", "2", "-c:a", "aac", "-b:a", "256k")
	// Once armed, the next answer sends 256 KB, then nothing until released.
	var armed atomic.Bool
	stalled, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var from int
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &from)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(data)-1, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)-from))
		w.WriteHeader(http.StatusPartialContent)
		body := data[from:]
		if armed.CompareAndSwap(true, false) {
			w.Write(body[:256<<10])
			w.(http.Flusher).Flush()
			close(stalled)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			body = body[256<<10:]
		}
		w.Write(body)
	}))
	defer srv.Close()

	d, err := Open(context.Background(), srv.URL+"/long.mp4", -1, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	read(t, d, 0) // at the end: going back takes a new request
	armed.Store(true)
	if err := d.Seek(context.Background(), 5000); err != nil {
		t.Fatalf("seek: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var last int64
	var cut error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			data, pos, err := d.Next(ctx)
			if err != nil {
				cut = err
				return
			}
			last = pos + int64(len(data)/2)*1000/SampleRate
		}
	}()
	select {
	case <-stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("the source was never asked again")
	}
	time.Sleep(300 * time.Millisecond) // the 256 KB go in a moment; then the read waits
	cancel()
	<-done
	if !errors.Is(cut, context.Canceled) || last <= 5000 {
		t.Fatalf("decoding stopped at %d ms with %v, want cancelled past 5000 ms", last, cut)
	}

	close(release)
	at := last + 200
	if err := d.Seek(context.Background(), at); err != nil {
		t.Fatalf("seek after the cancel: %v", err)
	}
	got := read(t, d, 500)
	if got.first < at || got.first > at+25 {
		t.Errorf("first sample after the cancel at %d ms, want %d", got.first, at)
	}
	if p := got.pitch(at + 100); !near(p, 1000, 30) {
		t.Errorf("pitch after the cancel = %.0f Hz, want 1000", p)
	}
}
