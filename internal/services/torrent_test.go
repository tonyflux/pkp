package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeTorrent puts a two-file torrent on disk and returns its path.
func writeTorrent(t *testing.T) string {
	t.Helper()
	enc := func(v any) string { return bencodeFixture(v) }
	data := enc(map[string]any{
		"announce": "udp://tracker.example:6969",
		"info": map[string]any{
			"name":         "Pack",
			"piece length": 262144,
			"pieces":       strings.Repeat("\x01", 20),
			"files": []any{
				map[string]any{"length": 1200, "path": []any{"Subs", "chs.srt"}},
				map[string]any{"length": 734003200, "path": []any{"movie.mkv"}},
			},
		},
	})
	path := filepath.Join(t.TempDir(), "pack.torrent")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func bencodeFixture(v any) string {
	switch x := v.(type) {
	case int:
		return fmt.Sprintf("i%de", x)
	case string:
		return fmt.Sprintf("%d:%s", len(x), x)
	case []any:
		var b strings.Builder
		b.WriteString("l")
		for _, item := range x {
			b.WriteString(bencodeFixture(item))
		}
		b.WriteString("e")
		return b.String()
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("d")
		for _, k := range keys {
			b.WriteString(bencodeFixture(k) + bencodeFixture(x[k]))
		}
		b.WriteString("e")
		return b.String()
	}
	panic("bencode fixture: unsupported value")
}

// The sheet lists what a .torrent contains without any server: the file
// comes from this computer and is parsed here.
func TestReadTorrentListsFilesLocally(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		t.Errorf("reading a torrent went to the server: %s %s", r.Method, r.URL.Path)
	})
	meta, err := svc.ReadTorrent(writeTorrent(t))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "Pack" || meta.Size != 1200+734003200 || len(meta.Files) != 2 {
		t.Fatalf("meta = %+v", meta)
	}
	if f := meta.Files[1]; f.Index != 1 || f.Path != "movie.mkv" || f.Category != "video" {
		t.Errorf("file 1 = %+v", f)
	}
	if _, err := svc.ReadTorrent(filepath.Join(t.TempDir(), "nothing.torrent")); err == nil {
		t.Error("a missing file read as a torrent")
	}
}

// A picked subset travels as the file indices of the torrent, and the task
// is created from the magnet link the torrent's own bytes make.
func TestAddTorrentSendsThePickedIndices(t *testing.T) {
	var body map[string]any
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"task":{"id":"t1","name":"Pack"},"file":{"id":"f1"}}`)
	})

	path := writeTorrent(t)
	result, err := svc.AddTorrent("", path, []int{1}, "dest")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.TaskID != "t1" || result.Name != "Pack" {
		t.Fatalf("result = %+v", result)
	}
	url, _ := body["url"].(map[string]any)
	link, _ := url["url"].(string)
	if !strings.HasPrefix(link, "magnet:?xt=urn:btih:") || !strings.Contains(link, "&tr=udp") {
		t.Errorf("url = %q, want a magnet link carrying the torrent's tracker", link)
	}
	if files := fmt.Sprint(url["files"]); files != "[1]" {
		t.Errorf("files = %v, want the picked index only", url["files"])
	}
	if body["parent_id"] != "dest" {
		t.Errorf("parent = %v", body["parent_id"])
	}

	// Everything picked is the server's own default: no filter is sent.
	body = nil
	if _, err := svc.AddTorrent("", path, []int{0, 1}, ""); err != nil {
		t.Fatal(err)
	}
	url, _ = body["url"].(map[string]any)
	if _, ok := url["files"]; ok {
		t.Errorf("a full selection still sent a filter: %v", url["files"])
	}

	// An index the torrent does not have is a bug in the caller, not a task.
	if _, err := svc.AddTorrent("", path, []int{7}, ""); err == nil {
		t.Error("an index outside the torrent was submitted")
	}
}
