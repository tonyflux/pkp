//go:build darwin

package mpv

/*
#include <stdlib.h>
#include <mpv/client.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

func errorString(status C.int) string {
	return C.GoString(C.mpv_error_string(status))
}

func boolToCInt(v bool) C.int {
	if v {
		return 1
	}
	return 0
}

func setOptionString(handle *C.mpv_handle, name, value string) error {
	cName := C.CString(name)
	cValue := C.CString(value)
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cValue))

	if status := C.mpv_set_option_string(handle, cName, cValue); status < 0 {
		return fmt.Errorf("mpv: option %s=%s: %s", name, value, errorString(status))
	}
	return nil
}

func setPropertyString(handle *C.mpv_handle, name, value string) error {
	cName := C.CString(name)
	cValue := C.CString(value)
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cValue))

	if status := C.mpv_set_property_string(handle, cName, cValue); status < 0 {
		return fmt.Errorf("mpv: set %s=%s: %s", name, value, errorString(status))
	}
	return nil
}

func endReason(reason C.mpv_end_file_reason) string {
	switch reason {
	case C.MPV_END_FILE_REASON_EOF:
		return ReasonEOF
	case C.MPV_END_FILE_REASON_STOP:
		return ReasonStop
	case C.MPV_END_FILE_REASON_QUIT:
		return ReasonQuit
	case C.MPV_END_FILE_REASON_ERROR:
		return ReasonError
	case C.MPV_END_FILE_REASON_REDIRECT:
		return ReasonRedirect
	default:
		return ""
	}
}

// nodeJSON renders an mpv_node as JSON so callers can decode it into their own
// types. Going through JSON keeps every property — scalars, track lists, the
// nested maps mpv returns — on one decoding path.
func nodeJSON(node *C.mpv_node) (json.RawMessage, error) {
	value, err := nodeValue(node)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// nodeValue converts one node. cgo renders mpv_node's anonymous union as an
// opaque byte array, so each branch reinterprets that storage as the member
// the format selects.
func nodeValue(node *C.mpv_node) (any, error) {
	if node == nil {
		return nil, nil
	}

	switch node.format {
	case C.MPV_FORMAT_NONE:
		return nil, nil
	case C.MPV_FORMAT_STRING:
		return C.GoString(*(**C.char)(unsafe.Pointer(&node.u))), nil
	case C.MPV_FORMAT_FLAG:
		return *(*C.int)(unsafe.Pointer(&node.u)) != 0, nil
	case C.MPV_FORMAT_INT64:
		return int64(*(*C.int64_t)(unsafe.Pointer(&node.u))), nil
	case C.MPV_FORMAT_DOUBLE:
		return float64(*(*C.double)(unsafe.Pointer(&node.u))), nil

	case C.MPV_FORMAT_NODE_ARRAY:
		list := *(**C.mpv_node_list)(unsafe.Pointer(&node.u))
		if list == nil {
			return []any{}, nil
		}
		out := make([]any, 0, int(list.num))
		for i := range int(list.num) {
			item, err := nodeValue(nodeAt(list.values, i))
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil

	case C.MPV_FORMAT_NODE_MAP:
		list := *(**C.mpv_node_list)(unsafe.Pointer(&node.u))
		if list == nil {
			return map[string]any{}, nil
		}
		out := make(map[string]any, int(list.num))
		keys := unsafe.Slice(list.keys, int(list.num))
		for i := range int(list.num) {
			item, err := nodeValue(nodeAt(list.values, i))
			if err != nil {
				return nil, err
			}
			out[C.GoString(keys[i])] = item
		}
		return out, nil

	case C.MPV_FORMAT_BYTE_ARRAY:
		array := *(**C.mpv_byte_array)(unsafe.Pointer(&node.u))
		if array == nil || array.data == nil {
			return []byte{}, nil
		}
		return C.GoBytes(array.data, C.int(array.size)), nil

	default:
		return nil, fmt.Errorf("mpv: unsupported node format %d", node.format)
	}
}

func nodeAt(values *C.mpv_node, index int) *C.mpv_node {
	if values == nil {
		return nil
	}
	return (*C.mpv_node)(unsafe.Add(unsafe.Pointer(values), uintptr(index)*unsafe.Sizeof(*values)))
}
