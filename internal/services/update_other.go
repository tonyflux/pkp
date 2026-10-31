//go:build !darwin && !windows

package services

import "errors"

func updatePlatform() (string, string) { return "", "" }
func openUpdatePackage(string) error {
	return errors.New("in-app updates are supported on macOS and Windows only")
}
