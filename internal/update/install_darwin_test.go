package update

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestUpdateInstallLockChild(t *testing.T) {
	if os.Getenv("PKP_TEST_INSTALL_LOCK") != "1" {
		t.Skip("subprocess only")
	}
	release, err := HoldInstallLock()
	if err != nil {
		fmt.Fprintln(os.Stdout, "blocked")
		os.Exit(19)
	}
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	release()
	os.Exit(0)
}

func TestUpdateInstallLockExcludesReplacementAndNewProfiles(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	bundle := filepath.Join(root, "PKP.app")
	binary := filepath.Join(bundle, "Contents", "MacOS", "pkp")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy test executable: %v, %v", copyErr, closeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestUpdateInstallLockChild$")
	child.Env = append(os.Environ(), "PKP_TEST_INSTALL_LOCK=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); _ = child.Process.Kill() })
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("profile startup: %q, %v", line, err)
	}
	path, err := InstallLockPath(bundle)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		t.Fatal("installer could replace a running profile")
	}
	input.Close()
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("exited profile retained install lock: %v", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	blocked := exec.CommandContext(ctx, binary, "-test.run=^TestUpdateInstallLockChild$")
	blocked.Env = child.Env
	got, err := blocked.Output()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 19 || string(got) != "blocked\n" {
		t.Fatalf("new profile entered an active installation: %q, %v", got, err)
	}
}
