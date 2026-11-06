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
