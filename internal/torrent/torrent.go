// Package torrent reads a .torrent file far enough for a Cloud Add: the info
// hash the drive API wants as a magnet link, and the files the torrent
// lists so the user can pick among them. It never talks to a tracker or a
// peer — the file carries everything needed.
package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// MaxSize bounds a .torrent file. A torrent of a 50 GB release with 16 KiB
// pieces is about 3 MB of hashes; 10 MB leaves room and keeps a file picked
// by mistake from being read into memory whole.
const MaxSize = 10 << 20

// File is one file the torrent lists.
type File struct {
	// Index is the file's position in the torrent, which is what the drive
	// API's file selection counts in.
	Index int `json:"index"`
	// Path is the path inside the torrent, "/" separated, without the
	// torrent's own name.
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Meta is what a .torrent says about its content.
type Meta struct {
	// Name is the torrent's display name: the single file, or the folder
	// the files live in.
	Name string `json:"name"`
	// InfoHash is the BitTorrent v1 info hash, 40 upper-case hex digits.
	InfoHash string `json:"hash"`
	// Size is every listed file together.
	Size     int64    `json:"size"`
	Files    []File   `json:"files"`
	Trackers []string `json:"-"`
}

// ErrNoV1Hash is a torrent that carries only BitTorrent v2 metadata. The
// drive API addresses a torrent by its v1 info hash, so there is nothing to
// send for one.
var ErrNoV1Hash = errors.New("torrent: this file has no BitTorrent v1 info hash (v2-only torrent)")

// maxTrackers bounds the trackers carried into the magnet link: the first
// few are what a client would try first, and the server needs no more to
// find the swarm.
const maxTrackers = 8

// magnetNameBytes bounds the dn parameter, which is a display hint only.
const magnetNameBytes = 200

// Parse reads a .torrent file's bytes.
func Parse(data []byte) (Meta, error) {
	if len(data) > MaxSize {
		return Meta{}, fmt.Errorf("torrent: file is larger than %d MB", MaxSize>>20)
	}
	d := &decoder{b: data}
	top, err := d.dict()
	if err != nil {
		return Meta{}, err
	}
	info, _ := top["info"].(map[string]any)
	if info == nil || d.info == nil {
		return Meta{}, errors.New("torrent: not a torrent file (no info dictionary)")
	}
	if _, ok := info["pieces"].(string); !ok {
		return Meta{}, ErrNoV1Hash
	}

	sum := sha1.Sum(d.info)
	meta := Meta{
		Name:     text(info, "name"),
		InfoHash: strings.ToUpper(hex.EncodeToString(sum[:])),
		Trackers: trackers(top),
	}
	meta.Files, meta.Size, err = files(info)
	if err != nil {
		return Meta{}, err
	}
	if meta.Name == "" && len(meta.Files) == 1 {
		meta.Name = meta.Files[0].Path
	}
	return meta, nil
}

// Magnet is the link the drive API takes for this torrent: the info hash,
// the name as a hint, and the trackers the file named so the server can
// find the swarm of a torrent nobody else is asking for.
func (m Meta) Magnet() string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:" + strings.ToLower(m.InfoHash))
	if name := trim(m.Name, magnetNameBytes); name != "" {
		b.WriteString("&dn=" + url.QueryEscape(name))
	}
	for _, t := range m.Trackers {
		b.WriteString("&tr=" + url.QueryEscape(t))
	}
	return b.String()
}

// files lists the torrent's files in torrent order, which is the order the
// drive API's indices count in.
func files(info map[string]any) ([]File, int64, error) {
	name := text(info, "name")
	raw, multi := info["files"].([]any)
	if !multi {
		size, ok := info["length"].(int64)
		if !ok {
			return nil, 0, errors.New("torrent: the info dictionary lists neither files nor a length")
		}
		return []File{{Index: 0, Path: name, Size: size}}, size, nil
	}

	out := make([]File, 0, len(raw))
	var total int64
	for i, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, 0, fmt.Errorf("torrent: file %d is not a dictionary", i)
		}
		size, _ := entry["length"].(int64)
		out = append(out, File{Index: i, Path: filePath(entry), Size: size})
		total += size
	}
	return out, total, nil
}

// filePath joins one entry's path segments. A torrent written by a Chinese
// client often carries both a locally encoded path and a UTF-8 one; the
// UTF-8 one wins.
func filePath(entry map[string]any) string {
	segments, _ := entry["path.utf-8"].([]any)
	if len(segments) == 0 {
		segments, _ = entry["path"].([]any)
	}
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		if part, ok := s.(string); ok && part != "" && part != "." && part != ".." {
			parts = append(parts, decode(part))
		}
	}
	return path.Join(parts...)
}

// trackers are the torrent's announce URLs, announce-list first (it is the
// ordered one), without duplicates.
func trackers(top map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v any) {
		u, ok := v.(string)
		u = strings.TrimSpace(u)
		if !ok || u == "" || seen[u] || len(out) >= maxTrackers {
			return
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "udp://") {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	for _, tier := range asList(top["announce-list"]) {
		for _, u := range asList(tier) {
			add(u)
		}
	}
	add(top["announce"])
	return out
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

// text reads a string field, preferring the UTF-8 twin a non-UTF-8 client
// wrote beside it.
func text(m map[string]any, key string) string {
	if s, ok := m[key+".utf-8"].(string); ok && s != "" {
		return decode(s)
	}
	s, _ := m[key].(string)
	return decode(s)
}

// decode makes a bencoded byte string displayable: bencode has no encoding
// of its own, and torrents from Chinese sites are routinely GBK.
func decode(s string) string {
	if s == "" || utf8.ValidString(s) {
		return s
	}
	if out, err := simplifiedchinese.GBK.NewDecoder().String(s); err == nil && utf8.ValidString(out) {
		return out
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// trim cuts a string to at most n bytes on a rune boundary.
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

/* ---------------- bencode ---------------- */

var errTruncated = errors.New("torrent: the file ends in the middle of a value")

// decoder reads bencode and remembers the raw bytes of the top-level info
// value: the info hash is the SHA-1 of exactly those bytes, so they cannot
// be re-encoded from the parsed form.
type decoder struct {
	b     []byte
	i     int
	depth int
	info  []byte
}

func (d *decoder) value() (any, error) {
	if d.i >= len(d.b) {
		return nil, errTruncated
	}
	switch c := d.b[d.i]; {
	case c == 'd':
		return d.dict()
	case c == 'l':
		return d.list()
	case c == 'i':
		return d.integer()
	case c >= '0' && c <= '9':
		s, err := d.str()
		return s, err
	default:
		return nil, fmt.Errorf("torrent: unexpected %q at byte %d", c, d.i)
	}
}

func (d *decoder) dict() (map[string]any, error) {
	if d.i >= len(d.b) || d.b[d.i] != 'd' {
		return nil, errors.New("torrent: not a torrent file (it does not start with a dictionary)")
	}
	d.i++
	d.depth++
	defer func() { d.depth-- }()

	out := map[string]any{}
	for {
		if d.i >= len(d.b) {
			return nil, errTruncated
		}
		if d.b[d.i] == 'e' {
			d.i++
			return out, nil
		}
		key, err := d.str()
		if err != nil {
			return nil, err
		}
		start := d.i
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		if d.depth == 1 && key == "info" && d.info == nil {
			d.info = d.b[start:d.i]
		}
		out[key] = v
	}
}

func (d *decoder) list() ([]any, error) {
	d.i++ // 'l'
	var out []any
	for {
		if d.i >= len(d.b) {
			return nil, errTruncated
		}
		if d.b[d.i] == 'e' {
			d.i++
			return out, nil
		}
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

func (d *decoder) integer() (int64, error) {
	end := bytes.IndexByte(d.b[d.i:], 'e')
	if end < 0 {
		return 0, errTruncated
	}
	n, err := strconv.ParseInt(string(d.b[d.i+1:d.i+end]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("torrent: bad integer at byte %d", d.i)
	}
	d.i += end + 1
	return n, nil
}

func (d *decoder) str() (string, error) {
	colon := bytes.IndexByte(d.b[d.i:], ':')
	if colon < 0 {
		return "", errTruncated
	}
	n, err := strconv.Atoi(string(d.b[d.i : d.i+colon]))
	if err != nil || n < 0 {
		return "", fmt.Errorf("torrent: bad string length at byte %d", d.i)
	}
	start := d.i + colon + 1
	if start+n > len(d.b) {
		return "", errTruncated
	}
	d.i = start + n
	return string(d.b[start : start+n]), nil
}
