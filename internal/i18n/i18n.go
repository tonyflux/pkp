// Package i18n localizes Go-generated user-facing errors, notifications and menus
// into Simplified Chinese, Traditional Chinese, English, Japanese and Korean.
//
// Messages use Simplified Chinese source text as keys, like the webview.
// T(key) falls back to the source; msg_*.go holds the other four languages.
// A lowercase ASCII context prefix followed by "|" disambiguates meanings
// and is omitted from displayed source text. Formatting keys keep fmt verbs;
// translations that need a different argument order use indexed verbs (%[2]s).
//
// The language is process-wide. Until Set it is SimplifiedChinese, so tests
// read the source text; main applies the user's choice at start.
package i18n

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

// Lang is one of the supported languages, as a BCP 47 tag.
type Lang string

const (
	SimplifiedChinese  Lang = "zh-Hans"
	TraditionalChinese Lang = "zh-Hant"
	English            Lang = "en"
	Japanese           Lang = "ja"
	Korean             Lang = "ko"
)

// Langs lists the supported languages in the order the page offers them.
var Langs = []Lang{SimplifiedChinese, TraditionalChinese, English, Japanese, Korean}

// Msg is one message in the languages besides its Simplified Chinese key. EnOne is
// the English singular for N when the count is 1 ("1 file" beside
// "%d files"); without it English uses En for every count.
type Msg struct {
	Hant, En, Ja, Ko string
	EnOne            string
}

var (
	current   atomic.Value // Lang
	catalog   = map[string]Msg{}
	conflicts []string // keys two msg_*.go files translate differently

	hooksMu sync.Mutex
	hooks   []func()
)

func init() { current.Store(SimplifiedChinese) }

// add merges one area's messages; init in each msg_*.go calls it.
func add(m map[string]Msg) {
	for k, v := range m {
		if old, ok := catalog[k]; ok && old != v {
			conflicts = append(conflicts, k)
		}
		catalog[k] = v
	}
}

// Current is the language in force.
func Current() Lang { return current.Load().(Lang) }

// Set puts a language in force and tells everything registered with
// OnChange when it differs from the one before.
func Set(l Lang) {
	if current.Swap(l) == l {
		return
	}
	hooksMu.Lock()
	fns := append([]func(){}, hooks...)
	hooksMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// OnChange registers fn to run after Set changes the language: native text
// that was drawn once (the menu bar item) redraws there.
func OnChange(fn func()) {
	hooksMu.Lock()
	hooks = append(hooks, fn)
	hooksMu.Unlock()
}

// Supported reports whether s names a supported language.
func Supported(s string) (Lang, bool) {
	for _, l := range Langs {
		if string(l) == s {
			return l, true
		}
	}
	return "", false
}

// Resolve picks the first supported language of a preference list (BCP 47
// or POSIX tags, most wanted first). Chinese is Traditional when the tag
// says Hant or a Traditional region (TW, HK, MO), else Simplified. Nothing
// supported reads as English.
func Resolve(tags []string) Lang {
	for _, tag := range tags {
		tag, _, _ = strings.Cut(tag, ".") // zh_CN.UTF-8
		parts := strings.FieldsFunc(strings.ToLower(tag), func(r rune) bool { return r == '-' || r == '_' })
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "zh":
			hant, hans, traditionalRegion := false, false, false
			for _, p := range parts[1:] {
				switch p {
				case "hant":
					hant = true
				case "hans":
					hans = true
				case "tw", "hk", "mo":
					traditionalRegion = true
				}
			}
			if hant || (!hans && traditionalRegion) {
				return TraditionalChinese
			}
			return SimplifiedChinese
		case "en":
			return English
		case "ja":
			return Japanese
		case "ko":
			return Korean
		}
	}
	return English
}

// System is this computer's preferred language among the supported ones.
func System() Lang { return Resolve(preferred()) }

// Tag is the language with a region, for services that want one (TMDB's
// language parameter): zh-CN, zh-TW, en-US, ja-JP, ko-KR.
func Tag() string {
	switch Current() {
	case TraditionalChinese:
		return "zh-TW"
	case English:
		return "en-US"
	case Japanese:
		return "ja-JP"
	case Korean:
		return "ko-KR"
	}
	return "zh-CN"
}

var contextPrefix = regexp.MustCompile(`^[a-z][a-z0-9-]*\|`)

// T is key in the language in force; a missing translation falls back to
// Simplified Chinese source text.
func T(key string) string {
	return lookup(key, false)
}

func lookup(key string, one bool) string {
	if l := Current(); l != SimplifiedChinese {
		if m, ok := catalog[key]; ok {
			var s string
			switch l {
			case TraditionalChinese:
				s = m.Hant
			case English:
				s = m.En
				if one && m.EnOne != "" {
					s = m.EnOne
				}
			case Japanese:
				s = m.Ja
			case Korean:
				s = m.Ko
			}
			if s != "" {
				return s
			}
		}
	}
	return contextPrefix.ReplaceAllString(key, "")
}

// F formats the translated format with args, as fmt.Sprintf.
func F(format string, args ...any) string {
	return fmt.Sprintf(T(format), args...)
}

// N is F for a message about n things: English takes Msg.EnOne when n is 1.
func N(n int, format string, args ...any) string {
	return fmt.Sprintf(lookup(format, n == 1), args...)
}

// Errorf is fmt.Errorf with the translated format; %w still wraps.
func Errorf(format string, args ...any) error {
	return fmt.Errorf(T(format), args...)
}

// New is an error whose text is key in whatever language is in force when
// it is read: for package-level sentinels compared with errors.Is.
func New(key string) error { return &message{key} }

type message struct{ key string }

func (m *message) Error() string { return T(m.key) }
