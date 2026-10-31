// Package download saves drive files to this computer from PikPak's
// pre-signed links. Each file is cut into byte ranges fetched in parallel
// with HTTP Range requests and written in place; what is already on disk is
// kept in a journal, so a pause or a quit loses nothing and Resume picks up
// from there. Connections are budgeted against the CDN's two limits: at most
// eight at once on one FETCH link and twenty for one user, beyond which it
// answers 503.
package download

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
)

// Task states. A job goes queued → downloading → done, or stops paused,
// failed or cancelled.
const (
	StateQueued      = "queued"
	StateDownloading = "downloading"
	StatePaused      = "paused"
	StateDone        = "done"
	StateFailed      = "failed"
	StateCancelled   = "cancelled"
	// StateInterrupted is a job that was queued or downloading when the
	// session ended. Nothing restarts on its own: Resume picks it up.
	StateInterrupted = "interrupted"
)

const (
	// MaxThreads is the most connections one file opens: the CDN serves one
	// FETCH link to at most eight at once (more get 503, code 207).
	MaxThreads = 8
	// AccountConnections is what all of one account's downloads may hold
	// open together: the CDN allows a user twenty (more get 503, code 208),
	// and four stay free for previews and text reads, which use FETCH links
	// too.
	AccountConnections = 16
	// PartialExt marks a file still being downloaded. It sits beside where
	// the file lands and is renamed into place once complete.
	PartialExt = ".pkpdownload"
)

const (
	// minPart is the least one thread is given when a file is first cut
	// up: a small file does not need eight connections.
	minPart = 4 << 20
	// minSplit is the least either half keeps when an idle thread takes
	// over the back of a busy range.
	minSplit = 1 << 20
	// align puts range boundaries on 64 KiB.
	align = 64 << 10
	// bufSize is each thread's read buffer: one disk write per 256 KiB.
	bufSize = 256 << 10
	// emitInterval bounds how often a running job wakes the UI.
	emitInterval = 200 * time.Millisecond
	// speedWindow is how far back the rate is measured.
	speedWindow = time.Second
	// saveEvery is how often a running job's ranges reach the journal: a
	// crash costs at most that much downloading again.
	saveEvery = 5 * time.Second
	// closeGrace is how long Close waits for jobs to flush their files.
	closeGrace = 3 * time.Second
	// linkMargin renews a link this long before it expires.
	linkMargin = 2 * time.Minute
	// spaceMargin is the free space a download leaves the disk.
	spaceMargin = 64 << 20
	// maxRefreshes is how many new links a rejected one gets while no
	// bytes arrive.
	maxRefreshes = 3
	// maxFailures is how many broken connections in a row one range takes.
	maxFailures = 8
)

// Tests shorten these.
var (
	stallTimeout = 30 * time.Second
	retryDelays  = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second}
	busyDelays   = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second}
	busyGiveUp   = 10 * time.Minute
	freeSpace    = diskFree
)

var (
	// ErrNoTask is an id the queue does not know.
	ErrNoTask = i18n.New("下载列表里没有这一项")
	// ErrQueued refuses a file that is already in the list and not done.
	ErrQueued = i18n.New("这个文件已经在下载列表里")
)

// Why a job's context was cancelled, read back with context.Cause.
var (
	errPaused    = errors.New("download: paused")
	errCancelled = errors.New("download: cancelled")
	errRemoved   = errors.New("download: removed")
	errClosed    = errors.New("download: the app is closing")
)

// Task is one file's place in the queue, as the UI sees it.
type Task struct {
	ID     string `json:"id"`
	FileID string `json:"fileId"`
	// Name is the local file name: made safe for the file system and
	// numbered when the folder already had one by that name.
	Name string `json:"name"`
	// Path is where the file lands, absolute.
	Path string `json:"path"`
	// Origin names the drive folder it comes from, "All Files / Movies / Season 1".
	Origin string `json:"origin"`
	Size   int64  `json:"size"`
	State  string `json:"state"`
	// Done is bytes on disk; a stopped download keeps them.
	Done int64 `json:"done"`
	// Speed is bytes per second over the last window, downloading only.
	Speed int64 `json:"speed"`
	// Threads is how many connections are open right now, downloading only.
	Threads int    `json:"threads"`
	Error   string `json:"error"`
	// Resumable marks a stopped download that Resume continues where it
	// stopped: part of it is on disk and the queue knows which part.
	Resumable bool `json:"resumable"`
	// UpdatedAt is the RFC 3339 time of the last state change.
	UpdatedAt string `json:"updatedAt"`
}

// Active is whether the job still has work ahead of it.
func (t Task) Active() bool {
	return t.State == StateQueued || t.State == StateDownloading
}

// stopped is whether Resume and Redownload apply.
func (t Task) stopped() bool {
	switch t.State {
	case StatePaused, StateFailed, StateInterrupted, StateCancelled:
		return true
	}
	return false
}

// Source is one drive file to download into Dir, an absolute local folder
// that is made when the job runs. Origin is how its drive folder reads.
type Source struct {
	FileID string
	Name   string
	Size   int64
	Dir    string
	Origin string
}

// Backend is what the queue needs from the drive: a fresh download link.
type Backend interface {
	GetFile(ctx context.Context, id string, params drive.GetFileParams) (*drive.File, error)
}

// Options shape a queue. Parallel is how many files download at once and
// Threads how many connections each opens; UserAgent goes with every CDN
// request. HTTPClient defaults to one made for ranged downloads, OnChange to
// nothing.
type Options struct {
	Parallel   int
	Threads    int
	UserAgent  string
	HTTPClient *http.Client
	OnChange   func(Task)
}

// part is one byte range of the file: [Start, End), with Pos the next byte
// to write. Ranges still to fetch go into the journal.
type part struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Pos   int64 `json:"pos"`
	// busy is set while a thread fetches it.
	busy bool
}

type job struct {
	Task
	// pmu guards parts and every part's Pos, End and busy. It is taken
	// after mu when both are needed.
	pmu sync.Mutex
	// parts holds the ranges not yet on disk; nil before the first byte
	// is planned, and again after a restart.
	parts  []*part
	cancel context.CancelCauseFunc
	direct *directJob
	// lastEmit throttles progress events; lastDone and lastAt are the
	// bytes received at the previous speed sample and when.
	lastEmit time.Time
	lastDone int64
	lastAt   time.Time
}

// written is how much of the file is on disk; pmu must be held.
func (j *job) written() int64 {
	if j.parts == nil {
		return 0
	}
	return j.Size - left(j.parts)
}

// Queue runs downloads, a limited number of files at a time, and reports
// every change through onChange: state changes at once, progress at most
// every emitInterval per file.
type Queue struct {
	backend  Backend
	client   *http.Client
	agent    string
	onChange func(Task)

	mu     sync.Mutex
	jobs   map[string]*job
	direct map[*job]struct{}
	seq    int
	// closing is set by Close and Purge; purging by Purge alone, which
	// throws the account's downloads away without a word to anyone.
	closing bool
	purging bool
	// running counts job goroutines, so Close can let them wind down.
	running       sync.WaitGroup
	directRunning sync.WaitGroup
	// limit is how many files may download at once and active how many
	// do. waiting lines up the started jobs without a slot, in start order;
	// only the head may take a free slot. freed is closed, and replaced,
	// whenever a slot may have opened or the head changed.
	limit   int
	active  int
	waiting []*job
	freed   chan struct{}
	// threads is how many connections a file starting now opens.
	threads int
	// conns is the account's connection budget: a request holds a token
	// from before it is sent until its body is closed.
	conns chan struct{}

	// jmu guards journal; lock order is jmu, then mu, then a job's pmu.
	jmu     sync.Mutex
	journal *journal
}

// New makes an idle queue.
func New(backend Backend, opts Options) *Queue {
	client := opts.HTTPClient
	if client == nil {
		client = newHTTPClient()
	}
	onChange := opts.OnChange
	if onChange == nil {
		onChange = func(Task) {}
	}
	return &Queue{
		backend:  backend,
		client:   client,
		agent:    opts.UserAgent,
		onChange: onChange,
		jobs:     map[string]*job{},
		direct:   map[*job]struct{}{},
		limit:    max(opts.Parallel, 1),
		freed:    make(chan struct{}),
		threads:  min(max(opts.Threads, 1), MaxThreads),
		conns:    make(chan struct{}, AccountConnections),
	}
}

// SetParallel changes how many files download at once. Files already moving
// keep going; waiting ones start as soon as the count is under the new limit.
func (q *Queue) SetParallel(n int) {
	q.mu.Lock()
	q.limit = max(n, 1)
	q.wake()
	q.mu.Unlock()
}

// SetThreads changes how many connections files starting from now open,
// held to 1–MaxThreads. Files already downloading keep theirs.
func (q *Queue) SetThreads(n int) {
	q.mu.Lock()
	q.threads = min(max(n, 1), MaxThreads)
	q.mu.Unlock()
}

// wake tells every waiting job to look again; mu must be held.
func (q *Queue) wake() {
	close(q.freed)
	q.freed = make(chan struct{})
}

// acquire waits for j's turn: a free slot while j heads the line. It gives up
// when ctx ends, leaving the line so the next job can move up.
func (q *Queue) acquire(ctx context.Context, j *job) error {
	for {
		q.mu.Lock()
		if ctx.Err() != nil {
			q.waiting = slices.DeleteFunc(q.waiting, func(w *job) bool { return w == j })
			q.wake()
			q.mu.Unlock()
			return ctx.Err()
		}
		if q.active < q.limit && len(q.waiting) > 0 && q.waiting[0] == j {
			q.waiting = q.waiting[1:]
			q.active++
			// The next in line may fit as well.
			q.wake()
			q.mu.Unlock()
			return nil
		}
		freed := q.freed
		q.mu.Unlock()
		select {
		case <-freed:
		case <-ctx.Done():
		}
	}
}

// release gives a slot back.
func (q *Queue) release() {
	q.mu.Lock()
	q.active--
	q.wake()
	q.mu.Unlock()
}

// view is the job as the webview sees it; mu must be held.
func (q *Queue) view(j *job) Task {
	t := j.Task
	j.pmu.Lock()
	kept := j.parts != nil
	j.pmu.Unlock()
	t.Resumable = kept && t.Done > 0 &&
		(t.State == StatePaused || t.State == StateFailed || t.State == StateInterrupted)
	return t
}

// Add queues one drive file. Its local name is made safe and, when the
// folder already has that name — on disk, half-downloaded, or promised to
// another job — numbered; nothing is ever overwritten. A file already in the
// list and not done is refused with ErrQueued.
func (q *Queue) Add(src Source) (Task, error) {
	if src.FileID == "" {
		return Task{}, i18n.New("没有指定要下载的文件")
	}
	if !filepath.IsAbs(src.Dir) {
		return Task{}, i18n.New("保存位置必须是完整的文件夹路径")
	}
	q.mu.Lock()
	for _, j := range q.jobs {
		if j.FileID == src.FileID && j.State != StateDone && j.State != StateCancelled {
			q.mu.Unlock()
			return Task{}, ErrQueued
		}
	}
	path, err := q.claimLocked(src.Dir, SafeName(src.Name, runtime.GOOS))
	if err != nil {
		q.mu.Unlock()
		return Task{}, err
	}
	q.seq++
	j := &job{Task: Task{
		ID:     "d" + strconv.Itoa(q.seq),
		FileID: src.FileID,
		Name:   filepath.Base(path),
		Path:   path,
		Origin: src.Origin,
		Size:   src.Size,
	}}
	q.jobs[j.ID] = j
	started := q.startLocked(j)
	queued := q.view(j)
	q.mu.Unlock()
	if !started {
		q.emit(j, true)
	}
	return queued, nil
}

// List returns every job in the order it was added.
func (q *Queue) List() []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Task, 0, len(q.jobs))
	for _, j := range q.jobs {
		out = append(out, q.view(j))
	}
	sort.Slice(out, func(a, b int) bool { return seqOf(out[a].ID) < seqOf(out[b].ID) })
	return out
}

// seqOf is the number in a job id ("d12" → 12); 0 for anything else.
func seqOf(id string) int {
	if len(id) < 2 || id[0] != 'd' {
		return 0
	}
	n, _ := strconv.Atoi(id[1:])
	return n
}

// Pause stops a queued or downloading job and keeps what is on disk;
// anything else is left as it is.
func (q *Queue) Pause(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return ErrNoTask
	}
	if j.Active() && j.cancel != nil {
		j.cancel(errPaused)
	}
	return nil
}

// Resume puts a stopped job back at the end of the line. It continues from
// what is on disk; a cancelled one kept nothing and starts over.
func (q *Queue) Resume(id string) error {
	return q.again(id, false)
}

// Restart throws away what a stopped job has on disk and starts it over.
func (q *Queue) Restart(id string) error {
	return q.again(id, true)
}

func (q *Queue) again(id string, over bool) error {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return ErrNoTask
	}
	if !j.stopped() {
		q.mu.Unlock()
		return i18n.New("这一项还没有停止")
	}
	if over {
		removePartial(j.Path)
		j.pmu.Lock()
		j.parts = nil
		j.pmu.Unlock()
		j.Done = 0
	}
	started := q.startLocked(j)
	q.mu.Unlock()
	if !started {
		q.emit(j, true)
	}
	return nil
}

// Cancel stops a job for good and deletes its partial file. Done and
// cancelled jobs are left alone.
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return ErrNoTask
	}
	if j.Active() {
		if j.cancel != nil {
			// The run deletes the partial once its threads are out of it.
			j.cancel(errCancelled)
		}
		q.mu.Unlock()
		return nil
	}
	if !j.stopped() || j.State == StateCancelled {
		q.mu.Unlock()
		return nil
	}
	removePartial(j.Path)
	j.pmu.Lock()
	j.parts = nil
	j.pmu.Unlock()
	j.State, j.Done, j.Error, j.UpdatedAt = StateCancelled, 0, "", stamp()
	q.mu.Unlock()
	q.emit(j, true)
	return nil
}

// Remove forgets a job, stopping it first if needed. An unfinished one's
// partial file goes with it; a finished download's file stays.
func (q *Queue) Remove(id string) error {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return ErrNoTask
	}
	delete(q.jobs, id)
	if j.Active() {
		if j.cancel != nil {
			j.cancel(errRemoved)
		}
	} else if j.State != StateDone {
		removePartial(j.Path)
	}
	q.mu.Unlock()
	q.touchJournal()
	return nil
}

// Locate is where a job's bytes are: the file once done, otherwise the
// partial file, or the folder when there is none yet.
func (q *Queue) Locate(id string) (path string, done bool, err error) {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return "", false, ErrNoTask
	}
	path, state := j.Path, j.State
	q.mu.Unlock()
	if state == StateDone {
		return path, true, nil
	}
	if _, err := os.Stat(path + PartialExt); err == nil {
		return path + PartialExt, false, nil
	}
	return filepath.Dir(path), false, nil
}

// Close stops the queue for good. Queued and downloading jobs flush their
// files and are recorded as interrupted; then the journal is written one
// last time.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closing = true
	for _, j := range q.jobs {
		if j.Active() && j.cancel != nil {
			j.cancel(errClosed)
		}
	}
	for j := range q.direct {
		j.cancel(errClosed)
	}
	q.mu.Unlock()
	q.directRunning.Wait()
	q.settle()
	q.client.CloseIdleConnections()

	q.jmu.Lock()
	defer q.jmu.Unlock()
	q.writeJournalLocked()
	if q.journal != nil {
		q.journal.closed = true
	}
}

// Purge stops the queue for good and deletes every unfinished download's
// partial file, for an account leaving this computer. Finished files stay;
// the journal is left for the caller to delete and never written again.
func (q *Queue) Purge() {
	q.mu.Lock()
	q.closing, q.purging = true, true
	for _, j := range q.jobs {
		if j.Active() && j.cancel != nil {
			j.cancel(errRemoved)
		}
	}
	for j := range q.direct {
		j.cancel(errClosed)
	}
	q.mu.Unlock()
	q.directRunning.Wait()
	q.settle()
	q.client.CloseIdleConnections()

	q.mu.Lock()
	var partials []string
	for _, j := range q.jobs {
		if j.State != StateDone {
			partials = append(partials, j.Path)
		}
	}
	q.mu.Unlock()
	for _, path := range partials {
		removePartial(path)
	}
	q.jmu.Lock()
	if q.journal != nil {
		q.journal.closed = true
	}
	q.jmu.Unlock()
}

// settle waits up to closeGrace for the job goroutines to finish.
func (q *Queue) settle() {
	done := make(chan struct{})
	go func() {
		q.running.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeGrace):
	}
}

// startLocked puts j at the end of the line with a fresh context; mu must be
// held. It reports false when the queue is closing: then j is interrupted
// instead, and the caller emits that.
func (q *Queue) startLocked(j *job) bool {
	j.Speed, j.Threads, j.Error, j.UpdatedAt = 0, 0, "", stamp()
	if q.closing {
		j.State = StateInterrupted
		return false
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	j.State, j.cancel = StateQueued, cancel
	q.waiting = append(q.waiting, j)
	q.running.Add(1)
	go q.run(ctx, j)
	return true
}

func (q *Queue) run(ctx context.Context, j *job) {
	defer q.running.Done()
	q.emit(j, true)
	if err := q.acquire(ctx, j); err != nil {
		q.finish(j, context.Cause(ctx))
		return
	}
	defer q.release()

	q.mu.Lock()
	threads := q.threads
	j.State, j.UpdatedAt = StateDownloading, stamp()
	j.lastDone, j.lastAt = 0, time.Now()
	q.mu.Unlock()
	q.emit(j, true)

	err := q.download(ctx, j, threads)
	if err != nil && ctx.Err() != nil {
		err = context.Cause(ctx)
	}
	q.finish(j, err)
}

// progress refreshes a downloading job's byte count from its ranges, and its
// speed from the bytes this attempt received.
func (q *Queue) progress(j *job, received int64, threads int) {
	q.mu.Lock()
	j.pmu.Lock()
	j.Done = j.written()
	j.pmu.Unlock()
	j.Threads = threads
	if since := time.Since(j.lastAt); since >= speedWindow {
		j.Speed = int64(float64(received-j.lastDone) / since.Seconds())
		j.lastDone, j.lastAt = received, time.Now()
	}
	q.mu.Unlock()
	q.emit(j, false)
}

// finish records how a run ended. A job removed meanwhile, or purged with
// its account, only loses its partial file.
func (q *Queue) finish(j *job, err error) {
	q.mu.Lock()
	if j.direct == nil && (q.purging || q.jobs[j.ID] != j) {
		path := j.Path
		q.mu.Unlock()
		if err != nil {
			removePartial(path)
		}
		return
	}
	j.Speed, j.Threads, j.UpdatedAt = 0, 0, stamp()
	j.pmu.Lock()
	switch {
	case err == nil:
		j.State, j.Done, j.parts = StateDone, j.Size, nil
	case errors.Is(err, errPaused):
		j.State, j.Done = StatePaused, j.written()
	case errors.Is(err, errClosed):
		j.State, j.Done = StateInterrupted, j.written()
	case errors.Is(err, errCancelled):
		// Gone before anyone can see the job cancelled.
		removePartial(j.Path)
		j.State, j.Done, j.parts = StateCancelled, 0, nil
	default:
		j.State, j.Done, j.Error = StateFailed, j.written(), err.Error()
	}
	j.pmu.Unlock()
	q.mu.Unlock()
	q.emit(j, true)
}

// removePartial deletes the half-downloaded file of the download to path.
func removePartial(path string) {
	_ = os.Remove(path + PartialExt)
}

// stamp is the time every state change records, in the form the UI parses.
func stamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// emit publishes the job. Progress-only changes are dropped while the last
// event is younger than emitInterval; forced ones — state changes — always
// go out, and are the ones the journal records.
func (q *Queue) emit(j *job, force bool) {
	q.mu.Lock()
	now := time.Now()
	if q.purging || (!force && now.Sub(j.lastEmit) < emitInterval) {
		q.mu.Unlock()
		return
	}
	j.lastEmit = now
	snapshot := q.view(j)
	q.mu.Unlock()
	if j.direct != nil {
		if j.direct.progress != nil {
			j.direct.progress(snapshot)
		}
		return
	}
	q.onChange(snapshot)
	if force {
		q.touchJournal()
	}
}
