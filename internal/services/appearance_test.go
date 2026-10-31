package services

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, path string, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func getWallpaper(svc *AppearanceService) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wallpaper", nil))
	return rec
}

// The page shows whatever was set last, under a new version each time so
// the webview does not keep the old picture; clearing goes back to nothing.
func TestWallpaperRoundTrip(t *testing.T) {
	src := t.TempDir()
	svc := NewAppearanceService(t.TempDir())

	if rec := getWallpaper(svc); rec.Code != http.StatusNotFound {
		t.Fatalf("before any wallpaper: status %d, want 404", rec.Code)
	}

	first := writePNG(t, filepath.Join(src, "a.png"), color.White)
	v1, err := svc.SetWallpaper(filepath.Join(src, "a.png"))
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	rec := getWallpaper(svc)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), first) {
		t.Fatalf("served %d %q (%d bytes), want the PNG that was set", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}

	// The source may go away: the copy is what is served.
	second := writePNG(t, filepath.Join(src, "b.png"), color.Black)
	v2, err := svc.SetWallpaper(filepath.Join(src, "b.png"))
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if v2 == v1 {
		t.Errorf("replacing kept version %q; the page would keep the cached picture", v1)
	}
	if rec := getWallpaper(svc); !bytes.Equal(rec.Body.Bytes(), second) {
		t.Error("the replaced wallpaper is still served")
	}

	if err := svc.ClearWallpaper(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if rec := getWallpaper(svc); rec.Code != http.StatusNotFound {
		t.Errorf("after clearing: status %d, want 404", rec.Code)
	}
	if err := svc.ClearWallpaper(); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
}

// Something that is not a picture the webview can draw is refused, and the
// wallpaper already set stays.
func TestWallpaperRefusesNonImages(t *testing.T) {
	dir := t.TempDir()
	svc := NewAppearanceService(t.TempDir())
	kept := writePNG(t, filepath.Join(dir, "ok.png"), color.White)
	if _, err := svc.SetWallpaper(filepath.Join(dir, "ok.png")); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string][]byte{
		"notes.png": []byte("just text, named like a picture"),
		"empty.jpg": nil,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetWallpaper(path); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := svc.SetWallpaper(dir); err == nil {
		t.Error("a folder was accepted")
	}
	if rec := getWallpaper(svc); !bytes.Equal(rec.Body.Bytes(), kept) {
		t.Error("a refused file replaced the wallpaper")
	}
}
