//go:build darwin && cgo && !production

package pcm

// Development and test binaries load FFmpeg straight from the build output,
// as internal/mpv does for libmpv; production builds use the bundle's
// Frameworks.

// #cgo LDFLAGS: -Wl,-rpath,${SRCDIR}/../../build/darwin/libmpv/lib
import "C"
