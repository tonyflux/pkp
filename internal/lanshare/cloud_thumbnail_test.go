//go:build windows || (darwin && cgo)

package lanshare

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type coverThumbnailCloud struct {
	*controlledCloud
	cover        []byte
	coverErr     error
	original     *Source
	resolveCalls int
	coverCalls   int
	entered      chan struct{}
	continueRead chan struct{}
}

func (c *coverThumbnailCloud) entry() Entry {
	if c.original != nil {
		return c.original.Entry
	}
	return Entry{ID: "file", Name: "movie.mp4", Version: c.version, MIME: "video/mp4", Size: 100, Thumbnail: "https://private.example/signed-cover?secret=1"}
}

func (c *coverThumbnailCloud) List(ctx context.Context, account, root, parent, identity string) ([]Entry, error) {
	if err := c.ValidateRoot(ctx, account, root, identity); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return []Entry{c.entry(), {ID: "folder", IsDir: true, Thumbnail: "https://private.example/folder"}}, nil
}

func (c *coverThumbnailCloud) VisitorPage(ctx context.Context, account, root, parent, identity, token string) ([]Entry, string, error) {
	entries, err := c.List(ctx, account, root, parent, identity)
	return entries, "", err
}

func (c *coverThumbnailCloud) Resolve(ctx context.Context, account, root, id, identity string) (Source, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolveCalls++
	if !c.allowed || identity != c.identity || id != "file" || root != "shared-root" {
		return Source{}, errForbidden
	}
	if c.original != nil {
		return *c.original, nil
	}
	return Source{Entry: c.entry()}, nil
}

func (c *coverThumbnailCloud) Thumbnail(ctx context.Context, account, root, id, identity, version string) ([]byte, error) {
	check := func() error {
		if !c.allowed || account != "device-A" || root != "shared-root" || id != "file" || identity != c.identity {
			return errForbidden
		}
		if version != "" && version != c.version {
			return errUnavailable
		}
		return nil
	}
	c.mu.Lock()
	c.coverCalls++
	err := check()
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if c.entered != nil {
		c.entered <- struct{}{}
		select {
		case <-c.continueRead:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := check(); err != nil {
		return nil, err
	}
	return c.cover, c.coverErr
}

func thumbnailPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 500, 300))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func cloudThumbnailSpace(t *testing.T, cloud *coverThumbnailCloud) (*Manager, Space) {
	t.Helper()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", RootID: "shared-root"})
	if err != nil {
		t.Fatal(err)
	}
	return m, s
}

func TestVisitorCloudThumbnailWithoutOriginal(t *testing.T) {
	cloud := &coverThumbnailCloud{controlledCloud: newCloud(), cover: thumbnailPNG(t)}
	m, space := cloudThumbnailSpace(t, cloud)
	cookie := authenticated(t, m)
	for _, entries := range []func() ([]Entry, error){
		func() ([]Entry, error) { return m.list(context.Background(), space.ID, "") },
		func() ([]Entry, error) {
			s, err := m.space(space.ID, false)
			if err != nil {
				return nil, err
			}
			entries, _, err := m.visitorEntries(context.Background(), s, "", "")
			return entries, err
		},
	} {
		list, err := entries()
		if err != nil || len(list) != 2 {
			t.Fatalf("cloud list: %+v %v", list, err)
		}
		for _, entry := range list {
			if entry.HasThumbnail == entry.IsDir {
				t.Fatalf("cover availability: %+v", entry)
			}
		}
	}
	list := apiRequest(t, m, http.MethodGet, "/api/files?space="+space.ID, cookie, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"hasThumbnail":true`) || strings.Contains(list.Body.String(), "private.example") || strings.Contains(list.Body.String(), `"thumbnail":`) {
		t.Fatalf("visitor list: %d %s", list.Code, list.Body.String())
	}
	endpoint := "/api/thumbnail?space=" + space.ID + "&id=file&version=version-1"
	response := apiRequest(t, m, http.MethodGet, endpoint, cookie, nil)
	config, format, err := image.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" || err != nil || format != "jpeg" || config.Width != 256 || config.Height != 153 || bytes.Equal(response.Body.Bytes(), cloud.cover) {
		t.Fatalf("cover response: status=%d config=%+v format=%s err=%v", response.Code, config, format, err)
	}
	if _, err := m.PreparationThumbnail(context.Background(), space.ID, "file"); err != nil {
		t.Fatalf("desktop current cover: %v", err)
	}
	for _, query := range []string{"&id=file&version=stale", "&id=outside&version=version-1"} {
		response := apiRequest(t, m, http.MethodGet, "/api/thumbnail?space="+space.ID+query, cookie, nil)
		if response.Code == http.StatusOK || response.Header().Get("Content-Type") == "image/jpeg" {
			t.Fatalf("unscoped thumbnail: %d", response.Code)
		}
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if cloud.resolveCalls != 0 || cloud.calls != 0 || cloud.coverCalls != 4 || len(m.Tasks()) != 0 {
		t.Fatalf("cover accessed original or preparation: resolve=%d prepare=%d cover=%d", cloud.resolveCalls, cloud.calls, cloud.coverCalls)
	}
}

func TestVisitorCloudThumbnailRejectsUnsafeImageBytes(t *testing.T) {
	for name, data := range map[string][]byte{
		"corrupt":     []byte("not an image"),
		"oversized":   make([]byte, thumbnailMaxBytes+1),
		"pixel-limit": {'G', 'I', 'F', '8', '9', 'a', 255, 255, 255, 255, 0, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			cloud := &coverThumbnailCloud{controlledCloud: newCloud(), cover: data}
			m, space := cloudThumbnailSpace(t, cloud)
			response := apiRequest(t, m, http.MethodGet, "/api/thumbnail?space="+space.ID+"&id=file&version=version-1", authenticated(t, m), nil)
			if response.Code != http.StatusConflict || response.Body.String() != "{\"error\":\"not_ready\"}\n" || response.Header().Get("Content-Type") == "image/jpeg" {
				t.Fatalf("unsafe cover escaped icon fallback: %d %s", response.Code, response.Body.String())
			}
			if cloud.calls != 0 || len(m.Tasks()) != 0 {
				t.Fatal("invalid cover initiated original preparation")
			}
		})
	}
}

func TestCloudThumbnailFallbackOnlyForMissingOrUndecodableCover(t *testing.T) {
	data := thumbnailPNG(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "picture.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	token, err := LocalVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		cover []byte
		err   error
		want  error
	}{
		{name: "missing", err: errors.New("not_ready")},
		{name: "unsupported", cover: []byte("unsupported codec")},
		{name: "forbidden", err: errForbidden, want: errForbidden},
		{name: "source", err: errors.New("source_unavailable"), want: errUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cloud := &coverThumbnailCloud{controlledCloud: newCloud(), cover: tc.cover, coverErr: tc.err, original: &Source{Entry: Entry{ID: "file", Name: "picture.png", Version: "version-1", MIME: "image/png", Size: int64(len(data))}, LocalPath: path, LocalVersion: token}}
			m, space := cloudThumbnailSpace(t, cloud)
			result, err := m.thumbnail(context.Background(), space.ID, "file", "version-1")
			if !errors.Is(err, tc.want) || (err == nil && len(result) == 0) {
				t.Fatalf("fallback: bytes=%d err=%v want=%v", len(result), err, tc.want)
			}
			if tc.want != nil && cloud.resolveCalls != 0 {
				t.Fatal("authorization failure bypassed through original")
			}
			if cloud.calls != 0 || len(m.Tasks()) != 0 {
				t.Fatal("fallback initiated preparation")
			}
		})
	}
}

func TestCloudThumbnailPrefersCoverForReadyVideo(t *testing.T) {
	cloud := &coverThumbnailCloud{controlledCloud: newCloud(), cover: thumbnailPNG(t), original: &Source{Entry: Entry{ID: "file", Version: "version-1", MIME: "video/mp4", Ready: true}, LocalPath: "/must-not-open-original"}}
	m, space := cloudThumbnailSpace(t, cloud)
	if _, err := m.thumbnail(context.Background(), space.ID, "file", "version-1"); err != nil {
		t.Fatal(err)
	}
	if cloud.resolveCalls != 0 {
		t.Fatal("ready video original was discovered instead of its cover")
	}
}

func TestVisitorCloudThumbnailRevokesDuringCoverRead(t *testing.T) {
	cloud := &coverThumbnailCloud{controlledCloud: newCloud(), cover: thumbnailPNG(t), entered: make(chan struct{}, 1), continueRead: make(chan struct{})}
	m, space := cloudThumbnailSpace(t, cloud)
	cookie := authenticated(t, m)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- apiRequest(t, m, http.MethodGet, "/api/thumbnail?space="+space.ID+"&id=file&version=version-1", cookie, nil)
	}()
	select {
	case <-cloud.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("cloud cover did not start")
	}
	if err := m.UpdateSpace(SpaceUpdate{ID: space.ID, Name: space.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	close(cloud.continueRead)
	select {
	case response := <-done:
		if response.Body.Len() != 0 {
			t.Fatalf("revoked cover published bytes: %s", response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revocation did not stop cover response")
	}
}

func TestVisitorCloudThumbnailChecksVersionAfterRead(t *testing.T) {
	cloud := &coverThumbnailCloud{controlledCloud: newCloud(), cover: thumbnailPNG(t), entered: make(chan struct{}, 1), continueRead: make(chan struct{})}
	m, space := cloudThumbnailSpace(t, cloud)
	cookie := authenticated(t, m)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- apiRequest(t, m, http.MethodGet, "/api/thumbnail?space="+space.ID+"&id=file&version=version-1", cookie, nil)
	}()
	select {
	case <-cloud.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("cloud cover did not start")
	}
	cloud.mu.Lock()
	cloud.version = "version-2"
	cloud.mu.Unlock()
	close(cloud.continueRead)
	select {
	case response := <-done:
		if response.Code != http.StatusConflict || response.Body.String() != "{\"error\":\"source_unavailable\"}\n" || cloud.resolveCalls != 0 {
			t.Fatalf("changed version accepted: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("changed version response stalled")
	}
}
