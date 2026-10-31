package download

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeName(t *testing.T) {
	for _, tc := range []struct{ name, goos, want string }{
		{"电影.mkv", "darwin", "电影.mkv"},
		{"a/b.txt", "darwin", "a_b.txt"},
		{"tab\there\x00.txt", "linux", "tab_here_.txt"},
		{`a<b>:c"d\e|f?g*.txt`, "darwin", `a<b>:c"d\e|f?g*.txt`},
		{`a<b>:c"d\e|f?g*.txt`, "windows", "a_b__c_d_e_f_g_.txt"},
		{"trailing. . ", "windows", "trailing"},
		{"trailing. ", "darwin", "trailing. "},
		{"con", "windows", "_con"},
		{"CON.txt", "windows", "_CON.txt"},
		{"lpt9.tar.gz", "windows", "_lpt9.tar.gz"},
		{"com10.txt", "windows", "com10.txt"},
		{"console.txt", "windows", "console.txt"},
		{"con", "darwin", "con"},
		{"", "darwin", "_"},
		{".", "darwin", "_"},
		{"..", "linux", "_"},
		{"...", "windows", "_"},
	} {
		if got := SafeName(tc.name, tc.goos); got != tc.want {
			t.Errorf("SafeName(%q, %s) = %q, want %q", tc.name, tc.goos, got, tc.want)
		}
	}

	// Long names lose the end of the stem, on a character boundary, and
	// keep the extension.
	long := strings.Repeat("影", 100) + ".mkv" // 304 bytes
	got := SafeName(long, "darwin")
	if len(got) > maxName || !utf8.ValidString(got) || !strings.HasSuffix(got, "影.mkv") {
		t.Fatalf("SafeName(long) = %d bytes %q", len(got), got)
	}
	if got := SafeName(strings.Repeat("a", 300), "linux"); len(got) != maxName {
		t.Fatalf("SafeName(300 a) = %d bytes", len(got))
	}
}

func TestFreeDir(t *testing.T) {
	parent := t.TempDir()
	first, err := FreeDir(parent, "电影")
	if err != nil || first != filepath.Join(parent, "电影") {
		t.Fatalf("FreeDir = %s, %v", first, err)
	}
	second, err := FreeDir(parent, "电影")
	if err != nil || second != filepath.Join(parent, "电影 (1)") {
		t.Fatalf("FreeDir again = %s, %v", second, err)
	}
	if info, err := os.Stat(second); err != nil || !info.IsDir() {
		t.Fatalf("FreeDir did not make %s: %v", second, err)
	}
	if _, err := FreeDir(filepath.Join(parent, "missing", "deeper"), "x"); err == nil {
		t.Fatal("FreeDir under a missing folder must fail")
	}
}
