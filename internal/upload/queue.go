// Package upload pushes local files into the drive using an OSS grant and
// parallel multipart transfers. Each file is a job with its own context; a
// queue opened on a journal file keeps its records across restarts and, with
// Resumable upload on, what an unfinished upload had already sent. Every transfer
// goes through the app's Proxy setting.
package upload

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	osstransport "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/transport"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// Task states, in the order a job passes through them.
const (
	StateQueued    = "queued"
	StateUploading = "uploading"
	StateDone      = "done"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	// StateInterrupted is a job the app quit under: it was queued or
	// running when the session ended. Retrying continues it when it kept
	// its slot and starts it from the top otherwise.
	StateInterrupted = "interrupted"
)

const (
	// partSize and parallelParts shape the multipart transfer: four 8 MiB
	// parts in flight saturate a home uplink without holding much memory.
	partSize      = 8 << 20
	parallelParts = 4
	// emitInterval bounds how often a running job wakes the UI.
	emitInterval = 200 * time.Millisecond
	// speedWindow is how far back the rate is measured; parts complete in
	// bursts, so a shorter window would flicker.
	speedWindow = time.Second
	// closeGrace is how long Close waits for cancelled jobs to trash their
	// placeholders before writing the journal and returning.
	closeGrace = 3 * time.Second
	// trashTimeout bounds the best-effort trash of one placeholder.
	trashTimeout = 10 * time.Second
	// grantMargin is how long a kept grant must still run for an upload to
	// continue under it. PikPak issues grants for 12 hours; with less than
	// this left, starting over under a fresh one beats sending parts the
	// deadline would refuse halfway.
	grantMargin = 10 * time.Minute
	// expirySkew allows for this computer's clock running ahead of OSS's
	// when deciding that a failure was the grant running out.
	expirySkew = 30 * time.Second
)

// Task is one file's place in the queue, as the UI sees it.
type Task struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	ParentID string `json:"parentId"`
	// Dir is the folder path under ParentID the file goes into, "/"-joined
	// and relative, e.g. "photos/2024"; empty for a file dropped directly.
	// The folders are created on the server when the job runs.
	Dir   string `json:"dir"`
	Size  int64  `json:"size"`
	State string `json:"state"`
	// Done is bytes sent to OSS. A stopped upload that can resume keeps
	// it: that much is already there.
	Done int64 `json:"done"`
	// Speed is bytes per second over the last window, uploading only.
	Speed int64  `json:"speed"`
	Error string `json:"error"`
	// FileID is the drive file once the server has it.
	FileID string `json:"fileId"`
	// Resumable marks a stopped upload that retrying continues where it
	// stopped: resumable transfer is on, and the queue still holds its placeholder,
	// the part already sent and a grant with time left.
	Resumable bool `json:"resumable"`
	// SavedTo names the destination for people, "All Files / Photos / 2024";
	// empty when it could not be named.
	SavedTo string `json:"savedTo"`
	// UpdatedAt is the RFC 3339 time of the last state change: when it was
	// queued, started, or finished, failed or was cancelled.
	UpdatedAt string `json:"updatedAt"`
}

// Target is where a queued file lands: the folder it was dropped on, the
// relative folder path under it (made on the server when the job runs, ""
// for none), and how that destination reads in the list.
type Target struct {
	ParentID string
	Dir      string
	SavedTo  string
}

// Active is whether the job still has work ahead of it.
func (t Task) Active() bool {
	return t.State == StateQueued || t.State == StateUploading
}

// Backend is what the queue needs from the drive: a slot per file, and a
// way to discard the placeholder when the transfer does not finish.
type Backend interface {
	CreateUpload(ctx context.Context, params drive.UploadParams) (*drive.UploadSlot, error)
	EnsureFolder(ctx context.Context, parentID, name string) (*drive.File, error)
	Trash(ctx context.Context, ids []string) error
}

type job struct {
	Task
	cancel context.CancelFunc
	// resume is the slot the job sends into: kept between attempts when
	// the queue resumes, nil when it has none.
	resume *resumeState
	direct *directJob
	// lastEmit throttles progress events; lastDone/lastAt are the previous
	// speed sample.
	lastEmit time.Time
	lastDone int64
	lastAt   time.Time
}

// resumeState is a job's slot on the drive: the placeholder file, the OSS
// grant for its object, and the local file as it was when the slot was
// made. A failed or interrupted upload keeps it, so the next attempt can
// continue in the same object. It goes into the journal and never to the
// webview: the grant is a credential.
type resumeState struct {
	FileID  string               `json:"fileId"`
	Grant   drive.OSSCredentials `json:"grant"`
	Size    int64                `json:"size"`
	ModTime time.Time            `json:"modTime"`
}

// fresh says whether the grant still has grantMargin to run at now; a grant
// without a deadline is taken at its word.
func (r *resumeState) fresh(now time.Time) bool {
	return r.Grant.Expiration.IsZero() || r.Grant.Expiration.Sub(now) > grantMargin
}

// fits says whether the slot was made for the file as it is now: an edited
// file is a different upload.
func (r *resumeState) fits(info os.FileInfo) bool {
	return r.Size == info.Size() && r.ModTime.Equal(info.ModTime())
}

// expired tells a transfer that failed because its grant ran out: OSS says
// the token expired, or the failure came at the deadline or after it.
func expired(g drive.OSSCredentials, err error) bool {
	var se *oss.ServiceError
	if errors.As(err, &se) && se.Code == "SecurityTokenExpired" {
		return true
	}
	return !g.Expiration.IsZero() && !time.Now().Before(g.Expiration.Add(-expirySkew))
}

// Queue runs uploads, a limited number of files at a time, and reports every
// change through onChange. Progress is coalesced to emitInterval per job;
// state changes go out at once.
type Queue struct {
	backend  Backend
	onChange func(Task)

	mu     sync.Mutex
	jobs   map[string]*job
	direct map[*job]struct{}
	seq    int
	// closing is set by Close: jobs cancelled from then on were interrupted
	// by the app going away, not by the user.
	closing bool
	// running counts job goroutines, so Close can let them wind down.
	running       sync.WaitGroup
	directRunning sync.WaitGroup
	// limit is how many files may transfer at once and active how many do.
	// waiting lines up the started jobs that have no slot yet, in the order
	// they were started: only the head may take a free slot, so files go up
	// in the order they were queued. freed is closed, and replaced, whenever
	// a slot may have opened or the head changed.
	limit   int
	active  int
	waiting []*job
	freed   chan struct{}
	// folders memoises server folders made for Dir paths, keyed by
	// parent id and name. folderMu is held across the create call so two
	// files of one tree never race to make the same folder twice.
	folderMu sync.Mutex
	folders  map[string]string

	// jmu guards journal; when both are needed it is taken before mu.
	jmu     sync.Mutex
	journal *journal

	// resumable is the resumable transfer setting. checkpoints is where the OSS SDK
	// records each job's multipart upload, one directory per job; Open sets
	// it, since a slot that cannot outlive the process is not worth keeping.
	resumable   bool
	checkpoints string

	// oss is the HTTP client every transfer hands the SDK. The SDK's own
	// default transport proxies nothing at all, so uploads would walk past
	// the app's proxy setting; this one keeps the SDK's tuning and asks
	// netproxy where to go. One per queue: a client per job would leave a
	// transport behind for every file.
	oss *http.Client
}

// New makes an idle queue that transfers up to parallel files at once.
// onChange may be nil.
func New(backend Backend, parallel int, onChange func(Task)) *Queue {
	if onChange == nil {
		onChange = func(Task) {}
	}
	return &Queue{
		backend:  backend,
		onChange: onChange,
		limit:    max(parallel, 1),
		freed:    make(chan struct{}),
		jobs:     map[string]*job{},
		direct:   map[*job]struct{}{},
		folders:  map[string]string{},
		oss:      osstransport.NewHttpClient(&osstransport.Config{}, func(t *http.Transport) { netproxy.Install(t) }),
	}
}

// SetParallel changes how many files transfer at once. Files already moving
// keep going; waiting ones start as soon as the count is under the new limit.
func (q *Queue) SetParallel(n int) {
	q.mu.Lock()
	q.limit = max(n, 1)
	q.wake()
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

// SetResumable switches resumable transfer. On, an upload that fails or that a quit
// cuts short keeps its placeholder and the parts already sent, and retrying
// continues from there; off, every stop throws them away and a retry starts
// over. A cancel always throws them away. An upload already running keeps
// the setting it started with.
func (q *Queue) SetResumable(on bool) {
	q.mu.Lock()
	q.resumable = on
	q.mu.Unlock()
}

// canResume reports whether attempts starting now keep their slot; mu must
// be held.
func (q *Queue) canResume() bool {
	return q.resumable && q.checkpoints != ""
}

// checkpointDir is where the SDK records job id's multipart upload; mu must
// be held. Empty when the queue has no journal to keep it beside.
func (q *Queue) checkpointDir(id string) string {
	if q.checkpoints == "" {
		return ""
	}
	return filepath.Join(q.checkpoints, id)
}

// view is the job as the webview sees it; mu must be held.
func (q *Queue) view(j *job) Task {
	t := j.Task
	t.Resumable = q.canResume() && j.resume != nil && j.resume.fresh(time.Now()) &&
		(t.State == StateFailed || t.State == StateInterrupted)
	return t
}

// Add queues one regular file for to. It fails for anything that cannot be
// uploaded as is: a folder, an unreadable path.
func (q *Queue) Add(path string, to Target) (Task, error) {
	info, err := regularFile(path)
	if err != nil {
		return Task{}, err
	}

	q.mu.Lock()
	q.seq++
	j := &job{Task: Task{
		ID:        "u" + strconv.Itoa(q.seq),
		Name:      info.Name(),
		Path:      path,
		ParentID:  to.ParentID,
		Dir:       to.Dir,
		SavedTo:   to.SavedTo,
		Size:      info.Size(),
		State:     StateQueued,
		UpdatedAt: stamp(),
	}}
	q.jobs[j.ID] = j
	queued := j.Task
	q.mu.Unlock()

	q.start(j)
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

// seqOf is the number in a job id ("u12" → 12); 0 for anything else.
func seqOf(id string) int {
	if len(id) < 2 || id[0] != 'u' {
		return 0
	}
	n, _ := strconv.Atoi(id[1:])
	return n
}

// regularFile is the check every upload passes before it is (re)queued.
func regularFile(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, i18n.Errorf("「%s」不是普通文件，不能上传", filepath.Base(path))
	}
	return info, nil
}

// Cancel stops a job that still has work ahead of it; finished jobs are
// left alone.
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if ok && j.Active() && j.cancel != nil {
		j.cancel()
	}
	q.mu.Unlock()
	if !ok {
		return i18n.New("上传列表里没有这一项")
	}
	return nil
}

// Retry puts a failed, cancelled or interrupted job back at the end of the
// queue. The file is looked at again first: it may have changed, or gone,
// since it was queued — perhaps in an earlier session. A job that kept its
// slot continues in it.
func (q *Queue) Retry(id string) error {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return i18n.New("上传列表里没有这一项")
	}
	path := j.Path
	q.mu.Unlock()

	info, err := regularFile(path)
	if err != nil {
		return err
	}

	q.mu.Lock()
	if j.State != StateFailed && j.State != StateCancelled && j.State != StateInterrupted {
		q.mu.Unlock()
		return i18n.New("这一项还没有结束")
	}
	j.State, j.Done, j.Speed, j.Error, j.FileID, j.UpdatedAt = StateQueued, 0, 0, "", "", stamp()
	j.Size = info.Size()
	q.mu.Unlock()

	q.start(j)
	return nil
}

// Remove forgets a job, cancelling it first if it is still running. A slot
// it kept goes with it: the placeholder to the trash, the checkpoint away.
func (q *Queue) Remove(id string) error {
	q.mu.Lock()
	j, ok := q.jobs[id]
	kept := false
	if ok {
		if j.Active() && j.cancel != nil {
			// The run settles the slot as for any cancel.
			j.cancel()
		} else if j.resume != nil {
			kept = true
			q.running.Add(1)
		}
		delete(q.jobs, id)
	}
	q.mu.Unlock()
	if !ok {
		return i18n.New("上传列表里没有这一项")
	}
	if kept {
		go func() {
			defer q.running.Done()
			q.discard(j)
		}()
	}
	q.touchJournal()
	return nil
}

// Close stops the queue for good. Running and queued jobs are cancelled and
// recorded as interrupted; each gets a moment to trash its half-made file on
// the drive; then the journal is written one last time.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closing = true
	for _, j := range q.jobs {
		if j.Active() && j.cancel != nil {
			j.cancel()
		}
	}
	for j := range q.direct {
		j.cancel()
	}
	q.mu.Unlock()
	q.directRunning.Wait()

	settled := make(chan struct{})
	go func() {
		q.running.Wait()
		close(settled)
	}()
	select {
	case <-settled:
	case <-time.After(closeGrace):
	}

	q.jmu.Lock()
	defer q.jmu.Unlock()
	q.writeJournalLocked()
	if q.journal != nil {
		q.journal.closed = true
	}
}

func (q *Queue) start(j *job) {
	ctx, cancel := context.WithCancel(context.Background())
	q.mu.Lock()
	if q.closing {
		q.mu.Unlock()
		cancel()
		q.finish(j, "", context.Canceled)
		return
	}
	j.cancel = cancel
	q.waiting = append(q.waiting, j)
	q.running.Add(1)
	q.mu.Unlock()
	q.emit(j, true)
	go q.run(ctx, j)
}

func (q *Queue) run(ctx context.Context, j *job) {
	defer q.running.Done()
	q.mu.Lock()
	resumable := q.canResume()
	q.mu.Unlock()
	if err := q.acquire(ctx, j); err != nil {
		q.settle(j, resumable, err)
		q.finish(j, "", err)
		return
	}
	defer q.release()

	q.setState(j, StateUploading)
	fileID, err := q.upload(ctx, j, resumable)
	if err != nil {
		q.settle(j, resumable, err)
	}
	q.finish(j, fileID, err)
}

// upload makes the file's folders, then sends it into the job's slot: the
// kept one when it can continue there, a new one otherwise. A grant that
// runs out mid-transfer is replaced once and the file starts over, since a
// new grant comes with a new object and nothing sent to the old one counts.
func (q *Queue) upload(ctx context.Context, j *job, resumable bool) (string, error) {
	parent, err := q.folderFor(ctx, j.ParentID, j.Dir)
	if err != nil {
		return "", err
	}
	info, err := regularFile(j.Path)
	if err != nil {
		return "", err
	}
	renewed := false
	for {
		slot, err := q.slot(ctx, j, parent, info, resumable)
		if err != nil {
			return "", err
		}
		err = q.transfer(ctx, j, slot, resumable)
		if err == nil {
			q.forget(j)
			return slot.FileID, nil
		}
		if renewed || ctx.Err() != nil || !expired(slot.Grant, err) {
			return "", err
		}
		renewed = true
		q.discard(j)
		q.setState(j, StateUploading)
	}
}

// slot is where this attempt sends the file: the job's kept slot when it can
// continue there, otherwise a new placeholder and grant. A kept slot that
// cannot be used — its grant is running out, the file changed, the queue no
// longer resumes — is thrown away first.
func (q *Queue) slot(ctx context.Context, j *job, parent string, info os.FileInfo, resumable bool) (*resumeState, error) {
	q.mu.Lock()
	kept := j.resume
	q.mu.Unlock()
	if kept != nil && resumable && kept.fresh(time.Now()) && kept.fits(info) {
		return kept, nil
	}
	if kept != nil {
		q.discard(j)
	}
	made, err := q.backend.CreateUpload(ctx, drive.UploadParams{ParentID: parent, Name: j.Name, Size: info.Size()})
	if err != nil {
		return nil, err
	}
	if made == nil || made.OSS == nil {
		return nil, errors.New("upload: server returned no resumable upload grant")
	}
	slot := &resumeState{FileID: made.File.ID, Grant: *made.OSS, Size: info.Size(), ModTime: info.ModTime()}
	q.mu.Lock()
	j.resume, j.Size = slot, info.Size()
	q.mu.Unlock()
	// Recorded straight away, so even a crash leaves the placeholder
	// accounted for.
	q.touchJournal()
	return slot, nil
}

// settle decides what a stopped job keeps of its slot. Resuming, a failure
// or a quit keeps the placeholder and the parts sent; a cancel, or any stop
// without resumable transfer, throws them away.
func (q *Queue) settle(j *job, resumable bool, err error) {
	q.mu.Lock()
	cancelled := errors.Is(err, context.Canceled) && !q.closing
	q.mu.Unlock()
	if resumable && !cancelled {
		return
	}
	q.discard(j)
}

// discard throws the job's slot away: the checkpoint, and the placeholder on
// the drive — best effort, the job already has the error that matters.
func (q *Queue) discard(j *job) {
	q.mu.Lock()
	kept := j.resume
	j.resume = nil
	dir := q.checkpointDir(j.ID)
	q.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	if kept == nil {
		return
	}
	if kept.FileID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), trashTimeout)
		_ = q.backend.Trash(ctx, []string{kept.FileID})
		cancel()
	}
	q.touchJournal()
}

// forget drops a finished job's slot: the file is complete, and only the
// checkpoint directory is left to clear.
func (q *Queue) forget(j *job) {
	q.mu.Lock()
	j.resume = nil
	dir := q.checkpointDir(j.ID)
	q.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// folderFor returns the server folder for dir under parentID, creating the
// missing levels. Empty dir is parentID itself.
func (q *Queue) folderFor(ctx context.Context, parentID, dir string) (string, error) {
	if dir == "" {
		return parentID, nil
	}
	q.folderMu.Lock()
	defer q.folderMu.Unlock()
	for _, name := range strings.Split(dir, "/") {
		key := parentID + "\x00" + name
		if id, ok := q.folders[key]; ok {
			parentID = id
			continue
		}
		folder, err := q.backend.EnsureFolder(ctx, parentID, name)
		if err != nil {
			return "", err
		}
		q.folders[key] = folder.ID
		parentID = folder.ID
	}
	return parentID, nil
}

// transfer pushes the file to OSS under the slot's grant, parts in parallel.
// Resuming, the SDK records the multipart upload in the job's checkpoint
// directory and, the next time, asks OSS which parts arrived and sends only
// the rest; otherwise it aborts the upload on failure so no orphan parts
// are left behind.
func (q *Queue) transfer(ctx context.Context, j *job, slot *resumeState, resumable bool) error {
	grant := slot.Grant
	cfg := oss.LoadDefaultConfig().
		WithEndpoint(grant.Endpoint).
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(grant.AccessKeyID, grant.AccessKeySecret, grant.SecurityToken)).
		// The grant names no region, which the V4 signer needs; V1 signs
		// against the host alone.
		WithSignatureVersion(oss.SignatureVersionV1).
		WithUseCName(grant.CName == "true").
		// The SDK's default transport proxies nothing; q.oss is the same
		// transport with the app's proxy setting on it.
		WithHttpClient(q.oss)
	checkpoint := ""
	if resumable {
		if j.direct != nil {
			checkpoint = j.direct.checkpoint
		} else {
			q.mu.Lock()
			checkpoint = q.checkpointDir(j.ID)
			q.mu.Unlock()
		}
		if err := os.MkdirAll(checkpoint, 0o700); err != nil {
			return err
		}
	}
	uploader := oss.NewUploader(oss.NewClient(cfg), func(o *oss.UploaderOptions) {
		o.PartSize = partSize
		o.ParallelNum = parallelParts
		if checkpoint != "" {
			o.EnableCheckpoint = true
			// The SDK keeps its file in the parent of what it is given.
			o.CheckpointDir = checkpoint + string(filepath.Separator)
		}
	})
	_, err := uploader.UploadFile(ctx, &oss.PutObjectRequest{
		Bucket: oss.Ptr(grant.Bucket),
		Key:    oss.Ptr(grant.Key),
		ProgressFn: func(_, transferred, _ int64) {
			q.progress(j, transferred)
		},
	}, j.Path)
	return err
}

func (q *Queue) setState(j *job, state string) {
	q.mu.Lock()
	j.State, j.Done, j.Speed, j.UpdatedAt = state, 0, 0, stamp()
	j.lastDone, j.lastAt = 0, time.Now()
	q.mu.Unlock()
	q.emit(j, true)
}

func (q *Queue) progress(j *job, done int64) {
	q.mu.Lock()
	j.Done = done
	if j.State == StateUploading {
		if since := time.Since(j.lastAt); since >= speedWindow {
			j.Speed = int64(float64(done-j.lastDone) / since.Seconds())
			j.lastDone, j.lastAt = done, time.Now()
		}
	}
	q.mu.Unlock()
	q.emit(j, false)
}

func (q *Queue) finish(j *job, fileID string, err error) {
	q.mu.Lock()
	j.Speed, j.UpdatedAt = 0, stamp()
	switch {
	case err == nil:
		j.State, j.FileID = StateDone, fileID
	case errors.Is(err, context.Canceled) && q.closing:
		j.State = StateInterrupted
	case errors.Is(err, context.Canceled):
		j.State = StateCancelled
	default:
		j.State, j.Error = StateFailed, err.Error()
	}
	q.mu.Unlock()
	q.emit(j, true)
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
	if !force && now.Sub(j.lastEmit) < emitInterval {
		q.mu.Unlock()
		return
	}
	j.lastEmit = now
	snapshot := q.view(j)
	direct := j.direct
	q.mu.Unlock()
	if direct != nil {
		if direct.progress != nil {
			direct.progress(snapshot)
		}
		return
	}
	q.onChange(snapshot)
	if force {
		q.touchJournal()
	}
}
