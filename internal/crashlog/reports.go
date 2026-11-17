package crashlog

import (
	"os"
	"path/filepath"
)

// ReportDirs lists the same desktop and fallback directories used by the recorder.
func ReportDirs() []string {
	var dirs []string
	if dir, err := desktopDir(); err == nil && dir != "" {
		dirs = append(dirs, dir)
	}
	if cache, err := os.UserCacheDir(); err == nil && cache != "" {
		dirs = append(dirs, filepath.Join(cache, "pkp", "crash-reports"))
	}
	return dirs
}
