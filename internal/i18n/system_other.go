//go:build !darwin && !windows

package i18n

import (
	"os"
	"strings"
)

// preferred reads the POSIX locale variables in the order gettext does;
// LANGUAGE may list several, colon-separated.
func preferred() []string {
	var out []string
	for _, name := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		for _, tag := range strings.Split(os.Getenv(name), ":") {
			if tag != "" && tag != "C" && tag != "POSIX" {
				out = append(out, tag)
			}
		}
	}
	return out
}

// SetAppLanguage does nothing here: there is no per-app system language.
func SetAppLanguage(Lang) {}
