//go:build linux

package services

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/tonyflux/pkp/internal/update"
)

func updatePlatform() (string, string) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", ""
	}
	format := os.Getenv("PKP_LINUX_FORMAT")
	switch format {
	case "deb", "rpm", "appimage":
	default:
		format = "appimage"
		if os.Getenv("APPIMAGE") == "" && os.Getenv("PKP_APPIMAGE") != "1" {
			if _, err := os.Stat("/usr/bin/dpkg"); err == nil {
				format = "deb"
			} else if _, err := os.Stat("/usr/bin/rpm"); err == nil {
				format = "rpm"
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/bin/getconf", "GNU_LIBC_VERSION").Output()
	if err != nil {
		return "", ""
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] != "glibc" {
		return "", ""
	}
	return "linux-" + runtime.GOARCH + "-" + format, fields[1]
}

func prepareUpdatePackage(context.Context, string, update.Package, string, string) (*updateQuit, error) {
	return nil, update.ErrInstallLocation
}
