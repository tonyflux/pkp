package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/netproxy"
)

const lanTestCoverURL = "https://sg-thumbnail-drive.mypikpak.com/cover.jpg?signature=private-cover-secret"

type lanThumbnailTransport func(*http.Request) (*http.Response, error)

func (f lanThumbnailTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func lanThumbnailTestTransport(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	client := netproxy.Client()
	original := client.Transport
	client.Transport = transport
	t.Cleanup(func() { client.Transport = original })
}

type lanThumbnailFixture struct {
	cloud   *lanCloud
	session *accountSession
	mu      sync.Mutex
	files   map[string]drive.File
}

func newLANThumbnailFixture(t *testing.T) *lanThumbnailFixture {
	t.Helper()
	f := &lanThumbnailFixture{files: map[string]drive.File{
		"root":    {ID: "root", Kind: drive.KindFolder, UserID: "u"},
		"folder":  {ID: "folder", ParentID: "root", Kind: drive.KindFolder, UserID: "u"},
		"outside": {ID: "outside", Kind: drive.KindFolder, UserID: "u"},
		"film":    {ID: "film", ParentID: "folder", Kind: drive.KindFile, UserID: "u", Name: "film.mp4", MimeType: "video/mp4", Size: 900000000, Phase: drive.PhaseComplete, ModifiedTime: "v1", ThumbnailLink: lanTestCoverURL, WebContentLink: "https://original.example/private-film"},
	}}
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/drive/v1/files" {
			list := drive.FileList{}
			for _, file := range f.files {
				if file.ParentID == r.URL.Query().Get("parent_id") {
					list.Files = append(list.Files, file)
				}
			}
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		if r.URL.Query().Get("thumbnail_size") != string(drive.ThumbnailMedium) {
			t.Errorf("metadata did not request medium thumbnail: %s", r.URL.Path)
		}
		if r.URL.Path == "/drive/v1/files:batchGet" {
			list := drive.FileList{}
			for _, id := range r.URL.Query()["ids"] {
				if file, ok := f.files[id]; ok {
					list.Files = append(list.Files, file)
				}
			}
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		file, ok := f.files[strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(file)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	f.session, _ = svc.active()
	f.cloud = &lanCloud{drive: svc}
	return f
}

func (f *lanThumbnailFixture) change(id string, change func(*drive.File)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file := f.files[id]
	change(&file)
	f.files[id] = file
}

func (f *lanThumbnailFixture) version() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	file := f.files["film"]
	return lanVersion(&file)
}

func TestLANCloudThumbnailWithoutOriginal(t *testing.T) {
	fixture := newLANThumbnailFixture(t)
	var picture bytes.Buffer
	if err := jpeg.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 24, 18)), nil); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Host != "sg-thumbnail-drive.mypikpak.com" || r.URL.Path != "/cover.jpg" || r.URL.Query().Get("signature") != "private-cover-secret" {
			t.Errorf("unexpected cover request host/path")
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-LAN-Session") != "" {
			t.Error("cover request carried private or visitor headers")
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(picture.Bytes())
	}))
	defer cdn.Close()
	origin, _ := url.Parse(cdn.URL)
	lanThumbnailTestTransport(t, lanThumbnailTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "sg-thumbnail-drive.mypikpak.com" {
			t.Errorf("attempted original download: %s", r.URL.Hostname())
			return nil, errors.New("unexpected original download")
		}
		clone := r.Clone(r.Context())
		clone.URL.Host = origin.Host
		return cdn.Client().Transport.RoundTrip(clone)
	}))
	client := netproxy.Client()
	previousJar := client.Jar
	client.Jar, _ = cookiejar.New(nil)
	coverURL, _ := url.Parse(lanTestCoverURL)
	client.Jar.SetCookies(coverURL, []*http.Cookie{{Name: "private", Value: "must-not-leak"}})
	t.Cleanup(func() { client.Jar = previousJar })

	// Neither foreground account nor original readiness is needed to fetch a cover.
	fixture.cloud.drive.mu.Lock()
	fixture.cloud.drive.current = nil
	fixture.cloud.drive.mu.Unlock()
	version := fixture.version()
	for _, requestedVersion := range []string{version, ""} {
		body, err := fixture.cloud.Thumbnail(context.Background(), fixture.session.key, "root", "film", "u", requestedVersion)
		if err != nil || !bytes.Equal(body, picture.Bytes()) {
			t.Fatalf("cloud-only video cover: bytes=%d err=%v", len(body), err)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("cover requests = %d", requests.Load())
	}
	if queue := fixture.session.downloadQueue(); queue != nil && len(queue.List()) != 0 {
		t.Fatal("thumbnail prepared an original download")
	}
	if len(fixture.cloud.verified) != 0 {
		t.Fatal("thumbnail verified local original checksums")
	}
	entries, err := fixture.cloud.List(context.Background(), fixture.session.key, "root", "folder", "u")
	if err != nil || len(entries) != 1 || !entries[0].HasThumbnail || entries[0].Ready || entries[0].Thumbnail != lanTestCoverURL {
		t.Fatalf("cloud-only cover metadata: %+v %v", entries, err)
	}
	encoded, err := json.Marshal(entries)
	if err != nil || strings.Contains(string(encoded), "private-cover-secret") || strings.Contains(string(encoded), "thumbnail-drive") || !strings.Contains(string(encoded), `"hasThumbnail":true`) {
		t.Fatalf("visitor metadata exposed signed URL or omitted availability: %s %v", encoded, err)
	}
}

func TestLANCloudThumbnailRejectsBeforeFetch(t *testing.T) {
	fixture := newLANThumbnailFixture(t)
	var requests atomic.Int32
	lanThumbnailTestTransport(t, lanThumbnailTransport(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected cover fetch")
	}))
	original := fixture.files["film"]
	for _, test := range []struct {
		name     string
		identity string
		version  string
		change   func(*drive.File)
		want     error
	}{
		{name: "foreign identity", identity: "other", want: errLANSource},
		{name: "stale version", version: "old", want: errLANSource},
		{name: "moved", change: func(f *drive.File) { f.ParentID = "outside" }, want: errLANScope},
		{name: "foreign file", change: func(f *drive.File) { f.UserID = "other" }, want: errLANSource},
		{name: "trashed", change: func(f *drive.File) { f.Trashed = true }, want: errLANSource},
		{name: "missing cover", change: func(f *drive.File) { f.ThumbnailLink = "" }, want: errLANNotReady},
		{name: "incomplete", change: func(f *drive.File) { f.Phase = "PHASE_TYPE_RUNNING" }, want: errLANNotReady},
		{name: "folder", change: func(f *drive.File) { f.Kind = drive.KindFolder }, want: errLANNotReady},
		{name: "unversioned", change: func(f *drive.File) { f.ModifiedTime = "" }, want: errLANNotReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture.change("film", func(f *drive.File) { *f = original })
			if test.change != nil {
				fixture.change("film", test.change)
			}
			identity := test.identity
			if identity == "" {
				identity = "u"
			}
			body, err := fixture.cloud.Thumbnail(context.Background(), fixture.session.key, "root", "film", identity, test.version)
			if !errors.Is(err, test.want) || len(body) != 0 {
				t.Fatalf("unauthorized cover: bytes=%d err=%v want=%v", len(body), err, test.want)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("unauthorized metadata triggered %d cover requests", requests.Load())
	}
}

func TestLANCloudThumbnailRechecksAfterFetch(t *testing.T) {
	fixture := newLANThumbnailFixture(t)
	original := fixture.files["film"]
	for _, test := range []struct {
		name   string
		change func()
		want   error
	}{
		{name: "moved", change: func() { fixture.change("film", func(f *drive.File) { f.ParentID = "outside" }) }, want: errLANScope},
		{name: "changed", change: func() { fixture.change("film", func(f *drive.File) { f.ModifiedTime = "v2" }) }, want: errLANSource},
		{name: "trashed", change: func() { fixture.change("film", func(f *drive.File) { f.Trashed = true }) }, want: errLANSource},
		{name: "ancestor moved", change: func() { fixture.change("folder", func(f *drive.File) { f.ParentID = "outside" }) }, want: errLANScope},
		{name: "identity rebound", change: func() { fixture.session.mu.Lock(); fixture.session.rec.UserID = "other"; fixture.session.mu.Unlock() }, want: errLANSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture.change("film", func(f *drive.File) { *f = original })
			fixture.change("folder", func(f *drive.File) { f.ParentID = "root" })
			fixture.session.mu.Lock()
			fixture.session.rec.UserID = "u"
			fixture.session.mu.Unlock()
			version := fixture.version()
			lanThumbnailTestTransport(t, lanThumbnailTransport(func(*http.Request) (*http.Response, error) {
				test.change()
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/jpeg"}}, Body: io.NopCloser(strings.NewReader("image bytes"))}, nil
			}))
			body, err := fixture.cloud.Thumbnail(context.Background(), fixture.session.key, "root", "film", "u", version)
			if !errors.Is(err, test.want) || len(body) != 0 {
				t.Fatalf("stale proof released cover: bytes=%d err=%v want=%v", len(body), err, test.want)
			}
		})
	}
}

func TestLANFetchThumbnailTrustBoundary(t *testing.T) {
	var requests atomic.Int32
	lanThumbnailTestTransport(t, lanThumbnailTransport(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/jpeg; charset=binary"}}, Body: io.NopCloser(strings.NewReader("image bytes"))}, nil
	}))
	for _, raw := range []string{
		"http://sg-thumbnail-drive.mypikpak.com/cover?secret=private",
		"https://localhost/cover?secret=private",
		"https://127.0.0.1/cover?secret=private",
		"https://private.example/cover?secret=private",
		"https://thumbnail-drive.mypikpak.com.evil.example/cover?secret=private",
		"https://evil-thumbnail-drive.mypikpak.com.evil.example/cover?secret=private",
		"https://user:password@sg-thumbnail-drive.mypikpak.com/cover?secret=private",
		"https://sg-thumbnail-drive.mypikpak.com:8443/cover?secret=private",
		"https://sg-thumbnail-drive.mypikpak.com:/cover?secret=private",
		"https://sg-thumbnail-drive.mypikpak.com./cover?secret=private",
		"https://[::1/cover?secret=private",
	} {
		body, err := lanFetchThumbnail(context.Background(), raw)
		if !errors.Is(err, errLANSource) || len(body) != 0 || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe URL was accepted or disclosed: bytes=%d err=%v", len(body), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("untrusted origins reached transport: %d", requests.Load())
	}
	for _, host := range []string{"thumbnail-drive.mypikpak.com", "sg-thumbnail-drive.mypikpak.com", "z01a-thumbnail-drive.mypikpak.com", "sg-thumbnail-drive.mypikpak.com:443"} {
		if _, err := lanFetchThumbnail(context.Background(), "https://"+host+"/cover"); err != nil {
			t.Fatalf("trusted thumbnail host %s: %v", host, err)
		}
	}
}

func TestLANFetchThumbnailRejectsResponse(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		contentType string
		length      int64
		body        io.Reader
	}{
		{name: "redirect", status: http.StatusFound, contentType: "image/jpeg", body: strings.NewReader("redirect")},
		{name: "upstream error", status: http.StatusForbidden, contentType: "image/jpeg", body: strings.NewReader("denied")},
		{name: "nonimage", status: http.StatusOK, contentType: "text/html", body: strings.NewReader("<html>private</html>")},
		{name: "missing type", status: http.StatusOK, body: strings.NewReader("image bytes")},
		{name: "declared overflow", status: http.StatusOK, contentType: "image/jpeg", length: lanThumbnailMaxBytes + 1, body: strings.NewReader("image bytes")},
		{name: "stream overflow", status: http.StatusOK, contentType: "image/jpeg", length: -1, body: io.LimitReader(lanThumbnailZeroReader{}, lanThumbnailMaxBytes+1)},
		{name: "empty image", status: http.StatusOK, contentType: "image/jpeg", body: strings.NewReader("")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests int
			lanThumbnailTestTransport(t, lanThumbnailTransport(func(*http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": {test.contentType}, "Location": {"https://private.example/secret"}}, ContentLength: test.length, Body: io.NopCloser(test.body)}, nil
			}))
			body, err := lanFetchThumbnail(context.Background(), lanTestCoverURL)
			if !errors.Is(err, errLANSource) || len(body) != 0 || requests != 1 || strings.Contains(err.Error(), "private-cover-secret") {
				t.Fatalf("unsafe response accepted/followed/disclosed: bytes=%d requests=%d err=%v", len(body), requests, err)
			}
		})
	}
}

type lanThumbnailZeroReader struct{}

func (lanThumbnailZeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestLANCloudThumbnailCancellation(t *testing.T) {
	fixture := newLANThumbnailFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.cloud.Thumbnail(ctx, fixture.session.key, "root", "film", "u", fixture.version()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled metadata request: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	lanThumbnailTestTransport(t, lanThumbnailTransport(func(r *http.Request) (*http.Response, error) {
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	if _, err := fixture.cloud.Thumbnail(ctx, fixture.session.key, "root", "film", "u", fixture.version()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cover request: %v", err)
	}
}

func TestLANEntryCoverHintRequiresCompleteFile(t *testing.T) {
	for _, test := range []struct {
		kind  string
		phase string
		link  string
		want  bool
	}{
		{drive.KindFile, drive.PhaseComplete, lanTestCoverURL, true},
		{drive.KindFile, drive.PhaseComplete, "", false},
		{drive.KindFile, "PHASE_TYPE_RUNNING", lanTestCoverURL, false},
		{drive.KindFolder, drive.PhaseComplete, lanTestCoverURL, false},
	} {
		entry := lanEntry(&drive.File{Kind: test.kind, Phase: test.phase, ThumbnailLink: test.link})
		if entry.HasThumbnail != test.want || (entry.Thumbnail != "") != test.want {
			t.Fatalf("cover hint kind=%s phase=%s: %+v", test.kind, test.phase, entry)
		}
	}
}
