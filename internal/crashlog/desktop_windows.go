package crashlog

import "golang.org/x/sys/windows"

func desktopDir() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Desktop, windows.KF_FLAG_DEFAULT)
}
