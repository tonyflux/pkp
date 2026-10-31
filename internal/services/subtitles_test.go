package services

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Subtitle Style reaches mpv, is saved, survives into a new DriveService (the next
// start) and refuses what mpv could not draw; the two shifts hold for one
// video and are back at 0 for the next.
func TestSubtitleStyleAndShifts(t *testing.T) {
	clip, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.mp4", time.Unix(0, 0), bytes.NewReader(clip))
	}))
	defer origin.Close()

	player := NewPlayerService(PlayerOptions{Headless: true, Logger: quiet()})
	if err := player.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = player.ServiceShutdown() })
	dir := t.TempDir()
	d := NewDriveService(DriveOptions{ConfigDir: dir, NoIndexSync: true, Logger: quiet()}, player)

	mpvStyle := func() (scale, pos float64, color string) {
		t.Helper()
		ctx := context.Background()
		for name, out := range map[string]any{"sub-scale": &scale, "sub-pos": &pos, "sub-color": &color} {
			if err := player.mpv.GetProperty(ctx, name, out); err != nil {
				t.Fatalf("get %s: %v", name, err)
			}
		}
		return scale, pos, color
	}
	if scale, pos, _ := mpvStyle(); scale != 1 || pos != 100 {
		t.Errorf("default style in mpv = scale %v pos %v, want 1 and 100", scale, pos)
	}

	style := SubtitleStyle{Scale: 1.6, Raise: 20, Color: "#ffd60a"}
	if err := d.SetSubtitleStyle(style); err != nil {
		t.Fatalf("SetSubtitleStyle: %v", err)
	}
	// mpv keeps sub-scale as a float32.
	scale, pos, color := mpvStyle()
	if math.Abs(scale-1.6) > 1e-6 || pos != 80 {
		t.Errorf("mpv sub-scale %v sub-pos %v, want 1.6 and 80", scale, pos)
	}
	// mpv writes colours back as #AARRGGBB.
	if color != "#FFFFD60A" && color != "#FFD60A" {
		t.Errorf("mpv sub-color = %q, want the yellow asked for", color)
	}
	want := SubtitleStyle{Scale: 1.6, Raise: 20, Color: "#FFD60A"}
	if got := player.State().SubtitleStyle; got != want {
		t.Errorf("state style = %+v, want %+v", got, want)
	}
	if got := NewDriveService(DriveOptions{ConfigDir: dir, NoIndexSync: true, Logger: quiet()}, nil).LocalSettings().SubtitleStyle; got != want {
		t.Errorf("saved style = %+v, want %+v", got, want)
	}
	for _, bad := range []SubtitleStyle{
		{Scale: 0.4, Color: "#FFFFFF"},
		{Scale: 1, Raise: 101, Color: "#FFFFFF"},
		{Scale: 1, Raise: -1, Color: "#FFFFFF"},
		{Scale: 1, Color: "yellow"},
	} {
		if err := d.SetSubtitleStyle(bad); err == nil {
			t.Errorf("SetSubtitleStyle(%+v) accepted", bad)
		}
	}
	if got := player.State().SubtitleStyle; got != want {
		t.Errorf("after refused styles, state style = %+v, want %+v", got, want)
	}

	if _, err := player.Open(origin.URL, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	waitFor(t, 20*time.Second, "the first video to load", func() bool { return player.State().Duration > 1 })
	if err := player.SetSubtitleDelay(1.5); err != nil {
		t.Fatalf("SetSubtitleDelay: %v", err)
	}
	if err := player.SetAudioDelay(-0.3); err != nil {
		t.Fatalf("SetAudioDelay: %v", err)
	}
	waitFor(t, 5*time.Second, "both shifts in the state", func() bool {
		s := player.State()
		return s.SubtitleDelay == 1.5 && s.AudioDelay == -0.3
	})
	if err := player.SetSubtitleDelay(601); err == nil {
		t.Error("SetSubtitleDelay(601) accepted")
	}

	if _, err := player.Open(origin.URL+"/next", nil); err != nil {
		t.Fatalf("open next: %v", err)
	}
	waitFor(t, 5*time.Second, "both shifts back at 0 for the next video", func() bool {
		s := player.State()
		return s.SubtitleDelay == 0 && s.AudioDelay == 0
	})
	if scale, pos, _ := mpvStyle(); math.Abs(scale-1.6) > 1e-6 || pos != 80 {
		t.Errorf("next video: sub-scale %v sub-pos %v, want the style kept", scale, pos)
	}
}
