package mpv

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A jump into an HEVC TS stream makes FFmpeg write three lines per dropped
// frame (as seen in the app's log); the log at its usual level shows one
// line per run with the frame count, and other mpv messages as before.
func TestUndecodableFramesCollapseIntoOneLine(t *testing.T) {
	old := dropWindow
	dropWindow = 50 * time.Millisecond
	t.Cleanup(func() { dropWindow = old })

	var out syncBuffer
	l := &mpvLog{log: slog.New(slog.NewTextHandler(&out, nil))}
	burst := func(frames int) {
		for i := range frames {
			l.message("ffmpeg/video", "hevc: Could not find ref with POC 99")
			l.message("ffmpeg/video", "hevc: Error constructing the frame RPS.")
			l.message("ffmpeg/video", "hevc: Skipping invalid undecodable NALU: "+string(rune('0'+i%2)))
		}
	}
	burst(3)
	l.message("ffmpeg/demuxer", "http: HTTP error 503")
	time.Sleep(200 * time.Millisecond)
	burst(2)
	time.Sleep(200 * time.Millisecond)

	got := out.String()
	if strings.Contains(got, "Could not find ref") || strings.Contains(got, "frame RPS") || strings.Contains(got, "NALU") {
		t.Errorf("per-frame decoder lines reached the log:\n%s", got)
	}
	if !strings.Contains(got, `level=WARN msg=mpv prefix=ffmpeg/demuxer text="http: HTTP error 503"`) {
		t.Errorf("other mpv messages must still be logged as warnings:\n%s", got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	var runs []string
	for _, line := range lines {
		if strings.Contains(line, "skipped") {
			runs = append(runs, line)
		}
	}
	if len(runs) != 2 || !strings.HasSuffix(runs[0], "frames=3") || !strings.HasSuffix(runs[1], "frames=2") {
		t.Errorf("want one line per run, frames=3 then frames=2; got %q", runs)
	}
}
