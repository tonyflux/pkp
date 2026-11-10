package lanmedia

import (
	"context"
	"encoding/binary"
	"io"
)

// Read box headers only: large media payloads are skipped, never scanned or copied.
func mp4IndexBytes(ctx context.Context, source io.ReaderAt, size int64) (int64, error) {
	var header [16]byte
	for offset, boxes := int64(0), 0; offset < size && boxes < 4096; boxes++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if size-offset < 8 {
			return 0, nil
		}
		n, _ := source.ReadAt(header[:], offset)
		if n < 8 {
			return 0, nil
		}
		length, minimum := uint64(binary.BigEndian.Uint32(header[:4])), uint64(8)
		if length == 1 {
			if n < 16 {
				return 0, nil
			}
			length, minimum = binary.BigEndian.Uint64(header[8:]), 16
		} else if length == 0 {
			length = uint64(size - offset)
		}
		if length < minimum || length > uint64(size-offset) {
			return 0, nil
		}
		if string(header[4:8]) == "moov" {
			return int64(length), nil
		}
		offset += int64(length)
	}
	return 0, nil
}
