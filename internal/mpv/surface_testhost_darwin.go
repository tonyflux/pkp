//go:build darwin && pkp_surface_test

package mpv

/*
#cgo LDFLAGS: -framework WebKit
#include <stdint.h>
void pkp_darwin_test_prepare(void);
void *pkp_darwin_test_window(int index);
long pkp_darwin_test_window_number(int index);
void pkp_darwin_test_invoke(uintptr_t handle);
void pkp_darwin_test_run(void);
void pkp_darwin_test_stop(void);
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

func darwinSurfaceTestPrepare() { C.pkp_darwin_test_prepare() }
func darwinSurfaceTestRun()     { C.pkp_darwin_test_run() }
func darwinSurfaceTestStop()    { C.pkp_darwin_test_stop() }
func darwinSurfaceTestWindow(index int) unsafe.Pointer {
	return C.pkp_darwin_test_window(C.int(index))
}
func darwinSurfaceTestWindowNumber(index int) int64 {
	return int64(C.pkp_darwin_test_window_number(C.int(index)))
}
func darwinSurfaceTestInvoke(fn func()) <-chan struct{} {
	done := make(chan struct{})
	handle := cgo.NewHandle(func() { defer close(done); fn() })
	C.pkp_darwin_test_invoke(C.uintptr_t(handle))
	return done
}

//export pkpDarwinSurfaceTestCall
func pkpDarwinSurfaceTestCall(id C.uintptr_t) {
	handle := cgo.Handle(id)
	defer handle.Delete()
	handle.Value().(func())()
}
