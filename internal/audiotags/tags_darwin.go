//go:build darwin

package audiotags

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/darwin/libmpv/include
#cgo LDFLAGS: -L${SRCDIR}/../../build/darwin/libmpv/lib -lavformat -lavcodec -lavutil
#include <stdlib.h>
#include "tags_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// Read opens url and returns what its header says. Cancelling ctx makes a
// pending read return ctx's error.
func Read(ctx context.Context, url string) (Tags, error) {
	if err := ctx.Err(); err != nil {
		return Tags{}, err
	}
	t := C.pkp_tags_new()
	if t == nil {
		return Tags{}, errors.New("audiotags: out of memory")
	}
	// The interrupt may only reach t while it exists.
	var mu sync.Mutex
	alive := true
	defer func() {
		mu.Lock()
		alive = false
		C.pkp_tags_close(t)
		mu.Unlock()
	}()
	stop := context.AfterFunc(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		if alive {
			C.pkp_tags_interrupt(t)
		}
	})
	defer stop()

	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	if ret := C.pkp_tags_open(t, cURL); ret < 0 {
		if err := ctx.Err(); err != nil {
			return Tags{}, err
		}
		var buf [128]C.char
		C.pkp_tags_strerror(ret, &buf[0], C.int(len(buf)))
		return Tags{}, fmt.Errorf("audiotags: open: %s", C.GoString(&buf[0]))
	}

	get := func(keys ...string) string {
		for _, key := range keys {
			cKey := C.CString(key)
			v := C.pkp_tags_get(t, cKey)
			C.free(unsafe.Pointer(cKey))
			if v != nil {
				if s := clean(C.GoString(v)); s != "" {
					return s
				}
			}
		}
		return ""
	}
	tags := Tags{
		Title:  get("title"),
		Artist: get("artist", "album_artist", "performer"),
		Album:  get("album"),
		Date:   get("date", "year", "originaldate"),
	}
	var data *C.uint8_t
	var size C.int
	if kind := C.pkp_tags_cover(t, &data, &size); kind > 0 && int(size) <= MaxCover {
		tags.Cover = C.GoBytes(unsafe.Pointer(data), size)
		tags.CoverType = coverTypes[int(kind)]
	}
	return tags, nil
}

var coverTypes = map[int]string{1: "image/jpeg", 2: "image/png", 3: "image/webp", 4: "image/gif", 5: "image/bmp"}
