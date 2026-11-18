//go:build windows

package mpv

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"unsafe"
)

// LoadFile replaces playback at start seconds using named libmpv arguments.
func (c *Client) LoadFile(_ context.Context, url string, start float64) error {
	words, err := cArgv([]string{"name", "url", "flags", "options", "start", "loadfile", url, "replace", strconv.FormatFloat(start, 'f', 3, 64)})
	if err != nil {
		return fmt.Errorf("mpv: loadfile: %w", err)
	}
	optionValue := node{format: formatString}
	*(*unsafe.Pointer)(unsafe.Pointer(&optionValue.u)) = unsafe.Pointer(words[8])
	options := nodeList{num: 1, keys: &words[4], values: &optionValue}
	values := [4]node{{format: formatString}, {format: formatString}, {format: formatString}, {format: formatNodeMap}}
	for i := range 3 {
		*(*unsafe.Pointer)(unsafe.Pointer(&values[i].u)) = unsafe.Pointer(words[5+i])
	}
	*(*unsafe.Pointer)(unsafe.Pointer(&values[3].u)) = unsafe.Pointer(&options)
	arguments := nodeList{num: 4, keys: &words[0], values: &values[0]}
	command := node{format: formatNodeMap}
	*(*unsafe.Pointer)(unsafe.Pointer(&command.u)) = unsafe.Pointer(&arguments)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.handle == 0 {
		return ErrNotRunning
	}
	l, _ := lib()
	r, _, _ := l.commandNamed.Call(c.handle, uintptr(unsafe.Pointer(&command)), 0)
	runtime.KeepAlive(words)
	runtime.KeepAlive(values)
	runtime.KeepAlive(options)
	runtime.KeepAlive(optionValue)
	if status := int32(r); status < 0 {
		return fmt.Errorf("mpv: loadfile: %s", l.errorText(status))
	}
	return nil
}
