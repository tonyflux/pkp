//go:build windows

package services

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// sealLockSecret encrypts data with DPAPI for the Windows user signed in:
// another user, or the file copied to another computer, cannot open it.
func sealLockSecret(data []byte) ([]byte, error) {
	return dpapi(data, true)
}

func unsealLockSecret(data []byte) ([]byte, error) {
	return dpapi(data, false)
}

func dpapi(data []byte, seal bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, windows.ERROR_INVALID_DATA
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if seal {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
