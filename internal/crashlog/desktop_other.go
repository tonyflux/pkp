//go:build !windows && !linux

package crashlog

import (
	"os"
	"path/filepath"
)

func desktopDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Desktop"), nil
}
