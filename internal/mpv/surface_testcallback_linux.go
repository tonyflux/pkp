//go:build linux && pkp_surface_test

package mpv

/*
#include <stdint.h>
extern void pkp_surface_test_wait(void);
*/
import "C"

import "runtime/cgo"

//export pkpSurfaceTestClose
func pkpSurfaceTestClose(id C.uintptr_t) {
	handle := cgo.Handle(id)
	test := handle.Value().(*surfaceCloseTest)
	close(test.entered)
	C.pkp_surface_test_wait()
	test.closed <- test.client.Close()
	handle.Delete()
}
