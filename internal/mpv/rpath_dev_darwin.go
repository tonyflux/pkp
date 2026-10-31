//go:build darwin && !production

package mpv

// Development and test binaries load libmpv straight from the build output.
// Production builds are bundled instead: the Makefile and Taskfile give them
// @executable_path/../Frameworks, a path cgo refuses in #cgo directives.

// #cgo LDFLAGS: -Wl,-rpath,${SRCDIR}/../../build/darwin/libmpv/lib
import "C"
