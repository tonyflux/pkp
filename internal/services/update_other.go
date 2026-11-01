//go:build !darwin && !windows

package services

import (
	"context"
	"errors"

	"github.com/tonyflux/pkp/internal/update"
)

func updatePlatform() (string, string) { return "", "" }
func prepareUpdatePackage(context.Context, string, update.Package, string, string) (*updateQuit, error) {
	return nil, errors.New("in-app updates are supported on macOS and Windows only")
}
