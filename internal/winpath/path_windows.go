//go:build windows

// Package winpath resolves DOS drive aliases before no-reparse filesystem opens.
package winpath

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Native resolves only the drive alias; filesystem links remain for the caller to reject.
func Native(path string) (string, error) {
	volume := filepath.VolumeName(path)
	if !filepath.IsAbs(path) || len(volume) != 2 || volume[1] != ':' {
		return "", windows.ERROR_NOT_SUPPORTED
	}
	name, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return "", err
	}
	var target [32768]uint16
	n, err := windows.QueryDosDevice(name, &target[0], uint32(len(target)))
	if err != nil {
		return "", err
	}
	device := windows.UTF16ToString(target[:n])
	if !strings.HasPrefix(device, `\Device\`) {
		return "", windows.ERROR_NOT_SUPPORTED
	}
	return device + path[len(volume):], nil
}
