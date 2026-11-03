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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// Anonymous Usage Statistics reports daily active installations, not people or
// startup frequency. Enabled by default, each active local day contributes
// only app.open=1, with a random installation id, version, OS and interface language.
// Switching off deletes the id and pending days; switching on creates a new id.
// Reports are idempotent per (install, day), so retries never add another activity.

const (
	telemetryFileName = "telemetry.json"
	// The first report waits for the app to settle; then one an hour.
	telemetryFirstSend = 30 * time.Second
	telemetrySendEvery = time.Hour
	telemetryTimeout   = 15 * time.Second
	// Days kept while the server cannot be reached; older ones are dropped.
	telemetryKeepDays  = 30
	telemetryDayLayout = "2006-01-02"
)

// TelemetryState is Anonymous Usage Statistics as the settings page shows it.
type TelemetryState struct {
	Enabled     bool `json:"enabled"`
	NeedsNotice bool `json:"needsNotice"`
}

// telemetryDay is one active local date, represented as app.open=1.
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

// TelemetryService records and reports daily active installations while enabled.
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
	wake chan struct{}
	once sync.Once

	sendMu sync.Mutex
	mu     sync.Mutex
	// All setting, record, disk and cancellation changes are serialized by mu.
	noticeSeen bool
	rec        *telemetryRecord
	dirty      bool
	cancelSend context.CancelFunc
}

// NewTelemetryService prepares Anonymous Usage Statistics. An empty endpoint (a development
// build without PKP_TELEMETRY_API) records and sends nothing.
func NewTelemetryService(drive *DriveService, endpoint, version string) *TelemetryService {
	u := &TelemetryService{
		drive:    drive,
		endpoint: strings.TrimRight(endpoint, "/"),
		version:  version,
		log:      slog.Default(),
		now:      time.Now,
		client:   netproxy.Client,
		stop:     make(chan struct{}),
		wake:     make(chan struct{}, 1),
	}
	settings := drive.LocalSettings()
	u.off.Store(!settings.UsageStatsEnabled)
	u.noticeSeen = settings.UsageStatsNoticeSeen
	if u.endpoint != "" && u.off.Load() {
		if err := u.removeLocked(); err != nil {
			u.log.Warn("telemetry: old activity not removed", "error", err)
		}
	}
	return u
}

// ServiceStartup marks this local day active and begins the hourly report.
func (u *TelemetryService) ServiceStartup(context.Context, application.ServiceOptions) error {
	if u.endpoint == "" {
		return nil
	}
	u.recordActivity(false)
	go u.loop()
	return nil
}

// ServiceShutdown keeps pending active days for the next run.
func (u *TelemetryService) ServiceShutdown() error {
	u.once.Do(func() { close(u.stop) })
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancelSend != nil {
		u.cancelSend()
	}
	u.pruneLocked()
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
		case <-u.wake:
		}
		select {
		case <-u.stop:
			return
		default:
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
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stateLocked()
}

func (u *TelemetryService) stateLocked() TelemetryState {
	return TelemetryState{
		Enabled:     !u.off.Load(),
		NeedsNotice: u.endpoint != "" && !u.off.Load() && !u.noticeSeen,
	}
}

// AcknowledgeNotice remembers the disclosure without changing collection or activity.
func (u *TelemetryService) AcknowledgeNotice() (TelemetryState, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.endpoint == "" || u.noticeSeen {
		return u.stateLocked(), nil
	}
	if err := u.drive.updateSettings(func(s *LocalSettings) {
		s.UsageStatsNoticeSeen = true
	}); err != nil {
		return u.stateLocked(), err
	}
	u.noticeSeen = true
	return u.stateLocked(), nil
}

// SetEnabled saves the setting and dismisses the notice. Off forgets the id and
// pending days; enabling marks today active even when the app started while off.
func (u *TelemetryService) SetEnabled(on bool) (TelemetryState, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	// Never reuse a leftover id after a failed migration cleanup.
	if on && u.off.Load() {
		if err := u.removeLocked(); err != nil {
			return u.stateLocked(), err
		}
	}
	if err := u.drive.updateSettings(func(s *LocalSettings) {
		s.UsageStatsEnabled = on
		s.UsageStatsNoticeSeen = true
	}); err != nil {
		return u.stateLocked(), err
	}
	u.off.Store(!on)
	u.noticeSeen = true
	if !on {
		if u.cancelSend != nil {
			u.cancelSend()
		}
		u.rec, u.dirty = nil, false
		return u.stateLocked(), u.removeLocked()
	}
	if u.endpoint == "" {
		return u.stateLocked(), nil
	}
	_, err := u.recordActivityLocked()
	u.wakeLocked()
	return u.stateLocked(), err
}

func (u *TelemetryService) removeLocked() error {
	if u.endpoint == "" {
		return nil
	}
	if path, ok := u.path(); ok {
		for _, name := range []string{path, path + ".tmp"} {
			if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// RecordActivity marks the current local date active once. Call only for startup
// or foreground activity, never from a background resend timer.
func (u *TelemetryService) RecordActivity() {
	u.recordActivity(true)
}

func (u *TelemetryService) recordActivity(wake bool) {
	if u.endpoint == "" || u.off.Load() {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.off.Load() {
		return
	}
	added, err := u.recordActivityLocked()
	if err != nil {
		u.log.Debug("telemetry: activity not saved", "error", err)
	}
	if wake && added {
		u.wakeLocked()
	}
}

func (u *TelemetryService) recordActivityLocked() (bool, error) {
	rec := u.loadLocked()
	u.pruneLocked()
	today := u.now().Format(telemetryDayLayout)
	for _, day := range rec.Days {
		if day.Day == today {
			return false, u.saveLocked()
		}
	}
	rec.Days = append(rec.Days, telemetryDay{Day: today, Counts: map[string]int{"app.open": 1}})
	sort.Slice(rec.Days, func(i, j int) bool { return rec.Days[i].Day < rec.Days[j].Day })
	u.dirty = true
	return true, u.saveLocked()
}

func (u *TelemetryService) wakeLocked() {
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

// send reports retained active days, but never creates activity itself.
func (u *TelemetryService) send(ctx context.Context) error {
	u.sendMu.Lock()
	defer u.sendMu.Unlock()
	u.mu.Lock()
	if u.endpoint == "" || u.off.Load() {
		u.mu.Unlock()
		return nil
	}
	rec := u.loadLocked()
	u.pruneLocked()
	if len(rec.Days) == 0 {
		err := u.saveLocked()
		u.mu.Unlock()
		return err
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
	ctx, cancel := context.WithCancel(ctx)
	u.cancelSend = cancel
	u.mu.Unlock()
	defer func() {
		cancel()
		u.mu.Lock()
		u.cancelSend = nil
		u.mu.Unlock()
	}()

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
	// Older builds queued feature counters. Only a positive app.open proves
	// an active day; all feature names and startup frequencies are discarded.
	days := make([]telemetryDay, 0, len(rec.Days))
	seen := make(map[string]bool, len(rec.Days))
	for _, day := range rec.Days {
		if day.Counts["app.open"] > 0 && !seen[day.Day] {
			days = append(days, telemetryDay{Day: day.Day, Counts: map[string]int{"app.open": 1}})
			seen[day.Day] = true
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Day < days[j].Day })
	rec.Days = days
	u.dirty = true
	u.rec = rec
	u.pruneLocked()
	return rec
}

// Retention is by local calendar age, not the number of rows collected.
func (u *TelemetryService) pruneLocked() {
	if u.rec == nil {
		return
	}
	now := u.now()
	today := now.Format(telemetryDayLayout)
	oldest := now.AddDate(0, 0, 1-telemetryKeepDays).Format(telemetryDayLayout)
	kept := u.rec.Days[:0]
	for _, day := range u.rec.Days {
		parsed, err := time.Parse(telemetryDayLayout, day.Day)
		if err != nil || parsed.Format(telemetryDayLayout) != day.Day || day.Day < oldest || day.Day > today {
			continue
		}
		kept = append(kept, day)
	}
	if len(kept) != len(u.rec.Days) {
		u.rec.Days = kept
		u.dirty = true
	}
}

// saveLocked writes telemetry.json when it changed; u.mu must be held.
func (u *TelemetryService) saveLocked() error {
	if u.off.Load() || u.rec == nil || !u.dirty {
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
