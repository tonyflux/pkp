package audiotags

import (
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// widened is what FFmpeg makes of bytes in a tag declared Latin-1: one rune
// per byte.
func widened(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func TestCleanRepairsMisdeclaredTags(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().String("周杰伦 - 晴天")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ in, want string }{
		"GBK as Latin-1":   {widened([]byte(gbk)), "周杰伦 - 晴天"},
		"UTF-8 as Latin-1": {widened([]byte("邓紫棋")), "邓紫棋"},
		"real Latin-1":     {"Café del Mar", "Café del Mar"},
		"accented name":    {"Beyoncé", "Beyoncé"},
		"plain":            {"  Hello\x00", "Hello"},
		"already fine":     {"龙卷风", "龙卷风"},
	} {
		if got := clean(tc.in); got != tc.want {
			t.Errorf("%s: clean(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
}
