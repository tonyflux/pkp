package torrent

import (
	"strings"
	"testing"
)

func TestBTIH(t *testing.T) {
	const hash = "8f2a1b3c4d5e6f708192a3b4c5d6e7f80912c941"
	upper := strings.ToUpper(hash)
	for link, want := range map[string]string{
		"magnet:?xt=urn:btih:" + hash + "&dn=x":                upper,
		"MAGNET:?dn=a%ZZ&xt=URN:BTIH:" + hash:                  upper,
		"magnet:?xt=urn:btih:R4VBWPCNLZXXBAMSUO2MLVXH7AERFSKB": upper,
		"magnet:?xt.1=urn:btih:" + hash:                        upper,
		"magnet:?xt=urn:btmh:1220" + hash:                      "",
		"https://example.com/a.torrent":                        "",
		"magnet:?xt=urn:btih:" + hash[:39]:                     "",
		"":                                                     "",
	} {
		if got := BTIH(link); got != want {
			t.Errorf("BTIH(%q) = %q, want %q", link, got, want)
		}
	}
}
