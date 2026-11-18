//go:build linux

package crashlog

import (
	"errors"

	"github.com/adrg/xdg"
)

func desktopDir() (string, error) {
	if xdg.UserDirs.Desktop == "" {
		return "", errors.New("XDG Desktop directory is unavailable")
	}
	return xdg.UserDirs.Desktop, nil
}
