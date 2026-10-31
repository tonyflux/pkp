//go:build !darwin && !windows && !linux

package syncfs

func RenameNoReplace(from, to string) error { return ErrUnsupported }
