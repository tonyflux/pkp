//go:build !darwin && !windows

package mpv

import (
	"context"
	"image"
	"log/slog"
	"unsafe"
)

// Client is the placeholder where no backend exists: embedded playback
// needs a native surface under a transparent webview, and only macOS and
// Windows have one so far.
//
// Every method fails loudly rather than silently doing nothing, so a build
// for another platform cannot look like it works.
type Client struct {
	log *slog.Logger
}

func New(opts Options) *Client {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Client{log: log}
}

func (c *Client) SetEventHandler(func(Event)) {}

func (c *Client) Running() bool { return false }

func (c *Client) Start(context.Context) error { return ErrUnsupported }

func (c *Client) Close() error { return nil }

func (c *Client) AttachSurface(unsafe.Pointer) error { return ErrUnsupported }

func (c *Client) MoveSurface(unsafe.Pointer) error { return ErrUnsupported }

func (c *Client) SetSurfaceRect(Rect) {}

func (c *Client) SurfaceStats() SurfaceStats { return SurfaceStats{} }

func (c *Client) Capture(context.Context) (*image.RGBA, error) { return nil, ErrUnsupported }

func (c *Client) Command(context.Context, ...any) error { return ErrUnsupported }

func (c *Client) GetProperty(context.Context, string, any) error { return ErrUnsupported }

func (c *Client) SetProperty(context.Context, string, any) error { return ErrUnsupported }

func (c *Client) ObserveProperty(context.Context, string) error { return ErrUnsupported }
