//go:build !darwin || !cgo

package pcm

import "context"

// Supported reports whether this build includes the native audio decoder.
const Supported = false

// Decoder is the placeholder for platforms without a decoder; Open always
// fails, so nothing can look like it works.
type Decoder struct{}

func Open(context.Context, string, int, int64) (*Decoder, error) { return nil, ErrUnsupported }

func (d *Decoder) Seek(context.Context, int64) error { return ErrUnsupported }

func (d *Decoder) Next(context.Context) ([]byte, int64, error) { return nil, 0, ErrUnsupported }

func (d *Decoder) Close() {}
