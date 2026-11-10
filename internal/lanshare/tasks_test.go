//go:build windows || (darwin && cgo)

package lanshare

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// A valid cloud folder can take longer than three seconds to prove: each
// ancestor is fetched live, and authentication may also need a round trip.
type slowValidationCloud struct {
	*controlledCloud
	resolves atomic.Int32
}

func (c *slowValidationCloud) Resolve(ctx context.Context, account, root, id, identity string) (Source, error) {
	if c.resolves.Add(1) > 1 {
		timer := time.NewTimer(4 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Source{}, ctx.Err()
		case <-timer.C:
		}
	}
	return c.controlledCloud.Resolve(ctx, account, root, id, identity)
}

func TestPreparationCompletesWithSlowValidSourceChecks(t *testing.T) {
	cloud := &slowValidationCloud{controlledCloud: newCloud()}
	cloud.release <- struct{}{}
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	cookie := authenticated(t, m)
	task, err := m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "original"}, cookie)
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		m.mu.Lock()
		actual, original := m.tasks[task.ID].Task, m.tasks[task.ID].Original
		m.mu.Unlock()
		switch actual.State {
		case "failed", "held", "cancelled":
			t.Fatalf("valid source check interrupted preparation: state=%s error=%s", actual.State, actual.Error)
		case "ready":
			body, err := os.ReadFile(original)
			if err != nil || string(body) != "data" || actual.Done != 4 {
				t.Fatalf("original was not completely prepared: done=%d bytes=%q error=%v", actual.Done, body, err)
			}
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("preparation did not finish")
		case <-tick.C:
		}
	}
}

type blockingStatusCloud struct {
	*controlledCloud
	entered chan struct{}
	release chan struct{}
}

func (c *blockingStatusCloud) ValidateRoot(ctx context.Context, account, root, identity string) error {
	if c.entered != nil {
		c.entered <- struct{}{}
		select {
		case <-c.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.controlledCloud.ValidateRoot(ctx, account, root, identity)
}

func TestDesktopProgressWhileSourceValidationIsSlow(t *testing.T) {
	cloud := &blockingStatusCloud{controlledCloud: newCloud()}
	m := newTestManager(t, cloud)
	space, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	cloud.entered, cloud.release = make(chan struct{}, 1), make(chan struct{})
	m.mu.Lock()
	m.tasks["job"] = &taskRecord{Task: Task{ID: "job", SpaceID: space.ID, State: "downloading", Done: 10, Total: 100}}
	m.mu.Unlock()
	statuses := make(chan Status, 1)
	go func() { statuses <- m.Status() }()
	select {
	case <-cloud.entered:
	case <-time.After(time.Second):
		t.Fatal("source validation did not start")
	}
	m.mu.Lock()
	m.tasks["job"].Done = 60
	m.mu.Unlock()
	if tasks := m.Tasks(); len(tasks) != 1 || tasks[0].Done != 60 {
		t.Fatalf("fast task snapshot: %+v", tasks)
	}
	close(cloud.release)
	select {
	case result := <-statuses:
		if len(result.Tasks) != 1 || result.Tasks[0].Done != 60 {
			t.Fatalf("status returned stale progress: %+v", result.Tasks)
		}
	case <-time.After(time.Second):
		t.Fatal("status did not return")
	}
}
