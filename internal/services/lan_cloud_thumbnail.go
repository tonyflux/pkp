package services

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/netproxy"
)

const lanThumbnailMaxBytes = 16 << 20

// Thumbnail only uses fresh cloud metadata; a cover never prepares or verifies an original.
func (c *lanCloud) Thumbnail(ctx context.Context, accountID, rootID, fileID, identity, version string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a, err := c.account(accountID)
	if err != nil {
		return nil, err
	}
	if identity == "" || a.user != identity {
		return nil, errLANSource
	}
	f, err := c.within(c.SourceProofContext(ctx), a, rootID, fileID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	currentVersion := lanVersion(f)
	if version != "" && version != currentVersion {
		return nil, errLANSource
	}
	if f.Kind != drive.KindFile || f.Phase != drive.PhaseComplete || currentVersion == "" || f.ThumbnailLink == "" {
		return nil, errLANNotReady
	}
	if err := c.current(a); err != nil {
		return nil, err
	}
	body, err := lanFetchThumbnail(ctx, f.ThumbnailLink)
	if err != nil {
		return nil, err
	}
	// Network work may outlive a move, account removal, or content replacement.
	fresh, err := c.within(c.SourceProofContext(ctx), a, rootID, fileID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if fresh.Kind != drive.KindFile || fresh.Phase != drive.PhaseComplete || lanVersion(fresh) != currentVersion {
		return nil, errLANSource
	}
	if err := c.current(a); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}

func lanFetchThumbnail(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errLANSource
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" || u.User != nil || strings.HasSuffix(u.Host, ":") || u.Port() != "" && u.Port() != "443" ||
		(host != "thumbnail-drive.mypikpak.com" && !strings.HasSuffix(host, "-thumbnail-drive.mypikpak.com")) {
		return nil, errLANSource
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errLANSource
	}
	client := *netproxy.Client()
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errLANSource
	}
	defer resp.Body.Close()
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || resp.ContentLength > lanThumbnailMaxBytes || err != nil || !strings.HasPrefix(mediaType, "image/") {
		return nil, errLANSource
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, lanThumbnailMaxBytes+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(body) == 0 || len(body) > lanThumbnailMaxBytes {
		return nil, errLANSource
	}
	return body, nil
}
