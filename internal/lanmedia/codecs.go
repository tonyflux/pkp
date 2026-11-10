package lanmedia

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
)

func fraction(value, separator string) float64 {
	a, b, ok := strings.Cut(value, separator)
	if !ok {
		return 0
	}
	n, err := strconv.ParseFloat(a, 64)
	if err != nil {
		return 0
	}
	d, err := strconv.ParseFloat(b, 64)
	if err != nil || d <= 0 {
		return 0
	}
	return n / d
}

// ffprobe shares the Matroska demuxer name with WebM. Read the EBML DocType
// rather than trusting the file extension or guessing from its streams.
func webmHeader(header []byte) bool {
	if !bytes.HasPrefix(header, []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		return false
	}
	for i := 4; i+3 < len(header); i++ {
		if header[i] != 0x42 || header[i+1] != 0x82 {
			continue
		}
		first := header[i+2]
		mask, width := byte(0x80), 1
		for width <= 8 && first&mask == 0 {
			mask >>= 1
			width++
		}
		if width > 8 || i+2+width > len(header) {
			return false
		}
		size := uint64(first & (mask - 1))
		for j := 1; j < width; j++ {
			size = size<<8 | uint64(header[i+2+j])
		}
		start := i + 2 + width
		return size == 4 && start+4 <= len(header) && string(header[start:start+4]) == "webm"
	}
	return false
}

func videoMIME(info Info, dump string) string {
	data := codecData(dump)
	switch info.VideoCodec {
	case "h264":
		if len(data) >= 4 && data[0] == 1 {
			return fmt.Sprintf("video/mp4; codecs=\"avc1.%02X%02X%02X\"", data[1], data[2], data[3])
		}
	case "hevc":
		if len(data) >= 13 && data[0] == 1 {
			space := []string{"", "A", "B", "C"}[data[1]>>6]
			tier := "L"
			if data[1]&32 != 0 {
				tier = "H"
			}
			codec := fmt.Sprintf("hvc1.%s%d.%X.%s%d", space, data[1]&31, bits.Reverse32(binary.BigEndian.Uint32(data[2:6])), tier, data[12])
			end := 12
			for end > 6 && data[end-1] == 0 {
				end--
			}
			for _, b := range data[6:end] {
				codec += fmt.Sprintf(".%02X", b)
			}
			return "video/mp4; codecs=\"" + codec + "\""
		}
	case "vp9":
		if info.Profile == "Profile 0" && info.BitDepth == 8 {
			return "video/webm; codecs=\"vp9\""
		}
	case "av1":
		if len(data) >= 3 && data[0] == 0x81 {
			tier := "M"
			if data[2]&128 != 0 {
				tier = "H"
			}
			return fmt.Sprintf("video/mp4; codecs=\"av01.%d.%02d%s.%02d\"", data[1]>>5, data[1]&31, tier, info.BitDepth)
		}
	}
	return ""
}

func codecData(dump string) []byte {
	var data []byte
	for _, line := range strings.Split(dump, "\n") {
		_, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		value, _, _ = strings.Cut(value, "  ")
		decoded, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
		if err != nil {
			return nil
		}
		data = append(data, decoded...)
	}
	return data
}

func displaySize(info Info) (int, int) {
	width, height := info.Width, info.Height
	// Reduce the opposite axis for non-square pixels; never enlarge either axis.
	if info.SampleAspect > 1 {
		height = int(float64(height) / info.SampleAspect)
	}
	if info.SampleAspect > 0 && info.SampleAspect < 1 {
		width = int(float64(width) * info.SampleAspect)
	}
	if info.Rotation%180 != 0 {
		width, height = height, width
	}
	return outputSize(width, height)
}
