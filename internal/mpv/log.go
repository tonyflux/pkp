package mpv

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// undecodable are the lines FFmpeg's HEVC decoder writes for every frame it
// drops because a frame it refers to was never decoded: each frame between a
// jump into an MPEG-TS stream (whose demuxer cannot seek to a keyframe) and
// the next keyframe — a resume, a quality switch, a seek past the buffer.
// Dozens per jump, two of them saying "Error", and nothing wrong is shown:
// the frames are skipped. The last one is written once per dropped frame.
var undecodable = []string{
	"Could not find ref with POC",
	"Error constructing the frame RPS",
	"Skipping invalid undecodable NALU",
}

const droppedFrameLine = "Skipping invalid undecodable NALU"

// dropWindow is how long a run of dropped frames is gathered before one
// line reports it. A variable for tests.
var dropWindow = time.Second

// mpvLog writes mpv's log messages to the client's logger, a run of
// undecodable lines as one line with the count of dropped frames.
type mpvLog struct {
	log *slog.Logger

	mu      sync.Mutex
	dropped int
	timer   *time.Timer
}

func (l *mpvLog) message(prefix, text string) {
	if prefix != "ffmpeg/video" || !isUndecodable(text) {
		l.log.Warn("mpv", "prefix", prefix, "text", text)
		return
	}
	l.log.Debug("mpv", "prefix", prefix, "text", text)
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.Contains(text, droppedFrameLine) {
		l.dropped++
	}
	if l.timer == nil {
		l.timer = time.AfterFunc(dropWindow, l.flush)
	}
}

func (l *mpvLog) flush() {
	l.mu.Lock()
	n := l.dropped
	l.dropped, l.timer = 0, nil
	l.mu.Unlock()
	if n > 0 {
		l.log.Info("mpv: video decoding resumed at the next keyframe after a jump; frames before it had no reference and were skipped", "frames", n)
	}
}

func isUndecodable(text string) bool {
	for _, s := range undecodable {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}
