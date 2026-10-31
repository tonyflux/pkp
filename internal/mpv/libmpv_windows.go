//go:build windows

package mpv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// libraryName is the libmpv the installer puts beside pkp.exe, named as
// mpv's own Windows builds name it.
const libraryName = "libmpv-2.dll"

// lib is libmpv, loaded on first use from beside the executable. The Windows
// build has no cgo, so it cross-compiles like the rest of the app and calls
// libmpv's exported C functions at run time; a library that is missing or
// will not load leaves the app running without a player (ErrNoLibrary)
// instead of refusing to start.
var lib = sync.OnceValues(loadLibrary)

// library holds libmpv's entry points and the wakeup callback handed to it.
// The ints they return are C ints: only the low 32 bits of the register are
// defined.
type library struct {
	create, initialize, terminateDestroy, errorString                 *windows.Proc
	setOptionString, setPropertyString, getProperty, freeNodeContents *windows.Proc
	command, commandRet, observeProperty, requestLogMessages          *windows.Proc
	waitEvent, wakeup, setWakeupCallback                              *windows.Proc

	wakeupCallback uintptr
}

func loadLibrary() (*library, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoLibrary, err)
	}
	path := filepath.Join(filepath.Dir(exe), libraryName)
	// What it imports in turn comes from its own directory or System32,
	// never from the working directory or PATH.
	module, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNoLibrary, path, err)
	}
	dll := &windows.DLL{Name: path, Handle: module}
	l := &library{wakeupCallback: windows.NewCallback(wakeupFromMpv)}
	for name, proc := range map[string]**windows.Proc{
		"mpv_create":               &l.create,
		"mpv_initialize":           &l.initialize,
		"mpv_terminate_destroy":    &l.terminateDestroy,
		"mpv_error_string":         &l.errorString,
		"mpv_set_option_string":    &l.setOptionString,
		"mpv_set_property_string":  &l.setPropertyString,
		"mpv_get_property":         &l.getProperty,
		"mpv_free_node_contents":   &l.freeNodeContents,
		"mpv_command":              &l.command,
		"mpv_command_ret":          &l.commandRet,
		"mpv_observe_property":     &l.observeProperty,
		"mpv_request_log_messages": &l.requestLogMessages,
		"mpv_wait_event":           &l.waitEvent,
		"mpv_wakeup":               &l.wakeup,
		"mpv_set_wakeup_callback":  &l.setWakeupCallback,
	} {
		if *proc, err = dll.FindProc(name); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNoLibrary, err)
		}
	}
	return l, nil
}

// cString is s as a NUL-terminated C string. libmpv copies what it is handed
// before returning, so Go memory serves.
func cString(s string) (*byte, error) { return windows.BytePtrFromString(s) }

// cArgv is args as the NULL-terminated array of C strings mpv's commands take.
func cArgv(args []string) ([]*byte, error) {
	argv := make([]*byte, len(args)+1)
	for i, arg := range args {
		p, err := cString(arg)
		if err != nil {
			return nil, err
		}
		argv[i] = p
	}
	return argv, nil
}

// cPointer is an address libmpv returned, into its own memory, which Go's
// collector neither moves nor frees.
func cPointer[T any](address uintptr) *T { return *(**T)(unsafe.Pointer(&address)) }

func (l *library) errorText(status int32) string {
	r, _, _ := l.errorString.Call(uintptr(status))
	return windows.BytePtrToString(cPointer[byte](r))
}

func (l *library) setOption(handle uintptr, name, value string) error {
	return l.setString(l.setOptionString, "option", handle, name, value)
}

func (l *library) setProperty(handle uintptr, name, value string) error {
	return l.setString(l.setPropertyString, "set", handle, name, value)
}

func (l *library) setString(proc *windows.Proc, verb string, handle uintptr, name, value string) error {
	cName, err := cString(name)
	if err != nil {
		return fmt.Errorf("mpv: %s %s: %w", verb, name, err)
	}
	cValue, err := cString(value)
	if err != nil {
		return fmt.Errorf("mpv: %s %s: %w", verb, name, err)
	}
	r, _, _ := proc.Call(handle, uintptr(unsafe.Pointer(cName)), uintptr(unsafe.Pointer(cValue)))
	if status := int32(r); status < 0 {
		return fmt.Errorf("mpv: %s %s=%s: %s", verb, name, value, l.errorText(status))
	}
	return nil
}

// commandNode runs a command whose result mpv hands back as a node, which
// the caller frees with freeNode.
func (l *library) commandNode(handle uintptr, result *node, args ...string) error {
	argv, err := cArgv(args)
	if err != nil {
		return fmt.Errorf("mpv: %s: %w", args[0], err)
	}
	r, _, _ := l.commandRet.Call(handle, uintptr(unsafe.Pointer(&argv[0])), uintptr(unsafe.Pointer(result)))
	if status := int32(r); status < 0 {
		return fmt.Errorf("mpv: %s: %s", args[0], l.errorText(status))
	}
	return nil
}

func (l *library) freeNode(n *node) { l.freeNodeContents.Call(uintptr(unsafe.Pointer(n))) }

// mpv_format values.
const (
	formatNone      = 0
	formatString    = 1
	formatFlag      = 3
	formatInt64     = 4
	formatDouble    = 5
	formatNode      = 6
	formatNodeArray = 7
	formatNodeMap   = 8
	formatByteArray = 9
)

// node is mpv_node: an 8-byte union, then its format. The union is a uint64
// so the struct keeps C's 8-byte alignment and 16-byte size, the stride of
// a node list's values.
type node struct {
	u      uint64
	format int32
}

// Does not compile unless node is as large as mpv_node.
var _ = [1]struct{}{}[unsafe.Sizeof(node{})-16]

// pointer is the union read as its pointer members: string, list, ba.
func (n *node) pointer() unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&n.u)) }

type nodeList struct {
	num    int32
	values *node
	keys   **byte
}

type byteArray struct {
	data unsafe.Pointer
	size uintptr
}

// nodeJSON renders an mpv_node as JSON so callers can decode it into their own
// types, as the macOS backend does: one decoding path for every property.
func nodeJSON(n *node) (json.RawMessage, error) {
	value, err := nodeValue(n)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func nodeValue(n *node) (any, error) {
	if n == nil {
		return nil, nil
	}

	switch n.format {
	case formatNone:
		return nil, nil
	case formatString:
		return windows.BytePtrToString((*byte)(n.pointer())), nil
	case formatFlag:
		// A C int, the union's first four bytes.
		return int32(n.u) != 0, nil
	case formatInt64:
		return int64(n.u), nil
	case formatDouble:
		return math.Float64frombits(n.u), nil

	case formatNodeArray:
		list := (*nodeList)(n.pointer())
		if list == nil {
			return []any{}, nil
		}
		values := unsafe.Slice(list.values, list.num)
		out := make([]any, 0, len(values))
		for i := range values {
			item, err := nodeValue(&values[i])
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil

	case formatNodeMap:
		list := (*nodeList)(n.pointer())
		if list == nil {
			return map[string]any{}, nil
		}
		values := unsafe.Slice(list.values, list.num)
		out := make(map[string]any, int(list.num))
		for i, key := range unsafe.Slice(list.keys, list.num) {
			item, err := nodeValue(&values[i])
			if err != nil {
				return nil, err
			}
			out[windows.BytePtrToString(key)] = item
		}
		return out, nil

	case formatByteArray:
		array := (*byteArray)(n.pointer())
		if array == nil || array.data == nil {
			return []byte{}, nil
		}
		return bytes.Clone(unsafe.Slice((*byte)(array.data), array.size)), nil

	default:
		return nil, fmt.Errorf("mpv: unsupported node format %d", n.format)
	}
}

// mpv_event_id values.
const (
	eventNone            = 0
	eventShutdown        = 1
	eventLogMessage      = 2
	eventEndFile         = 7
	eventFileLoaded      = 8
	eventPlaybackRestart = 21
	eventPropertyChange  = 22
)

type event struct {
	id            int32
	error         int32
	replyUserdata uint64
	data          unsafe.Pointer
}

type propertyChange struct {
	name   *byte
	format int32
	data   unsafe.Pointer
}

type logMessage struct {
	prefix, level, text *byte
	logLevel            int32
}

// endFile is the head of mpv_event_end_file; the playlist fields after it
// are never read.
type endFile struct {
	reason int32
	error  int32
}

func endReason(reason int32) string {
	switch reason {
	case 0:
		return ReasonEOF
	case 2:
		return ReasonStop
	case 3:
		return ReasonQuit
	case 4:
		return ReasonError
	case 5:
		return ReasonRedirect
	default:
		return ""
	}
}

// wakeup tells a core's event loop there is something to read.
//
// mpv_wait_event cannot be asked to block: its timeout is a double, which
// Go's Windows calls do not put in a floating-point register on arm64, so it
// would wait for whatever that register held. Instead mpv calls back, on its
// own threads, when events arrive, and the loop drains the queue without
// waiting: mpv_wakeup right before mpv_wait_event makes that call return at
// once, whatever timeout it reads. That mpv_wakeup calls back as well; self
// drops it, or the loop would wake itself forever. A callback from mpv's
// threads dropped meanwhile is not lost: its event was queued first, and the
// mpv_wait_event that follows returns it.
type wakeup struct {
	id   uintptr
	ch   chan struct{}
	self atomic.Bool
}

// wakeups maps the userdata mpv hands the callback to the loop it wakes; a
// restarted core gets a new entry.
var (
	wakeups  sync.Map
	wakeupID atomic.Uintptr
)

func newWakeup() *wakeup {
	w := &wakeup{id: wakeupID.Add(1), ch: make(chan struct{}, 1)}
	w.ch <- struct{}{} // for anything queued before the loop runs
	wakeups.Store(w.id, w)
	return w
}

func (w *wakeup) release() { wakeups.Delete(w.id) }

// wake rouses the loop without ever blocking.
func (w *wakeup) wake() {
	select {
	case w.ch <- struct{}{}:
	default:
	}
}

// wakeupFromMpv is mpv's wakeup callback. It runs inside mpv, on any of its
// threads, and must return at once.
func wakeupFromMpv(id uintptr) uintptr {
	if v, ok := wakeups.Load(id); ok {
		if w := v.(*wakeup); !w.self.Load() {
			w.wake()
		}
	}
	return 0
}

// nextEvent returns the next queued event, one of eventNone when there is
// none, without waiting (see wakeup). It is libmpv's, valid until the next
// call.
func (l *library) nextEvent(handle uintptr, w *wakeup) *event {
	w.self.Store(true)
	l.wakeup.Call(handle)
	w.self.Store(false)
	r, _, _ := l.waitEvent.Call(handle, 0)
	return cPointer[event](r)
}
