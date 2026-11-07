package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
)

func TestLANCloudPathHintsFetchFreshParents(t *testing.T) {
	var mu sync.Mutex
	files := map[string]drive.File{
		"root":    {ID: "root", Kind: drive.KindFolder, UserID: "u"},
		"folder":  {ID: "folder", ParentID: "root", Kind: drive.KindFolder, UserID: "u"},
		"new":     {ID: "new", ParentID: "root", Kind: drive.KindFolder, UserID: "u"},
		"outside": {ID: "outside", Kind: drive.KindFolder, UserID: "u"},
		"film":    {ID: "film", Name: "film.mp4", ParentID: "folder", Kind: drive.KindFile, UserID: "u", Phase: drive.PhaseComplete, ModifiedTime: "v1"},
	}
	batches, singles := 0, 0
	mode := "normal"
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/drive/v1/files:batchGet" {
			batches++
			if mode == "unavailable" {
				http.Error(w, "unsupported", http.StatusNotFound)
				return
			}
			list := drive.FileList{}
			for _, id := range r.URL.Query()["ids"] {
				f, ok := files[id]
				if ok && !(mode == "omitted" && id == "film") {
					list.Files = append(list.Files, f)
				}
			}
			if mode == "foreign" {
				list.Files = append(list.Files, drive.File{ID: "foreign", Kind: drive.KindFolder, UserID: "other"})
			}
			if mode == "duplicate" && len(list.Files) > 0 {
				list.Files = append(list.Files, list.Files[0])
			}
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		singles++
		f, ok := files[strings.TrimPrefix(r.URL.Path, "/drive/v1/files/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(f)
	})
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	s, _ := svc.active()
	c := &lanCloud{drive: svc}
	a, err := c.account(s.key)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func() (*drive.File, error) {
		return c.within(c.SourceProofContext(context.Background()), a, "root", "film")
	}
	first, err := lookup()
	if err != nil || first.ID != "film" {
		t.Fatalf("first lookup: %+v %v", first, err)
	}
	mu.Lock()
	if singles != 3 || batches != 0 {
		t.Fatalf("cold chain: singles=%d batches=%d", singles, batches)
	}
	f := files["film"]
	f.ModifiedTime = "v2"
	files["film"] = f
	singles, batches = 0, 0
	mu.Unlock()
	second, err := lookup()
	if err != nil || second.ModifiedTime != "v2" {
		t.Fatalf("hint reused stale metadata: %+v %v", second, err)
	}
	mu.Lock()
	if singles != 0 || batches != 1 {
		t.Fatalf("warm live chain: singles=%d batches=%d", singles, batches)
	}
	f = files["film"]
	f.ParentID = "new"
	files["film"] = f
	mu.Unlock()
	if _, err := lookup(); err != nil {
		t.Fatalf("new live in-scope parent was not followed: %v", err)
	}
	for _, broken := range []string{"unavailable", "omitted", "foreign", "duplicate"} {
		mu.Lock()
		mode = broken
		mu.Unlock()
		if _, err := lookup(); err != nil {
			t.Fatalf("%s batch must use ordinary live proof: %v", broken, err)
		}
	}
	mu.Lock()
	mode = "normal"
	f = files["film"]
	f.ParentID = "outside"
	files["film"] = f
	mu.Unlock()
	if _, err := lookup(); !errors.Is(err, errLANScope) {
		t.Fatalf("moved source trusted old parent hints: %v", err)
	}
	mu.Lock()
	f.ParentID = "new"
	f.Trashed = true
	files["film"] = f
	mu.Unlock()
	if _, err := lookup(); !errors.Is(err, errLANSource) {
		t.Fatalf("trashed source trusted hints: %v", err)
	}
	mu.Lock()
	f.Trashed = false
	files["film"] = f
	root := files["root"]
	root.Kind = drive.KindFile
	files["root"] = root
	mu.Unlock()
	if _, err := lookup(); !errors.Is(err, errLANScope) {
		t.Fatalf("non-folder root trusted hints: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.within(c.SourceProofContext(cancelled), a, "root", "film"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled batch proof: %v", err)
	}
}
