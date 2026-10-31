package services

import (
	"os/exec"
	"strings"
)

func updatePlatform() (string, string) {
	version, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return "", ""
	}
	return "darwin-universal", strings.TrimSpace(string(version))
}

func openUpdatePackage(path string) error { return exec.Command("/usr/bin/open", path).Run() }
