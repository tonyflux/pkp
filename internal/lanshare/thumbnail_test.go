//go:build windows || (darwin && cgo)

package lanshare

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparationThumbnailLocalScopeAndVersion(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 500, 300))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "picture.png")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.png"), []byte("not a png"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range entries {
		ids[e.Name] = e.ID
	}
	task, err := m.PrepareDesktop(context.Background(), s.ID, ids["picture.png"], "original")
	if err != nil || task.State != "ready" {
		t.Fatalf("ready task: %+v %v", task, err)
	}
	before := len(m.Tasks())
	thumb, err := m.PreparationThumbnail(context.Background(), s.ID, ids["picture.png"])
	if err != nil || !strings.HasPrefix(thumb, "data:image/jpeg;base64,") {
		t.Fatalf("thumbnail: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(thumb, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width != 256 || config.Height != 153 {
		t.Fatalf("thumbnail dimensions: %+v %v", config, err)
	}
	reused, err := m.PrepareDesktop(context.Background(), s.ID, ids["picture.png"], "original")
	if err != nil || reused.ID != task.ID || reused.State != "ready" {
		t.Fatalf("thumbnail changed ready task: %+v %v", reused, err)
	}
	for _, id := range []string{ids["broken.png"], ids["inside.txt"], "forged"} {
		if _, err := m.PreparationThumbnail(context.Background(), s.ID, id); err == nil {
			t.Fatalf("unexpected thumbnail for %q", id)
		}
	}
	other, _ := localSpace(t, m, false)
	if _, err := m.PreparationThumbnail(context.Background(), other.ID, ids["picture.png"]); err == nil {
		t.Fatal("cross-space ID accepted")
	}
	if err := os.Rename(path, filepath.Join(root, "previous.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PreparationThumbnail(context.Background(), s.ID, ids["picture.png"]); err == nil {
		t.Fatal("replaced file accepted")
	}
	if err := m.UpdateSpace(SpaceUpdate{ID: s.ID, Name: s.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PreparationThumbnail(context.Background(), s.ID, ids["broken.png"]); err == nil {
		t.Fatal("disabled space accepted")
	}
	if len(m.Tasks()) != before {
		t.Fatal("thumbnail created a task")
	}
}

type thumbnailCloud struct{ *controlledCloud }

func (c thumbnailCloud) List(ctx context.Context, account, root, parent, identity string) ([]Entry, error) {
	entries, err := c.controlledCloud.List(ctx, account, root, parent, identity)
	if err == nil {
		entries[0].Thumbnail = "https://private.example/cover"
	}
	return entries, err
}

func TestPreparationThumbnailCloudPrivacyAndNoDownload(t *testing.T) {
	cloud := thumbnailCloud{newCloud()}
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 || entries[0].Thumbnail != "https://private.example/cover" {
		t.Fatalf("desktop list: %+v %v", entries, err)
	}
	encoded, err := json.Marshal(entries[0])
	if err != nil || !bytes.Contains(encoded, []byte(`"thumbnail":"https://private.example/cover"`)) {
		t.Fatalf("desktop JSON: %s %v", encoded, err)
	}
	visitor := authenticated(t, m)
	response := apiRequest(t, m, "GET", "/api/files?space="+s.ID, visitor, nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), "private.example") || strings.Contains(response.Body.String(), "thumbnail") {
		t.Fatalf("visitor listing leaked thumbnail: %d %s", response.Code, response.Body.String())
	}
	if _, err := m.PreparationThumbnail(context.Background(), s.ID, "file"); err == nil {
		t.Fatal("unready cloud original accepted")
	}
	cloud.mu.Lock()
	calls := cloud.calls
	cloud.mu.Unlock()
	if calls != 0 || len(m.Tasks()) != 0 {
		t.Fatal("thumbnail initiated preparation")
	}
}
