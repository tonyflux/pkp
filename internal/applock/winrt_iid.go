package applock

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
)

// guid has the memory layout of a Windows GUID, so a pointer to one can be
// passed straight to COM as a REFIID. It lives in an untagged file so the
// IID arithmetic below can be tested on any OS.
type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// String formats g the way WinRT type signatures spell GUIDs: lower case,
// braced.
func (g guid) String() string {
	return fmt.Sprintf("{%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x}",
		g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3],
		g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}

// bigEndian serializes g in RFC 4122 network order, which is what the
// pinterface hash feeds to SHA-1 (not the little-endian in-memory layout).
func (g guid) bigEndian() [16]byte {
	var b [16]byte
	binary.BigEndian.PutUint32(b[0:4], g.Data1)
	binary.BigEndian.PutUint16(b[4:6], g.Data2)
	binary.BigEndian.PutUint16(b[6:8], g.Data3)
	copy(b[8:], g.Data4[:])
	return b
}

// pinterfaceNamespace is the fixed namespace WinRT hashes parameterized type
// signatures under (Windows Runtime type system, "Guid generation for
// parameterized types").
var pinterfaceNamespace = guid{0x11f47ad5, 0x7b73, 0x42c0, [8]byte{0xab, 0xae, 0x87, 0x8b, 0x1e, 0x16, 0xad, 0xee}}

// iidIAsyncOperation is the generic IAsyncOperation`1 interface's PIID.
var iidIAsyncOperation = guid{0x9fc2b0bb, 0xe446, 0x44e2, [8]byte{0xaa, 0x61, 0x9c, 0xab, 0x8f, 0x63, 0x6a, 0xf2}}

// parameterizedIID computes the IID of a WinRT parameterized interface
// instance from its type signature. WinRT has no registry of these IIDs: a
// caller must derive them the same way the MIDL compiler does, as an RFC 4122
// version 5 (SHA-1) UUID of the signature.
func parameterizedIID(signature string) guid {
	h := sha1.New()
	ns := pinterfaceNamespace.bigEndian()
	h.Write(ns[:])
	h.Write([]byte(signature))
	sum := h.Sum(nil)

	sum[6] = sum[6]&0x0f | 0x50 // version 5
	sum[8] = sum[8]&0x3f | 0x80 // RFC 4122 variant

	var g guid
	g.Data1 = binary.BigEndian.Uint32(sum[0:4])
	g.Data2 = binary.BigEndian.Uint16(sum[4:6])
	g.Data3 = binary.BigEndian.Uint16(sum[6:8])
	copy(g.Data4[:], sum[8:16])
	return g
}

// asyncOperationIID is the IID of IAsyncOperation<T> for the WinRT type
// signature of T, e.g. "b1" or "enum(Some.Enum;i4)".
func asyncOperationIID(resultSignature string) guid {
	return parameterizedIID("pinterface(" + iidIAsyncOperation.String() + ";" + resultSignature + ")")
}
