package services

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const externalTestMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"

func TestExternalMagnetValidation(t *testing.T) {
	for _, value := range []string{
		externalTestMagnet,
		strings.ToUpper(externalTestMagnet[:len("magnet:")]) + externalTestMagnet[len("magnet:"):],
		"magnet:?xt=urn:btih:" + strings.Repeat("A", 32),
		"magnet:?xt.1=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Hello%20World",
		"magnet:?xt=urn:btmh:1220" + strings.Repeat("ab", 32),
	} {
		if !externalMagnet(value) {
			t.Errorf("rejected supported magnet %q", value)
		}
	}
	for _, value := range []string{
		"", "magnet:", "magnet:?dn=missing-hash", "magnet:?xt=urn:btih:abc",
		"magnet:?xt=urn:btih:" + strings.Repeat("g", 40),
		"magnet:?xt=urn:btih:" + strings.Repeat("!", 32),
		"magnet:?xt=urn:btmh:1220" + strings.Repeat("ab", 31),
		"magnet:?xt=urn:btmh:1220" + strings.Repeat("zz", 32),
		"magnet://?xt=urn:btmh:1220" + strings.Repeat("ab", 32),
		externalTestMagnet + "\nhttps://example.com", externalTestMagnet + "\x00",
		externalTestMagnet + "\u2028", externalTestMagnet + "&dn=two words",
		externalTestMagnet + "&dn=%0a", externalTestMagnet + "&dn=%00",
		externalTestMagnet + "&dn=%E2%80%A9", externalTestMagnet + "&%0d=x",
		externalTestMagnet + "&dn=%ZZ", externalTestMagnet + "&dn=a;b",
		externalTestMagnet + "#", externalTestMagnet + "#fragment",
		"https://example.com/a.torrent", "file:///tmp/a.torrent", "pkp://open?file=/tmp/a.torrent",
	} {
		if externalMagnet(value) {
			t.Errorf("accepted malformed or unsupported input %q", value)
		}
	}
}

func externalFileURL(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestExternalTorrentPathResolvesLaunchDirectory(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "space 文件.TORRENT")
	for _, value := range []string{
		"space 文件.TORRENT", path, externalFileURL(path),
		strings.Replace(externalFileURL(path), "file://", "file://localhost", 1),
	} {
		if got := externalTorrentPath(value, cwd); got != path {
			t.Errorf("path %q = %q, want %q", value, got, path)
		}
	}
	if got := externalTorrentPath(filepath.Join("nested", "..", "a.torrent"), cwd); got != filepath.Join(cwd, "a.torrent") {
		t.Fatalf("relative argv was not resolved against launching cwd: %q", got)
	}
	if got := externalTorrentPath("100% ready.torrent", cwd); got != filepath.Join(cwd, "100% ready.torrent") {
		t.Fatalf("literal filesystem percent was parsed as URL escaping: %q", got)
	}
	for _, value := range []string{
		"", "a.mp4", "a.torrent.exe", "a.torrent\n", "a\x00.torrent",
		"https://example.com/a.torrent", "pkp://open/a.torrent", "javascript:a.torrent",
		"file:relative.torrent", "file://remote-host/tmp/a.torrent", "file:////remote-host/a.torrent",
		"file:///tmp/a.torrent?download=1", "file:///tmp/a.torrent?", "file:///tmp/a.torrent#",
		"file:///tmp/a%0a.torrent", "file:///tmp/a%00.torrent", "file:///tmp/a%ZZ.torrent",
	} {
		if got := externalTorrentPath(value, cwd); got != "" {
			t.Errorf("accepted unsupported file input %q as %q", value, got)
		}
	}
	if got := externalTorrentPath("relative.torrent", ""); got != "" {
		t.Fatalf("missing sender cwd fell back to receiver cwd: %q", got)
	}
	if runtime.GOOS == "windows" {
		if got := externalTorrentPath(`C:\Downloads\a.TORRENT`, cwd); got != `C:\Downloads\a.TORRENT` {
			t.Fatalf("native Windows path = %q", got)
		}
		if got := externalTorrentPath(`C:relative.torrent`, cwd); got != "" {
			t.Fatalf("ambiguous drive-relative path accepted: %q", got)
		}
	}
}

func TestExternalOpenColdStartRetainsUntilAcknowledged(t *testing.T) {
	s := NewExternalOpenService()
	cwd := t.TempDir()
	// No file is created: OS ingress must not open or parse torrent contents.
	s.OpenArgs([]string{"pkp", externalTestMagnet, "missing.torrent", "https://example.com/ignored.torrent"}, cwd)
	before := s.Pending()
	if len(before) != 2 || before[0].Kind != "magnet" || before[1].Kind != "torrent" || before[1].Value != filepath.Join(cwd, "missing.torrent") {
		t.Fatalf("cold-start queue = %+v", before)
	}
	var raised int
	s.start(func() { raised++ })
	if raised != 1 || len(s.Pending()) != 2 {
		t.Fatalf("startup did not notify retained requests: raised=%d, pending=%+v", raised, s.Pending())
	}
	before[0].Value = "mutated page snapshot"
	if got := s.Pending()[0].Value; got != externalTestMagnet {
		t.Fatalf("page snapshot mutated queue: %q", got)
	}
	// The page may be signed out or locked and read again without draining it.
	if got := s.Pending(); len(got) != 2 || got[0].ID != before[0].ID {
		t.Fatalf("Pending drained or renumbered requests: %+v", got)
	}
	s.Ack(before[1].ID)
	if got := s.Pending(); len(got) != 1 || got[0].ID != before[0].ID {
		t.Fatalf("Ack removed another request: %+v", got)
	}
	s.Ack(before[1].ID)
	s.Ack(999999)
	s.Ack(before[0].ID)
	if got := s.Pending(); got == nil || len(got) != 0 {
		t.Fatalf("empty queue must serialize as [], got %+v", got)
	}
	if !s.OpenURL(strings.Replace(externalTestMagnet, "magnet:", "MAGNET:", 1)) || s.Pending()[0].Value != externalTestMagnet || raised != 2 {
		t.Fatalf("running URL was not normalized and notified: %+v, raised=%d", s.Pending(), raised)
	}
}

func TestExternalOpenRejectsWithoutRaising(t *testing.T) {
	s := NewExternalOpenService()
	raised := 0
	s.start(func() { raised++ })
	cwd := t.TempDir()
	if s.OpenURL(externalFileURL(filepath.Join(cwd, "a.torrent"))) {
		t.Fatal("browser URL ingress accepted a local file")
	}
	s.OpenArgs(nil, cwd)
	s.OpenArgs([]string{"pkp"}, cwd)
	s.OpenArgs([]string{"pkp", "--unrecognized", "https://example.com/a.torrent", "magnet:?xt=broken", "pkp://open/a.torrent"}, cwd)
	if raised != 0 || len(s.Pending()) != 0 {
		t.Fatalf("unsupported launch raised app or enqueued input: raised=%d, pending=%+v", raised, s.Pending())
	}
}

func TestExternalShareURLsStageOnlyTrustedShareLinks(t *testing.T) {
	const link = "https://mypikpak.com/s/Share_123-abc?pass_code=Ab12"
	s := NewExternalOpenService()
	if !s.OpenURL("pkp://share?url=" + url.QueryEscape(link)) {
		t.Fatal("explicit share deeplink was rejected")
	}
	s.OpenURL("https://www.mypikpak.com/s/Share_123-abc/nested?password=Ab12")
	pending := s.Pending()
	if len(pending) != 1 || pending[0].Kind != "share" || pending[0].Value != link {
		t.Fatalf("share identity or access code was lost: %+v", pending)
	}
	for _, bad := range []string{
		"pkp://share?url=" + url.QueryEscape(link) + "&file=/tmp/a.torrent",
		"pkp://share?url=" + url.QueryEscape(link) + "&url=" + url.QueryEscape(link),
		"pkp://share?url=file%3A%2F%2F%2Ftmp%2Fa.torrent",
		"pkp://share?url=" + url.QueryEscape(link) + "#",
		"pkp://share/open?url=" + url.QueryEscape(link),
		"pkp://user@share?url=" + url.QueryEscape(link),
		"pkp://share?url=" + url.QueryEscape("https://mypikpak.com.evil.example/s/a"),
		"https://mypikpak.com/s/a%2fb",
		"https://mypikpak.com/s/a?pass_code=%E2%80%A8",
		"pkp://share?url=" + url.QueryEscape("https://mypikpak.com/s/a?password=%E2%80%A9"),
		"https://mypikpak.com/s/a?pass_code=%FF",
	} {
		if s.OpenURL(bad) {
			t.Errorf("unsafe URL accepted: %q", bad)
		}
	}
	if len(s.Pending()) != 1 {
		t.Fatal("rejected URLs changed the retained share")
	}
	s.Ack(pending[0].ID)
	s.OpenArgs([]string{"pkp", link, externalTestMagnet}, t.TempDir())
	again := s.Pending()
	if len(again) != 2 || again[0].Kind != "share" || again[0].ID <= pending[0].ID || again[1].Kind != "magnet" {
		t.Fatalf("share reopen or existing magnet route was lost: %+v", again)
	}
}

func TestExternalOpenCoalescesPendingDuplicatesButAllowsReopening(t *testing.T) {
	s := NewExternalOpenService()
	cwd := t.TempDir()
	s.OpenURL(externalTestMagnet)
	s.OpenURL(strings.Replace(externalTestMagnet, "magnet:", "MAGNET:", 1))
	s.OpenFile("sample.torrent", cwd)
	s.OpenArgs([]string{"pkp", externalTestMagnet, "sample.torrent"}, cwd)
	pending := s.Pending()
	if len(pending) != 2 || pending[0].Kind != "magnet" || pending[1].Kind != "torrent" {
		t.Fatalf("duplicate OS deliveries were queued again: %+v", pending)
	}
	s.Ack(pending[0].ID)
	s.OpenURL(externalTestMagnet)
	reopened := s.Pending()
	if len(reopened) != 2 || reopened[0] != pending[1] || reopened[1].Value != externalTestMagnet || reopened[1].ID <= pending[1].ID {
		t.Fatalf("deliberately reopening an acknowledged link was lost: %+v", reopened)
	}
}

func TestExternalOpenAckDoesNotLoseConcurrentArrivals(t *testing.T) {
	s := NewExternalOpenService()
	s.OpenURL(externalTestMagnet)
	staged := s.Pending()[0]
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.OpenURL(fmt.Sprintf("%s&dn=%d", externalTestMagnet, i))
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Ack(staged.ID)
		s.Ack(staged.ID)
	}()
	wg.Wait()
	pending := s.Pending()
	if len(pending) != 64 {
		t.Fatalf("stale page acknowledgement lost newer arrivals: %d pending", len(pending))
	}
	last := staged.ID
	seen := make(map[string]bool)
	for _, item := range pending {
		if item.ID <= last || seen[item.Value] {
			t.Fatalf("queue lost arrival order or duplicated a request: %+v", pending)
		}
		last = item.ID
		seen[item.Value] = true
	}
}

func TestClipboardShareFiltersPrivateTextWithoutEnqueuing(t *testing.T) {
	if application.System.IsServer() {
		t.Skip("desktop clipboard filtering")
	}
	const link = "https://mypikpak.com/s/Clipboard_123?pass_code=Ab12"
	for _, tc := range []struct{ text, want string }{
		{" \n" + link + "\n ", link},
		{"pkp://share?url=" + url.QueryEscape(link), link},
		{"private password 123456", ""},
		{"https://foreign.example/s/Clipboard_123", ""},
		{link + "\nhttps://example.com", ""},
	} {
		s := NewExternalOpenService()
		s.clipboard = func() (string, bool) { return tc.text, true }
		if got := s.ClipboardShare(); got != tc.want {
			t.Errorf("clipboard %q = %q, want %q", tc.text, got, tc.want)
		}
		if len(s.Pending()) != 0 {
			t.Fatal("sampling the clipboard enqueued an external launch")
		}
	}
	s := NewExternalOpenService()
	s.clipboard = func() (string, bool) { return link, false }
	if got := s.ClipboardShare(); got != "" {
		t.Fatalf("failed clipboard read returned %q", got)
	}
}

func TestClipboardShareNeverReadsServerClipboard(t *testing.T) {
	if !application.System.IsServer() {
		t.Skip("server clipboard boundary")
	}
	s := NewExternalOpenService()
	s.clipboard = func() (string, bool) {
		t.Fatal("server client accessed the host clipboard")
		return "", false
	}
	if got := s.ClipboardShare(); got != "" {
		t.Fatalf("server returned clipboard share %q", got)
	}
}
