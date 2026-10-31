package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/tonyflux/pkp/internal/drive"
)

// A transcode carries none of the original's subtitles; PikPak's extracted
// copies are added to it, a choice made on the original carries over to its
// copy, and the original itself gets nothing added.
func TestExtractedSubtitlesFollowTheTranscode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	const gcid = "0123456789ABCDEF0123456789ABCDEF01234567"
	dir := t.TempDir()
	srt := filepath.Join(dir, "chs.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:02,000\n内封\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The original: a Matroska file with its own subtitle, titled as
	// Chinese releases title it. The transcode: the bare clip.
	origin := filepath.Join(dir, "origin.mkv")
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", fixture, "-i", srt, "-map", "0", "-map", "1",
		"-c:v", "copy", "-c:a", "copy", "-c:s", "srt", "-metadata:s:s:0", "language=chi",
		"-metadata:s:s:0", "title=简体中字", "-disposition:s:0", "default", origin)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, msg)
	}
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := fixture
		if r.URL.Path == "/origin" {
			name = origin
		}
		http.ServeFile(w, r, name)
	}))
	t.Cleanup(media.Close)

	// The extracted files, one of them UTF-16 as some releases ship.
	utf16le := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune("1\r\n00:00:00,000 --> 00:00:02,000\r\n提取的中字\r\n")) {
		utf16le = append(utf16le, byte(u), byte(u>>8))
	}
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eng.srt":
			fmt.Fprint(w, "1\n00:00:00,000 --> 00:00:02,000\nextracted english\n")
		case "/chs.srt":
			w.Write(utf16le)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(files.Close)

	var mu sync.Mutex
	var asked []string
	oracle := func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/oracle/subtitle" {
			return false
		}
		mu.Lock()
		asked = append(asked, r.URL.RawQuery+" auth="+fmt.Sprint(r.Header.Get("Authorization") != ""))
		mu.Unlock()
		list := []drive.Subtitle{
			{URL: files.URL + "/eng.srt", Ext: "srt", Languages: []string{"eng"}, Source: drive.SubtitleExtracted},
			{URL: files.URL + "/chs.srt", Ext: "srt", Languages: []string{"简体中字"}, Source: drive.SubtitleExtracted},
			// Not the video's own, or not text: left out.
			{URL: files.URL + "/eng.srt", Ext: "srt", Languages: []string{"fre"}, Source: drive.SubtitleUploaded},
			{URL: files.URL + "/eng.srt", Ext: "sup", Languages: []string{"jpn"}, Source: drive.SubtitleExtracted},
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "result": "ok", "data": list})
		return true
	}
	video := fmt.Sprintf(`{"kind":"drive#file","id":"f1","name":"clip.mkv","phase":"PHASE_TYPE_COMPLETE","hash":%q,
		"medias":[
		 {"media_id":"m0","media_name":"Original","is_origin":true,"is_visible":true,
		  "video":{"width":1920,"height":1080,"bit_rate":5000000,"duration":2},"link":{"url":%q}},
		 {"media_id":"m1","media_name":"480P","is_visible":true,
		  "video":{"width":852,"height":480,"bit_rate":500000,"duration":2},"link":{"url":%q}}]}`,
		strings.ToLower(gcid), media.URL+"/origin", media.URL+"/t480")
	d, p := playingDriveWith(t, oracle, func() string { return video })

	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 20*time.Second, "the original to play", func() bool { return p.State().Duration > 1.5 })
	if err := p.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "the extracted subtitles to arrive", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.subs.items) == 2
	})
	tracks, err := p.Tracks()
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range tracks {
		if tr.External {
			t.Fatalf("the original got an extracted track: %+v", tr)
		}
	}
	if own := selectedTrack(tracks, "sub"); own.title != "简体中字" {
		t.Fatalf("the original's own subtitle is not on (%+v); the carry-over proves nothing", own)
	}
	mu.Lock()
	if len(asked) != 1 || !strings.Contains(asked[0], "gcid="+gcid) || !strings.Contains(asked[0], "duration=2") || !strings.HasSuffix(asked[0], "auth=true") {
		t.Errorf("subtitles asked as %q, want once, by the upper-case gcid and duration, signed in", asked)
	}
	mu.Unlock()

	if err := d.SetPlaybackQuality("480"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	waitFor(t, 20*time.Second, "the transcode with its extracted subtitles, 简体中字 still on", func() bool {
		if p.State().Quality.Current != "480" {
			return false
		}
		tracks, err := p.Tracks()
		if err != nil {
			return false
		}
		var langs []string
		for _, tr := range tracks {
			if tr.Type == "sub" && tr.External {
				langs = append(langs, tr.Lang)
			}
		}
		return strings.Join(langs, ",") == "eng,简体中字" && selectedTrack(tracks, "sub").lang == "简体中字"
	})
	waitFor(t, 5*time.Second, "the UTF-16 file's text on screen", func() bool {
		var text string
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.mpv.GetProperty(ctx, "sub-text", &text)
		return text == "提取的中字"
	})
}

// Started on a transcode, the extracted subtitles come with it and the
// service's best one shows, as the original would show its default track.
func TestExtractedSubtitlesOnAFirstTranscode(t *testing.T) {
	media, _ := serveClip(t, mustRead(t, fixture))
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "1\n00:00:00,000 --> 00:00:02,000\n"+strings.TrimPrefix(r.URL.Path, "/")+"\n")
	}))
	t.Cleanup(files.Close)
	oracle := func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/oracle/subtitle" {
			return false
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": []drive.Subtitle{
			{URL: files.URL + "/best", Ext: "srt", Languages: []string{"chiSimplified"}, Source: drive.SubtitleExtracted},
			{URL: files.URL + "/other", Ext: "srt", Languages: []string{"eng"}, Source: drive.SubtitleExtracted},
		}})
		return true
	}
	video := strings.Replace(videoJSON(media.URL+"/origin", media.URL+"/t480"), `"phase"`, `"hash":"0123456789ABCDEF0123456789ABCDEF01234567","phase"`, 1)
	d, p := playingDriveWith(t, oracle, func() string { return video })
	if err := d.SetPlaybackQuality("480"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PlayFile("", "f1"); err != nil {
		t.Fatalf("play: %v", err)
	}
	waitFor(t, 20*time.Second, "the transcode with the best extracted subtitle on", func() bool {
		if p.State().Quality.Current != "480" {
			return false
		}
		tracks, err := p.Tracks()
		if err != nil {
			return false
		}
		n := 0
		for _, tr := range tracks {
			if tr.External {
				n++
			}
		}
		return n == 2 && selectedTrack(tracks, "sub").lang == "chiSimplified"
	})
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
