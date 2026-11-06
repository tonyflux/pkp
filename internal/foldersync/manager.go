package foldersync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/syncfs"
)

type runtimePair struct {
	record           pairRecord
	runMu            sync.Mutex
	cancel           context.CancelFunc
	wake             chan struct{}
	hints            chan struct{}
	stop             chan struct{}
	editing          bool
	started          bool
	broken           bool
	completedVersion string
	completedPaths   map[string]string
	completedPending map[string]struct{}
}

type pendingPreview struct {
	plan     *plan
	identity syncfs.Identity
	owner    string
	old      *runtimePair
	starting bool
}

type Manager struct {
	mu       sync.Mutex
	opts     Options
	pairs    map[string]*runtimePair
	previews map[string]*pendingPreview
	ctx      context.Context
	storeErr error
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closed   bool
}

func New(opts Options) (*Manager, error) {
	if opts.Dir == "" || opts.Backend == nil {
		return nil, i18n.New("同步存储或账号接口不可用")
	}
	if err := os.MkdirAll(opts.Dir, 0700); err != nil {
		return nil, err
	}
	m := &Manager{opts: opts, pairs: map[string]*runtimePair{}, previews: map[string]*pendingPreview{}}
	var saved manifest
	err := readJSON(filepath.Join(opts.Dir, "pairs.json"), &saved)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(ErrRepair, err)
	}
	if errors.Is(err, os.ErrNotExist) {
		entries, e := os.ReadDir(opts.Dir)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != "checks" {
				if _, e = os.Stat(filepath.Join(opts.Dir, entry.Name(), "removed.json")); errors.Is(e, os.ErrNotExist) {
					return nil, ErrRepair
				} else if e != nil {
					return nil, e
				}
			}
		}
	}
	if err == nil && saved.Version != 1 {
		return nil, ErrRepair
	}
	for _, v := range saved.Pairs {
		if v.Pair.ID == "" || m.pairs[v.Pair.ID] != nil {
			return nil, ErrRepair
		}
		v.Pair.Progress = Progress{}
		v.Pair.Phase = ""
		v.Baseline = map[string]baseline{}
		var b baselineFile
		err = readJSON(filepath.Join(opts.Dir, v.Pair.ID, "baseline-"+v.Version+".json"), &b)
		if err != nil || b.Version != v.Version || digest(b) != v.BaselineHash || !validBaseline(b.Entries) || v.Repair || v.Snapshot == nil || !v.Snapshot.Complete {
			v.Repair = true
			v.Pair.State = "repair"
			v.Pair.Error = ErrRepair.Error()
		} else {
			v.Baseline = b.Entries
			if v.Pair.Paused {
				v.Pair.State = "paused"
			} else if v.Pair.DeletePlan != nil {
				v.Pair.State = "delete-guard"
			} else if v.Pair.State == "scanning" || v.Pair.State == "syncing" || v.Pair.State == "pausing" {
				v.Pair.State = "idle"
			}
		}
		m.pairs[v.Pair.ID] = &runtimePair{record: v, wake: make(chan struct{}, 1), hints: make(chan struct{}, 1), stop: make(chan struct{})}
	}
	return m, nil
}

func validBaseline(base map[string]baseline) bool {
	if base == nil {
		return false
	}
	for p, b := range base {
		if p == "" || p == "." || filepath.IsAbs(p) || p == ".." || containsTraversal(p) {
			return false
		}
		for _, n := range []*Node{b.Local, b.Cloud} {
			if n != nil && (n.Path != p || !n.Directory && len(n.Hash) != 64) {
				return false
			}
		}
		if b.Preserved != "" && b.Preserved != "local" && b.Preserved != "cloud" {
			return false
		}
	}
	return true
}
func containsTraversal(p string) bool {
	for _, x := range filepath.SplitList(p) {
		if x == ".." {
			return true
		}
	}
	for _, x := range splitPath(p) {
		if x == ".." || x == "" {
			return true
		}
	}
	return false
}

func (m *Manager) List() []Pair { m.mu.Lock(); defer m.mu.Unlock(); return m.listLocked() }
func (m *Manager) listLocked() []Pair {
	out := make([]Pair, 0, len(m.pairs))
	for _, r := range m.pairs {
		out = append(out, clone(r.record.Pair))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (m *Manager) notify() {
	if m.opts.OnChange != nil {
		m.opts.OnChange(m.List())
	}
}
func (m *Manager) StartLoop(ctx context.Context) {
	m.mu.Lock()
	if m.ctx != nil || m.closed {
		m.mu.Unlock()
		return
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	for _, r := range m.pairs {
		m.startLocked(r)
	}
	m.mu.Unlock()
}
func (m *Manager) startLocked(r *runtimePair) {
	if m.ctx == nil || r.started || m.closed {
		return
	}
	r.started = true
	m.wg.Add(1)
	go m.loop(r)
}
func (m *Manager) loop(r *runtimePair) {
	defer m.wg.Done()
	m.mu.Lock()
	identity := r.record.Identity
	m.mu.Unlock()
	stopWatch, _ := syncfs.Watch(m.ctx, identity, func() {
		select {
		case r.hints <- struct{}{}:
		default:
		}
	})
	if stopWatch != nil {
		defer stopWatch()
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-r.stop:
			return
		case <-r.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(0)
		case <-r.hints:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(7 * time.Second)
		case <-timer.C:
			m.reconcile(r)
			timer.Reset(2 * time.Minute)
		}
	}
}
func (m *Manager) signal(r *runtimePair) {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func normalize(c Config) (Config, error) {
	if c.Account == "" || c.LocalPath == "" {
		return c, i18n.New("请选择同步账号和两端文件夹")
	}
	if c.Mode != Bidirectional && c.Mode != Backup && c.Mode != Download {
		return c, i18n.New("不支持的文件夹同步模式")
	}
	if c.Deletes == nil {
		c.Deletes = map[string]bool{Bidirectional: true, Backup: false, Download: false}
	} else {
		c.Deletes = clone(c.Deletes)
	}
	if c.ID == "" {
		c.ID = uid()
	}
	return c, nil
}
func resetBaseline(a, b Config) bool {
	return a.LocalPath != b.LocalPath || a.CloudID != b.CloudID || a.Account != b.Account || a.Mode != b.Mode || !a.Deletes[b.Mode] && b.Deletes[b.Mode]
}
func configProof(c Config) string { c.CloudPath = ""; c.Name = ""; return digest(c) }

func (m *Manager) BeginEdit(ctx context.Context, id string) error {
	m.mu.Lock()
	r := m.pairs[id]
	if r == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	prior := r.editing
	r.editing = true
	if r.cancel != nil {
		r.cancel()
	}
	m.mu.Unlock()
	if err := waitPair(ctx, r); err != nil {
		m.mu.Lock()
		r.editing = prior
		m.mu.Unlock()
		if !prior {
			m.signal(r)
		}
		return err
	}
	r.runMu.Lock()
	err := m.settleRemoval(ctx, r)
	r.runMu.Unlock()
	if err != nil {
		m.mu.Lock()
		r.record.Pair.State = classify(err)
		r.record.Pair.Error = err.Error()
		_ = m.saveLocked()
		m.mu.Unlock()
		m.notify()
		return err
	}
	return nil
}

func (m *Manager) Preview(ctx context.Context, c Config) (Preview, error) {
	var err error
	c, err = normalize(c)
	if err != nil {
		return Preview{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Preview{}, context.Canceled
	}
	old := m.pairs[c.ID]
	priorEdit := old != nil && old.editing
	if old != nil {
		old.editing = true
		if old.cancel != nil {
			old.cancel()
		}
	}
	m.mu.Unlock()
	keepEdit := false
	defer func() {
		if old != nil && !keepEdit {
			m.mu.Lock()
			old.editing = priorEdit
			m.mu.Unlock()
			if !priorEdit {
				m.signal(old)
			}
		}
	}()
	if old != nil {
		old.runMu.Lock()
		defer old.runMu.Unlock()
		m.mu.Lock()
		pending := false
		for _, op := range old.record.Operations {
			if op.Stage != "planned" || len(op.Children) > 0 {
				pending = true
				break
			}
		}
		m.mu.Unlock()
		if pending {
			return Preview{}, ErrUnknown
		}
	}
	id, err := syncfs.Identify(c.LocalPath)
	if err != nil {
		return Preview{}, err
	}
	c.LocalPath = id.Path
	var owner string
	var oldRoot *Node
	var previous *snapshot
	base := map[string]baseline{}
	if old != nil {
		m.mu.Lock()
		r := old.record
		owner = r.Owner
		if !resetBaseline(r.Pair.Config, c) && !r.Repair {
			base = clone(r.Baseline)
			previous = clone(r.Snapshot)
			oldRoot = new(r.Root)
		}
		m.mu.Unlock()
		if r.Pair.Account != c.Account {
			return Preview{}, ErrRepair
		}
	}
	s, b, err := m.collect(ctx, c, id, owner, oldRoot, previous)
	if err != nil {
		return Preview{}, err
	}
	if err = ctx.Err(); err != nil {
		return Preview{}, err
	}
	p := makePlan(c, id, s, base)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Preview{}, context.Canceled
	}
	for token, prior := range m.previews {
		if prior.plan.preview.Config.ID == c.ID {
			delete(m.previews, token)
		}
	}
	m.previews[p.preview.ID] = &pendingPreview{plan: p, identity: id, owner: b.AccountID(), old: old}
	keepEdit = true
	return clone(p.preview), nil
}

func (m *Manager) CancelPreview(id string) {
	m.mu.Lock()
	p := m.previews[id]
	delete(m.previews, id)
	var old *runtimePair
	if p != nil {
		old = p.old
	} else {
		old = m.pairs[id]
	}
	if old != nil {
		old.editing = false
	}
	m.mu.Unlock()
	if old != nil {
		m.signal(old)
	}
}

func (m *Manager) Start(ctx context.Context, previewID string) (Pair, *Preview, error) {
	m.mu.Lock()
	pending := m.previews[previewID]
	if pending == nil {
		m.mu.Unlock()
		return Pair{}, nil, ErrNotFound
	}
	if pending.starting {
		m.mu.Unlock()
		return Pair{}, nil, ErrChanged
	}
	pending.starting = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); pending.starting = false; m.mu.Unlock() }()
	c := pending.plan.preview.Config
	var base map[string]baseline
	var oldRoot *Node
	if pending.old != nil {
		pending.old.runMu.Lock()
		defer pending.old.runMu.Unlock()
		m.mu.Lock()
		r := pending.old.record
		if !resetBaseline(r.Pair.Config, c) && !r.Repair {
			base = clone(r.Baseline)
			oldRoot = new(r.Root)
		}
		m.mu.Unlock()
	}
	if base == nil {
		base = map[string]baseline{}
	}
	s, b, err := m.collect(ctx, c, pending.identity, pending.owner, oldRoot, pending.plan.snapshot)
	if err != nil {
		return Pair{}, nil, err
	}
	p := makePlan(c, pending.identity, s, base)
	if p.fingerprint != pending.plan.fingerprint {
		m.mu.Lock()
		delete(m.previews, previewID)
		m.previews[p.preview.ID] = &pendingPreview{plan: p, identity: pending.identity, owner: b.AccountID(), old: pending.old}
		m.mu.Unlock()
		return Pair{}, new(clone(p.preview)), nil
	}
	if p.preview.Blocked {
		return Pair{}, new(clone(p.preview)), ErrChanged
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.previews[previewID] != pending {
		return Pair{}, nil, context.Canceled
	}
	for key, other := range m.pairs {
		if key == c.ID {
			continue
		}
		if syncfs.Overlaps(pending.identity, other.record.Identity) || c.Account == other.record.Pair.Account && cloudOverlap(s.Root, other.record.Root) {
			return Pair{}, nil, ErrChanged
		}
	}
	r := pending.old
	if r == nil {
		r = &runtimePair{wake: make(chan struct{}, 1), hints: make(chan struct{}, 1), stop: make(chan struct{})}
		m.pairs[c.ID] = r
	}
	paused := r.record.Pair.Paused
	activities := r.record.Pair.Activities
	r.record = pairRecord{Pair: Pair{Config: c, State: "idle", Paused: paused, Issues: p.issues, Activities: activities}, Identity: pending.identity, Owner: b.AccountID(), Root: s.Root, Baseline: base, Snapshot: s}
	for _, o := range p.operations {
		o.ConfigVersion = configProof(c)
		r.record.Operations = append(r.record.Operations, o)
	}
	r.editing = false
	r.broken = false
	if paused {
		r.record.Pair.State = "paused"
	}
	for rel, ob := range p.observed {
		if ob.Local == nil && ob.Cloud == nil {
			delete(r.record.Baseline, rel)
		} else {
			r.record.Baseline[rel] = ob
		}
	}
	if err = m.saveBaselineLocked(r); err != nil {
		r.record.Pair.State = "repair"
		r.record.Pair.Error = err.Error()
		return Pair{}, nil, err
	}
	delete(m.previews, previewID)
	m.startLocked(r)
	m.signal(r)
	go m.notify()
	return clone(r.record.Pair), nil, nil
}

func (m *Manager) Pause(ctx context.Context, id string, paused bool) error {
	m.mu.Lock()
	r := m.pairs[id]
	if r == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	r.record.Pair.Paused = paused
	if paused {
		r.record.Pair.State = "pausing"
		if r.cancel != nil {
			r.cancel()
		}
	}
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if paused {
		if err = waitPair(ctx, r); err != nil {
			return err
		}
		m.mu.Lock()
		r.record.Pair.State = "paused"
		err = m.saveLocked()
		m.mu.Unlock()
	} else {
		m.signal(r)
	}
	m.notify()
	return err
}
func waitPair(ctx context.Context, r *runtimePair) error {
	done := make(chan struct{})
	go func() { r.runMu.Lock(); r.runMu.Unlock(); close(done) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
func (m *Manager) Scan(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	r := m.pairs[id]
	m.mu.Unlock()
	if r == nil {
		return ErrNotFound
	}
	m.signal(r)
	return nil
}
func (m *Manager) Remove(ctx context.Context, id string) error {
	m.mu.Lock()
	r := m.pairs[id]
	if r == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	r.record.Removing = true
	r.record.Pair.Paused = true
	if r.cancel != nil {
		r.cancel()
	}
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if err = waitPair(ctx, r); err != nil {
		return err
	}
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if err = m.settleRemoval(ctx, r); err != nil {
		m.mu.Lock()
		r.record.Pair.State = classify(err)
		r.record.Pair.Error = err.Error()
		_ = m.saveLocked()
		m.mu.Unlock()
		m.notify()
		return err
	}
	if err = m.releaseStage(ctx, r); err != nil {
		return err
	}
	// Accepted writes are verified before releasing either root relationship.
	m.mu.Lock()
	if err = atomicJSON(filepath.Join(m.opts.Dir, id, "removed.json"), r.record); err != nil {
		m.mu.Unlock()
		return err
	}
	delete(m.pairs, id)
	for key, p := range m.previews {
		if p.old == r {
			delete(m.previews, key)
		}
	}
	close(r.stop)
	err = m.saveLocked()
	m.mu.Unlock()
	m.notify()
	return err
}
func (m *Manager) RemoveAccount(ctx context.Context, account string) error {
	for _, p := range m.List() {
		if p.Account == account {
			if err := m.Remove(ctx, p.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.pairs {
		if r.cancel != nil {
			n++
		}
	}
	return n
}
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	for _, r := range m.pairs {
		if r.cancel != nil {
			r.cancel()
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}

func (m *Manager) reconcile(r *runtimePair) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	m.mu.Lock()
	if m.closed || r.editing || r.record.Removing || r.record.Pair.Paused || r.record.Repair || r.broken {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	r.cancel = cancel
	r.record.Pair.LastAttempt = stamp()
	r.record.Pair.State = "scanning"
	r.record.Pair.Phase = "scan"
	c := clone(r.record.Pair.Config)
	m.mu.Unlock()
	m.notify()
	defer func() {
		cancel()
		m.mu.Lock()
		r.cancel = nil
		r.record.Pair.Progress = Progress{}
		r.record.Pair.Phase = ""
		if r.record.Pair.Paused {
			r.record.Pair.State = "paused"
		}
		m.mu.Unlock()
		m.notify()
	}()
	fail := func(err error) {
		m.mu.Lock()
		r.record.Pair.State = classify(err)
		r.record.Pair.Error = err.Error()
		if errors.Is(err, ErrChanged) {
			kept := r.record.Operations[:0]
			for _, o := range r.record.Operations {
				if o.Stage != "planned" {
					kept = append(kept, o)
				}
			}
			r.record.Operations = kept
			r.record.Approved = ""
			r.record.Pair.State = "scanning"
			select {
			case r.hints <- struct{}{}:
			default:
			}
		}
		if errors.Is(err, ErrRepair) || errors.Is(err, syncfs.ErrRootChanged) {
			r.record.Repair = true
		}
		_ = m.saveLocked()
		m.mu.Unlock()
	}
	b, _, err := m.roots(ctx, c, r.record.Identity, r.record.Owner, new(r.record.Root))
	if err != nil {
		fail(err)
		return
	}
	if err = m.recover(ctx, r, b); err != nil {
		fail(err)
		return
	}
	s, b, err := m.collect(ctx, c, r.record.Identity, r.record.Owner, new(r.record.Root), r.record.Snapshot)
	if err != nil {
		fail(err)
		return
	}
	p := makePlan(c, r.record.Identity, s, r.record.Baseline)
	m.mu.Lock()
	r.record.Snapshot = s
	r.record.Pair.CloudPath = s.Root.Path
	r.record.Pair.Issues = p.issues
	r.record.Pair.Error = ""
	guard := p.preview.Error != ""
	if guard && r.record.Approved != p.fingerprint {
		r.record.Pair.State = "delete-guard"
		r.record.Pair.DeletePlan = new(clone(p.preview))
		_ = m.saveLocked()
		m.mu.Unlock()
		return
	}
	r.record.Pair.DeletePlan = nil
	for rel, ob := range p.observed {
		if ob.Local == nil && ob.Cloud == nil {
			delete(r.record.Baseline, rel)
		} else {
			r.record.Baseline[rel] = ob
		}
	}
	for _, o := range p.operations {
		o.ConfigVersion = configProof(c)
		r.record.Operations = append(r.record.Operations, o)
	}
	if err = m.saveBaselineLocked(r); err != nil {
		m.mu.Unlock()
		fail(err)
		return
	}
	r.record.Pair.State = "syncing"
	r.record.Pair.Phase = "transfer"
	r.record.Pair.Progress.Files = len(p.operations)
	m.mu.Unlock()
	m.notify()
	for _, o := range p.operations {
		if err = ctx.Err(); err != nil {
			return
		}
		m.mu.Lock()
		stop := r.editing || r.record.Pair.Paused || r.record.Removing
		m.mu.Unlock()
		if stop {
			return
		}
		if err = m.execute(ctx, r, b, o); err != nil {
			fail(err)
			return
		}
	}
	m.mu.Lock()
	r.record.Pair.State = "idle"
	r.record.Pair.Error = ""
	r.record.Pair.LastSuccess = stamp()
	r.record.Approved = ""
	_ = m.saveLocked()
	m.mu.Unlock()
}

func (m *Manager) ApproveDeletes(ctx context.Context, id, previewID string) error {
	m.mu.Lock()
	r := m.pairs[id]
	if r == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	wanted := r.record.Pair.DeletePlan
	if wanted == nil || wanted.ID != previewID {
		m.mu.Unlock()
		return ErrChanged
	}
	m.mu.Unlock()
	r.runMu.Lock()
	defer r.runMu.Unlock()
	s, _, err := m.collect(ctx, r.record.Pair.Config, r.record.Identity, r.record.Owner, new(r.record.Root), r.record.Snapshot)
	if err != nil {
		return err
	}
	p := makePlan(r.record.Pair.Config, r.record.Identity, s, r.record.Baseline)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.preview.ID != previewID {
		r.record.Pair.DeletePlan = new(clone(p.preview))
		return ErrChanged
	}
	r.record.Approved = p.fingerprint
	if err = m.saveLocked(); err != nil {
		return err
	}
	m.signal(r)
	return nil
}
