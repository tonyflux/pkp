//go:build !windows

package lanmedia

import "os/exec"

func configureCommand(cmd *exec.Cmd) {}
