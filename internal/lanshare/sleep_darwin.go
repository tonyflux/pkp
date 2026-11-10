//go:build darwin && cgo

package lanshare

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <stdlib.h>

static IOReturn pkp_acquire_sleep_block(const char *reason, IOPMAssertionID *assertion) {
    CFStringRef name = CFStringCreateWithCString(kCFAllocatorDefault, reason, kCFStringEncodingUTF8);
    if (name == NULL) {
        return kIOReturnNoMemory;
    }
    IOReturn result = IOPMAssertionCreateWithName(
        kIOPMAssertionTypePreventUserIdleSystemSleep, kIOPMAssertionLevelOn, name, assertion);
    CFRelease(name);
    return result;
}
*/
import "C"

import (
	"fmt"
	"io"
	"sync"
	"unsafe"

	"github.com/tonyflux/pkp/internal/i18n"
)

const sleepPreventionSupported = true

type nativeSleepBlock struct {
	mu        sync.Mutex
	assertion C.IOPMAssertionID
	closed    bool
}

func acquireSleepBlock() (io.Closer, error) {
	reason := C.CString("PKP — " + i18n.T("局域网共享"))
	defer C.free(unsafe.Pointer(reason))
	var assertion C.IOPMAssertionID
	if result := C.pkp_acquire_sleep_block(reason, &assertion); result != C.kIOReturnSuccess {
		return nil, fmt.Errorf("IOPMAssertionCreateWithName: IOReturn 0x%08x", uint32(result))
	}
	return &nativeSleepBlock{assertion: assertion}, nil
}

func (b *nativeSleepBlock) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	if result := C.IOPMAssertionRelease(b.assertion); result != C.kIOReturnSuccess {
		return fmt.Errorf("IOPMAssertionRelease: IOReturn 0x%08x", uint32(result))
	}
	b.closed = true
	return nil
}
