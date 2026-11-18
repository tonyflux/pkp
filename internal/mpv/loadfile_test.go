package mpv

import (
	"context"
	"testing"
	"time"
)

func TestLoadFileUsesNamedResumeOptions(t *testing.T) {
	c, events := startClient(t)
	ctx := context.Background()
	if err := c.SetProperty(ctx, "pause", true); err != nil {
		t.Fatal(err)
	}
	if err := c.LoadFile(ctx, testClip, 1); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, func(event Event) bool { return event.Name == EventFileLoaded }, 10*time.Second)
	var start string
	if err := c.GetProperty(ctx, "options/start", &start); err != nil {
		t.Fatal(err)
	}
	if start != "1" && start != "1.000000" && start != "1.000" {
		t.Fatalf("resume option=%q, want1 second", start)
	}
	if err := c.LoadFile(ctx, testClip, 0); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, func(event Event) bool { return event.Name == EventFileLoaded }, 10*time.Second)
	if err := c.GetProperty(ctx, "options/start", &start); err != nil {
		t.Fatal(err)
	}
	if start != "0" && start != "0.000000" && start != "0.000" {
		t.Fatalf("new load retained old resume position: %q", start)
	}
}
