package download

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
)

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// payload is what every fake link serves: 37 MiB and change, so ranges never
// end on round numbers.
var payload = func() []byte {
	b := make([]byte, 37<<20+123)
	_, _ = rand.Read(b)
	return b
}()

func TestMain(m *testing.M) {
	stallTimeout = 5 * time.Second
	retryDelays = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
	busyDelays = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
	busyGiveUp = 20 * time.Second
	os.Exit(m.Run())
}

// fakeCDN serves payload under /f?token=N with http.ServeContent, plus the
// misbehaviour a test asks for. Configure it before newCDN starts it.
type fakeCDN struct {
	*httptest.Server
	// pace sleeps after every 32 KiB written, so a file's ranges are in
	// flight together.
	pace time.Duration
	// noRange answers every request 200 with the whole file.
	noRange bool
	// dropFirst makes the first so many requests break off after 1 MiB.
	dropFirst int
	// expire is a token answered 403 once it has served 1 MiB.
	expire string
	// busyAbove answers 503 while that many requests are being served.
	busyAbove int
	// holds are tokens whose requests stop after 1 MiB until free.
	holds map[string]bool
	// archived answers like OSS for an object in archive storage: the
	// headers, naming the class, and then not a byte.
	archived bool

	release  chan struct{}
	freeOnce sync.Once

	mu       sync.Mutex
	requests int
	sent     int64
	sentBy   map[string]int64
	serving  int
	peak     int
	held     int
}

func newCDN(t *testing.T, c *fakeCDN) *fakeCDN {
	t.Helper()
	c.release = make(chan struct{})
	c.sentBy = map[string]int64{}
	c.Server = httptest.NewServer(c)
	t.Cleanup(c.Close)
	t.Cleanup(c.free)
	return c
}

// free lets held requests go on.
func (c *fakeCDN) free() { c.freeOnce.Do(func() { close(c.release) }) }

func (c *fakeCDN) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	c.mu.Lock()
	c.requests++
	n := c.requests
	switch {
	case c.expire != "" && token == c.expire && c.sentBy[token] >= 1<<20:
		c.mu.Unlock()
		http.Error(w, "link expired", http.StatusForbidden)
		return
	case c.busyAbove > 0 && c.serving >= c.busyAbove:
		c.mu.Unlock()
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	c.serving++
	c.peak = max(c.peak, c.serving)
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.serving--
		c.mu.Unlock()
	}()

	out := &cdnWriter{ResponseWriter: w, c: c, ctx: r.Context(), token: token}
	if n <= c.dropFirst {
		out.dropAt = 1 << 20
	}
	if c.holds[token] {
		out.holdAt = 1 << 20
	}
	if c.noRange {
		r.Header.Del("Range")
	}
	if c.archived {
		w.Header().Set("X-Oss-Storage-Class", "Archive")
		out.empty = true
	}
	http.ServeContent(out, r, "f", time.Time{}, bytes.NewReader(payload))
}

func (c *fakeCDN) stats() (requests int, sent int64, peak int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests, c.sent, c.peak
}

// waitHeld waits until n requests are held.
func (c *fakeCDN) waitHeld(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c.mu.Lock()
		held := c.held
		c.mu.Unlock()
		if held >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d requests held, want %d", held, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// cdnWriter counts what a request sends and stops it where the CDN says.
type cdnWriter struct {
	http.ResponseWriter
	c       *fakeCDN
	ctx     context.Context
	token   string
	written int64
	dropAt  int64
	holdAt  int64
	// empty sends the headers and breaks off before the first byte.
	empty bool
}

func (w *cdnWriter) Write(b []byte) (int, error) {
	if w.empty {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
		panic(http.ErrAbortHandler)
	}
	if w.holdAt > 0 && w.written >= w.holdAt {
		w.holdAt = 0
		_ = http.NewResponseController(w.ResponseWriter).Flush()
		w.c.mu.Lock()
		w.c.held++
		w.c.mu.Unlock()
		select {
		case <-w.c.release:
		case <-w.ctx.Done():
		}
		w.c.mu.Lock()
		w.c.held--
		w.c.mu.Unlock()
		if w.ctx.Err() != nil {
			return 0, w.ctx.Err()
		}
	}
	if w.dropAt > 0 && w.written >= w.dropAt {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
		panic(http.ErrAbortHandler)
	}
	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)
	w.c.mu.Lock()
	w.c.sent += int64(n)
	w.c.sentBy[w.token] += int64(n)
	w.c.mu.Unlock()
	if w.c.pace > 0 {
		time.Sleep(w.c.pace)
	}
	return n, err
}

// fakeBackend hands out a new link token on every GetFile: "1", "2", …
type fakeBackend struct {
	url   string
	sizes map[string]int64 // file id → size; payload's when absent

	mu    sync.Mutex
	calls int
}

func (b *fakeBackend) GetFile(_ context.Context, id string, p drive.GetFileParams) (*drive.File, error) {
	if p.Usage != drive.UsageFetch {
		return nil, errors.New("not a download link request")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	size, ok := b.sizes[id]
	if !ok {
		size = int64(len(payload))
	}
	return &drive.File{ID: id, Name: id, Size: drive.Int64(size), Links: map[string]drive.Link{
		"application/octet-stream": {
			URL:    fmt.Sprintf("%s/f?token=%d", b.url, b.calls),
			Expire: time.Now().Add(time.Hour).Format("2006-01-02T15:04:05.000Z07:00"),
		},
	}}, nil
}

func (b *fakeBackend) getFiles() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// meter counts the requests this side has open, per link token and in all,
// from the moment one is sent until its body is closed — the connections a
// queue holds, which the CDN limits — and the body bytes the queue read.
// The CDN's own count also has what it wrote into the socket of a body the
// queue let go of after another thread took over the rest of its range.
type meter struct {
	base *http.Transport
	mu   sync.Mutex
	open map[string]int
	peak map[string]int
	all  int
	most int
	read int64
}

func newMeter() *meter {
	return &meter{base: newHTTPClient().Transport.(*http.Transport), open: map[string]int{}, peak: map[string]int{}}
}

func (m *meter) client() *http.Client { return &http.Client{Transport: m} }

func (m *meter) CloseIdleConnections() { m.base.CloseIdleConnections() }

func (m *meter) RoundTrip(r *http.Request) (*http.Response, error) {
	token := r.URL.Query().Get("token")
	m.mu.Lock()
	m.open[token]++
	m.all++
	m.peak[token] = max(m.peak[token], m.open[token])
	m.most = max(m.most, m.all)
	m.mu.Unlock()
	resp, err := m.base.RoundTrip(r)
	if err != nil {
		m.leave(token)
		return nil, err
	}
	resp.Body = &meteredBody{ReadCloser: resp.Body, m: m, token: token}
	return resp, nil
}

func (m *meter) leave(token string) {
	m.mu.Lock()
	m.open[token]--
	m.all--
	m.mu.Unlock()
}

func (m *meter) peaks() (perLink map[string]int, all int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	perLink = map[string]int{}
	for k, v := range m.peak {
		perLink[k] = v
	}
	return perLink, m.most
}

func (m *meter) bytesRead() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.read
}

type meteredBody struct {
	io.ReadCloser
	m     *meter
	token string
	once  sync.Once
}

func (b *meteredBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.m.mu.Lock()
	b.m.read += int64(n)
	b.m.mu.Unlock()
	return n, err
}

func (b *meteredBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.m.leave(b.token) })
	return err
}

// newQueue makes a queue whose requests go through m, closed when the test
// ends — before the CDN, so held requests are let go.
func newQueue(t *testing.T, b Backend, m *meter, opts Options) *Queue {
	t.Helper()
	opts.HTTPClient = m.client()
	q := New(b, opts)
	t.Cleanup(q.Close)
	return q
}

func add(t *testing.T, q *Queue, src Source) Task {
	t.Helper()
	if src.Size == 0 {
		src.Size = int64(len(payload))
	}
	task, err := q.Add(src)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// waitTask polls the list until task id satisfies ok.
func waitTask(t *testing.T, q *Queue, id string, ok func(Task) bool) Task {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		for _, tk := range q.List() {
			if tk.ID == id && ok(tk) {
				return tk
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s never got there: %+v", id, q.List())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func stopped(tk Task) bool { return !tk.Active() }

// samePayload checks that path holds exactly payload, and that no partial
// file is left beside it.
func samePayload(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Fatalf("%s: %d bytes that are not the file (want %d)", filepath.Base(path), len(got), len(payload))
	}
	if _, err := os.Stat(path + PartialExt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file left behind: %v", err)
	}
}

func TestDownloadsWholeFileWithParallelParts(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{pace: time.Millisecond})
	m := newMeter()
	q := newQueue(t, &fakeBackend{url: cdn.URL}, m, Options{Parallel: 1, Threads: 4})
	dir := t.TempDir()
	task := add(t, q, Source{FileID: "f1", Name: "movie.mkv", Dir: dir, Origin: "全部文件 / 电影"})
	if _, err := q.Add(Source{FileID: "f1", Name: "movie.mkv", Dir: dir}); !errors.Is(err, ErrQueued) {
		t.Fatalf("adding the same file twice: %v, want ErrQueued", err)
	}

	done := waitTask(t, q, task.ID, stopped)
	if done.State != StateDone || done.Size != int64(len(payload)) || done.Done != done.Size || done.Origin != "全部文件 / 电影" {
		t.Fatalf("download ended as %+v", done)
	}
	if done.Path != filepath.Join(dir, "movie.mkv") || done.Name != "movie.mkv" {
		t.Fatalf("landed at %s (%s)", done.Path, done.Name)
	}
	samePayload(t, done.Path)
	if perLink, _ := m.peaks(); perLink["1"] != 4 {
		t.Fatalf("at most %d connections at once, want all 4 threads", perLink["1"])
	}
	if got := m.bytesRead(); got != int64(len(payload)) {
		t.Fatalf("read %d bytes for a %d-byte file", got, len(payload))
	}
}

// Three files of eight threads each want 24 connections: the account gets
// 16, and no link more than 8.
func TestRespectsConnectionLimits(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{pace: time.Millisecond})
	m := newMeter()
	q := newQueue(t, &fakeBackend{url: cdn.URL}, m, Options{Parallel: 3, Threads: 8})
	dir := t.TempDir()
	var tasks []Task
	for _, id := range []string{"a", "b", "c"} {
		tasks = append(tasks, add(t, q, Source{FileID: id, Name: id + ".bin", Dir: dir}))
	}
	for _, task := range tasks {
		if done := waitTask(t, q, task.ID, stopped); done.State != StateDone {
			t.Fatalf("%s ended as %+v", task.Name, done)
		}
		samePayload(t, task.Path)
	}
	perLink, all := m.peaks()
	if all != AccountConnections {
		t.Errorf("%d connections at once, want the account's %d", all, AccountConnections)
	}
	for token, n := range perLink {
		if n > MaxThreads {
			t.Errorf("link %s had %d connections at once, more than %d", token, n, MaxThreads)
		}
	}
}

func TestPauseKeepsPartsAndResumeFinishes(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{holds: map[string]bool{"1": true}})
	m := newMeter()
	q := newQueue(t, &fakeBackend{url: cdn.URL}, m, Options{Parallel: 1, Threads: 4})
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	cdn.waitHeld(t, 4)
	waitTask(t, q, task.ID, func(tk Task) bool { return tk.Done >= 4<<20 })

	if err := q.Pause(task.ID); err != nil {
		t.Fatal(err)
	}
	paused := waitTask(t, q, task.ID, stopped)
	if paused.State != StatePaused || !paused.Resumable || paused.Done != 4<<20 || paused.Speed != 0 || paused.Threads != 0 {
		t.Fatalf("paused as %+v", paused)
	}
	if _, err := os.Stat(task.Path + PartialExt); err != nil {
		t.Fatalf("the partial file went: %v", err)
	}

	if err := q.Resume(task.ID); err != nil {
		t.Fatal(err)
	}
	done := waitTask(t, q, task.ID, stopped)
	if done.State != StateDone || done.Done != done.Size {
		t.Fatalf("resumed download ended as %+v", done)
	}
	samePayload(t, task.Path)
	// Every byte once: what was on disk is not fetched again.
	if got := m.bytesRead(); got != int64(len(payload)) {
		t.Fatalf("read %d bytes for a %d-byte file", got, len(payload))
	}
}

func TestRestartStartsOver(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{holds: map[string]bool{"1": true}})
	q := newQueue(t, &fakeBackend{url: cdn.URL}, newMeter(), Options{Parallel: 1, Threads: 4})
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	cdn.waitHeld(t, 4)
	waitTask(t, q, task.ID, func(tk Task) bool { return tk.Done >= 4<<20 })
	if err := q.Pause(task.ID); err != nil {
		t.Fatal(err)
	}
	waitTask(t, q, task.ID, stopped)
	if err := q.Restart(task.ID); err != nil {
		t.Fatal(err)
	}
	if again := q.List()[0]; again.Done != 0 || again.Resumable || !again.Active() {
		t.Fatalf("restarted as %+v", again)
	}
	done := waitTask(t, q, task.ID, stopped)
	if done.State != StateDone {
		t.Fatalf("restarted download ended as %+v", done)
	}
	samePayload(t, task.Path)
	if _, sent, _ := cdn.stats(); sent < int64(len(payload))+4<<20 {
		t.Fatalf("the CDN sent %d bytes: the restart kept what was on disk", sent)
	}
	if err := q.Restart(task.ID); err == nil {
		t.Fatal("restarting a finished download must fail")
	}
}

func TestCancelDeletesPartial(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{holds: map[string]bool{"1": true, "2": true}})
	q := newQueue(t, &fakeBackend{url: cdn.URL}, newMeter(), Options{Parallel: 2, Threads: 2})
	dir := t.TempDir()
	running := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: dir})
	paused := add(t, q, Source{FileID: "f2", Name: "b.bin", Dir: dir})
	cdn.waitHeld(t, 4)

	// Downloading: the run deletes the partial once its threads are out.
	if err := q.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
	// Paused: the partial goes at once.
	if err := q.Pause(paused.ID); err != nil {
		t.Fatal(err)
	}
	waitTask(t, q, paused.ID, stopped)
	if err := q.Cancel(paused.ID); err != nil {
		t.Fatal(err)
	}
	for _, task := range []Task{running, paused} {
		got := waitTask(t, q, task.ID, stopped)
		if got.State != StateCancelled || got.Done != 0 || got.Resumable {
			t.Fatalf("%s cancelled as %+v", task.Name, got)
		}
		if _, err := os.Stat(task.Path + PartialExt); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: partial file kept: %v", task.Name, err)
		}
	}
	if err := q.Remove(paused.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Remove(paused.ID); !errors.Is(err, ErrNoTask) {
		t.Fatalf("removing twice: %v", err)
	}
}

// The first link breaks off after 1 MiB and is then turned down: the queue
// asks the drive for a new one, once, and carries on from where it was.
func TestRefreshesRejectedLink(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{dropFirst: 1, expire: "1"})
	b := &fakeBackend{url: cdn.URL}
	q := newQueue(t, b, newMeter(), Options{Parallel: 1, Threads: 1})
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	if done := waitTask(t, q, task.ID, stopped); done.State != StateDone {
		t.Fatalf("ended as %+v", done)
	}
	samePayload(t, task.Path)
	if n := b.getFiles(); n != 2 {
		t.Fatalf("GetFile called %d times, want 2", n)
	}
	if _, sent, _ := cdn.stats(); sent >= int64(len(payload))+2<<20 {
		t.Fatalf("the CDN sent %d bytes: the new link started over", sent)
	}
}

func TestFallsBackWithoutRanges(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{noRange: true})
	m := newMeter()
	q := newQueue(t, &fakeBackend{url: cdn.URL}, m, Options{Parallel: 1, Threads: 4})
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	if done := waitTask(t, q, task.ID, stopped); done.State != StateDone {
		t.Fatalf("ended as %+v", done)
	}
	samePayload(t, task.Path)
	if perLink, _ := m.peaks(); perLink["1"] != 1 {
		t.Fatalf("%d connections to a server without ranges, want 1", perLink["1"])
	}
}

func TestRetriesDroppedConnections(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{dropFirst: 3})
	b := &fakeBackend{url: cdn.URL}
	q := newQueue(t, b, newMeter(), Options{Parallel: 1, Threads: 4})
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	if done := waitTask(t, q, task.ID, stopped); done.State != StateDone {
		t.Fatalf("ended as %+v", done)
	}
	samePayload(t, task.Path)
	if n := b.getFiles(); n != 1 {
		t.Fatalf("GetFile called %d times for dropped connections, want 1", n)
	}
}

func TestBusyBacksOff(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{busyAbove: 3})
	q := newQueue(t, &fakeBackend{url: cdn.URL}, newMeter(), Options{Parallel: 1, Threads: 8})
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	if done := waitTask(t, q, task.ID, stopped); done.State != StateDone {
		t.Fatalf("ended as %+v", done)
	}
	samePayload(t, task.Path)
	if _, _, peak := cdn.stats(); peak > 3 {
		t.Fatalf("served %d at once, the CDN allows 3", peak)
	}
}

// Nothing is overwritten: not a file already there, not another download's.
func TestNameConflicts(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{})
	q := newQueue(t, &fakeBackend{url: cdn.URL}, newMeter(), Options{Parallel: 2, Threads: 2})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: dir})
	second := add(t, q, Source{FileID: "f2", Name: "a.bin", Dir: dir})
	for task, want := range map[Task]string{first: "a (1).bin", second: "a (2).bin"} {
		if task.Name != want || task.Path != filepath.Join(dir, want) {
			t.Fatalf("named %s at %s, want %s", task.Name, task.Path, want)
		}
		if done := waitTask(t, q, task.ID, stopped); done.State != StateDone {
			t.Fatalf("%s ended as %+v", want, done)
		}
		samePayload(t, task.Path)
	}
	if mine, _ := os.ReadFile(filepath.Join(dir, "a.bin")); string(mine) != "mine" {
		t.Fatalf("the file already there was overwritten: %d bytes", len(mine))
	}
}

// An archived object sends its headers and no bytes: the download stops at
// once with that reason, rather than retrying a network that is fine.
func TestArchivedFileFailsAtOnce(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{archived: true})
	q := newQueue(t, &fakeBackend{url: cdn.URL}, newMeter(), Options{Parallel: 1, Threads: 8})
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	failed := waitTask(t, q, task.ID, stopped)
	if failed.State != StateFailed || failed.Error != errArchived.Error() {
		t.Fatalf("ended as %+v", failed)
	}
	if requests, _, _ := cdn.stats(); requests != 1 {
		t.Fatalf("%d requests for a file the CDN cannot serve, want 1", requests)
	}
}

func TestZeroByteFile(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{})
	q := newQueue(t, &fakeBackend{url: cdn.URL, sizes: map[string]int64{"empty": 0}}, newMeter(), Options{Parallel: 1, Threads: 8})
	task, err := q.Add(Source{FileID: "empty", Name: "empty.txt", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitTask(t, q, task.ID, stopped); done.State != StateDone || done.Done != 0 {
		t.Fatalf("ended as %+v", done)
	}
	if info, err := os.Stat(task.Path); err != nil || info.Size() != 0 {
		t.Fatalf("empty file: %v, %v", info, err)
	}
	if requests, _, _ := cdn.stats(); requests != 0 {
		t.Fatalf("%d requests for an empty file", requests)
	}
}

func TestNoSpaceFails(t *testing.T) {
	freeSpace = func(string) (uint64, error) { return 1, nil }
	t.Cleanup(func() { freeSpace = diskFree })
	cdn := newCDN(t, &fakeCDN{})
	q := newQueue(t, &fakeBackend{url: cdn.URL}, newMeter(), Options{Parallel: 1, Threads: 4})
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	failed := waitTask(t, q, task.ID, stopped)
	if failed.State != StateFailed || !strings.Contains(failed.Error, "磁盘空间不足") {
		t.Fatalf("ended as %+v", failed)
	}
	if _, err := os.Stat(task.Path + PartialExt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("made a partial file it had no room for: %v", err)
	}
}

func TestRestartAfterCloseResumes(t *testing.T) {
	cdn := newCDN(t, &fakeCDN{holds: map[string]bool{"1": true}})
	b := &fakeBackend{url: cdn.URL}
	journalPath := filepath.Join(t.TempDir(), "downloads.json")
	m := newMeter()
	q := New(b, Options{Parallel: 1, Threads: 4, HTTPClient: m.client()})
	q.Open(journalPath, quietLog)
	task := add(t, q, Source{FileID: "f1", Name: "a.bin", Dir: t.TempDir()})
	cdn.waitHeld(t, 4)
	waitTask(t, q, task.ID, func(tk Task) bool { return tk.Done >= 4<<20 })
	q.Close()

	rs, err := readJournal(journalPath)
	if err != nil || len(rs) != 1 {
		t.Fatalf("journal: %d records, %v", len(rs), err)
	}
	if rs[0].State != StateInterrupted || len(rs[0].Parts) != 4 || left(partsOf(rs[0].Parts)) != int64(len(payload))-4<<20 {
		t.Fatalf("journal kept %+v", rs[0])
	}

	m2 := newMeter()
	q2 := newQueue(t, b, m2, Options{Parallel: 1, Threads: 4})
	q2.Open(journalPath, quietLog)
	restored := q2.List()
	if len(restored) != 1 || restored[0].State != StateInterrupted || !restored[0].Resumable || restored[0].Done != 4<<20 {
		t.Fatalf("restored %+v", restored)
	}
	if err := q2.Resume(task.ID); err != nil {
		t.Fatal(err)
	}
	if done := waitTask(t, q2, task.ID, stopped); done.State != StateDone {
		t.Fatalf("ended as %+v", done)
	}
	samePayload(t, task.Path)
	if got := m.bytesRead() + m2.bytesRead(); got != int64(len(payload)) {
		t.Fatalf("read %d bytes over two sessions for a %d-byte file", got, len(payload))
	}
}

func partsOf(ps []part) []*part {
	out := make([]*part, len(ps))
	for i := range ps {
		out[i] = &ps[i]
	}
	return out
}
