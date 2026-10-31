package torrent

import (
	"encoding/base32"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

var hexHash = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// BTIH is the info hash a magnet link names (xt=urn:btih:, 40 hex digits
// or the 32-character base32 form), as 40 upper-case hex digits; "" when
// link is not a magnet link or names no v1 hash.
func BTIH(link string) string {
	const scheme = "magnet:?"
	if len(link) < len(scheme) || !strings.EqualFold(link[:len(scheme)], scheme) {
		return ""
	}
	// A malformed pair (a stray % in dn) is skipped; the rest still parse.
	values, _ := url.ParseQuery(link[len(scheme):])
	for key, xts := range values {
		// Several hashes come as xt.1, xt.2…
		if key != "xt" && !strings.HasPrefix(key, "xt.") {
			continue
		}
		for _, xt := range xts {
			const urn = "urn:btih:"
			if len(xt) < len(urn) || !strings.EqualFold(xt[:len(urn)], urn) {
				continue
			}
			v := xt[len(urn):]
			if hexHash.MatchString(v) {
				return strings.ToUpper(v)
			}
			if len(v) == 32 {
				if raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(v)); err == nil && len(raw) == 20 {
					return strings.ToUpper(hex.EncodeToString(raw))
				}
			}
		}
	}
	return ""
}
