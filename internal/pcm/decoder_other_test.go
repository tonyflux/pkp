//go:build !darwin || !cgo

package pcm

import (
	"context"
	"errors"
	"testing"
)

func TestUnavailableDecoderReportsUnsupported(t *testing.T) {
	if Supported {
		t.Fatal("a build without the native decoder must not report support")
	}
	ctx := context.Background()
	if d, err := Open(ctx, "http://localhost/media", -1, 0); d != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Open = %v, %v, want no decoder and ErrUnsupported", d, err)
	}
	d := &Decoder{}
	if err := d.Seek(ctx, 0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Seek = %v, want ErrUnsupported", err)
	}
	if data, _, err := d.Next(ctx); data != nil || !errors.Is(err, ErrUnsupported) {
		t.Errorf("Next = %v, %v, want no samples and ErrUnsupported", data, err)
	}
	d.Close()
}
