//go:build windows || (darwin && cgo)

package lanshare

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

func TestDesktopPreparationWithoutVisitorPermission(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("listing: %+v %v", entries, err)
	}
	if _, err = m.PrepareDesktop(context.Background(), s.ID, "file", "invalid"); !errors.Is(err, errInvalid) {
		t.Fatalf("kind: %v", err)
	}
	if _, err = m.PrepareDesktop(context.Background(), s.ID, "other", "original"); err == nil {
		t.Fatal("out-of-scope file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = m.PrepareDesktop(ctx, s.ID, "file", "original"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
	task, err := m.PrepareDesktop(context.Background(), s.ID, "file", "original")
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	cookie := authenticated(t, m)
	if _, err = m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "original"}, cookie); !errors.Is(err, errPrepareDisabled) {
		t.Fatalf("visitor permission: %v", err)
	}
	if err = m.Stop(); err != nil {
		t.Fatal(err)
	}
	cloud.release <- struct{}{}
	waitState(t, m, task.ID, "ready")
	src, err := m.resolve(context.Background(), s.ID, "file")
	if err != nil || !src.Entry.Ready {
		t.Fatalf("original: %+v %v", src, err)
	}
	body, err := os.ReadFile(src.LocalPath)
	if err != nil || string(body) != "data" {
		t.Fatalf("original bytes: %q %v", body, err)
	}
	if m.Status().Spaces[0].AllowPrepare {
		t.Fatal("visitor permission changed")
	}
}

func TestDesktopAndVisitorDeduplicateAndRevoke(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	visitor := authenticated(t, m)
	guest, err := m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "original"}, visitor)
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	const n = 12
	var wg sync.WaitGroup
	results := make(chan Task, n)
	failures := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := m.PrepareDesktop(context.Background(), s.ID, "file", "original")
			if err != nil {
				failures <- err
			} else {
				results <- task
			}
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	for task := range results {
		if task.ID != guest.ID {
			t.Fatalf("duplicate: %+v", task)
		}
	}
	if err = m.cancelForSession(context.Background(), s.ID, guest.ID, visitor); err != nil {
		t.Fatal(err)
	}
	if _, err = m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "original"}, visitor); err != nil {
		t.Fatal(err)
	}
	if err = m.UpdateSpace(SpaceUpdate{ID: s.ID, Name: s.Name, Enabled: true, AllowPrepare: false}); err != nil {
		t.Fatal(err)
	}
	if err = m.cancelForSession(context.Background(), s.ID, guest.ID, visitor); !errors.Is(err, errForbidden) {
		t.Fatalf("revoked subscriber: %v", err)
	}
	if err = m.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cloud.cancelled:
		t.Fatal("desktop task cancelled")
	default:
	}
	cloud.release <- struct{}{}
	waitState(t, m, guest.ID, "ready")
	cloud.mu.Lock()
	calls := cloud.calls
	cloud.mu.Unlock()
	if calls != 1 {
		t.Fatalf("download count: %d", calls)
	}
}

func TestDesktopAdoptsPreviouslyReadyVisitorTask(t *testing.T) {
	m := newTestManager(t, nil)
	s, _ := localSpace(t, m, true)
	visitor := authenticated(t, m)
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	guest, err := m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: entries[0].ID, Kind: "original"}, visitor)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.DismissTaskHistory(s.ID, guest.ID); err != nil {
		t.Fatal(err)
	}
	owner, err := m.PrepareDesktop(context.Background(), s.ID, entries[0].ID, "original")
	if err != nil || owner.ID != guest.ID {
		t.Fatalf("owner did not reuse visitor task: %+v %v", owner, err)
	}
	m.mu.Lock()
	desktop := m.tasks[guest.ID].Desktop
	m.mu.Unlock()
	if !desktop || len(m.Tasks()) != 1 {
		t.Fatal("owner reuse did not restore and retain desktop ownership")
	}
}

func TestDesktopDisabledRemovedAndRestartResume(t *testing.T) {
	cloud := newCloud()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{ConfigDir: dir, Cloud: cloud, MinFreeBytes: 1}
	m, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.PrepareDesktop(context.Background(), s.ID, "file", "original")
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	if err = m.UpdateSpace(SpaceUpdate{ID: s.ID, Name: s.Name, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.cancelled)
	waitState(t, m, task.ID, "held")
	if _, err = m.ListPreparationEntries(context.Background(), s.ID, ""); err == nil {
		t.Fatal("disabled listing")
	}
	if _, err = m.PrepareDesktop(context.Background(), s.ID, "file", "original"); err == nil {
		t.Fatal("disabled prepare")
	}
	if err = m.UpdateSpace(SpaceUpdate{ID: s.ID, Name: s.Name, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	waitState(t, m, task.ID, "held")
	select {
	case <-cloud.started:
		t.Fatal("restart auto-resumed")
	default:
	}
	if err = m.ResumeTask(task.ID); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	cloud.release <- struct{}{}
	waitState(t, m, task.ID, "ready")
	if err = m.RemoveSpace(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.PrepareDesktop(context.Background(), s.ID, "file", "original"); err == nil {
		t.Fatal("removed prepare")
	}
}

func TestDesktopLocalScopeAndCompatibleTarget(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, false)
	other, _ := localSpace(t, m, false)
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("list: %+v %v", entries, err)
	}
	if _, err = m.PrepareDesktop(context.Background(), other.ID, entries[0].ID, "original"); err == nil {
		t.Fatal("cross-space id accepted")
	}
	before, err := os.ReadFile(filepath.Join(root, "inside.txt"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.PrepareDesktop(context.Background(), s.ID, entries[0].ID, "original")
	if err != nil || task.State != "ready" {
		t.Fatalf("local original: %+v %v", task, err)
	}
	reused, err := m.PrepareDesktop(context.Background(), s.ID, entries[0].ID, "original")
	if err != nil || reused.ID != task.ID {
		t.Fatalf("desktop ready reuse: %+v %v", reused, err)
	}
	if err = m.DismissTaskHistory(s.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	reused, err = m.PrepareDesktop(context.Background(), s.ID, entries[0].ID, "original")
	if err != nil || reused.ID != task.ID || len(m.Tasks()) != 1 {
		t.Fatalf("hidden desktop ready reuse: %+v %v", reused, err)
	}
	after, err := os.ReadFile(filepath.Join(root, "inside.txt"))
	if err != nil || string(before) != string(after) {
		t.Fatal("source changed")
	}
	cloud := newCloud()
	m2 := newTestManager(t, cloud)
	cs, err := m2.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	compatible, err := m2.PrepareDesktop(context.Background(), cs.ID, "file", "compatible")
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	m2.mu.Lock()
	caps := m2.tasks[compatible.ID].Capabilities
	m2.mu.Unlock()
	if caps != (lanmedia.Capabilities{H264: true, AAC: true, MP4: true}) {
		t.Fatalf("capabilities: %+v", caps)
	}
	if err = m2.CancelTask(compatible.ID); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.cancelled)
}

func TestDesktopActiveLimitAndSourceFailure(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	for range 32 {
		id := randomID()
		m.tasks[id] = &taskRecord{Task: Task{ID: id, SpaceID: s.ID, State: "downloading"}, Created: time.Now()}
	}
	m.mu.Unlock()
	if _, err = m.PrepareDesktop(context.Background(), s.ID, "file", "original"); err == nil || err.Error() != "rate_limited" {
		t.Fatalf("limit: %v", err)
	}
	cloud.mu.Lock()
	cloud.allowed = false
	cloud.mu.Unlock()
	if _, err = m.ListPreparationEntries(context.Background(), s.ID, ""); err == nil {
		t.Fatal("unavailable listing")
	}
	if _, err = m.PrepareDesktop(context.Background(), s.ID, "file", "original"); err == nil {
		t.Fatal("unavailable prepare")
	}
}

func TestDesktopPreparationPersistenceRollback(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A", AllowPrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	visitor := authenticated(t, m)
	guest, err := m.prepare(context.Background(), prepareRequest{SpaceID: s.ID, FileID: "file", Kind: "original"}, visitor)
	if err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.started)
	badDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(badDir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	bad, err := os.OpenRoot(badDir)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	m.mu.Lock()
	storage := m.storage
	m.storage = bad
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.storage = storage; m.mu.Unlock() }()
	if _, err := m.PrepareDesktop(context.Background(), s.ID, "file", "original"); err == nil {
		t.Fatal("join succeeded despite save failure")
	}
	m.mu.Lock()
	desktop := m.tasks[guest.ID].Desktop
	m.mu.Unlock()
	if desktop {
		t.Fatal("failed join retained desktop ownership")
	}
	if _, err := m.PrepareDesktop(context.Background(), s.ID, "file", "compatible"); err == nil {
		t.Fatal("new task succeeded despite save failure")
	}
	m.mu.Lock()
	count := len(m.tasks)
	m.mu.Unlock()
	if count != 1 {
		t.Fatalf("failed task retained: %d", count)
	}
	m.mu.Lock()
	m.storage = storage
	m.mu.Unlock()
	if err := m.cancelForSession(context.Background(), s.ID, guest.ID, visitor); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, cloud.cancelled)
	waitState(t, m, guest.ID, "cancelled")
	m.mu.Lock()
	m.storage = bad
	m.mu.Unlock()
	if err := m.ResumeTask(guest.ID); err == nil {
		t.Fatal("resume succeeded despite save failure")
	}
	m.mu.Lock()
	state, desktop := m.tasks[guest.ID].State, m.tasks[guest.ID].Desktop
	m.mu.Unlock()
	if state != "cancelled" || desktop {
		t.Fatalf("failed resume retained changes: %s %v", state, desktop)
	}
}

func TestDesktopCompatiblePreservesSource(t *testing.T) {
	processor := lanmedia.New()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	var ffmpeg string
	for _, dir := range []string{filepath.Dir(exe), filepath.Join(filepath.Dir(exe), "Helpers"), filepath.Join(filepath.Dir(exe), "..", "Helpers")} {
		candidate := filepath.Join(dir, "ffmpeg"+suffix)
		if _, err := os.Stat(candidate); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "ffprobe"+suffix)); err == nil {
				ffmpeg = candidate
				break
			}
		}
	}
	if ffmpeg == "" {
		t.Skip("bundled media tools unavailable")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(t.TempDir(), "frames.yuv")
	if err := os.WriteFile(raw, make([]byte, 320*240*3/2*24), 0600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "source.avi")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "rawvideo", "-pixel_format", "yuv420p", "-video_size", "320x240", "-framerate", "24", "-i", raw, "-c:v", "mpeg4", input)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, output)
	}
	before, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, nil)
	m.media = processor
	s, err := m.AddSpace(SpaceRequest{Name: "Media", Kind: "local", LocalPath: root})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("list: %+v %v", entries, err)
	}
	if !entries[0].Ready || entries[0].DesktopCompatibleReady {
		t.Fatalf("original alone must not prove compatible readiness: %+v", entries[0])
	}
	task, err := m.PrepareDesktop(context.Background(), s.ID, entries[0].ID, "compatible")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, task.ID, "ready")
	entries, err = m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 || !entries[0].DesktopCompatibleReady {
		t.Fatalf("converted desktop result: %+v %v", entries, err)
	}
	after, err := os.ReadFile(input)
	if err != nil || string(before) != string(after) {
		t.Fatal("source changed")
	}
	m.mu.Lock()
	cached, file := m.cachedLocked(resourceKey(s.ID, entries[0].ID, entries[0].Version))
	m.mu.Unlock()
	if file == nil || cached.Path == input {
		t.Fatal("compatible copy not published separately")
	}
	file.Close()
	info, err := processor.Probe(context.Background(), filepath.Join(m.dir, cached.Path))
	if err != nil || info.VideoCodec != "h264" || info.Container != "mp4" {
		t.Fatalf("compatible output: %+v %v", info, err)
	}
	if _, err = m.ClearCache(); err != nil {
		t.Fatal(err)
	}
	entries, err = m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 || entries[0].DesktopCompatibleReady {
		t.Fatalf("cleared compatible cache: %+v %v", entries, err)
	}
}

func TestDesktopCompatibleReadinessProof(t *testing.T) {
	m := newTestManager(t, nil)
	s, root := localSpace(t, m, true)
	ctx := context.Background()
	list := func() PreparationEntry {
		t.Helper()
		entries, err := m.ListPreparationEntries(ctx, s.ID, "")
		if err != nil || len(entries) != 1 {
			t.Fatalf("list: %+v %v", entries, err)
		}
		return entries[0]
	}
	entry := list()
	if !entry.Ready || entry.DesktopCompatibleReady {
		t.Fatalf("original alone: %+v", entry)
	}
	token, err := LocalVersion(filepath.Join(root, entry.Name))
	if err != nil {
		t.Fatal(err)
	}
	key := resourceKey(s.ID, entry.ID, entry.Version)
	m.mu.Lock()
	m.mediaInfo[key+"/original/"+token] = lanmedia.Info{Container: "mp4", MIME: "video/mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 12, BitDepth: 8, PixelFormat: "yuv420p"}
	m.mu.Unlock()
	task, err := m.PrepareDesktop(ctx, s.ID, entry.ID, "compatible")
	if err != nil || task.State != "ready" || !list().DesktopCompatibleReady {
		t.Fatalf("direct task: %+v %v", task, err)
	}
	if err = m.DismissTaskHistory(s.ID, task.ID); err != nil || !list().DesktopCompatibleReady {
		t.Fatalf("hidden direct task: %v", err)
	}
	config := filepath.Dir(m.dir)
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{ConfigDir: config, AllowLoopback: true, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	m = reopened
	if !list().DesktopCompatibleReady {
		t.Fatal("unchanged direct proof lost on restart")
	}
	if err = os.WriteFile(filepath.Join(root, entry.Name), []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	if list().DesktopCompatibleReady {
		t.Fatal("changed local source retained direct proof")
	}
}

func TestDesktopCompatibleReadinessRejectsVisitorCopy(t *testing.T) {
	m := newTestManager(t, nil)
	s, _ := localSpace(t, m, true)
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("list: %+v %v", entries, err)
	}
	entry := entries[0]
	dir, err := m.newOutputDirectory()
	if err != nil {
		t.Fatal(err)
	}
	rel := dir + "/compatible.webm"
	if err = os.WriteFile(filepath.Join(m.dir, rel), []byte("cache"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := LocalVersion(filepath.Join(m.dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	key := resourceKey(s.ID, entry.ID, entry.Version)
	plan := planKey(lanmedia.Plan{Kind: "remux", Container: "webm"})
	m.mu.Lock()
	delete(m.pending, dir)
	m.cache[key] = &cacheFile{Path: rel, Token: token, Size: 5, Kind: "remux", PlanKey: plan, Used: time.Now()}
	m.tasks["visitor"] = &taskRecord{Task: Task{ID: "visitor", SpaceID: s.ID, FileID: entry.ID, Kind: "compatible", State: "ready"}, Version: entry.Version, RequestKey: key + "/compatible/" + plan, OutputToken: token, Capabilities: lanmedia.Capabilities{VP9: true, Opus: true, WebM: true}}
	m.mu.Unlock()
	entries, err = m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 || !entries[0].CompatibleReady || entries[0].DesktopCompatibleReady {
		t.Fatalf("visitor WebM copy: %+v %v", entries, err)
	}
	m.mu.Lock()
	visitor := m.tasks["visitor"]
	visitor.Capabilities = lanmedia.Capabilities{HEVC: true, AAC: true, MP4: true}
	visitor.RequestKey = key + "/compatible/" + planKey(lanmedia.Plan{Kind: "direct", Container: "mp4"})
	visitor.OutputToken = ""
	m.mu.Unlock()
	entries, err = m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 || entries[0].DesktopCompatibleReady {
		t.Fatalf("visitor HEVC direct: %+v %v", entries, err)
	}
	m.mu.Lock()
	visitor.Capabilities = lanmedia.Capabilities{H264: true, AAC: true, MP4: true}
	visitor.OutputToken = token
	visitor.RequestKey = key + "/compatible/" + plan
	m.mu.Unlock()
	// The task and cache must agree; a desktop task cannot prove a different copy.
	m.mu.Lock()
	m.cache[key].PlanKey = "video/mp4/false"
	m.mu.Unlock()
	if entries, err = m.ListPreparationEntries(context.Background(), s.ID, ""); err != nil || len(entries) != 1 || entries[0].DesktopCompatibleReady {
		t.Fatalf("different cache plan: %+v %v", entries, err)
	}
	m.mu.Lock()
	visitor.RequestKey = key + "/compatible/video/mp4/false"
	m.mu.Unlock()
	if entries, err = m.ListPreparationEntries(context.Background(), s.ID, ""); err != nil || len(entries) != 1 || !entries[0].DesktopCompatibleReady {
		t.Fatalf("matching task and cache: %+v %v", entries, err)
	}
	m.mu.Lock()
	visitor.State = "queued"
	m.mu.Unlock()
	if entries, err = m.ListPreparationEntries(context.Background(), s.ID, ""); err != nil || len(entries) != 1 || entries[0].DesktopCompatibleReady {
		t.Fatalf("retried task: %+v %v", entries, err)
	}
	m.mu.Lock()
	visitor.State = "ready"
	m.mu.Unlock()
	if err = os.Remove(filepath.Join(m.dir, rel)); err != nil {
		t.Fatal(err)
	}
	if entries, err = m.ListPreparationEntries(context.Background(), s.ID, ""); err != nil || len(entries) != 1 || entries[0].DesktopCompatibleReady {
		t.Fatalf("deleted cache: %+v %v", entries, err)
	}
}

func TestDesktopCompatibleCloudDirectNeedsLocalTokenProof(t *testing.T) {
	cloud := newCloud()
	m := newTestManager(t, cloud)
	s, err := m.AddSpace(SpaceRequest{Name: "Cloud", Kind: "cloud", AccountID: "device-A"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := m.ListPreparationEntries(context.Background(), s.ID, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("list: %+v %v", entries, err)
	}
	entry := entries[0]
	key := resourceKey(s.ID, entry.ID, entry.Version)
	m.mu.Lock()
	m.tasks["direct"] = &taskRecord{Task: Task{ID: "direct", SpaceID: s.ID, FileID: entry.ID, Kind: "compatible", State: "ready"}, Version: entry.Version, RequestKey: key + "/compatible/" + planKey(lanmedia.Plan{Kind: "direct", Container: "mp4"}), Capabilities: lanmedia.Capabilities{H264: true, AAC: true, MP4: true}}
	m.mu.Unlock()
	entry.Ready = true
	if m.desktopCompatibleReady(s.ID, entry.Entry) {
		t.Fatal("cloud direct task does not prove the current local token")
	}
}
