//go:build darwin

package audiotags

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// served renders an ffmpeg command's output and serves it over HTTP, which
// is how the app reads media (through the loopback proxy).
func served(t *testing.T, name string, args ...string) (string, []byte) {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, name, time.Unix(0, 0), bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/" + name, data
}

func cover(t *testing.T) string {
	t.Helper()
	_, png := served(t, "cover.png", "-f", "lavfi", "-i", "color=c=orange:s=64x64", "-frames:v", "1")
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func tagged(art string, extra ...string) []string {
	return append([]string{
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-i", art,
		"-map", "0:a", "-map", "1:v", "-disposition:v", "attached_pic",
		"-metadata", "title=海边的晚风", "-metadata", "artist=旅行录音", "-metadata", "album=夏", "-metadata", "date=2025",
	}, extra...)
}

func TestReadsTagsAndCover(t *testing.T) {
	art := cover(t)
	for _, name := range []string{"song.mp3", "song.flac", "song.m4a"} {
		t.Run(name, func(t *testing.T) {
			url, _ := served(t, name, tagged(art, "-c:v", "png")...)
			tags, err := Read(context.Background(), url)
			if err != nil {
				t.Fatal(err)
			}
			if tags.Title != "海边的晚风" || tags.Artist != "旅行录音" || tags.Album != "夏" || tags.Date != "2025" {
				t.Errorf("tags = %+v", tags)
			}
			if tags.CoverType != "image/png" || !bytes.HasPrefix(tags.Cover, []byte("\x89PNG")) {
				t.Errorf("cover = %q, %d bytes", tags.CoverType, len(tags.Cover))
			}
		})
	}
}

// A file without a picture or tags answers with nothing, not an error.
func TestBareFile(t *testing.T) {
	url, _ := served(t, "bare.mp3", "-f", "lavfi", "-i", "sine=duration=1", "-map_metadata", "-1")
	tags, err := Read(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if tags.Cover != nil || tags.CoverType != "" || tags.Title != "" || tags.Artist != "" {
		t.Errorf("tags = %+v", tags)
	}
}

// A connection that never answers must not hold the caller.
func TestCancelInterruptsAStalledOpen(t *testing.T) {
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-hold }))
	defer srv.Close()
	defer close(hold)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Read(ctx, srv.URL+"/x.mp3"); err == nil {
		t.Fatal("read of a stalled source succeeded")
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Errorf("returned after %v", waited)
	}
}
