package drive

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCreateUploadUsesIndependentPlaceholders(t *testing.T) {
	seen := make(map[string]bool)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		placeholder, _ := body["hash"].(string)
		raw, err := hex.DecodeString(placeholder)
		if err != nil || len(raw) != 20 || placeholder != strings.ToUpper(placeholder) || seen[placeholder] {
			t.Errorf("invalid or reused upload placeholder %q", placeholder)
			http.Error(w, "file_verification_failed", http.StatusBadRequest)
			return
		}
		seen[placeholder] = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"upload_type": uploadTypeResumable,
			"file":        map[string]string{"id": "upload-file"},
			"resumable": map[string]any{
				"provider": providerAliyun,
				"params":   map[string]string{"bucket": "bucket", "key": "key", "endpoint": "upload.example.com"},
			},
		})
	}, newStaticTokens("token"))

	// Identical names and sizes, including a fresh create after retry, must
	// not produce a content-derived or shared placeholder.
	for range 3 {
		slot, err := client.CreateUpload(context.Background(), UploadParams{Name: "same.txt", Size: 123})
		if err != nil {
			t.Fatal(err)
		}
		if slot.OSS == nil || slot.File.ID != "upload-file" {
			t.Fatalf("invalid upload slot: %+v", slot)
		}
	}
}

func TestCreateUploadRejectsResponseWithoutGrant(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"upload_type": "UPLOAD_TYPE_UNKNOWN",
			"file":        map[string]string{"id": "already-present", "phase": PhaseComplete},
		})
	}, newStaticTokens("token"))
	slot, err := client.CreateUpload(context.Background(), UploadParams{Name: "file", Size: 123})
	if err == nil || slot != nil {
		t.Fatalf("response without OSS grant accepted as success: slot=%+v err=%v", slot, err)
	}
}
