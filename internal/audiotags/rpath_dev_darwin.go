//go:build darwin && !production

package audiotags

// Development and test binaries load FFmpeg straight from the build output,
// production builds use the bundle's Frameworks.

// #cgo LDFLAGS: -Wl,-rpath,${SRCDIR}/../../build/darwin/libmpv/lib
import "C"
