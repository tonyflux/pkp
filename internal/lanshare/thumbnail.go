package lanshare

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"time"
)

const (
	thumbnailMaxBytes  = 16 << 20
	thumbnailMaxPixels = 24_000_000
	thumbnailMaxSide   = 256
)

var thumbnailSlots = make(chan struct{}, 2)

type thumbnailResult struct {
	data string
	err  error
}

// PreparationThumbnail only reads an already-authorized, ready original.
func (m *Manager) PreparationThumbnail(ctx context.Context, spaceID, fileID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case thumbnailSlots <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	result := make(chan thumbnailResult, 1)
	go func() {
		defer func() { <-thumbnailSlots }()
		data, err := m.preparationThumbnail(ctx, spaceID, fileID)
		result <- thumbnailResult{data, err}
	}()
	select {
	case r := <-result:
		return r.data, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (m *Manager) preparationThumbnail(ctx context.Context, spaceID, fileID string) (string, error) {
	f, entry, err := m.original(ctx, spaceID, fileID, "")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if !strings.HasPrefix(strings.ToLower(entry.MIME), "image/") || entry.Size <= 0 {
		return "", errUnavailable
	}
	config, _, err := image.DecodeConfig(io.LimitReader(f, thumbnailMaxBytes))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > thumbnailMaxPixels/config.Height {
		return "", errUnavailable
	}
	if entry.Size > thumbnailMaxBytes {
		return "", errUnavailable
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", errUnavailable
	}
	img, _, err := image.Decode(io.LimitReader(f, thumbnailMaxBytes))
	if err != nil || ctx.Err() != nil {
		return "", errUnavailable
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w != config.Width || h != config.Height {
		return "", errUnavailable
	}
	if w > thumbnailMaxSide || h > thumbnailMaxSide {
		if w >= h {
			h = max(1, h*thumbnailMaxSide/w)
			w = thumbnailMaxSide
		} else {
			w = max(1, w*thumbnailMaxSide/h)
			h = thumbnailMaxSide
		}
	}
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		for x := range w {
			thumb.Set(x, y, img.At(bounds.Min.X+x*bounds.Dx()/w, bounds.Min.Y+y*bounds.Dy()/h))
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, thumb, &jpeg.Options{Quality: 80}); err != nil {
		return "", err
	}
	_, token, _, err := handleIdentity(f)
	if err != nil || token != entry.localToken {
		return "", errUnavailable
	}
	check, fresh, err := m.original(ctx, spaceID, fileID, entry.Version)
	if err != nil {
		return "", err
	}
	check.Close()
	if fresh.localToken != entry.localToken || ctx.Err() != nil {
		return "", errUnavailable
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(out.Bytes()), nil
}
