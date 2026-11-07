//go:build windows || (darwin && cgo)

package lanshare

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type testSleepBlock struct {
	closed atomic.Int32
	fail   atomic.Bool
}

func (b *testSleepBlock) Close() error {
	if b.fail.Load() {
		return errors.New("native release failed")
	}
	b.closed.Add(1)
	return nil
}

func sleepTestRequest(t *testing.T) StartRequest {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return StartRequest{InterfaceID: "lo@127.0.0.1", Port: port}
}

func sleepTestManager(t *testing.T) *Manager {
	t.Helper()
	m := newTestManager(t, nil)
	localSpace(t, m, false)
	if err := m.SetPassword("sleep-test"); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSleepProtectionFollowsListenerAndSurvivesRestartAsPreferenceOnly(t *testing.T) {
	m := sleepTestManager(t)
	var acquired atomic.Int32
	var blocks []*testSleepBlock
	m.acquireSleep = func() (io.Closer, error) {
		acquired.Add(1)
		block := &testSleepBlock{}
		blocks = append(blocks, block)
		return block, nil
	}
	if s := m.Status(); s.PreventSleep || s.PreventSleepActive {
		t.Fatalf("sleep protection must be opt in: %+v", s)
	}
	if err := m.Start(sleepTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	if acquired.Load() != 0 {
		t.Fatal("disabled setting acquired a system blocker")
	}
	if err := m.SetPreventSleep(true); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); !s.Running || !s.PreventSleep || !s.PreventSleepActive {
		t.Fatalf("running preference was not applied: %+v", s)
	}
	if err := m.SetPreventSleep(true); err != nil || acquired.Load() != 1 {
		t.Fatal("repeated enable duplicated the blocker", err)
	}
	if err := m.SetPreventSleep(false); err != nil || blocks[0].closed.Load() != 1 || m.Status().PreventSleepActive || !m.Status().Running {
		t.Fatal("disabling did not release while keeping sharing alive", err)
	}
	if err := m.SetPreventSleep(true); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil || blocks[1].closed.Load() != 1 {
		t.Fatal("stop did not release the blocker", err)
	}
	if s := m.Status(); !s.PreventSleep || s.PreventSleepActive || s.Running {
		t.Fatalf("stop lost preference or retained protection: %+v", s)
	}
	if err := m.Start(sleepTestRequest(t)); err != nil || acquired.Load() != 3 {
		t.Fatal("restart did not reacquire", err)
	}
	config := filepath.Dir(m.dir)
	if err := m.Close(); err != nil || blocks[2].closed.Load() != 1 {
		t.Fatal("close did not release", err)
	}
	reopened, err := New(Options{ConfigDir: config, AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if s := reopened.Status(); !s.PreventSleep || s.PreventSleepActive || s.Running {
		t.Fatalf("restart must restore only the preference: %+v", s)
	}
}

func TestSleepAcquisitionFailureDoesNotStartOrEnableSharingProtection(t *testing.T) {
	m := sleepTestManager(t)
	m.acquireSleep = func() (io.Closer, error) { return nil, errors.New("system rejected request") }
	if err := m.SetPreventSleep(true); err != nil || m.Status().PreventSleepActive {
		t.Fatal("stopped setting must not acquire", err)
	}
	req := sleepTestRequest(t)
	if err := m.Start(req); !errors.Is(err, errSleepPreventionFailed) {
		t.Fatal("expected system acquisition error", err)
	}
	if s := m.Status(); s.Running || s.Address != "" || s.PreventSleepActive {
		t.Fatalf("failed start advertised sharing or protection: %+v", s)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(req.Port)))
	if err != nil {
		t.Fatal("failed start leaked listening socket", err)
	}
	listener.Close()
	if err := m.SetPreventSleep(false); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(sleepTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPreventSleep(true); !errors.Is(err, errSleepPreventionFailed) {
		t.Fatal(err)
	}
	if s := m.Status(); s.PreventSleep || s.PreventSleepActive || !s.Running {
		t.Fatalf("failed toggle changed live preference: %+v", s)
	}
}

func TestSleepPreferenceSaveFailureReleasesNewProtectionAndPreservesExisting(t *testing.T) {
	m := sleepTestManager(t)
	block := &testSleepBlock{}
	m.acquireSleep = func() (io.Closer, error) { return block, nil }
	if err := m.Start(sleepTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	badDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(badDir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	bad, err := os.OpenRoot(badDir)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	m.mu.Lock()
	storage := m.storage
	m.storage = bad
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.storage = storage; m.mu.Unlock() }()
	if err := m.SetPreventSleep(true); !errors.Is(err, errStorageUnavailable) {
		t.Fatal(err)
	}
	if s := m.Status(); s.PreventSleep || s.PreventSleepActive || block.closed.Load() != 1 {
		t.Fatalf("failed save leaked new protection: %+v", s)
	}
	m.mu.Lock()
	m.storage = storage
	m.mu.Unlock()
	block = &testSleepBlock{}
	if err := m.SetPreventSleep(true); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.storage = bad
	m.mu.Unlock()
	if err := m.SetPreventSleep(false); !errors.Is(err, errStorageUnavailable) {
		t.Fatal(err)
	}
	if s := m.Status(); !s.PreventSleep || !s.PreventSleepActive || block.closed.Load() != 0 {
		t.Fatalf("failed save disabled existing protection: %+v", s)
	}
}

func TestSleepReleaseFailureRemainsVisibleAndCanBeRetried(t *testing.T) {
	m := sleepTestManager(t)
	block := &testSleepBlock{}
	m.acquireSleep = func() (io.Closer, error) { return block, nil }
	if err := m.SetPreventSleep(true); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(sleepTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	block.fail.Store(true)
	if err := m.Stop(); !errors.Is(err, errSleepPreventionFailed) {
		t.Fatal(err)
	}
	if s := m.Status(); s.Running || !s.PreventSleepActive || s.Error != "sleep_prevention_failed" {
		t.Fatalf("release failure was hidden: %+v", s)
	}
	block.fail.Store(false)
	if err := m.Stop(); err != nil || m.Status().PreventSleepActive || block.closed.Load() != 1 {
		t.Fatal("retry failed to release", err)
	}
}

func TestUnexpectedListenerFailureReleasesSleepProtection(t *testing.T) {
	m := sleepTestManager(t)
	block := &testSleepBlock{}
	m.acquireSleep = func() (io.Closer, error) { return block, nil }
	if err := m.SetPreventSleep(true); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(sleepTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(m.Status().Address + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	m.mu.Lock()
	listener := m.listener
	m.mu.Unlock()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for m.Status().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s := m.Status(); s.Running || s.PreventSleepActive || block.closed.Load() != 1 {
		t.Fatalf("failed listener retained protection: %+v", s)
	}
}

func TestFailedSleepAcquisitionRetainsInactiveHandleUntilCleanup(t *testing.T) {
	m := sleepTestManager(t)
	block := &testSleepBlock{}
	m.acquireSleep = func() (io.Closer, error) { return block, errors.New("request and cleanup failed") }
	if err := m.SetPreventSleep(true); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(sleepTestRequest(t)); !errors.Is(err, errSleepPreventionFailed) {
		t.Fatal(err)
	}
	if s := m.Status(); s.Running || s.PreventSleepActive {
		t.Fatalf("inactive handle was advertised as protection: %+v", s)
	}
	if err := m.Stop(); err != nil || block.closed.Load() != 1 {
		t.Fatal("failed native request lost cleanup ownership", err)
	}
}

func TestCloseRetriesSleepReleaseAfterFailure(t *testing.T) {
	m := sleepTestManager(t)
	block := &testSleepBlock{}
	m.acquireSleep = func() (io.Closer, error) { return block, nil }
	if err := m.SetPreventSleep(true); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(sleepTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	block.fail.Store(true)
	if err := m.Close(); !errors.Is(err, errSleepPreventionFailed) {
		t.Fatal(err)
	}
	block.fail.Store(false)
	if err := m.Close(); err != nil || block.closed.Load() != 1 {
		t.Fatal("repeat close did not retry native release", err)
	}
}
