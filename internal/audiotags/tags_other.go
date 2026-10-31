//go:build !darwin

package audiotags

import "context"

// Read is unavailable where no reader is built in.
func Read(context.Context, string) (Tags, error) { return Tags{}, ErrUnsupported }
