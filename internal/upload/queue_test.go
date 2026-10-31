package upload

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
)

// fakeBackend never hands out a grant, so a job that reaches CreateUpload
// fails with "no grant"; folders record which parent each was made under.
type fakeBackend struct {
	created chan drive.UploadParams
	mu      sync.Mutex
	folders []string // "parent/name" in creation order
}

func (f *fakeBackend) CreateUpload(ctx context.Context, p drive.UploadParams) (*drive.UploadSlot, error) {
	select {
	case f.created <- p:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &drive.UploadSlot{File: drive.File{ID: "file-" + p.Name}}, nil
}

func (f *fakeBackend) EnsureFolder(_ context.Context, parentID, name string) (*drive.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folders = append(f.folders, parentID+"/"+name)
	return &drive.File{ID: "dir-" + name}, nil
}

func (f *fakeBackend) Trash(context.Context, []string) error { return nil }

// Two files in the same subtree must share one server folder chain, created
// once per level and passed down as the parent of the next.
func TestQueueCreatesFolderTreeOnce(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"a", "b"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	backend := &fakeBackend{created: make(chan drive.UploadParams, 2)}
	events := make(chan Task, 64)
	q := New(backend, 2, func(task Task) { events <- task })
	for _, p := range paths {
		if _, err := q.Add(p, Target{ParentID: "root", Dir: "photos/2024"}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if got := (<-backend.created).ParentID; got != "dir-2024" {
			t.Fatalf("file created under %q, want dir-2024", got)
		}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if strings.Join(backend.folders, ",") != "root/photos,dir-photos/2024" {
		t.Fatalf("folders created = %v", backend.folders)
	}
}

func TestQueueRejectsMissingGrant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{created: make(chan drive.UploadParams, 1)}
	events := make(chan Task, 64)
	q := New(backend, 1, func(task Task) { events <- task })
	task, err := q.Add(path, Target{ParentID: "folder"})
	if err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, events, task.ID, func(tk Task) bool { return !tk.Active() })
	if final.State != StateFailed || final.Done != 0 || final.FileID != "" || !strings.Contains(final.Error, "no resumable upload grant") {
		t.Fatalf("missing grant incorrectly accepted: %+v", final)
	}
	if err := q.Remove(task.ID); err != nil {
		t.Fatal(err)
	}
	if len(q.List()) != 0 || q.Remove(task.ID) == nil {
		t.Fatal("removed task still listed")
	}
}

func TestQueueCancelWhileQueued(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"a", "b"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	// The first create holds the only slot until the test releases it.
	backend := &fakeBackend{created: make(chan drive.UploadParams)}
	events := make(chan Task, 64)
	q := New(backend, 1, func(task Task) { events <- task })
	first, err := q.Add(paths[0], Target{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, first.ID, func(tk Task) bool { return tk.State == StateUploading })
	second, err := q.Add(paths[1], Target{})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Cancel(second.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitFor(t, events, second.ID, func(tk Task) bool { return !tk.Active() })
	if cancelled.State != StateCancelled {
		t.Fatalf("queued job ended as %s", cancelled.State)
	}
	<-backend.created
	waitFor(t, events, first.ID, func(tk Task) bool { return !tk.Active() })
	if err := q.Retry(second.ID); err != nil {
		t.Fatal(err)
	}
	<-backend.created
	retried := waitFor(t, events, second.ID, func(tk Task) bool { return !tk.Active() })
	if retried.State != StateFailed || !strings.Contains(retried.Error, "no resumable upload grant") {
		t.Fatalf("retried job incorrectly completed without a grant: %+v", retried)
	}
}

// gateBackend holds every create until the test lets one through (it then
// fails, freeing the slot), so the test sees how many files are moving at
// once and in which order they started.
type gateBackend struct {
	started chan string
	release chan struct{}
}

func (b *gateBackend) CreateUpload(ctx context.Context, p drive.UploadParams) (*drive.UploadSlot, error) {
	b.started <- p.Name
	select {
	case <-b.release:
		return nil, errors.New("held")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*gateBackend) EnsureFolder(context.Context, string, string) (*drive.File, error) {
	return nil, errors.New("unexpected folder create")
}

func (*gateBackend) Trash(context.Context, []string) error { return nil }

// The concurrent uploads setting: raising it starts waiting files at once, lowering it
// lets moving files finish and holds the rest, and files start in the order
// they were queued whatever the limit.
func TestQueueLimitFollowsSetting(t *testing.T) {
	dir := t.TempDir()
	backend := &gateBackend{started: make(chan string, 8), release: make(chan struct{})}
	q := New(backend, 1, nil)
	defer q.Close()
	for _, name := range []string{"a", "b", "c", "d"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Add(p, Target{}); err != nil {
			t.Fatal(err)
		}
	}
	// expect checks which files start next: exactly these, in whatever order
	// their goroutines reach the server, and nothing beyond them.
	expect := func(names ...string) {
		t.Helper()
		var got []string
		for range names {
			select {
			case name := <-backend.started:
				got = append(got, name)
			case <-time.After(5 * time.Second):
				t.Fatalf("started %v, want %v", got, names)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, names) {
			t.Fatalf("started %v, want %v next in line", got, names)
		}
		select {
		case name := <-backend.started:
			t.Fatalf("%s started over the limit", name)
		case <-time.After(100 * time.Millisecond):
		}
	}

	expect("a")
	q.SetParallel(3)
	expect("b", "c")
	q.SetParallel(1)
	backend.release <- struct{}{}
	backend.release <- struct{}{}
	expect() // two still moving would be over a limit of one
	backend.release <- struct{}{}
	expect("d")
	backend.release <- struct{}{}
}

type ossBackend struct{ endpoint string }

func (b ossBackend) CreateUpload(context.Context, drive.UploadParams) (*drive.UploadSlot, error) {
	return &drive.UploadSlot{File: drive.File{ID: "uploaded"}, OSS: &drive.OSSCredentials{
		Endpoint: b.endpoint, Bucket: "bucket", Key: "object", AccessKeyID: "test", AccessKeySecret: "test",
	}}, nil
}
func (ossBackend) Trash(context.Context, []string) error { return nil }
func (ossBackend) EnsureFolder(context.Context, string, string) (*drive.File, error) {
	return nil, errors.New("unexpected folder create")
}

func TestQueueUploadsBytesThroughOSS(t *testing.T) {
	content := "local file bytes, without a hash pass"
	stored := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/bucket/object" {
			http.Error(w, "unexpected request", 400)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		stored <- string(body)
		w.Header().Set("ETag", `"uploaded"`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	events := make(chan Task, 64)
	q := New(ossBackend{endpoint: server.URL}, 1, func(task Task) { events <- task })
	task, err := q.Add(path, Target{})
	if err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, events, task.ID, func(tk Task) bool { return !tk.Active() })
	if final.State != StateDone || final.FileID != "uploaded" || final.Done != int64(len(content)) {
		t.Fatalf("upload ended as %+v", final)
	}
	select {
	case got := <-stored:
		if got != content {
			t.Fatalf("uploaded bytes = %q, want %q", got, content)
		}
	default:
		t.Fatal("completed without storing the file")
	}
}

func waitFor(t *testing.T, events <-chan Task, id string, ok func(Task) bool) Task {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case tk := <-events:
			if tk.ID == id && ok(tk) {
				return tk
			}
		case <-deadline:
			t.Fatalf("no matching event for %s", id)
		}
	}
}
