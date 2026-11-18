//go:build darwin || linux

package mpv

/*
#include <mpv/client.h>
#include <stdlib.h>

// Named arguments avoid loadfile's positional index added in mpv 0.38.
static int pkp_load_file(mpv_handle *handle, char *url, char *start) {
    char *option_keys[] = {"start"};
    mpv_node option_values[] = {{.format = MPV_FORMAT_STRING, .u.string = start}};
    mpv_node_list options = {.num = 1, .keys = option_keys, .values = option_values};
    char *keys[] = {"name", "url", "flags", "options"};
    mpv_node values[] = {
        {.format = MPV_FORMAT_STRING, .u.string = "loadfile"},
        {.format = MPV_FORMAT_STRING, .u.string = url},
        {.format = MPV_FORMAT_STRING, .u.string = "replace"},
        {.format = MPV_FORMAT_NODE_MAP, .u.list = &options}
    };
    mpv_node_list arguments = {.num = 4, .keys = keys, .values = values};
    mpv_node command = {.format = MPV_FORMAT_NODE_MAP, .u.list = &arguments};
    return mpv_command_node(handle, &command, NULL);
}
*/
import "C"

import (
	"context"
	"fmt"
	"strconv"
	"unsafe"
)

// LoadFile replaces playback at start seconds using named libmpv arguments.
func (c *Client) LoadFile(_ context.Context, url string, start float64) error {
	cURL := C.CString(url)
	cStart := C.CString(strconv.FormatFloat(start, 'f', 3, 64))
	defer C.free(unsafe.Pointer(cURL))
	defer C.free(unsafe.Pointer(cStart))
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.handle == nil {
		return ErrNotRunning
	}
	if status := C.pkp_load_file(c.handle, cURL, cStart); status < 0 {
		return fmt.Errorf("mpv: loadfile: %s", errorString(status))
	}
	return nil
}
