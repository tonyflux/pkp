package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/upload"
)

// What the user reported: quit the app, open it again, and the local upload
// list was empty. Records are per account and must come back on the next
// start, from the stored session alone.
func TestUploadRecordsSurviveRestart(t *testing.T) {
	oss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("ETag", `"stored"`)
	}))
	defer oss.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/drive/v1/files" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"upload_type": "UPLOAD_TYPE_RESUMABLE",
				"file":        map[string]string{"id": "f1"},
				"resumable": map[string]any{"provider": "PROVIDER_ALIYUN", "params": map[string]string{
					"endpoint": oss.URL, "bucket": "bucket", "key": "object", "access_key_id": "x", "access_key_secret": "y",
				}},
			})
			return
		}
		io.WriteString(w, `{}`)
	}))
	defer api.Close()

	config := t.TempDir()
	start := func() *DriveService {
		svc := NewDriveService(DriveOptions{ConfigDir: config, APIBaseURL: api.URL, AuthBaseURL: api.URL, NoIndexSync: true, Logger: quiet()}, nil)
		if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
			t.Fatalf("startup: %v", err)
		}
		return svc
	}

	first := start()
	seedSession(t, first, "user@example.com")
	photo := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(photo, []byte("pixels"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := first.UploadFiles("", []string{photo}, ""); err != nil {
		t.Fatalf("upload: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for first.Uploads()[0].Active() {
		if time.Now().After(deadline) {
			t.Fatalf("upload never finished: %+v", first.Uploads()[0])
		}
		time.Sleep(20 * time.Millisecond)
	}
	if done := first.Uploads()[0]; done.State != upload.StateDone {
		t.Fatalf("upload ended %q: %s", done.State, done.Error)
	}
	if err := first.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}

	second := start()
	defer second.ServiceShutdown()
	got := second.Uploads()
	if len(got) != 1 || got[0].Name != "photo.jpg" || got[0].State != upload.StateDone || got[0].FileID != "f1" || got[0].SavedTo != "全部文件" {
		t.Fatalf("after restart the list is %+v", got)
	}
	if got[0].Category != "image" {
		t.Errorf("restored record lost its icon bucket: %q", got[0].Category)
	}
}

// The upload settings start at their defaults, keep what was chosen across a
// restart, and never leave their range — not even from a hand-edited file.
func TestUploadSettingsPersistWithinBounds(t *testing.T) {
	config := t.TempDir()
	open := func() *DriveService {
		return NewDriveService(DriveOptions{ConfigDir: config, NoIndexSync: true, Logger: quiet()}, nil)
	}
	if s := open().LocalSettings(); !s.ResumableUploads || s.ParallelUploads != 2 {
		t.Fatalf("defaults = %+v; want resumable uploads on and 2 files at once", s)
	}
	svc := open()
	if err := svc.SetResumableUploads(false); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 6} {
		if err := svc.SetParallelUploads(n); err == nil {
			t.Errorf("%d files at once was accepted", n)
		}
	}
	if err := svc.SetParallelUploads(5); err != nil {
		t.Fatal(err)
	}
	if s := open().LocalSettings(); s.ResumableUploads || s.ParallelUploads != 5 {
		t.Fatalf("after a restart = %+v; want resumable uploads off and 5 files at once", s)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"parallelUploads": 64}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := open().LocalSettings(); s.ParallelUploads != 5 || !s.ResumableUploads {
		t.Fatalf("a hand-edited file reads as %+v; want 5 at most, and resumable uploads back at its default", s)
	}
}
