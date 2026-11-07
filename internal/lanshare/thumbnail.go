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
	data []byte
	err  error
}

// PreparationThumbnail reads an authorized cloud cover or an already-ready image original.
func (m *Manager) PreparationThumbnail(ctx context.Context, spaceID, fileID string) (string, error) {
	data, err := m.thumbnail(ctx, spaceID, fileID, "")
	if err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data), nil
}

func (m *Manager) thumbnail(ctx context.Context, spaceID, fileID, version string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case thumbnailSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	result := make(chan thumbnailResult, 1)
	go func() {
		defer func() { <-thumbnailSlots }()
		data, err := m.preparationThumbnail(ctx, spaceID, fileID, version)
		result <- thumbnailResult{data, err}
	}()
	select {
	case r := <-result:
		return r.data, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) preparationThumbnail(ctx context.Context, spaceID, fileID, version string) ([]byte, error) {
	if fileID == "" {
		return nil, errInvalid
	}
	s, err := m.space(spaceID, false)
	if err != nil {
		return nil, err
	}
	if cloud, ok := m.cloud.(interface {
		Thumbnail(context.Context, string, string, string, string, string) ([]byte, error)
	}); ok && s.Kind == "cloud" {
		if err := m.validateSpace(ctx, s); err != nil {
			return nil, err
		}
		data, err := cloud.Thumbnail(ctx, s.AccountID, s.RootID, fileID, s.AccountIdentity, version)
		if err != nil {
			if err = sourceError(err); err != errNotReady {
				return nil, err
			}
		} else if data, err = decodeThumbnail(ctx, bytes.NewReader(data), int64(len(data))); err == nil {
			if err := m.validateSpace(m.sourceProofContext(ctx), s); err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, err := m.space(spaceID, false); err != nil {
				return nil, err
			}
			return data, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	f, entry, err := m.original(ctx, spaceID, fileID, version)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if !strings.HasPrefix(strings.ToLower(entry.MIME), "image/") || entry.Size <= 0 {
		return nil, errUnavailable
	}
	data, err := decodeThumbnail(ctx, f, entry.Size)
	if err != nil {
		return nil, err
	}
	_, token, _, err := handleIdentity(f)
	if err != nil || token != entry.localToken {
		return nil, errUnavailable
	}
	check, fresh, err := m.original(m.sourceProofContext(ctx), spaceID, fileID, entry.Version)
	if err != nil {
		return nil, err
	}
	check.Close()
	if fresh.localToken != entry.localToken || ctx.Err() != nil {
		return nil, errUnavailable
	}
	if _, err := m.space(spaceID, false); err != nil {
		return nil, err
	}
	return data, nil
}

func decodeThumbnail(ctx context.Context, src io.ReadSeeker, size int64) ([]byte, error) {
	if size <= 0 || size > thumbnailMaxBytes {
		return nil, errUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(io.LimitReader(src, thumbnailMaxBytes))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > thumbnailMaxPixels/config.Height {
		return nil, errUnavailable
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, errUnavailable
	}
	img, _, err := image.Decode(io.LimitReader(src, thumbnailMaxBytes))
	if err != nil || ctx.Err() != nil {
		return nil, errUnavailable
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w != config.Width || h != config.Height {
		return nil, errUnavailable
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
			return nil, ctx.Err()
		}
		for x := range w {
			thumb.Set(x, y, img.At(bounds.Min.X+x*bounds.Dx()/w, bounds.Min.Y+y*bounds.Dy()/h))
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, thumb, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
