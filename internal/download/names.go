package download

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tonyflux/pkp/internal/i18n"
)

// maxName is the longest name SafeName returns, in bytes: file systems stop
// at 255, and a " (9999)" and PartialExt still have to fit.
const maxName = 235

// SafeName turns a drive name into one goos can create: path separators and
// control characters become "_"; on Windows also its reserved characters,
// trailing dots and spaces, and device names such as CON. Long names lose
// the end of the stem and keep the extension.
func SafeName(name, goos string) string {
	windows := goos == "windows"
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r < 0x20 || (windows && strings.ContainsRune(`<>:"\|?*`, r)) {
			r = '_'
		}
		b.WriteRune(r)
	}
	s := b.String()
	if len(s) > maxName {
		ext := extOf(s)
		if len(ext) >= maxName {
			ext = ""
		}
		stem := s[:len(s)-len(ext)]
		cut := maxName - len(ext)
		for cut > 0 && !utf8.RuneStart(stem[cut]) {
			cut--
		}
		s = stem[:cut] + ext
	}
	if windows {
		s = strings.TrimRight(s, ". ")
		device, _, _ := strings.Cut(s, ".")
		if reserved(strings.TrimRight(device, " ")) {
			s = "_" + s
		}
	}
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

// reserved is a Windows device name, which no file may be called whatever
// its extension.
func reserved(stem string) bool {
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && stem[3] >= '1' && stem[3] <= '9' {
		switch strings.ToUpper(stem[:3]) {
		case "COM", "LPT":
			return true
		}
	}
	return false
}

// extOf is the extension a numbered name keeps: ".mkv" of "a.mkv", none for
// a dotfile such as ".bashrc".
func extOf(name string) string {
	ext := filepath.Ext(name)
	if ext == name {
		return ""
	}
	return ext
}

// FreeDir makes a new folder for name under parent and returns its path:
// name itself, or "name (1)", "name (2)"… when taken. Making it reserves it,
// so two downloads of one folder never share a tree.
func FreeDir(parent, name string) (string, error) {
	for i := 0; i <= 9999; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)", name, i)
		}
		path := filepath.Join(parent, candidate)
		err := os.Mkdir(path, 0o755)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", i18n.Errorf("「%s」的同名文件夹太多", name)
}

// claimLocked picks the path a new download of name lands at in dir: name
// itself, or "stem (1).ext", "stem (2).ext"… while taken by a file, a
// partial file, or another job; mu must be held.
func (q *Queue) claimLocked(dir, name string) (string, error) {
	ext := extOf(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i <= 9999; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		path := filepath.Join(dir, candidate)
		if q.freeLocked(path) {
			return path, nil
		}
	}
	return "", i18n.Errorf("「%s」的同名文件太多", name)
}

// freeLocked is whether nothing has path: no file, no partial file, no job,
// compared without case as macOS and Windows do; mu must be held.
func (q *Queue) freeLocked(path string) bool {
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if _, err := os.Lstat(path + PartialExt); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	for _, j := range q.jobs {
		if strings.EqualFold(j.Path, path) {
			return false
		}
	}
	for j := range q.direct {
		if strings.EqualFold(j.Path, path) {
			return false
		}
	}
	return true
}
