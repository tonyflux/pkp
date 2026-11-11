// Package pcm decodes the audio of a media URL into what speech services
// take: 16 kHz, mono, signed 16-bit little-endian PCM, each run of samples
// with its position in the media, counted the way mpv counts time-pos.
//
// It reads the URL on its own connection (the loopback proxy's, in the app),
// so it can run ahead of playback and never touches the player.
package pcm

import "errors"

const (
	// SampleRate is the rate of every sample handed out.
	SampleRate = 16000
	// BytesPerMS is how much audio one millisecond is: 16 samples of 2 bytes.
	BytesPerMS = SampleRate * 2 / 1000
)

// ErrUnsupported is returned where no decoder is built in.
var ErrUnsupported = errors.New("pcm: audio decoding is not implemented on this platform")
