package upload

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
)

// fakeOSS is a local stand-in for OSS multipart uploads, path-style under
// /bucket/. A part can be held until its request is abandoned — what a quit
// does to parts in flight — or refused with an OSS error code.
type fakeOSS struct {
	mu       sync.Mutex
	nextID   int
	parts    map[string]map[int][]byte // by upload id
	objects  map[string][]byte         // completed, by key
	received map[string]int            // "key#part" → requests that stored it
	aborted  []string                  // keys
	hold     func(key string, part int) bool
	refuse   func(key string, part int) string // OSS error code, "" to accept
	stored   chan string                       // "key#part", as parts land
	quit     chan struct{}                     // releases held parts when the test ends
}

func newFakeOSS(t *testing.T) (*fakeOSS, string) {
	f := &fakeOSS{
		parts:    map[string]map[int][]byte{},
		objects:  map[string][]byte{},
		received: map[string]int{},
		stored:   make(chan string, 64),
		quit:     make(chan struct{}),
	}
	server := httptest.NewServer(f)
	// Cleanups run last-first: held parts are let go before the server
	// waits for its handlers.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(f.quit) })
	return f, server.URL
}

func (f *fakeOSS) set(hold func(string, int) bool, refuse func(string, int) string) {
	f.mu.Lock()
	f.hold, f.refuse = hold, refuse
	f.mu.Unlock()
}

func (f *fakeOSS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	q := r.URL.Query()
	id := q.Get("uploadId")
	xmlOK := func(body string) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, body)
	}
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.mu.Lock()
		f.nextID++
		id = "up-" + strconv.Itoa(f.nextID)
		f.parts[id] = map[int][]byte{}
		f.mu.Unlock()
		xmlOK(fmt.Sprintf("<InitiateMultipartUploadResult><Bucket>bucket</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>", key, id))
	case r.Method == http.MethodPut && q.Has("partNumber"):
		n, _ := strconv.Atoi(q.Get("partNumber"))
		f.mu.Lock()
		hold, refuse := f.hold, f.refuse
		f.mu.Unlock()
		if refuse != nil {
			if code := refuse(key, n); code != "" {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, "<Error><Code>"+code+"</Code><Message>refused</Message><RequestId>r</RequestId></Error>")
				return
			}
		}
		if hold != nil && hold(key, n) {
			select {
			case <-r.Context().Done():
			case <-f.quit:
			}
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.parts[id][n] = body
		f.received[fmt.Sprintf("%s#%d", key, n)]++
		f.mu.Unlock()
		w.Header().Set("ETag", fmt.Sprintf(`"%s-%d"`, key, n))
		f.stored <- fmt.Sprintf("%s#%d", key, n)
	case r.Method == http.MethodGet && id != "":
		f.mu.Lock()
		var nums []int
		for n := range f.parts[id] {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		var b strings.Builder
		for _, n := range nums {
			fmt.Fprintf(&b, `<Part><PartNumber>%d</PartNumber><LastModified>2026-10-12T00:00:00.000Z</LastModified><ETag>"%s-%d"</ETag><Size>%d</Size></Part>`, n, key, n, len(f.parts[id][n]))
		}
		f.mu.Unlock()
		xmlOK(fmt.Sprintf("<ListPartsResult><Bucket>bucket</Bucket><Key>%s</Key><UploadId>%s</UploadId><MaxParts>1000</MaxParts><IsTruncated>false</IsTruncated>%s</ListPartsResult>", key, id, b.String()))
	case r.Method == http.MethodPost && id != "":
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		var nums []int
		for n := range f.parts[id] {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		var whole []byte
		for _, n := range nums {
			whole = append(whole, f.parts[id][n]...)
		}
		f.objects[key] = whole
		delete(f.parts, id)
		f.mu.Unlock()
		xmlOK(fmt.Sprintf(`<CompleteMultipartUploadResult><Bucket>bucket</Bucket><Key>%s</Key><ETag>"whole"</ETag></CompleteMultipartUploadResult>`, key))
	case r.Method == http.MethodDelete && id != "":
		f.mu.Lock()
		delete(f.parts, id)
		f.aborted = append(f.aborted, key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusBadRequest)
	}
}

// waitStored blocks until OSS has kept the given "key#part".
func (f *fakeOSS) waitStored(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got := <-f.stored:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("OSS never stored %s", want)
		}
	}
}

// slotBackend hands out a new placeholder and grant per create, every grant
// for its own object on the same OSS stand-in, and records what it trashes.
type slotBackend struct {
	endpoint string
	lifetime time.Duration
	mu       sync.Mutex
	created  int
	trashed  []string
}

func (b *slotBackend) CreateUpload(_ context.Context, p drive.UploadParams) (*drive.UploadSlot, error) {
	b.mu.Lock()
	b.created++
	n := b.created
	b.mu.Unlock()
	return &drive.UploadSlot{File: drive.File{ID: "p-" + strconv.Itoa(n)}, OSS: &drive.OSSCredentials{
		Endpoint: b.endpoint, Bucket: "bucket", Key: "obj-" + strconv.Itoa(n),
		AccessKeyID: "k", AccessKeySecret: "s", Expiration: time.Now().Add(b.lifetime),
	}}, nil
}

func (b *slotBackend) EnsureFolder(context.Context, string, string) (*drive.File, error) {
	return nil, errors.New("unexpected folder create")
}

func (b *slotBackend) Trash(_ context.Context, ids []string) error {
	b.mu.Lock()
	b.trashed = append(b.trashed, ids...)
	b.mu.Unlock()
	return nil
}

func (b *slotBackend) state() (int, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.created, slices.Clone(b.trashed)
}

// threeParts writes a file that goes up as two full parts and a short one.
func threeParts(t *testing.T) (string, []byte) {
	t.Helper()
	content := make([]byte, 2*partSize+1000)
	_, _ = rand.Read(content)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content
}

func openQueue(t *testing.T, backend Backend, journal string, events chan Task) *Queue {
	q := New(backend, 1, func(tk Task) { events <- tk })
	q.Open(journal, quietLog)
	q.SetResumable(true)
	// Before the stand-in's cleanups, so held parts are cancelled by the
	// queue rather than left to the server.
	t.Cleanup(q.Close)
	return q
}

func listed(q *Queue, id string) Task {
	for _, tk := range q.List() {
		if tk.ID == id {
			return tk
		}
	}
	return Task{}
}

// interruptAfterFirstPart quits the app, as far as the queue can tell, with
// the first part of obj-1 on OSS and the other two in flight.
func interruptAfterFirstPart(t *testing.T, oss *fakeOSS, q *Queue, events chan Task, path string) Task {
	t.Helper()
	oss.set(func(key string, part int) bool { return key == "obj-1" && part > 1 }, nil)
	task, err := q.Add(path, Target{})
	if err != nil {
		t.Fatal(err)
	}
	oss.waitStored(t, "obj-1#1")
	deadline := time.Now().Add(5 * time.Second)
	for listed(q, task.ID).Done != partSize {
		if time.Now().After(deadline) {
			t.Fatalf("the first part never counted: %+v", listed(q, task.ID))
		}
		time.Sleep(5 * time.Millisecond)
	}
	q.Close()
	oss.set(nil, nil)
	return task
}

// What resumable transfer is for: the app quits mid-upload, and the next session
// sends only what had not arrived, into the same placeholder.
func TestResumeContinuesAfterRestart(t *testing.T) {
	oss, endpoint := newFakeOSS(t)
	backend := &slotBackend{endpoint: endpoint, lifetime: 12 * time.Hour}
	journal := filepath.Join(t.TempDir(), "uploads.json")
	path, content := threeParts(t)
	before := make(chan Task, 256)

	task := interruptAfterFirstPart(t, oss, openQueue(t, backend, journal, before), before, path)

	// A new session, as after relaunching: its own events.
	events := make(chan Task, 256)
	q := openQueue(t, backend, journal, events)
	stopped := listed(q, task.ID)
	if stopped.State != StateInterrupted || !stopped.Resumable || stopped.Done != partSize {
		t.Fatalf("after the restart the upload reads %+v; want interrupted, resumable, one part kept", stopped)
	}
	if err := q.Retry(task.ID); err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, events, task.ID, func(tk Task) bool { return !tk.Active() })
	if final.State != StateDone || final.FileID != "p-1" || final.Resumable {
		t.Fatalf("resumed upload ended as %+v", final)
	}
	created, trashed := backend.state()
	if created != 1 || len(trashed) != 0 {
		t.Errorf("resuming made %d slots and trashed %v; want the first slot, kept", created, trashed)
	}
	oss.mu.Lock()
	defer oss.mu.Unlock()
	if n := oss.received["obj-1#1"]; n != 1 {
		t.Errorf("the part already on OSS was sent %d times", n)
	}
	if !bytes.Equal(oss.objects["obj-1"], content) {
		t.Errorf("the object is %d bytes and not the file", len(oss.objects["obj-1"]))
	}
	if _, err := os.Stat(filepath.Join(q.checkpoints, task.ID)); !os.IsNotExist(err) {
		t.Errorf("a finished upload left its checkpoint behind: %v", err)
	}
}

// A kept grant near its end is not worth continuing under: the upload gets
// a new placeholder and starts over, and the old one goes to the trash.
func TestResumeStartsOverWhenGrantRunsOut(t *testing.T) {
	oss, endpoint := newFakeOSS(t)
	backend := &slotBackend{endpoint: endpoint, lifetime: grantMargin / 2}
	journal := filepath.Join(t.TempDir(), "uploads.json")
	path, content := threeParts(t)
	before := make(chan Task, 256)

	task := interruptAfterFirstPart(t, oss, openQueue(t, backend, journal, before), before, path)

	events := make(chan Task, 256)
	q := openQueue(t, backend, journal, events)
	if stopped := listed(q, task.ID); stopped.Resumable {
		t.Fatalf("an upload whose grant is running out reads as resumable: %+v", stopped)
	}
	if err := q.Retry(task.ID); err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, events, task.ID, func(tk Task) bool { return !tk.Active() })
	created, trashed := backend.state()
	if final.State != StateDone || final.FileID != "p-2" || created != 2 || !slices.Equal(trashed, []string{"p-1"}) {
		t.Fatalf("ended %+v with %d slots, trashed %v; want done in p-2 with p-1 trashed", final, created, trashed)
	}
	oss.mu.Lock()
	defer oss.mu.Unlock()
	if !bytes.Equal(oss.objects["obj-2"], content) || oss.received["obj-2#1"] != 1 {
		t.Errorf("the new object is not the whole file sent from the start")
	}
}

// OSS refusing an expired token mid-transfer gets the upload a new grant and
// a fresh start, once: a file that cannot finish within one grant must not
// loop forever.
func TestExpiredGrantMidTransferStartsOverOnce(t *testing.T) {
	oss, endpoint := newFakeOSS(t)
	backend := &slotBackend{endpoint: endpoint, lifetime: 12 * time.Hour}
	journal := filepath.Join(t.TempDir(), "uploads.json")
	path, content := threeParts(t)
	events := make(chan Task, 256)
	q := openQueue(t, backend, journal, events)

	oss.set(nil, func(key string, part int) string {
		if key == "obj-1" && part > 1 {
			return "SecurityTokenExpired"
		}
		return ""
	})
	task, err := q.Add(path, Target{})
	if err != nil {
		t.Fatal(err)
	}
	final := waitFor(t, events, task.ID, func(tk Task) bool { return !tk.Active() })
	created, trashed := backend.state()
	if final.State != StateDone || final.FileID != "p-2" || created != 2 || !slices.Equal(trashed, []string{"p-1"}) {
		t.Fatalf("ended %+v with %d slots, trashed %v; want done in p-2 with p-1 trashed", final, created, trashed)
	}
	oss.mu.Lock()
	whole := bytes.Equal(oss.objects["obj-2"], content)
	oss.mu.Unlock()
	if !whole {
		t.Fatal("the renewed upload did not store the whole file")
	}

	oss.set(nil, func(_ string, part int) string {
		if part > 1 {
			return "SecurityTokenExpired"
		}
		return ""
	})
	again, err := q.Add(path, Target{})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitFor(t, events, again.ID, func(tk Task) bool { return !tk.Active() })
	if after, _ := backend.state(); failed.State != StateFailed || after != created+2 {
		t.Fatalf("a grant expiring every time ended %s after %d new slots; want failed after 2", failed.State, after-created)
	}
}

// What each way of stopping leaves behind.
func TestStopsKeepOrDiscardTheSlot(t *testing.T) {
	refusePart2 := func(key string, part int) string {
		if part > 1 {
			return "AccessDenied"
		}
		return ""
	}
	cases := []struct {
		name      string
		resumable bool
		cancel    bool
		state     string
		kept      bool
	}{
		{name: "failure without 断点续传", resumable: false, state: StateFailed, kept: false},
		{name: "cancel with 断点续传", resumable: true, cancel: true, state: StateCancelled, kept: false},
		{name: "failure with 断点续传", resumable: true, state: StateFailed, kept: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oss, endpoint := newFakeOSS(t)
			backend := &slotBackend{endpoint: endpoint, lifetime: 12 * time.Hour}
			path, _ := threeParts(t)
			events := make(chan Task, 256)
			q := openQueue(t, backend, filepath.Join(t.TempDir(), "uploads.json"), events)
			q.SetResumable(tc.resumable)
			if tc.cancel {
				oss.set(func(_ string, part int) bool { return part > 1 }, nil)
			} else {
				oss.set(nil, refusePart2)
			}
			task, err := q.Add(path, Target{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.cancel {
				oss.waitStored(t, "obj-1#1")
				if err := q.Cancel(task.ID); err != nil {
					t.Fatal(err)
				}
			}
			final := waitFor(t, events, task.ID, func(tk Task) bool { return !tk.Active() })
			_, trashed := backend.state()
			checkpoint := filepath.Join(q.checkpoints, task.ID)
			_, statErr := os.Stat(checkpoint)
			if final.State != tc.state || final.Resumable != tc.kept || (len(trashed) == 0) != tc.kept || os.IsNotExist(statErr) == tc.kept {
				t.Fatalf("ended %+v, trashed %v, checkpoint present %v; want %s with the slot kept=%v", final, trashed, statErr == nil, tc.state, tc.kept)
			}
			if !tc.kept {
				return
			}
			// Removing the record is how a kept upload is given up.
			if err := q.Remove(task.ID); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				_, trashed = backend.state()
				_, statErr = os.Stat(checkpoint)
				if slices.Equal(trashed, []string{"p-1"}) && os.IsNotExist(statErr) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("removing a kept upload left trashed=%v, checkpoint err=%v", trashed, statErr)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
