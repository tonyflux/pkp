// Package audiotags reads what a music file says about itself — title,
// artist, album, date and the embedded cover picture — from the container's
// header, on its own connection to a media URL (the loopback proxy's, in the
// app). Nothing is decoded: FFmpeg's demuxer has all of it once the file is
// open, so only the first part of the file (or an MP4's moov) is read.
package audiotags

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// MaxCover is the largest cover picture handed out; bigger ones are left
// out rather than pushed whole through the webview.
const MaxCover = 8 << 20

// Tags is what the file names itself. Empty fields are unknown.
type Tags struct {
	Title  string
	Artist string
	Album  string
	Date   string
	// Cover is the embedded picture's bytes and CoverType its MIME type
	// (image/jpeg, image/png…); both empty when the file has none, or one
	// in a format a webview cannot show.
	Cover     []byte
	CoverType string
}

// ErrUnsupported is returned where no reader is built in.
var ErrUnsupported = errors.New("audiotags: not implemented on this platform")

// clean trims a tag and repairs the commonest damage in Chinese music
// files: GBK (or UTF-8) bytes in a tag declared Latin-1, which FFmpeg then
// widens rune by rune into "ÁúÉ½" lookalikes. Such a string has only runes
// up to U+00FF; read back as bytes it is either valid UTF-8 or GBK that
// decodes to CJK text. A genuine Latin-1 tag ("Café") is neither and stays.
func clean(s string) string {
	s = strings.TrimSpace(strings.Trim(s, "\x00"))
	raw := make([]byte, 0, len(s))
	high := false
	for _, r := range s {
		if r > 0xFF {
			return s
		}
		if r >= 0x80 {
			high = true
		}
		raw = append(raw, byte(r))
	}
	if !high {
		return s
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	if err != nil || !allCJK(string(decoded)) {
		return s
	}
	return string(decoded)
}

// allCJK reports whether every non-ASCII rune of s is Chinese or Japanese
// script or their punctuation and full-width forms.
func allCJK(s string) bool {
	for _, r := range s {
		switch {
		case r < 0x80,
			r >= 0x3000 && r <= 0x30FF, // CJK punctuation, kana
			r >= 0x3400 && r <= 0x9FFF, // ideographs
			r >= 0xFF00 && r <= 0xFFEF: // full-width forms
		default:
			return false
		}
	}
	return true
}
