package lanshare

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

type taskRecord struct {
	Task
	Version       string                `json:"version"`
	RequestKey    string                `json:"requestKey"`
	Capabilities  lanmedia.Capabilities `json:"capabilities"`
	Created       time.Time             `json:"created"`
	Original      string                `json:"original"`
	Checkpoint    string                `json:"checkpoint"`
	OutputToken   string                `json:"outputToken"`
	Hold          bool                  `json:"hold"`
	DesktopHidden bool                  `json:"desktopHidden,omitempty"`
	Sessions      map[string]bool       `json:"-"`
	Desktop       bool                  `json:"desktop,omitempty"`
	cancel        context.CancelFunc
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) holdLocked(sid string, visitorsOnly bool) {
	for _, t := range m.tasks {
		if (sid != "" && t.SpaceID != sid) || (visitorsOnly && t.Desktop) {
			continue
		}
		switch t.State {
		case "queued", "downloading", "converting":
			t.Hold = true
			if t.State == "queued" {
				t.State, t.Error = "held", "permission_changed"
			}
		}
	}
}

func (m *Manager) PrepareDesktop(ctx context.Context, spaceID, fileID, kind string) (Task, error) {
	return m.prepare(ctx, prepareRequest{SpaceID: spaceID, FileID: fileID, Kind: kind, Capabilities: lanmedia.Capabilities{H264: true, AAC: true, MP4: true}}, "")
}

func (m *Manager) prepare(ctx context.Context, request prepareRequest, sessionID string) (Task, error) {
	if request.Kind != "original" && request.Kind != "compatible" {
		return Task{}, errInvalid
	}
	if _, err := m.space(request.SpaceID, sessionID != ""); err != nil {
		return Task{}, err
	}
	source, err := m.resolve(ctx, request.SpaceID, request.FileID)
	if err != nil {
		return Task{}, err
	}
	if source.Entry.IsDir || source.Entry.Version == "" || source.Entry.Size < 0 {
		return Task{}, errInvalid
	}
	key := resourceKey(request.SpaceID, request.FileID, source.Entry.Version) + "/" + request.Kind
	direct := false
	if request.Kind == "compatible" {
		if source.Entry.Ready {
			info, err := m.probe(ctx, request.SpaceID, source, "original")
			if err != nil {
				return Task{}, err
			}
			plan, err := lanmedia.MakePlan(info, request.Capabilities)
			if err != nil {
				return Task{}, err
			}
			direct = plan.Kind == "direct"
			key += "/" + planKey(plan)
		} else {
			caps, _ := json.Marshal(request.Capabilities)
			key += "/pending/" + string(caps)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || ctx.Err() != nil {
		if ctx.Err() != nil {
			return Task{}, ctx.Err()
		}
		return Task{}, errForbidden
	}
	if sessionID != "" && (!m.running || m.sessions[sessionID] == nil) {
		return Task{}, errForbidden
	}
	s := m.spaces[request.SpaceID]
	if s == nil || !s.Enabled {
		return Task{}, errForbidden
	}
	if sessionID != "" && !s.AllowPrepare {
		return Task{}, errPrepareDisabled
	}
	active := 0
	for _, t := range m.tasks {
		if t.State == "queued" || t.State == "downloading" || t.State == "converting" {
			active++
		}
		pendingMatch := request.Kind == "compatible" && (t.State == "queued" || t.State == "downloading" || t.State == "converting") && t.SpaceID == request.SpaceID && t.FileID == request.FileID && t.Version == source.Entry.Version && t.Kind == request.Kind && strings.HasPrefix(t.RequestKey, key+"/pending/")
		if t.RequestKey != key && !pendingMatch {
			continue
		}
		switch t.State {
		case "queued", "downloading", "converting":
			if t.Hold {
				continue
			}
			if sessionID == "" && !t.Desktop {
				t.Desktop = true
				if err := m.saveLocked(); err != nil {
					t.Desktop = false
					return Task{}, err
				}
			} else if sessionID != "" {
				t.Sessions[sessionID] = true
			}
			return t.Task, nil
		case "ready":
			if (request.Kind == "original" || direct) && source.Entry.Ready {
				return m.reuseReadyLocked(t, sessionID)
			}
			if request.Kind == "compatible" {
				cached, f := m.cachedLocked(resourceKey(request.SpaceID, request.FileID, source.Entry.Version))
				if f != nil {
					f.Close()
					if cached.Token == t.OutputToken {
						return m.reuseReadyLocked(t, sessionID)
					}
				}
			}
		}
	}
	if active >= 32 {
		return Task{}, errors.New("rate_limited")
	}
	// Bound persisted history while preserving held work and all retained original objects.
	var oldest *taskRecord
	if len(m.tasks) >= 256 {
		for _, t := range m.tasks {
			if t.State == "ready" || t.State == "cancelled" || t.State == "failed" {
				if oldest == nil || t.Created.Before(oldest.Created) {
					oldest = t
				}
			}
		}
		if oldest == nil {
			return Task{}, errors.New("rate_limited")
		}
		delete(m.tasks, oldest.ID)
	}
	t := &taskRecord{Task: Task{ID: randomID(), SpaceID: request.SpaceID, FileID: request.FileID, Name: source.Entry.Name, Kind: request.Kind, State: "queued", Total: source.Entry.Size}, Version: source.Entry.Version, RequestKey: key, Capabilities: request.Capabilities, Created: time.Now(), Sessions: make(map[string]bool), Desktop: sessionID == ""}
	if sessionID != "" {
		t.Sessions[sessionID] = true
	}
	if (request.Kind == "original" || direct) && source.Entry.Ready {
		t.State, t.Done = "ready", t.Total
	}
	m.tasks[t.ID] = t
	if err := m.saveLocked(); err != nil {
		delete(m.tasks, t.ID)
		if oldest != nil {
			m.tasks[oldest.ID] = oldest
		}
		return Task{}, err
	}
	m.signal()
	return t.Task, nil
}

func (m *Manager) reuseReadyLocked(t *taskRecord, sessionID string) (Task, error) {
	if t.DesktopHidden || (sessionID == "" && !t.Desktop) {
		hidden, desktop := t.DesktopHidden, t.Desktop
		t.DesktopHidden = false
		if sessionID == "" {
			t.Desktop = true
		}
		if err := m.saveLocked(); err != nil {
			t.DesktopHidden, t.Desktop = hidden, desktop
			return Task{}, err
		}
	}
	if sessionID != "" {
		delete(m.sessions[sessionID].DismissedTasks, t.ID)
	}
	return t.Task, nil
}

func (m *Manager) CancelTask(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return errInvalid
	}
	m.cancelTaskLocked(t)
	return m.saveLocked()
}

func (m *Manager) cancelTaskLocked(t *taskRecord) {
	if t.State == "ready" || t.State == "cancelled" {
		return
	}
	t.State, t.Error, t.Hold = "cancelled", "", false
	t.DesktopHidden = false
	clear(t.Sessions)
	if t.cancel != nil {
		t.cancel()
	}
}

func (m *Manager) cancelForSession(ctx context.Context, sid, id, sessionID string) error {
	s, err := m.space(sid, false)
	if err != nil {
		return err
	}
	if err = m.validateSpace(ctx, s); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, current := m.tasks[id], m.spaces[sid]
	if !m.running || m.sessions[sessionID] == nil || current == nil || !current.Enabled || ctx.Err() != nil || t == nil || t.SpaceID != sid || !t.Sessions[sessionID] {
		return errForbidden
	}
	delete(t.Sessions, sessionID)
	live := false
	for id := range t.Sessions {
		if m.sessions[id] != nil {
			live = true
		}
	}
	if !live && !t.Desktop {
		m.cancelTaskLocked(t)
	}
	return m.saveLocked()
}

func (m *Manager) ResumeTask(id string) error {
	m.mu.Lock()
	t := m.tasks[id]
	if t == nil || (t.State != "held" && t.State != "failed" && t.State != "cancelled") || t.cancel != nil {
		m.mu.Unlock()
		return errInvalid
	}
	sid, fid, version, state, desktop := t.SpaceID, t.FileID, t.Version, t.State, t.Desktop
	m.mu.Unlock()
	if _, err := m.space(sid, !desktop); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	source, err := m.resolve(ctx, sid, fid)
	if err != nil {
		return err
	}
	if source.Entry.Version != version {
		return errUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[sid]
	if m.closed || ctx.Err() != nil || s == nil || !s.Enabled || (!desktop && !s.AllowPrepare) {
		return errForbidden
	}
	if m.tasks[id] != t || t.State != state || t.Desktop != desktop || t.cancel != nil {
		return errInvalid
	}
	active := 0
	for _, other := range m.tasks {
		if other.State == "queued" || other.State == "downloading" || other.State == "converting" {
			active++
		}
	}
	if active >= 32 {
		return errors.New("rate_limited")
	}
	previous, wasDesktop, hold, hidden := t.Task, t.Desktop, t.Hold, t.DesktopHidden
	t.Desktop, t.Hold, t.DesktopHidden, t.State, t.Error = true, false, false, "queued", ""
	if err = m.saveLocked(); err != nil {
		t.Task, t.Desktop, t.Hold, t.DesktopHidden = previous, wasDesktop, hold, hidden
		return err
	}
	m.signal()
	return nil
}

func (m *Manager) stageAllowed(t *taskRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.State == "cancelled" {
		return context.Canceled
	}
	if t.Hold || m.closed {
		return errHeld
	}
	s := m.spaces[t.SpaceID]
	if s == nil || !s.Enabled {
		return errForbidden
	}
	if !t.Desktop && !s.AllowPrepare {
		return errPrepareDisabled
	}
	if !t.Desktop {
		if !m.running {
			return errHeld
		}
		authorized := false
		for id := range t.Sessions {
			if m.sessions[id] != nil {
				authorized = true
			}
		}
		if !authorized {
			return errHeld
		}
	}
	return nil
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		}
		for {
			m.mu.Lock()
			var next *taskRecord
			for _, t := range m.tasks {
				if t.State == "queued" && (next == nil || t.Created.Before(next.Created)) {
					next = t
				}
			}
			if next == nil {
				m.mu.Unlock()
				break
			}
			ctx, cancel := context.WithCancel(m.ctx)
			next.cancel = cancel
			m.mu.Unlock()
			err := m.runTask(ctx, next)
			cancel()
			m.mu.Lock()
			next.cancel = nil
			if next.State != "cancelled" {
				if err == nil {
					next.State, next.Error = "ready", ""
					next.Done = next.Total
				} else if next.Error == "disk_full" {
					next.State = "failed"
				} else if next.Hold || errors.Is(err, errHeld) || errors.Is(err, context.Canceled) || errors.Is(err, errForbidden) || errors.Is(err, errPrepareDisabled) || errors.Is(err, errUnavailable) {
					next.State = "held"
					if next.Error == "" {
						next.Error = "permission_changed"
						if errors.Is(err, errUnavailable) {
							next.Error = "source_unavailable"
						}
					}
				} else {
					next.State, next.Error = "failed", errorCode(err)
				}
			}
			_ = m.saveLocked()
			m.mu.Unlock()
			if m.ctx.Err() != nil {
				return
			}
		}
	}
}

func (m *Manager) setStage(t *taskRecord, state string, total int64) error {
	if err := m.stageAllowed(t); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Recheck under the transition lock so revocation cannot dispatch a later stage.
	s := m.spaces[t.SpaceID]
	if t.Hold || m.closed || t.State == "cancelled" || s == nil || !s.Enabled || (!t.Desktop && !s.AllowPrepare) || (!t.Desktop && !m.running) {
		return errHeld
	}
	if !t.Desktop {
		live := false
		for id := range t.Sessions {
			if m.sessions[id] != nil {
				live = true
				break
			}
		}
		if !live {
			return errHeld
		}
	}
	t.State, t.Done, t.Total, t.Error = state, 0, total, ""
	return m.saveLocked()
}

func (m *Manager) taskProgress(t *taskRecord, done, total int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.State == "cancelled" {
		return
	}
	t.Done, t.Total = max(0, done), max(0, total)
}

func (m *Manager) monitorTask(ctx context.Context, cancel context.CancelFunc, t *taskRecord) func() {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
			}
			probe, done := context.WithTimeout(ctx, sourceRecheckTimeout)
			source, err := m.resolve(probe, t.SpaceID, t.FileID)
			done()
			if err == nil && source.Entry.Version != t.Version {
				err = errUnavailable
			}
			if err != nil && ctx.Err() == nil {
				m.mu.Lock()
				t.Hold, t.Error = true, "source_unavailable"
				m.mu.Unlock()
				cancel()
				return
			}
			free, e := freeBytes(m.dir)
			m.mu.Lock()
			original, state := t.Original, t.State
			m.mu.Unlock()
			if e == nil && original != "" && state == "downloading" {
				originalFree, originalErr := freeBytes(filepath.Dir(original))
				if originalErr != nil {
					e = originalErr
				} else {
					free = min(free, originalFree)
				}
			}
			if e != nil || free < m.minFree {
				m.mu.Lock()
				t.Error = "disk_full"
				m.mu.Unlock()
				cancel()
				return
			}
		}
	}()
	return func() { close(stop); wg.Wait() }
}

func (m *Manager) runTask(parent context.Context, t *taskRecord) error {
	if err := m.stageAllowed(t); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	finishMonitor := m.monitorTask(ctx, cancel, t)
	defer func() {
		cancel()
		finishMonitor()
	}()
	source, err := m.resolve(ctx, t.SpaceID, t.FileID)
	if err != nil {
		return err
	}
	if source.Entry.Version != t.Version {
		return errUnavailable
	}
	if !source.Entry.Ready {
		s, err := m.space(t.SpaceID, false)
		if err != nil {
			return err
		}
		if s.Kind != "cloud" {
			return errNotReady
		}
		dir, err := m.originalStorage()
		if err != nil {
			return err
		}
		free, err := freeBytes(dir)
		if err != nil || free < source.Entry.Size+m.minFree {
			return errDiskFull
		}
		m.mu.Lock()
		if t.Original == "" {
			name := randomID() + "-" + safeName(source.Entry.Name)
			t.Original = filepath.Join(dir, name)
			t.Checkpoint = t.Original + ".checkpoint"
		}
		destination, checkpoint := t.Original, t.Checkpoint
		err = m.saveLocked()
		m.mu.Unlock()
		if err != nil {
			return err
		}
		if err = m.setStage(t, "downloading", source.Entry.Size); err != nil {
			return err
		}
		if err = m.validateSpace(ctx, s); err != nil {
			return err
		}
		completedToken, prepareErr := m.cloud.Prepare(ctx, s.AccountID, s.RootID, t.FileID, t.Version, destination, checkpoint, s.AccountIdentity, func(done, total int64) { m.taskProgress(t, done, total) })
		err = prepareErr
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			if isDiskFull(err) || errorCode(err) == "disk_full" {
				return errDiskFull
			}
			return sourceError(err)
		}
		f, token, err := OpenLocal(destination)
		if err != nil {
			return errUnavailable
		}
		stat, err := f.Stat()
		f.Close()
		if err != nil || completedToken == "" || token != completedToken || stat.Size() != source.Entry.Size {
			return errUnavailable
		}
		current, err := m.resolve(ctx, t.SpaceID, t.FileID)
		if err != nil || current.Entry.Version != t.Version {
			return errUnavailable
		}
		m.mu.Lock()
		// Cancellation never publishes a newly completed mapping; physical original bytes remain retained.
		if t.State == "cancelled" {
			m.mu.Unlock()
			return context.Canceled
		}
		m.originals[resourceKey(t.SpaceID, t.FileID, t.Version)] = &preparedFile{Path: destination, Token: token}
		err = m.saveLocked()
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if t.Kind == "original" {
		return nil
	}
	if err = m.stageAllowed(t); err != nil {
		return err
	}
	if err = m.setStage(t, "queued", 0); err != nil {
		return err
	}
	input, inputEntry, err := m.original(ctx, t.SpaceID, t.FileID, t.Version)
	if err != nil {
		return err
	}
	defer input.Close()
	_, inputToken, _, err := handleIdentity(input)
	if err != nil || inputToken != inputEntry.localToken {
		return errUnavailable
	}
	select {
	case m.probeSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	info, err := m.probeOpened(ctx, input, t.SpaceID, t.FileID, t.Version, "original", inputToken)
	<-m.probeSlot
	if err != nil {
		return err
	}
	plan, err := lanmedia.MakePlan(info, t.Capabilities)
	if err != nil {
		return err
	}
	m.mu.Lock()
	t.RequestKey = resourceKey(t.SpaceID, t.FileID, t.Version) + "/compatible/" + planKey(plan)
	existing, cachedFile := m.cachedLocked(resourceKey(t.SpaceID, t.FileID, t.Version))
	reused := cachedFile != nil && existing.PlanKey == planKey(plan)
	if cachedFile != nil {
		cachedFile.Close()
	}
	if reused {
		t.OutputToken = existing.Token
	}
	m.mu.Unlock()
	if reused {
		return nil
	}
	if plan.Kind == "direct" {
		return nil
	}
	if err = m.stageAllowed(t); err != nil {
		return err
	}
	// Preserve the last valid copy until its replacement has been fully validated.
	unpin := m.pinCurrentCopy(resourceKey(t.SpaceID, t.FileID, t.Version))
	defer unpin()
	// FFmpeg -fs can overshoot by its final packet. Keep headroom outside its output budget.
	const headroom int64 = 16 << 20
	// Budget copied streams from their input size, and re-encoded video well
	// above the configured 7.5 Mbit/s peak plus audio and muxing overhead.
	estimate := float64(source.Entry.Size)*1.1 + float64(32<<20)
	if plan.Kind == "video" {
		estimate = info.Duration*2_000_000 + float64(32<<20)
	} else if plan.Kind == "audio" {
		estimate += info.Duration * 32_000
	}
	budget := m.outputBudget(headroom, int64(min(float64(m.cacheLimit), estimate)))
	if budget < 1 {
		return errDiskFull
	}
	if err = m.reserve(budget + headroom); err != nil {
		return err
	}
	defer m.release(budget + headroom)
	plan.MaxBytes = budget
	suffix := ".mp4"
	if plan.Container == "webm" {
		suffix = ".webm"
	}
	outputDir, err := m.newOutputDirectory()
	if err != nil {
		return err
	}
	name := outputDir + "/compatible" + suffix
	output := filepath.Join(m.dir, name)
	published := false
	defer func() {
		if !published {
			m.mu.Lock()
			m.removePendingLocked(outputDir)
			_ = m.saveLocked()
			m.mu.Unlock()
		}
	}()
	if err = m.setStage(t, "converting", 0); err != nil {
		return err
	}
	err = m.media.ConvertFile(ctx, input, output, plan, nil)
	if err != nil {
		return err
	}
	_, afterToken, _, statErr := handleIdentity(input)
	if statErr != nil || afterToken != inputToken {
		return errUnavailable
	}
	current, err := m.resolve(ctx, t.SpaceID, t.FileID)
	if err != nil || current.Entry.Version != t.Version {
		return errUnavailable
	}
	f, token, err := OpenLocal(output)
	if err != nil {
		return errUnavailable
	}
	stat, err := f.Stat()
	f.Close()
	if err != nil || stat.Size() >= budget || stat.Size() == 0 {
		return errDiskFull
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.State == "cancelled" {
		return context.Canceled
	}
	key := resourceKey(t.SpaceID, t.FileID, t.Version)
	old := m.cache[key]
	retired := key + "/retired/" + randomID()
	if old != nil {
		m.cache[retired] = old
	}
	m.cache[key] = &cacheFile{Path: name, Token: token, Size: stat.Size(), Kind: plan.Kind, MIME: plan.MIME, PlanKey: planKey(plan), Used: time.Now()}
	pendingKey := m.pending[outputDir]
	delete(m.pending, outputDir)
	t.OutputToken = token
	if err = m.saveLocked(); err != nil {
		delete(m.cache, key)
		delete(m.cache, retired)
		if old != nil {
			m.cache[key] = old
		}
		m.pending[outputDir] = pendingKey
		return err
	}
	published = true
	return nil
}

func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, ". ")
	if len(name) > 120 {
		name = "original" + filepath.Ext(name)
	}
	if name == "" {
		name = "original"
	}
	return name
}

func (m *Manager) taskList(ctx context.Context, sid, sessionID string, activeOnly bool) ([]Task, error) {
	s, err := m.space(sid, false)
	if err != nil {
		return nil, err
	}
	if err = m.validateSpace(ctx, s); err != nil {
		return nil, err
	}
	m.mu.Lock()
	visitor := m.sessions[sessionID]
	if visitor == nil {
		m.mu.Unlock()
		return nil, errForbidden
	}
	records := make([]taskRecord, 0)
	for _, t := range m.tasks {
		if t.SpaceID == sid && (!activeOnly || !terminalTask(t.State)) && !(terminalTask(t.State) && visitor.DismissedTasks[t.ID]) {
			records = append(records, *t)
		}
	}
	m.mu.Unlock()
	result := []Task{}
	type proof struct {
		source Source
		err    error
	}
	proofs := make(map[string]proof)
	for _, t := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, ok := proofs[t.FileID]
		if !ok {
			p.source, p.err = m.resolve(ctx, sid, t.FileID)
			proofs[t.FileID] = p
		}
		source := p.source
		if p.err != nil || source.Entry.Version != t.Version {
			continue
		}
		if t.State == "ready" {
			if t.Kind == "original" && !source.Entry.Ready {
				t.State, t.Error = "failed", "not_ready"
			}
			if t.Kind == "compatible" && t.OutputToken != "" {
				m.mu.Lock()
				cache, f := m.cachedLocked(resourceKey(sid, t.FileID, t.Version))
				m.mu.Unlock()
				if f != nil {
					f.Close()
				}
				if cache == nil || cache.Token != t.OutputToken {
					t.State, t.Error = "failed", "not_ready"
				}
			}
		}
		result = append(result, t.Task)
	}
	return result, nil
}

func planKey(plan lanmedia.Plan) string {
	return plan.Kind + "/" + plan.Container + "/" + strconv.FormatBool(plan.CopyAudio)
}
