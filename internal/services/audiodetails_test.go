package services

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The music page reads the playing file's own tags and cover, and only the
// playing file's.
func TestAudioDetailsReadsThePlayingFile(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	art, song := filepath.Join(dir, "art.png"), filepath.Join(dir, "song.mp3")
	for _, args := range [][]string{
		{"-f", "lavfi", "-i", "color=c=orange:s=32x32", "-frames:v", "1", art},
		{"-f", "lavfi", "-i", "sine=duration=3", "-i", art, "-map", "0:a", "-map", "1:v", "-c:v", "png",
			"-disposition:v", "attached_pic", "-metadata", "title=海边的晚风", "-metadata", "artist=旅行录音", song},
	} {
		if msg, err := exec.Command("ffmpeg", append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, msg)
		}
	}
	data, err := os.ReadFile(song)
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := serveClip(t, data)
	d, p := playingDrive(t, func() string {
		return `{"kind":"drive#file","id":"f1","name":"song.mp3","phase":"PHASE_TYPE_COMPLETE","mime_type":"audio/mpeg",
			"medias":[{"media_id":"m0","media_name":"Original","is_origin":true,"is_visible":true,"link":{"url":"` + srv.URL + `/song.mp3"}}]}`
	})

	if _, err := p.AudioDetails("f1"); !errors.Is(err, errNotPlaying) {
		t.Fatalf("before playing: %v", err)
	}
	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "the song to load", func() bool { return p.State().Duration > 1 })

	got, err := p.AudioDetails("f1")
	if err != nil {
		t.Fatal(err)
	}
	if got.FileID != "f1" || got.Title != "海边的晚风" || got.Artist != "旅行录音" || !strings.HasPrefix(got.Cover, "data:image/png;base64,") {
		t.Errorf("details = %q %q %q, cover %.30q", got.FileID, got.Title, got.Artist, got.Cover)
	}
	if _, err := p.AudioDetails("f2"); !errors.Is(err, errNotPlaying) {
		t.Errorf("another file: %v", err)
	}
}
