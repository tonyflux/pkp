//go:build (!darwin && !windows) || (darwin && !cgo)

package lanshare

import (
	"fmt"
	"io"
	"runtime"
)

const sleepPreventionSupported = false

func acquireSleepBlock() (io.Closer, error) {
	return nil, fmt.Errorf("idle system sleep prevention is unsupported on %s in this build", runtime.GOOS)
}
