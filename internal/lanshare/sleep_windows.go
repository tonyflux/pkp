//go:build windows

package lanshare

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tonyflux/pkp/internal/i18n"
)

const sleepPreventionSupported = true

const (
	powerRequestContextVersion      = 0
	powerRequestContextSimpleString = 1
	powerRequestSystemRequired      = 1
)

var (
	sleepKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procPowerCreateRequest = sleepKernel32.NewProc("PowerCreateRequest")
	procPowerSetRequest    = sleepKernel32.NewProc("PowerSetRequest")
	procPowerClearRequest  = sleepKernel32.NewProc("PowerClearRequest")
)

// REASON_CONTEXT's union must occupy the full Detailed member, even when only
// SimpleReasonString is used: 24 bytes on amd64/arm64, 16 bytes on 32-bit Windows.
// The two DWORD header fields put the union at offset 8 on each architecture.
type powerReasonContext struct {
	version            uint32
	flags              uint32
	simpleReasonString *uint16
	_                  [2]uint32
	_                  **uint16
}

const powerReasonContextSize = 16 + 2*unsafe.Sizeof(uintptr(0))

var (
	_ [powerReasonContextSize - unsafe.Sizeof(powerReasonContext{})]byte
	_ [unsafe.Sizeof(powerReasonContext{}) - powerReasonContextSize]byte
	_ [8 - unsafe.Offsetof(powerReasonContext{}.simpleReasonString)]byte
	_ [unsafe.Offsetof(powerReasonContext{}.simpleReasonString) - 8]byte
)

type nativeSleepBlock struct {
	mu     sync.Mutex
	handle windows.Handle
	active bool
}

func acquireSleepBlock() (io.Closer, error) {
	for _, proc := range [...]*windows.LazyProc{procPowerCreateRequest, procPowerSetRequest, procPowerClearRequest} {
		if err := proc.Find(); err != nil {
			return nil, fmt.Errorf("%s: %w", proc.Name, err)
		}
	}
	reason, err := windows.UTF16PtrFromString("PKP — " + i18n.T("局域网共享"))
	if err != nil {
		return nil, fmt.Errorf("PowerCreateRequest reason: %w", err)
	}
	context := powerReasonContext{
		version:            powerRequestContextVersion,
		flags:              powerRequestContextSimpleString,
		simpleReasonString: reason,
	}
	handle, _, callErr := procPowerCreateRequest.Call(uintptr(unsafe.Pointer(&context)))
	runtime.KeepAlive(&context)
	runtime.KeepAlive(reason)
	if windows.Handle(handle) == windows.InvalidHandle {
		return nil, sleepRequestError("PowerCreateRequest", callErr)
	}
	block := &nativeSleepBlock{handle: windows.Handle(handle)}
	if result, _, callErr := procPowerSetRequest.Call(handle, powerRequestSystemRequired); result == 0 {
		err := sleepRequestError("PowerSetRequest", callErr)
		if closeErr := block.Close(); closeErr != nil {
			// Preserve the inactive handle for the caller to retry cleanup.
			return block, errors.Join(err, closeErr)
		}
		return nil, err
	}
	block.active = true
	return block, nil
}

func (b *nativeSleepBlock) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handle == 0 {
		return nil
	}
	if b.active {
		if result, _, callErr := procPowerClearRequest.Call(uintptr(b.handle), powerRequestSystemRequired); result == 0 {
			return sleepRequestError("PowerClearRequest", callErr)
		}
		b.active = false
	}
	if err := windows.CloseHandle(b.handle); err != nil {
		return fmt.Errorf("CloseHandle power request: %w", err)
	}
	b.handle = 0
	return nil
}

func sleepRequestError(operation string, err error) error {
	if err == nil || err == windows.ERROR_SUCCESS {
		err = windows.ERROR_GEN_FAILURE
	}
	return fmt.Errorf("%s: %w", operation, err)
}
