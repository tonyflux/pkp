package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/syncfs"
)

// What a download stops on, as the list shows it.
var (
	errGone     = i18n.New("云端文件已不存在")
	ErrChanged  = i18n.New("云端文件与开始下载时不一致，请重新下载")
	errMismatch = i18n.New("服务器返回的数据范围与请求不符")
	errNoSpace  = i18n.New("磁盘空间不足")
	errBusy     = i18n.New("这个账号同时下载的连接已满（PikPak 限制），请稍后点「继续」")
	errStalled  = i18n.New("连接 30 秒没有收到数据")
	errDropped  = i18n.New("服务器提前断开了连接")
	errArchived = i18n.New("云端文件处于归档存储，暂时取不出数据")
)

// linkError is the CDN turning the link down: it expired or was revoked,
// and a new one from the drive may work.
type linkError struct {
	status int
	link   string
}

func (e linkError) Error() string { return i18n.F("下载链接被拒绝（HTTP %d）", e.status) }

// busyError is the CDN refusing another connection (503, or 429): too many
// at once on this link or for this user.
type busyError struct{ after time.Duration }

func (busyError) Error() string { return i18n.T("服务器繁忙") }

// retryable is a connection that broke off; the same request may work.
type retryable struct{ err error }

func (e retryable) Error() string { return e.err.Error() }
func (e retryable) Unwrap() error { return e.err }

// attempt is one run of one file: its open partial file and link, and the
// threads fetching into it.
type attempt struct {
	q       *Queue
	j       *job
	f       *os.File
	size    int64
	threads int

	// lmu guards the link, its deadline and the refresh count.
	lmu       sync.Mutex
	link      string
	expire    time.Time
	refreshes int
	// mark is received when refreshes was last reset: bytes arriving
	// since mean the links work.
	mark int64

	// received counts bytes written this attempt, for the speed and to
	// tell whether anything is getting through.
	received atomic.Int64
	// inflight counts requests holding a connection token.
	inflight atomic.Int32
	// verdict is closed once the first bytes land — so the server serves
	// this file, in ranges or whole — or once the first thread is gone.
	verdict chan struct{}
	decided sync.Once
	// ranged is set by a 206; single when the server ignored the range
	// and the whole file comes through one connection.
	ranged, single atomic.Bool
}

// download fetches j's file into its partial file beside j.Path, threads
// ranges at once, and renames it into place once every byte is there.
// Stopped early, it leaves the partial file and j.parts as they are, so the
// next attempt continues.
func (q *Queue) download(ctx context.Context, j *job, threads int) error {
	if j.direct != nil {
		return q.downloadDirect(ctx, j, threads)
	}
	link, expire, size, err := q.link(ctx, j.FileID)
	if err != nil {
		return err
	}
	q.mu.Lock()
	j.pmu.Lock()
	if j.parts != nil && size != j.Size {
		// The file changed on the drive: what is on disk is not part of it.
		j.parts = nil
	}
	resume := j.parts != nil
	need := size - j.written()
	j.pmu.Unlock()
	j.Size = size
	path := j.Path
	q.mu.Unlock()

	partial := path + PartialExt
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return i18n.Errorf("无法创建文件夹：%v", err)
	}
	var f *os.File
	if resume {
		if f = reopen(partial, size); f == nil {
			resume, need = false, size
		}
	}
	if free, err := freeSpace(dir); err == nil && free < uint64(need)+spaceMargin {
		if f != nil {
			_ = f.Close()
		}
		return i18n.Errorf("磁盘空间不足：还需 %s，可用 %s", humanBytes(need), humanBytes(int64(free)))
	}
	if !resume {
		if f, err = create(partial, size); err != nil {
			return err
		}
		j.pmu.Lock()
		j.parts = plan(size, threads)
		j.pmu.Unlock()
	}

	a := &attempt{q: q, j: j, f: f, size: size, threads: threads, link: link, expire: usable(expire), verdict: make(chan struct{})}
	err = a.run(ctx)
	if serr := f.Sync(); err == nil && serr != nil {
		err = i18n.Errorf("写入本地文件失败：%v", serr)
	}
	if cerr := f.Close(); err == nil && cerr != nil {
		err = i18n.Errorf("写入本地文件失败：%v", cerr)
	}
	if err != nil {
		return err
	}
	j.pmu.Lock()
	rest := len(j.parts)
	j.pmu.Unlock()
	if rest > 0 {
		return i18n.New("下载没有完成")
	}
	return q.place(j, partial)
}

// reopen is the partial file to continue in, or nil when it is gone or no
// longer the file's size.
func reopen(partial string, size int64) *os.File {
	f, err := os.OpenFile(partial, os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	if info, err := f.Stat(); err != nil || info.Size() != size {
		_ = f.Close()
		return nil
	}
	return f
}

// create makes an empty partial file of the file's size, sparse so ranges
// far apart cost no writes in between.
func create(partial string, size int64) (*os.File, error) {
	f, err := os.OpenFile(partial, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, i18n.Errorf("无法创建本地文件：%v", err)
	}
	// A file system without sparse files fills the gaps itself, slower.
	_ = sparse(f)
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		if isNoSpace(err) {
			return nil, errNoSpace
		}
		return nil, i18n.Errorf("无法创建本地文件：%v", err)
	}
	return f, nil
}

// place renames the finished partial file to the job's path, numbering the
// name again if something took it meanwhile.
func (q *Queue) place(j *job, partial string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	path := j.Path
	for {
		err := syncfs.RenameNoReplace(partial, path)
		if err == nil {
			j.Path, j.Name = path, filepath.Base(path)
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return i18n.Errorf("无法保存下载的文件：%v", err)
		}
		if path, err = q.claimLocked(filepath.Dir(path), j.Name); err != nil {
			return err
		}
	}
}

// link asks the drive for a fresh download link to the file, with its
// deadline (zero when there is none to read) and the file's size.
func (q *Queue) link(ctx context.Context, fileID string) (string, time.Time, int64, error) {
	f, err := q.backend.GetFile(ctx, fileID, drive.GetFileParams{Usage: drive.UsageFetch})
	return fileLink(f, err)
}

func fileLink(f *drive.File, err error) (string, time.Time, int64, error) {
	switch {
	case drive.IsNotFound(err):
		return "", time.Time{}, 0, errGone
	case err != nil:
		return "", time.Time{}, 0, err
	case f == nil:
		return "", time.Time{}, 0, errGone
	case f.Trashed:
		return "", time.Time{}, 0, i18n.New("云端文件已在回收站里")
	}
	link := f.ContentLink()
	if link == "" {
		return "", time.Time{}, 0, i18n.New("云端还没有可下载的链接，文件可能尚未保存完成")
	}
	expire, _ := time.Parse(time.RFC3339, f.Links["application/octet-stream"].Expire)
	return link, expire, int64(f.Size), nil
}

// usable is a link's deadline as far as renewing it goes: one already within
// linkMargin counts as unknown, or every request would renew it.
func usable(expire time.Time) time.Time {
	if time.Until(expire) <= linkMargin {
		return time.Time{}
	}
	return expire
}

// run starts one thread and waits for its first bytes: when the server
// serves ranges the other threads join, otherwise the one thread fetches the
// whole file. Waiting for bytes rather than headers keeps a file the CDN
// cannot serve (an archived one) to one request. The first thread to fail
// stops the rest, and its error is the attempt's.
func (a *attempt) run(ctx context.Context) error {
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	spawn := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer a.decide()
			if err := a.work(ctx); err != nil {
				once.Do(func() { first = err })
				stop(err)
			}
		}()
	}
	quit, watched := make(chan struct{}), make(chan struct{})
	go a.watch(quit, watched, stop)
	spawn()
	select {
	case <-a.verdict:
	case <-ctx.Done():
	}
	if !a.single.Load() && ctx.Err() == nil {
		for range a.threads - 1 {
			spawn()
		}
	}
	wg.Wait()
	close(quit)
	<-watched
	if first == nil {
		first = context.Cause(ctx)
	}
	if a.j.direct != nil {
		if err := a.q.saveDirect(a.j, a.f); err != nil {
			first = errors.Join(first, err)
		}
	}
	return first
}

func (a *attempt) decide() {
	a.decided.Do(func() { close(a.verdict) })
}

// watch reports progress every emitInterval and saves the ranges every
// saveEvery until quit closes.
func (a *attempt) watch(quit <-chan struct{}, done chan<- struct{}, stop context.CancelCauseFunc) {
	defer close(done)
	tick := time.NewTicker(emitInterval)
	defer tick.Stop()
	saved := time.Now()
	for {
		select {
		case <-quit:
			return
		case <-tick.C:
			a.q.progress(a.j, a.received.Load(), int(a.inflight.Load()))
			if time.Since(saved) >= saveEvery {
				saved = time.Now()
				if a.j.direct != nil {
					if err := a.q.saveDirect(a.j, a.f); err != nil {
						stop(err)
						return
					}
					continue
				}
				a.q.touchJournal()
			}
		}
	}
}

// work is one thread: it takes ranges until none is left to take or split.
func (a *attempt) work(ctx context.Context) error {
	buf := make([]byte, bufSize)
	for {
		p := a.next()
		if p == nil {
			return nil
		}
		if err := a.drain(ctx, &p, buf); err != nil {
			a.j.pmu.Lock()
			p.busy = false
			a.j.pmu.Unlock()
			return err
		}
	}
}

// drain fetches *pp to its end, retrying what is worth retrying: a rejected
// link after renewing it, a busy CDN after backing off, a broken connection
// after a pause that grows with every failure in a row.
func (a *attempt) drain(ctx context.Context, pp **part, buf []byte) error {
	var (
		failures, busy int
		busySince      time.Time
		busyMark       int64
	)
	for {
		select {
		case a.q.conns <- struct{}{}:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		a.inflight.Add(1)
		progressed, err := a.fetch(ctx, pp, buf)
		a.inflight.Add(-1)
		<-a.q.conns
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if progressed {
			failures = 0
		}
		var (
			le    linkError
			be    busyError
			re    retryable
			delay time.Duration
		)
		switch {
		case errors.As(err, &le):
			if err := a.refresh(ctx, le); err != nil {
				return err
			}
			continue
		case errors.As(err, &be):
			// Give up only when the whole file stands still that long.
			if got := a.received.Load(); busySince.IsZero() || got != busyMark {
				busySince, busyMark, busy = time.Now(), got, 0
			}
			if time.Since(busySince) >= busyGiveUp {
				return errBusy
			}
			delay = busyDelays[min(busy, len(busyDelays)-1)]
			busy++
			if be.after > 0 {
				delay = min(be.after, busyDelays[len(busyDelays)-1])
			}
		case errors.As(err, &re):
			failures++
			if failures > maxFailures {
				return i18n.Errorf("网络连接反复中断：%v", re.err)
			}
			delay = retryDelays[min(failures-1, len(retryDelays)-1)]
		default:
			return err
		}
		if err := sleep(ctx, delay); err != nil {
			return err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// fetch requests what is left of *pp and writes it as it arrives, reporting
// whether any of it landed. A server that ignores the range sends the whole
// file: then *pp becomes one range over all of it, from the top.
func (a *attempt) fetch(ctx context.Context, pp **part, buf []byte) (progressed bool, err error) {
	link, err := a.current(ctx)
	if err != nil {
		return false, err
	}
	rctx, abandon := context.WithCancelCause(ctx)
	defer abandon(nil)
	dog := time.AfterFunc(stallTimeout, func() { abandon(errStalled) })
	defer dog.Stop()

	req, err := http.NewRequestWithContext(rctx, http.MethodGet, link, nil)
	if err != nil {
		return false, err
	}
	if a.q.agent != "" {
		req.Header.Set("User-Agent", a.q.agent)
	}
	p := *pp
	a.j.pmu.Lock()
	pos, end := p.Pos, p.End
	a.j.pmu.Unlock()
	if !a.single.Load() {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", pos, end-1))
	}
	if a.j.direct != nil {
		a.j.pmu.Lock()
		etag := a.j.direct.etag
		a.j.pmu.Unlock()
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
	}
	resp, err := a.q.client.Do(req)
	if err != nil {
		return false, cut(ctx, rctx, err)
	}
	defer resp.Body.Close()
	if a.j.direct != nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
		etag := resp.Header.Get("ETag")
		a.j.pmu.Lock()
		changed := a.j.direct.etag != "" && a.j.direct.etag != etag
		if a.j.direct.etag == "" && !strings.HasPrefix(etag, "W/") {
			a.j.direct.etag = etag
		}
		a.j.pmu.Unlock()
		if changed {
			return false, ErrChanged
		}
	}

	switch code := resp.StatusCode; {
	case code == http.StatusPartialContent:
		start, total, ok := contentRange(resp.Header.Get("Content-Range"))
		if !ok || start != pos {
			return false, errMismatch
		}
		if total >= 0 && total != a.size {
			return false, ErrChanged
		}
		a.ranged.Store(true)
	case code == http.StatusOK:
		if a.ranged.Load() {
			return false, retryable{i18n.New("服务器没有按分段返回数据")}
		}
		if resp.ContentLength >= 0 && resp.ContentLength != a.size {
			return false, ErrChanged
		}
		p = a.whole()
		*pp = p
	case code == http.StatusUnauthorized || code == http.StatusForbidden || code == http.StatusGone:
		return false, linkError{status: code, link: link}
	case code == http.StatusNotFound:
		return false, errGone
	case code == http.StatusRequestedRangeNotSatisfiable || code == http.StatusPreconditionFailed:
		return false, ErrChanged
	case code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable:
		return false, busyError{after: retryAfter(resp.Header.Get("Retry-After"))}
	case code >= 500:
		return false, retryable{i18n.Errorf("服务器暂时出错（HTTP %d）", code)}
	default:
		return false, i18n.Errorf("下载失败（HTTP %d）", code)
	}

	body := stallReader{r: resp.Body, dog: dog}
	for {
		a.j.pmu.Lock()
		pos, end := p.Pos, p.End
		a.j.pmu.Unlock()
		// End moves in when another thread takes over the back of the range.
		if pos >= end {
			return progressed, nil
		}
		n, rerr := io.ReadFull(body, buf[:min(int64(len(buf)), end-pos)])
		if n > 0 {
			if _, werr := a.f.WriteAt(buf[:n], pos); werr != nil {
				if isNoSpace(werr) {
					return progressed, errNoSpace
				}
				return progressed, i18n.Errorf("写入本地文件失败：%v", werr)
			}
			a.advance(p, int64(n))
			a.decide()
			progressed = true
		}
		if rerr != nil {
			// An archived object answers with its headers and not a byte;
			// asking again only gets the same until it is restored.
			if ctx.Err() == nil && !progressed && archived(resp.Header) {
				return false, errArchived
			}
			return progressed, cut(ctx, rctx, rerr)
		}
	}
}

// advance records n more bytes of p on disk; a finished range leaves the job.
func (a *attempt) advance(p *part, n int64) {
	a.j.pmu.Lock()
	p.Pos += n
	if p.Pos >= p.End {
		a.j.parts = slices.DeleteFunc(a.j.parts, func(o *part) bool { return o == p })
	}
	a.j.pmu.Unlock()
	a.received.Add(n)
}

// whole gives up on ranges: one range over the whole file, fetched from the
// top by the one thread there is.
func (a *attempt) whole() *part {
	p := &part{End: a.size, busy: true}
	a.j.pmu.Lock()
	a.j.parts = []*part{p}
	a.j.pmu.Unlock()
	a.single.Store(true)
	return p
}

// current is the link to request, renewed first when it is about to expire.
func (a *attempt) current(ctx context.Context) (string, error) {
	a.lmu.Lock()
	defer a.lmu.Unlock()
	if a.expire.IsZero() || time.Until(a.expire) > linkMargin {
		return a.link, nil
	}
	if err := a.renewLocked(ctx); err != nil {
		return "", err
	}
	return a.link, nil
}

// refresh replaces a link the CDN turned down, once for all the threads that
// were using it. A link turned down maxRefreshes times without a byte
// arriving in between is not the link's fault.
func (a *attempt) refresh(ctx context.Context, le linkError) error {
	a.lmu.Lock()
	defer a.lmu.Unlock()
	if a.link != le.link {
		return nil
	}
	if got := a.received.Load(); got != a.mark {
		a.refreshes, a.mark = 0, got
	}
	if a.refreshes >= maxRefreshes {
		return i18n.Errorf("下载链接被拒绝（HTTP %d），请稍后点「继续」", le.status)
	}
	a.refreshes++
	return a.renewLocked(ctx)
}

// renewLocked fetches a new link; lmu must be held.
func (a *attempt) renewLocked(ctx context.Context) error {
	link, expire, size, err := a.q.linkFor(ctx, a.j)
	if err != nil {
		return err
	}
	if size != a.size {
		return ErrChanged
	}
	a.link, a.expire = link, usable(expire)
	return nil
}

// archived says the object behind the link is in archive storage: OSS and
// TOS name the class in a header ("Archive", "ColdArchive", "ARCHIVE",
// "COLD_ARCHIVE"…), and such an object sends no bytes until restored.
func archived(h http.Header) bool {
	class := h.Get("X-Oss-Storage-Class") + h.Get("X-Tos-Storage-Class")
	return strings.Contains(strings.ToLower(class), "archive")
}

// cut names why a request broke off: the job stopping, the stall watchdog,
// or the connection, which is worth another try.
func cut(ctx, rctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return context.Cause(ctx)
	case errors.Is(context.Cause(rctx), errStalled):
		return retryable{errStalled}
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		return retryable{errDropped}
	}
	return retryable{err}
}

// stallReader re-arms the stall watchdog whenever bytes arrive.
type stallReader struct {
	r   io.Reader
	dog *time.Timer
}

func (s stallReader) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	if n > 0 {
		s.dog.Reset(stallTimeout)
	}
	return n, err
}

// contentRange reads "bytes first-last/total"; total is -1 for "*".
func contentRange(h string) (first, total int64, ok bool) {
	rest, found := strings.CutPrefix(h, "bytes ")
	if !found {
		return 0, 0, false
	}
	span, size, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	from, _, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	first, err := strconv.ParseInt(from, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if size == "*" {
		return first, -1, true
	}
	total, err = strconv.ParseInt(size, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return first, total, true
}

// retryAfter reads a Retry-After in seconds; 0 when absent or a date.
func retryAfter(h string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}

// humanBytes renders a byte count for messages: "1.5 GB".
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n) / 1024
	units := []string{"KB", "MB", "GB", "TB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
