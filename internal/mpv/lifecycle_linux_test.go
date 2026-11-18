//go:build linux

package mpv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Wails may shut down services after startup fails, before GTK starts its loop.
// A core that never attached must not marshal its teardown to that absent loop.
func TestLinuxCloseWithoutSurface(t *testing.T) {
	c := New(Options{Logger: quiet(), Extra: map[string]string{"ao": "null"}})
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for a GTK loop although no surface ever attached")
	}
}

func TestLinuxPlaybackIgnoresInheritedProxy(t *testing.T) {
	// libavformat honors lowercase http_proxy even for loopback by default.
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("no_proxy", "")
	server := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	defer server.Close()
	c, events := startClient(t)
	if err := c.Command(context.Background(), "loadfile", server.URL+"/clip.mp4"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, func(event Event) bool { return event.Name == EventFileLoaded }, 10*time.Second)
}
