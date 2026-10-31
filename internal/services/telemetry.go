package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// Anonymous Usage Statistics (Settings · Privacy & Security): how many computers use PKP each day and
// which features they reach for. Only counts leave the computer — per local
// day, how often each name in telemetryNames happened — with a random install
// id, the app version, the system and the interface language. Never a file
// name, path, search, account or PikPak id. It starts off; switching it off
// deletes the id and the counts not yet sent.
//
// The report is a running total: today's day is sent again every hour and
// the server keeps the last copy of each (install, day), so a repeat or a
// retry never counts twice. Days before today leave once the server has them.

// The names the app may count. Anything else is dropped, so a typo or a page
// newer than this Go never grows the report. The page's TelemetryName and the
// dashboard's LABELS (cloudflare/telemetry/src/admin/page.html) list them too.
var telemetryNames = func() map[string]bool {
	names := map[string]bool{}
	for _, n := range []string{
		// Go: a start, and App Lock being on at that start.
		"app.open", "app_lock.on",
		// The page on screen (store.ts Route).
		"route.files", "route.transfers", "route.player", "route.shares", "route.settings",
		"route.viewer", "route.assistant", "route.cleanup",
		// What the user did.
		"play.video", "play.audio", "upload", "download", "cloud_add", "viewer.open",
		"scope.drive", "search", "share.create", "assistant.ask", "organize.run",
		"organize.undo", "cleanup.scan", "cleanup.remove",
		"menubar.open",
	} {
		names[n] = true
	}
	return names
}()

const (
	telemetryFileName = "telemetry.json"
	// The first report waits for the app to settle; then one an hour.
	telemetryFirstSend = 30 * time.Second
	telemetrySendEvery = time.Hour
	telemetryTimeout   = 15 * time.Second
	// Days kept while the server cannot be reached; older ones are dropped.
	telemetryKeepDays  = 30
	telemetryMaxCount  = 1_000_000
	telemetryDayLayout = "2006-01-02"
)

// TelemetryState is Anonymous Usage Statistics as the settings page shows it.
type TelemetryState struct {
	Enabled bool `json:"enabled"`
}

// telemetryDay is one local day's counts.
type telemetryDay struct {
	Day    string         `json:"day"`
	Counts map[string]int `json:"counts"`
}

// telemetryRecord is telemetry.json: the install id and the days not yet confirmed
// by the server, oldest first.
type telemetryRecord struct {
	Install string         `json:"install"`
	Days    []telemetryDay `json:"days"`
}

// telemetryReport is what POST /v1/usage receives.
type telemetryReport struct {
	Install string         `json:"install"`
	Version string         `json:"version"`
	OS      string         `json:"os"`
	Arch    string         `json:"arch"`
	Locale  string         `json:"locale"`
	Days    []telemetryDay `json:"days"`
}

// TelemetryService counts and reports Anonymous Usage Statistics.
type TelemetryService struct {
	drive    *DriveService
	endpoint string
	version  string
	log      *slog.Logger
	// now and client are test seams.
	now    func() time.Time
	client func() *http.Client

	off  atomic.Bool
	stop chan struct{}
	once sync.Once

	mu sync.Mutex
	// rec is telemetry.json once read; nil until the first count, and again
	// after switching off.
	rec   *telemetryRecord
	dirty bool
}

// NewTelemetryService prepares Anonymous Usage Statistics. An empty endpoint (a development
// build without PKP_TELEMETRY_API) counts and sends nothing.
func NewTelemetryService(drive *DriveService, endpoint, version string) *TelemetryService {
	u := &TelemetryService{
		drive:    drive,
		endpoint: strings.TrimRight(endpoint, "/"),
		version:  version,
		log:      slog.Default(),
		now:      time.Now,
		client:   netproxy.Client,
		stop:     make(chan struct{}),
	}
	u.off.Store(!drive.LocalSettings().UsageStatsEnabled)
	if u.off.Load() {
		if path, ok := u.path(); ok {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				u.log.Warn("telemetry: old counts not removed", "error", err)
			}
		}
	}
	return u
}

// ServiceStartup counts the start and begins the hourly report.
func (u *TelemetryService) ServiceStartup(context.Context, application.ServiceOptions) error {
	if u.endpoint == "" {
		return nil
	}
	u.Count("app.open")
	if u.drive.LocalSettings().AppLock {
		u.Count("app_lock.on")
	}
	go u.loop()
	return nil
}

// ServiceShutdown keeps what was counted since the last report for the next run.
func (u *TelemetryService) ServiceShutdown() error {
	u.once.Do(func() { close(u.stop) })
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.saveLocked()
}

func (u *TelemetryService) loop() {
	wait := time.NewTimer(telemetryFirstSend)
	defer wait.Stop()
	for {
		select {
		case <-u.stop:
			return
		case <-wait.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), telemetryTimeout)
		if err := u.send(ctx); err != nil {
			u.log.Debug("telemetry: report not sent", "error", err)
		}
		cancel()
		wait.Reset(telemetrySendEvery)
	}
}

// State is Anonymous Usage Statistics as it stands.
func (u *TelemetryService) State() TelemetryState {
	return TelemetryState{Enabled: !u.off.Load()}
}

// SetEnabled switches Anonymous Usage Statistics and saves the choice. Off forgets the
// install id and every count not yet sent; on again starts a new id.
func (u *TelemetryService) SetEnabled(on bool) (TelemetryState, error) {
	// A failed migration cleanup must not cause old counts to be sent after opt-in.
	if on && u.off.Load() {
		if path, ok := u.path(); ok {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return u.State(), err
			}
		}
	}
	if err := u.drive.updateSettings(func(s *LocalSettings) { s.UsageStatsEnabled = on }); err != nil {
		return u.State(), err
	}
	u.off.Store(!on)
	if !on {
		u.mu.Lock()
		u.rec, u.dirty = nil, false
		path, ok := u.path()
		u.mu.Unlock()
		if ok {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return u.State(), err
			}
		}
	}
	return u.State(), nil
}

// Count adds one to a name in telemetryNames for today. Off, in a build without
// an endpoint, or for any other name it does nothing.
func (u *TelemetryService) Count(name string) {
	if u.endpoint == "" || u.off.Load() || !telemetryNames[name] {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.off.Load() {
		return
	}
	rec := u.loadLocked()
	today := u.now().Format(telemetryDayLayout)
	if n := len(rec.Days); n == 0 || rec.Days[n-1].Day != today {
		rec.Days = append(rec.Days, telemetryDay{Day: today, Counts: map[string]int{}})
		if len(rec.Days) > telemetryKeepDays {
			rec.Days = rec.Days[len(rec.Days)-telemetryKeepDays:]
		}
		// A new day is a good moment to keep the last one.
		u.dirty = true
		if err := u.saveLocked(); err != nil {
			u.log.Debug("telemetry: counts not saved", "error", err)
		}
	}
	counts := rec.Days[len(rec.Days)-1].Counts
	counts[name] = min(counts[name]+1, telemetryMaxCount)
	u.dirty = true
}

// send reports every day held. On success the days before today are
// dropped — unless they were counted into while the report was on its way.
func (u *TelemetryService) send(ctx context.Context) error {
	if u.off.Load() {
		return nil
	}
	u.mu.Lock()
	rec := u.loadLocked()
	if len(rec.Days) == 0 {
		u.mu.Unlock()
		return nil
	}
	report := telemetryReport{
		Install: rec.Install,
		Version: u.version,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Locale:  string(i18n.Current()),
		Days:    make([]telemetryDay, len(rec.Days)),
	}
	for i, d := range rec.Days {
		report.Days[i] = telemetryDay{Day: d.Day, Counts: maps.Clone(d.Counts)}
	}
	if err := u.saveLocked(); err != nil {
		u.log.Debug("telemetry: counts not saved", "error", err)
	}
	u.mu.Unlock()

	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint+"/v1/usage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "PKP/"+u.version)
	resp, err := u.client().Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telemetry: server answered HTTP %d", resp.StatusCode)
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	if u.rec == nil || u.rec.Install != report.Install {
		return nil // switched off (and maybe on again) meanwhile
	}
	today := u.now().Format(telemetryDayLayout)
	sent := make(map[string]map[string]int, len(report.Days))
	for _, d := range report.Days {
		sent[d.Day] = d.Counts
	}
	kept := u.rec.Days[:0]
	for _, d := range u.rec.Days {
		if counts, ok := sent[d.Day]; ok && d.Day != today && maps.Equal(counts, d.Counts) {
			continue
		}
		kept = append(kept, d)
	}
	if len(kept) != len(u.rec.Days) {
		u.rec.Days = kept
		u.dirty = true
	}
	return u.saveLocked()
}

func (u *TelemetryService) path() (string, bool) {
	if u.drive.configErr != nil {
		return "", false
	}
	return filepath.Join(u.drive.configDir, telemetryFileName), true
}

// loadLocked reads telemetry.json once; a missing or unreadable file starts a
// new install id. u.mu must be held.
func (u *TelemetryService) loadLocked() *telemetryRecord {
	if u.rec != nil {
		return u.rec
	}
	rec := &telemetryRecord{}
	if path, ok := u.path(); ok {
		if data, err := os.ReadFile(path); err == nil {
			if json.Unmarshal(data, rec) != nil || !validInstallID(rec.Install) {
				rec = &telemetryRecord{}
			}
		}
	}
	if rec.Install == "" {
		rec.Install = newInstallID()
		u.dirty = true
	}
	for i := range rec.Days {
		if rec.Days[i].Counts == nil {
			rec.Days[i].Counts = map[string]int{}
		}
	}
	u.rec = rec
	return rec
}

// saveLocked writes telemetry.json when it changed; u.mu must be held.
func (u *TelemetryService) saveLocked() error {
	if u.rec == nil || !u.dirty {
		return nil
	}
	path, ok := u.path()
	if !ok {
		return nil
	}
	data, err := json.Marshal(u.rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	u.dirty = false
	return nil
}

// newInstallID is a random UUID (version 4): nothing about the computer or
// the account goes into it.
func newInstallID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func validInstallID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
				return false
			}
		}
	}
	return true
}
