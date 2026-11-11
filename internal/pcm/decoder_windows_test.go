//go:build windows

package pcm

import (
	"context"
	"errors"
	"testing"
)

func TestOpenRejectsNULInURL(t *testing.T) {
	if d, err := Open(context.Background(), "http://localhost/media\x00other", -1, 0); d != nil || err == nil {
		t.Fatalf("Open with NUL = %v, %v, want no decoder and an error", d, err)
	}
}

func TestOpenWithCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if d, err := Open(ctx, "http://localhost/media", -1, 0); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Open already cancelled = %v, %v, want no decoder and context.Canceled", d, err)
	}
}
