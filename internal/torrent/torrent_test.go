package torrent

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// bencode is the test's own encoder, so the parser is checked against bytes
// it did not produce. The expected info hashes were computed independently
// (Python, sha1 of the encoded info dictionary).
func bencode(v any) string {
	switch x := v.(type) {
	case int:
		return fmt.Sprintf("i%de", x)
	case string:
		return fmt.Sprintf("%d:%s", len(x), x)
	case []any:
		var b strings.Builder
		b.WriteString("l")
		for _, item := range x {
			b.WriteString(bencode(item))
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
			b.WriteString(bencode(k) + bencode(x[k]))
		}
		b.WriteString("e")
		return b.String()
	}
	panic("bencode: unsupported value")
}

// A GBK torrent from a Chinese site, with a UTF-8 twin on one path and not
// on the other: both must read as Chinese, and the files keep torrent order
// because that is what the API's file selection counts in.
func TestParseMultiFileTorrent(t *testing.T) {
	const gbkName = "\xc6\xac\xd7\xd3"    // GBK-encoded movie folder name.
	const gbkSub = "\xd6\xd0\xce\xc4.srt" // GBK-encoded subtitle filename.
	data := bencode(map[string]any{
		"announce": "udp://tracker.example:6969",
		"announce-list": []any{
			[]any{"udp://tracker.example:6969"},
			[]any{"http://back.example/announce"},
		},
		"comment": "x",
		"info": map[string]any{
			"name":         gbkName,
			"piece length": 262144,
			"pieces":       strings.Repeat("\x01", 20),
			"files": []any{
				map[string]any{"length": 1200, "path": []any{"Subs", gbkSub}, "path.utf-8": []any{"Subs", "中文.srt"}},
				map[string]any{"length": 734003200, "path": []any{gbkName + ".mkv"}},
			},
		},
	})

	meta, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if meta.InfoHash != "3942894D9A50130DEDFBCE1B874023067BFDFC45" {
		t.Errorf("info hash = %s", meta.InfoHash)
	}
	if meta.Name != "片子" {
		t.Errorf("name = %q", meta.Name)
	}
	if meta.Size != 1200+734003200 {
		t.Errorf("size = %d", meta.Size)
	}
	want := []File{
		{Index: 0, Path: "Subs/中文.srt", Size: 1200},
		{Index: 1, Path: "片子.mkv", Size: 734003200},
	}
	if len(meta.Files) != len(want) {
		t.Fatalf("files = %+v", meta.Files)
	}
	for i, f := range meta.Files {
		if f != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, f, want[i])
		}
	}

	magnet := meta.Magnet()
	const head = "magnet:?xt=urn:btih:3942894d9a50130dedfbce1b874023067bfdfc45"
	if !strings.HasPrefix(magnet, head) {
		t.Errorf("magnet = %s", magnet)
	}
	// announce-list order first, the plain announce deduplicated away.
	if want := "&dn=%E7%89%87%E5%AD%90&tr=udp%3A%2F%2Ftracker.example%3A6969&tr=http%3A%2F%2Fback.example%2Fannounce"; magnet != head+want {
		t.Errorf("magnet tail = %s", strings.TrimPrefix(magnet, head))
	}
}

func TestParseSingleFileTorrent(t *testing.T) {
	data := bencode(map[string]any{"info": map[string]any{
		"name": "clip.mp4", "length": 42, "piece length": 16384, "pieces": strings.Repeat("\x02", 20),
	}})
	meta, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if meta.InfoHash != "D90841F9E8E23F9ED6CCE9B63FFF1DFC0A4C902D" || meta.Size != 42 {
		t.Errorf("meta = %+v", meta)
	}
	if len(meta.Files) != 1 || meta.Files[0] != (File{Index: 0, Path: "clip.mp4", Size: 42}) {
		t.Errorf("files = %+v", meta.Files)
	}
	if meta.Magnet() != "magnet:?xt=urn:btih:d90841f9e8e23f9ed6cce9b63fff1dfc0a4c902d&dn=clip.mp4" {
		t.Errorf("magnet = %s", meta.Magnet())
	}
}

// A v2-only torrent has no v1 info hash, and the drive API addresses a
// torrent by that hash alone: it must be refused with a reason, not added
// as some other torrent.
func TestParseRefusesV2OnlyTorrent(t *testing.T) {
	data := bencode(map[string]any{"info": map[string]any{
		"name": "clip.mp4", "meta version": 2, "piece length": 16384,
		"file tree": map[string]any{"clip.mp4": map[string]any{"": map[string]any{"length": 42}}},
	}})
	if _, err := Parse([]byte(data)); err != ErrNoV1Hash {
		t.Errorf("err = %v, want ErrNoV1Hash", err)
	}
}

func TestParseRejectsRubbish(t *testing.T) {
	for name, data := range map[string]string{
		"empty":       "",
		"not bencode": "%PDF-1.7 not a torrent at all",
		"truncated":   bencode(map[string]any{"info": map[string]any{"name": "a", "length": 1, "pieces": "xx"}})[:12],
		"no info":     bencode(map[string]any{"announce": "udp://x:1"}),
	} {
		if meta, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: parsed as %+v", name, meta)
		}
	}
}
