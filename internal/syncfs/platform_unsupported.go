//go:build (!darwin && !windows) || (darwin && !cgo)

package syncfs

import (
	"context"
	"io/fs"
	"os"
)

func platformIdentity(path string) (Identity, error) { return Identity{}, ErrUnsupported }
func platformFile(info fs.FileInfo, path string) (string, string, error) {
	return "", "", ErrUnsupported
}
func platformVersion(info fs.FileInfo, path string) string { return "" }
func isReparse(info fs.FileInfo) bool                      { return info.Mode()&os.ModeSymlink != 0 }
func openNoFollow(path string) (*os.File, error)           { return nil, ErrUnsupported }
func Trash(path string) error                              { return ErrUnsupported }
func Watch(ctx context.Context, root Identity, notify func()) (func(), error) {
	return nil, ErrUnsupported
}
