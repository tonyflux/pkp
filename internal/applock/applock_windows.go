//go:build windows

package applock

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tonyflux/pkp/internal/i18n"
)

// Windows builds are CGO_ENABLED=0, so Windows Hello is reached through the
// WinRT ABI by hand: activation factories from combase, and COM vtable calls
// by slot number. Slot numbers count IUnknown (0-2) and IInspectable (3-5)
// first; the interface's own methods follow in the order the Windows SDK
// headers declare them.

var (
	modcombase  = windows.NewLazySystemDLL("combase.dll")
	moduser32   = windows.NewLazySystemDLL("user32.dll")
	modwtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")

	procRoInitialize           = modcombase.NewProc("RoInitialize")
	procRoUninitialize         = modcombase.NewProc("RoUninitialize")
	procRoGetActivationFactory = modcombase.NewProc("RoGetActivationFactory")
	procWindowsCreateString    = modcombase.NewProc("WindowsCreateString")
	procWindowsDeleteString    = modcombase.NewProc("WindowsDeleteString")

	procRegisterClassExW = moduser32.NewProc("RegisterClassExW")
	procCreateWindowExW  = moduser32.NewProc("CreateWindowExW")
	procDefWindowProcW   = moduser32.NewProc("DefWindowProcW")
	procGetMessageW      = moduser32.NewProc("GetMessageW")
	procTranslateMessage = moduser32.NewProc("TranslateMessage")
	procDispatchMessageW = moduser32.NewProc("DispatchMessageW")

	procWTSRegisterSessionNotification = modwtsapi32.NewProc("WTSRegisterSessionNotification")
)

const classUserConsentVerifier = "Windows.Security.Credentials.UI.UserConsentVerifier"

var (
	// IUserConsentVerifierInterop, userconsentverifierinterop.h.
	iidIUserConsentVerifierInterop = guid{0x39e050c3, 0x4e74, 0x441a, [8]byte{0x8d, 0xc0, 0xb8, 0x11, 0x04, 0xdf, 0x94, 0x9c}}
	// IUserConsentVerifierStatics, windows.security.credentials.ui.h.
	iidIUserConsentVerifierStatics = guid{0xaf4f3f91, 0x564c, 0x4ddc, [8]byte{0xb8, 0xb5, 0x97, 0x34, 0x47, 0x62, 0x7c, 0x65}}
	// IAsyncInfo, asyncinfo.h.
	iidIAsyncInfo = guid{0x00000036, 0x0000, 0x0000, [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	// IAsyncOperation<UserConsentVerificationResult>, which the interop call
	// must be told to return.
	iidVerificationOperation = asyncOperationIID("enum(Windows.Security.Credentials.UI.UserConsentVerificationResult;i4)")
)

// Vtable slots.
const (
	slotQueryInterface = 0
	slotRelease        = 2

	// IUserConsentVerifierInterop
	slotRequestVerificationForWindowAsync = 6
	// IUserConsentVerifierStatics
	slotCheckAvailabilityAsync = 6
	// IAsyncInfo
	slotGetStatus    = 7
	slotGetErrorCode = 8
	// IAsyncOperation`1
	slotGetResults = 8
)

// AsyncStatus
const (
	asyncStarted   = 0
	asyncCompleted = 1
	asyncCanceled  = 2
	asyncError     = 3
)

const (
	roInitMultithreaded = 1
	rpcEChangedMode     = -2147417850 // 0x80010106: thread already in an STA
	eFail               = -2147467259 // 0x80004005
	eInvalidArg         = -2147024809 // 0x80070057
)

// UserConsentVerificationResult and UserConsentVerifierAvailability share
// their first values (Verified/Available is 0).
const (
	consentDeviceNotPresent     = 1
	consentNotConfiguredForUser = 2
	consentDisabledByPolicy     = 3
	consentDeviceBusy           = 4
	consentRetriesExhausted     = 5
	consentCanceled             = 6
)

// pollInterval is how often a pending WinRT operation is checked. Polling
// IAsyncInfo spares implementing a COM completion handler in pure Go; the
// operations awaited here are a human answering a dialog, so 50ms is
// imperceptible.
const pollInterval = 50 * time.Millisecond

// comObject is a COM interface pointer: its first word points at the vtable.
// Keeping it typed (rather than as a uintptr) avoids converting integers back
// into pointers, which the Go toolchain cannot check.
type comObject struct {
	vtbl *[16]uintptr
}

// call invokes vtable slot with o as the this pointer and returns the
// HRESULT. uintptrescapes keeps Go memory passed in args (out parameters,
// IIDs) alive and unmoved for the duration of the call, as syscall.SyscallN
// itself does.
//
//go:uintptrescapes
func (o *comObject) call(slot int, args ...uintptr) int32 {
	all := make([]uintptr, 0, 1+len(args))
	all = append(all, uintptr(unsafe.Pointer(o)))
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[slot], all...)
	return int32(uint32(r))
}

func (o *comObject) release() {
	if o != nil {
		o.call(slotRelease)
	}
}

func (o *comObject) queryInterface(iid *guid) (*comObject, error) {
	var out *comObject
	if hr := o.call(slotQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); hr < 0 {
		return nil, hresult(hr)
	}
	return out, nil
}

// hresult is a failed COM call.
type hresult int32

func (h hresult) Error() string { return fmt.Sprintf("HRESULT 0x%08X", uint32(h)) }

// errAsyncCanceled reports a WinRT operation that ended as Canceled rather
// than producing a result.
var errAsyncCanceled = errors.New("operation canceled")

// hstring is an opaque WinRT string handle owned by combase.
type hstring uintptr

func newHString(s string) (hstring, error) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, hresult(eInvalidArg)
	}
	var h hstring
	r, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h)))
	if hr := int32(uint32(r)); hr < 0 {
		return 0, hresult(hr)
	}
	return h, nil
}

func (h hstring) delete() {
	if h != 0 {
		procWindowsDeleteString.Call(uintptr(h))
	}
}

// activationFactory returns UserConsentVerifier's activation factory as the
// interface iid; the factory implements both the statics and the interop
// interface.
func activationFactory(iid *guid) (*comObject, error) {
	class, err := newHString(classUserConsentVerifier)
	if err != nil {
		return nil, err
	}
	defer class.delete()
	var factory *comObject
	r, _, _ := procRoGetActivationFactory.Call(uintptr(class), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&factory)))
	if hr := int32(uint32(r)); hr < 0 {
		return nil, hresult(hr)
	}
	return factory, nil
}

// awaitInt32 blocks until op, an IAsyncOperation whose result is a 32-bit
// enum, finishes, and returns its result.
func awaitInt32(op *comObject) (int32, error) {
	info, err := op.queryInterface(&iidIAsyncInfo)
	if err != nil {
		return 0, err
	}
	defer info.release()
	for {
		var status int32
		if hr := info.call(slotGetStatus, uintptr(unsafe.Pointer(&status))); hr < 0 {
			return 0, hresult(hr)
		}
		switch status {
		case asyncStarted:
			time.Sleep(pollInterval)
			continue
		case asyncCompleted:
			var result int32
			if hr := op.call(slotGetResults, uintptr(unsafe.Pointer(&result))); hr < 0 {
				return 0, hresult(hr)
			}
			return result, nil
		case asyncCanceled:
			return 0, errAsyncCanceled
		default: // asyncError
			var code int32
			if hr := info.call(slotGetErrorCode, uintptr(unsafe.Pointer(&code))); hr < 0 {
				return 0, hresult(hr)
			}
			if code >= 0 {
				code = eFail
			}
			return 0, hresult(code)
		}
	}
}

// onWinRTThread runs fn on an OS thread of its own with the Windows Runtime
// initialized for the multithreaded apartment. A fresh thread keeps the
// apartment choice away from Wails' UI thread, which is single-threaded and
// must keep pumping messages while the Windows Hello dialog is up.
func onWinRTThread(fn func()) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		r, _, _ := procRoInitialize.Call(roInitMultithreaded)
		hr := int32(uint32(r))
		switch {
		case hr >= 0:
			defer procRoUninitialize.Call()
		case hr == rpcEChangedMode:
			// Already initialized as an STA by someone else; the runtime
			// is usable, and the apartment is not ours to tear down.
		default:
			done <- hresult(hr)
			return
		}
		fn()
		done <- nil
	}()
	return <-done
}

func unavailableHRESULT(err error) Outcome {
	return Outcome{Result: Unavailable, Message: unavailableHRESULTMessage(err)}
}

func unavailableHRESULTMessage(err error) string {
	return i18n.F("无法使用 Windows Hello（%v）", err)
}

// consentUnavailableMessage explains the reasons UserConsentVerifier gives
// for not being able to ask; ok is false for any other value.
func consentUnavailableMessage(code int32) (msg string, ok bool) {
	switch code {
	case consentDeviceNotPresent:
		return i18n.T("这台电脑没有可用的 Windows Hello 设备"), true
	case consentNotConfiguredForUser:
		return i18n.T("这台电脑没有设置 Windows Hello（PIN、指纹或面部）"), true
	case consentDisabledByPolicy:
		return i18n.T("Windows Hello 已被系统策略停用"), true
	}
	return "", false
}

// Available reports whether Windows Hello can verify the computer's owner
// now, and if not, why. A busy device counts as available: it can ask once
// it is free.
func Available() (bool, string) {
	var (
		ok  bool
		msg string
	)
	if err := onWinRTThread(func() { ok, msg = available() }); err != nil {
		return false, unavailableHRESULTMessage(err)
	}
	return ok, msg
}

func available() (bool, string) {
	statics, err := activationFactory(&iidIUserConsentVerifierStatics)
	if err != nil {
		return false, unavailableHRESULTMessage(err)
	}
	defer statics.release()
	var op *comObject
	if hr := statics.call(slotCheckAvailabilityAsync, uintptr(unsafe.Pointer(&op))); hr < 0 {
		return false, unavailableHRESULTMessage(hresult(hr))
	}
	defer op.release()
	availability, err := awaitInt32(op)
	if err != nil {
		return false, unavailableHRESULTMessage(err)
	}
	switch availability {
	case 0, consentDeviceBusy:
		return true, ""
	}
	if msg, ok := consentUnavailableMessage(availability); ok {
		return false, msg
	}
	return false, i18n.F("Windows Hello 不可用（状态 %d）", availability)
}

// Authenticate shows the Windows Hello dialog owned by window (an HWND; the
// foreground window if nil) with reason as its message, and blocks until it
// ends.
func Authenticate(window unsafe.Pointer, reason string) Outcome {
	hwnd := uintptr(window)
	if hwnd == 0 {
		// The interop call refuses a null owner.
		hwnd = uintptr(windows.GetForegroundWindow())
	}
	var out Outcome
	if err := onWinRTThread(func() { out = authenticate(hwnd, reason) }); err != nil {
		return unavailableHRESULT(err)
	}
	return out
}

func authenticate(hwnd uintptr, reason string) Outcome {
	interop, err := activationFactory(&iidIUserConsentVerifierInterop)
	if err != nil {
		return unavailableHRESULT(err)
	}
	defer interop.release()
	message, err := newHString(reason)
	if err != nil {
		return unavailableHRESULT(err)
	}
	defer message.delete()
	var op *comObject
	hr := interop.call(slotRequestVerificationForWindowAsync,
		hwnd, uintptr(message),
		uintptr(unsafe.Pointer(&iidVerificationOperation)), uintptr(unsafe.Pointer(&op)))
	if hr < 0 {
		return unavailableHRESULT(hresult(hr))
	}
	defer op.release()
	result, err := awaitInt32(op)
	if errors.Is(err, errAsyncCanceled) {
		return Outcome{Result: Cancelled, Message: i18n.T("已取消验证")}
	}
	if err != nil {
		return unavailableHRESULT(err)
	}
	switch result {
	case 0:
		return Outcome{Result: Verified}
	case consentDeviceBusy:
		return Outcome{Result: Failed, Message: i18n.T("Windows Hello 正忙，请稍后再试")}
	case consentRetriesExhausted:
		return Outcome{Result: Failed, Message: i18n.T("验证失败次数过多")}
	case consentCanceled:
		return Outcome{Result: Cancelled, Message: i18n.T("已取消验证")}
	}
	if msg, ok := consentUnavailableMessage(result); ok {
		return Outcome{Result: Unavailable, Message: msg}
	}
	return Outcome{Result: Failed, Message: i18n.F("Windows Hello 返回了未知结果（%d）", result)}
}

const (
	wmWTSSessionChange   = 0x02B1
	wtsConsoleDisconnect = 0x2
	wtsRemoteDisconnect  = 0x4
	wtsSessionLock       = 0x7
	notifyForThisSession = 0
	sessionWatcherClass  = "PKPAppLockSessionWatcher"
)

// wndClassEx is WNDCLASSEXW.
type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

// winMsg is MSG.
type winMsg struct {
	hwnd    windows.HWND
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

var (
	screenLockFn   atomic.Pointer[func()]
	screenLockOnce sync.Once
)

// WatchScreenLock calls fn on a goroutine of its own every time Windows locks
// the screen or the user's session is disconnected (switched to another
// user, or a remote session dropped). Only the first call installs the
// watcher; later calls just replace fn.
func WatchScreenLock(fn func()) {
	screenLockFn.Store(&fn)
	screenLockOnce.Do(func() { go watchSessions() })
}

// watchSessions owns a hidden window registered for session notifications
// and pumps its messages for the life of the process. The window is a real
// top-level one, not message-only, as session notifications are documented
// for top-level windows. If any step fails the watcher just never fires;
// the app lock still works, it only misses the automatic lock.
func watchSessions() {
	// Window messages are delivered to the creating thread; never unlock.
	runtime.LockOSThread()

	var instance windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &instance); err != nil {
		return
	}
	className, err := windows.UTF16PtrFromString(sessionWatcherClass)
	if err != nil {
		return
	}
	wc := wndClassEx{
		wndProc:   windows.NewCallback(sessionWndProc),
		instance:  instance,
		className: className,
	}
	wc.size = uint32(unsafe.Sizeof(wc))
	if atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return
	}
	hwnd, _, _ := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, // WS_OVERLAPPED without WS_VISIBLE: never shown
		0, 0, uintptr(instance), 0)
	if hwnd == 0 {
		return
	}
	if ok, _, _ := procWTSRegisterSessionNotification.Call(hwnd, notifyForThisSession); ok == 0 {
		return
	}
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(uint32(r)) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func sessionWndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	if message == wmWTSSessionChange {
		switch wParam {
		case wtsSessionLock, wtsConsoleDisconnect, wtsRemoteDisconnect:
			if fn := screenLockFn.Load(); fn != nil {
				go (*fn)()
			}
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}
