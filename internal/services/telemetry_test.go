package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// telemetryServer is a stand-in /v1/usage: it records each report and answers
// with status, which a test may change between reports.
type telemetryServer struct {
	mu      sync.Mutex
	reports []telemetryReport
	body    []byte
	status  int
	// during runs while a report is being answered.
	during func()
}

func (s *telemetryServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/usage" {
		http.NotFound(w, r)
		return
	}
	var rep telemetryReport
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &rep); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.reports = append(s.reports, rep)
	s.body = body
	status, during := s.status, s.during
	s.mu.Unlock()
	if during != nil {
		during()
	}
	w.WriteHeader(status)
}

func (s *telemetryServer) last(t *testing.T) telemetryReport {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reports) == 0 {
		t.Fatal("no report reached the server")
	}
	return s.reports[len(s.reports)-1]
}

type telemetryClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *telemetryClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *telemetryClock) set(at time.Time) {
	c.mu.Lock()
	c.at = at
	c.mu.Unlock()
}

func newTelemetryForTest(t *testing.T, config string) (*TelemetryService, *telemetryServer, *telemetryClock) {
	t.Helper()
	srv := &telemetryServer{status: http.StatusNoContent}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	d := NewDriveService(DriveOptions{ConfigDir: config, NoIndexSync: true, Logger: quiet()}, nil)
	u := NewTelemetryService(d, ts.URL+"/", "1.2.3")
	u.log = quiet()
	clock := &telemetryClock{at: time.Date(2026, 10, 17, 22, 0, 0, 0, time.Local)}
	u.now = clock.now
	u.client = ts.Client
	return u, srv, clock
}

func dayCounts(rep telemetryReport) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, d := range rep.Days {
		out[d.Day] = d.Counts
	}
	return out
}

func TestTelemetryDailyPresenceUsesLocalDatesAndDeduplicates(t *testing.T) {
	config := t.TempDir()
	u, srv, clock := newTelemetryForTest(t, config)
	clock.set(time.Date(2026, 10, 17, 23, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	if state, err := u.SetEnabled(true); err != nil || !state.Enabled || state.NeedsNotice {
		t.Fatalf("enable = %+v, %v", state, err)
	}
	first := u.rec.Install
	u.RecordActivity()
	u.RecordActivity()
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep := srv.last(t)
	if !validInstallID(rep.Install) || rep.Version != "1.2.3" || rep.OS == "" || rep.Arch == "" || rep.Locale == "" {
		t.Fatalf("report header = %+v", rep)
	}
	if got := dayCounts(rep); !equalDays(got, map[string]map[string]int{"2026-10-17": {"app.open": 1}}) {
		t.Fatalf("same-day activity = %v", got)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(srv.body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 6 || payload["install"] == nil || payload["version"] == nil || payload["os"] == nil || payload["arch"] == nil || payload["locale"] == nil || payload["days"] == nil {
		t.Fatalf("unexpected wire metadata: %s", srv.body)
	}
	var days []map[string]json.RawMessage
	if err := json.Unmarshal(payload["days"], &days); err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || len(days[0]) != 2 || days[0]["day"] == nil || days[0]["counts"] == nil {
		t.Fatalf("unexpected daily wire fields: %s", payload["days"])
	}
	clock.set(clock.now().Add(time.Hour))
	// Failed retries alone must not turn background residency into an active day.
	srv.status = http.StatusServiceUnavailable
	for range 2 {
		if err := u.send(context.Background()); err == nil {
			t.Fatal("503 treated as delivered")
		}
		if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-10-17": {"app.open": 1}}) {
			t.Fatalf("resend invented activity: %v", got)
		}
	}
	u.RecordActivity()
	u.RecordActivity()
	srv.status = http.StatusNoContent
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]int{"2026-10-17": {"app.open": 1}, "2026-10-18": {"app.open": 1}}
	if got := dayCounts(srv.last(t)); !equalDays(got, want) {
		t.Fatalf("foreground next day = %v, want %v", got, want)
	}
	if srv.last(t).Install != first {
		t.Fatal("daily activity changed installation identity")
	}
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-10-18": {"app.open": 1}}) {
		t.Fatalf("acknowledged past day retained: %v", got)
	}
}

func TestTelemetryOfflineDaysPersistPromptlyAcrossRestart(t *testing.T) {
	config := t.TempDir()
	u, srv, clock := newTelemetryForTest(t, config)
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().Add(4 * time.Hour))
	u.RecordActivity()
	srv.status = http.StatusServiceUnavailable
	if err := u.send(context.Background()); err == nil {
		t.Fatal("503 treated as delivered")
	}
	// No shutdown: a short run must already have saved both days.
	next, srv2, clock2 := newTelemetryForTest(t, config)
	clock2.set(clock.now())
	next.RecordActivity()
	if err := next.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]int{"2026-10-17": {"app.open": 1}, "2026-10-18": {"app.open": 1}}
	if rep := srv2.last(t); rep.Install != u.rec.Install || !equalDays(dayCounts(rep), want) {
		t.Fatalf("after outage = %+v, want days %v", rep, want)
	}
}

func TestTelemetryOffForgetsTheInstallAndCollectsNothing(t *testing.T) {
	config := t.TempDir()
	u, srv, _ := newTelemetryForTest(t, config)
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, telemetryFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("activity not kept: %v", err)
	}
	first := u.rec.Install

	if st, err := u.SetEnabled(false); err != nil || st.Enabled || st.NeedsNotice {
		t.Fatalf("SetEnabled(false) = %+v, %v", st, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("telemetry.json survived switching off: %v", err)
	}
	u.RecordActivity()
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("off collection recreated telemetry.json: %v", err)
	}
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(srv.reports) != 0 {
		t.Fatalf("sent while off: %+v", srv.reports)
	}

	// The choice survives a restart; on again is a new install.
	again, srv2, _ := newTelemetryForTest(t, config)
	if st := again.State(); st.Enabled || st.NeedsNotice {
		t.Fatalf("refusal was not saved: %+v", st)
	}
	if _, err := again.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := again.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rep := srv2.last(t); rep.Install == first {
		t.Fatal("switching off and on kept the old install id")
	}
}

func TestTelemetryDefaultOnAndLegacyMigration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		settings   string
		enabled    bool
		noticeSeen bool
	}{
		{"fresh", "", true, false},
		{"unspecified", `{}`, true, false},
		{"unrelated settings", `{"keepPlayingOnSwitch":true}`, true, false},
		{"legacy off", `{"usageStatsOff":true}`, false, false},
		{"legacy not off", `{"usageStatsOff":false}`, true, false},
		{"legacy accepted", `{"usageStatsEnabled":true}`, true, true},
		{"legacy explicit refusal", `{"usageStatsEnabled":false}`, false, true},
		{"legacy omitted refusal", `{"usageStatsChoiceMade":true}`, false, true},
		{"legacy unanswered", `{"usageStatsChoiceMade":false}`, true, false},
		{"legacy accepted answer", `{"usageStatsChoiceMade":true,"usageStatsEnabled":true}`, true, true},
		{"legacy refused answer", `{"usageStatsChoiceMade":true,"usageStatsEnabled":false}`, false, true},
		{"enabled overrides legacy off", `{"usageStatsEnabled":true,"usageStatsOff":true}`, true, true},
		{"disabled overrides legacy on", `{"usageStatsEnabled":false,"usageStatsOff":false}`, false, true},
		{"enabled overrides omitted refusal", `{"usageStatsEnabled":true,"usageStatsChoiceMade":true,"usageStatsOff":true}`, true, true},
		{"unanswered legacy off", `{"usageStatsChoiceMade":false,"usageStatsOff":true}`, false, false},
		{"modern unseen", `{"usageStatsEnabled":true,"usageStatsNoticeSeen":false}`, true, false},
		{"modern seen", `{"usageStatsEnabled":true,"usageStatsNoticeSeen":true}`, true, true},
		{"modern disabled unseen", `{"usageStatsEnabled":false,"usageStatsNoticeSeen":false}`, false, false},
		{"modern disabled seen", `{"usageStatsEnabled":false,"usageStatsNoticeSeen":true}`, false, true},
		{"notice only unseen", `{"usageStatsNoticeSeen":false}`, true, false},
		{"notice only seen", `{"usageStatsNoticeSeen":true}`, true, true},
		{"modern unseen overrides legacy answer", `{"usageStatsEnabled":true,"usageStatsChoiceMade":true,"usageStatsNoticeSeen":false}`, true, false},
		{"modern notice preserves legacy refusal", `{"usageStatsChoiceMade":true,"usageStatsNoticeSeen":true}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := t.TempDir()
			if tc.settings != "" {
				if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.enabled {
				if err := os.WriteFile(filepath.Join(config, telemetryFileName), []byte(`{"install":"old","days":[]}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			u, srv, _ := newTelemetryForTest(t, config)
			want := TelemetryState{Enabled: tc.enabled, NeedsNotice: tc.enabled && !tc.noticeSeen}
			if state := u.State(); state != want {
				t.Fatalf("migration = %+v, want %+v", state, want)
			}
			if settings := u.drive.LocalSettings(); settings.UsageStatsEnabled != tc.enabled || settings.UsageStatsNoticeSeen != tc.noticeSeen {
				t.Fatalf("migrated settings = %+v", settings)
			}
			if err := u.drive.SetKeepPlayingOnSwitch(true); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(config, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]json.RawMessage
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if _, exists := saved["usageStatsOff"]; exists {
				t.Fatal("unrelated save retained legacy off field")
			}
			if _, exists := saved["usageStatsChoiceMade"]; exists {
				t.Fatal("unrelated save retained legacy choice field")
			}
			if string(saved["usageStatsEnabled"]) != map[bool]string{true: "true", false: "false"}[tc.enabled] || string(saved["usageStatsNoticeSeen"]) != map[bool]string{true: "true", false: "false"}[tc.noticeSeen] {
				t.Fatalf("settings did not serialize both flags explicitly: %s", data)
			}
			restarted, _, _ := newTelemetryForTest(t, config)
			if state := restarted.State(); state != want {
				t.Fatalf("unrelated save and restart changed migration: %+v, want %+v", state, want)
			}
			u.RecordActivity()
			if err := u.send(context.Background()); err != nil {
				t.Fatal(err)
			}
			if tc.enabled {
				if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-10-17": {"app.open": 1}}) {
					t.Fatalf("default activity = %v", got)
				}
			} else {
				if len(srv.reports) != 0 {
					t.Fatal("explicit off reported activity")
				}
				if _, err := os.Stat(filepath.Join(config, telemetryFileName)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("explicit off kept activity: %v", err)
				}
			}
		})
	}
}

func TestTelemetryStartupReportsBeforeNoticeAcknowledgement(t *testing.T) {
	config := t.TempDir()
	u, srv, _ := newTelemetryForTest(t, config)
	if state := u.State(); !state.Enabled || !state.NeedsNotice {
		t.Fatalf("initial state = %+v", state)
	}
	if err := u.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = u.ServiceShutdown() })
	select {
	case <-u.wake:
		t.Fatal("startup bypassed the first-report delay")
	default:
	}
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-10-17": {"app.open": 1}}) {
		t.Fatalf("startup without acknowledgement = %v", got)
	}
	if state := u.State(); !state.Enabled || !state.NeedsNotice {
		t.Fatalf("report implicitly acknowledged notice: %+v", state)
	}
	if _, err := os.Stat(filepath.Join(config, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup implicitly wrote settings: %v", err)
	}
}

func TestTelemetryNoticeAcknowledgementOnlyChangesNotice(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "before activity", true: "after activity"}[active], func(t *testing.T) {
			config := t.TempDir()
			u, _, clock := newTelemetryForTest(t, config)
			var before []byte
			if active {
				u.RecordActivity()
				var err error
				before, err = os.ReadFile(filepath.Join(config, telemetryFileName))
				if err != nil {
					t.Fatal(err)
				}
			}
			clock.set(clock.now().AddDate(0, 0, 1))
			if state, err := u.AcknowledgeNotice(); err != nil || !state.Enabled || state.NeedsNotice {
				t.Fatalf("acknowledgement = %+v, %v", state, err)
			}
			if active {
				after, err := os.ReadFile(filepath.Join(config, telemetryFileName))
				if err != nil || string(before) != string(after) {
					t.Fatalf("acknowledgement changed identity or activity: %s -> %s, %v", before, after, err)
				}
			} else if u.rec != nil {
				t.Fatal("acknowledgement created an installation identity")
			} else if _, err := os.Stat(filepath.Join(config, telemetryFileName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("acknowledgement created activity: %v", err)
			}
			restarted, _, _ := newTelemetryForTest(t, config)
			if state := restarted.State(); !state.Enabled || state.NeedsNotice {
				t.Fatalf("acknowledgement did not survive restart: %+v", state)
			}
		})
	}
}

func TestTelemetryDisabledUnseenNoticeAcknowledgementCannotEnable(t *testing.T) {
	config := t.TempDir()
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"usageStatsEnabled":false,"usageStatsNoticeSeen":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	u, _, _ := newTelemetryForTest(t, config)
	if state, err := u.AcknowledgeNotice(); err != nil || state.Enabled || state.NeedsNotice {
		t.Fatalf("disabled acknowledgement = %+v, %v", state, err)
	}
	if settings := u.drive.LocalSettings(); settings.UsageStatsEnabled || !settings.UsageStatsNoticeSeen {
		t.Fatalf("disabled acknowledgement did not save only the notice: %+v", settings)
	}
	restarted, _, _ := newTelemetryForTest(t, config)
	if state := restarted.State(); state.Enabled || state.NeedsNotice {
		t.Fatalf("disabled acknowledgement re-enabled on restart: %+v", state)
	}
	if u.rec != nil {
		t.Fatal("disabled acknowledgement created an identity")
	}
	if _, err := os.Stat(filepath.Join(config, telemetryFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled acknowledgement created activity: %v", err)
	}
}

func TestTelemetryOptOutSurvivesConcurrentAndStaleNoticeAcknowledgement(t *testing.T) {
	config := t.TempDir()
	u, srv, _ := newTelemetryForTest(t, config)
	u.RecordActivity()
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := u.AcknowledgeNotice()
		results <- err
	}()
	go func() {
		<-start
		_, err := u.SetEnabled(false)
		results <- err
	}()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if state, err := u.AcknowledgeNotice(); err != nil || state.Enabled || state.NeedsNotice {
		t.Fatalf("stale acknowledgement changed opt-out: %+v, %v", state, err)
	}
	if err := u.drive.SetKeepPlayingOnSwitch(true); err != nil {
		t.Fatal(err)
	}
	restarted, _, _ := newTelemetryForTest(t, config)
	if state := restarted.State(); state.Enabled || state.NeedsNotice {
		t.Fatalf("opt-out lost after acknowledgement, unrelated save and restart: %+v", state)
	}
	u.RecordActivity()
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(srv.reports) != 0 || u.rec != nil {
		t.Fatal("stale acknowledgement restored collection")
	}
	if _, err := os.Stat(filepath.Join(config, telemetryFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale acknowledgement recreated pending file: %v", err)
	}
}

func TestTelemetryWithoutAnEndpointWritesNothing(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default on", true: "legacy off"}[disabled], func(t *testing.T) {
			config := t.TempDir()
			settingsPath := filepath.Join(config, "settings.json")
			if disabled {
				if err := os.WriteFile(settingsPath, []byte(`{"usageStatsOff":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(config, telemetryFileName)
			before := []byte(`{"install":"00000000-0000-4000-8000-000000000001","days":[]}`)
			if err := os.WriteFile(path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			d := NewDriveService(DriveOptions{ConfigDir: config, NoIndexSync: true, Logger: quiet()}, nil)
			u := NewTelemetryService(d, "", "dev")
			want := TelemetryState{Enabled: !disabled}
			if state := u.State(); state != want {
				t.Fatalf("no-endpoint state = %+v, want %+v", state, want)
			}
			if state, err := u.AcknowledgeNotice(); err != nil || state != want {
				t.Fatalf("no-endpoint acknowledgement = %+v, %v", state, err)
			}
			u.client = func() *http.Client {
				t.Fatal("no-endpoint build attempted a report")
				return nil
			}
			if err := u.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
				t.Fatal(err)
			}
			u.RecordActivity()
			if err := u.send(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := u.ServiceShutdown(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) || u.rec != nil {
				t.Fatalf("no-endpoint build touched activity: %s, %v", after, err)
			}
			if disabled {
				data, err := os.ReadFile(settingsPath)
				if err != nil || string(data) != `{"usageStatsOff":true}` {
					t.Fatalf("no-endpoint build rewrote settings: %s, %v", data, err)
				}
			} else if _, err := os.Stat(settingsPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("no-endpoint build wrote settings: %v", err)
			}
		})
	}
}

func TestTelemetryStartupPreservesExistingOptInAndMarksToday(t *testing.T) {
	config := t.TempDir()
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"usageStatsEnabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	u, _, _ := newTelemetryForTest(t, config)
	if state := u.State(); !state.Enabled || state.NeedsNotice {
		t.Fatalf("existing opt-in lost: %+v", state)
	}
	if err := u.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	restarted, srv, _ := newTelemetryForTest(t, config)
	if err := restarted.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-10-17": {"app.open": 1}}) {
		t.Fatalf("startup activity not saved: %v", got)
	}
}

func TestTelemetryEnableMarksTodayAfterStartingOff(t *testing.T) {
	u, srv, clock := newTelemetryForTest(t, t.TempDir())
	if _, err := u.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := u.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().AddDate(0, 0, 1))
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-10-18": {"app.open": 1}}) {
		t.Fatalf("enable backfilled inactive days: %v", got)
	}
}

func TestTelemetrySanitizesLegacyPendingAndPrunesByCalendarAge(t *testing.T) {
	config := t.TempDir()
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"usageStatsEnabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	record := `{"install":"00000000-0000-4000-8000-000000000001","days":[
		{"day":"2026-09-17","counts":{"app.open":1}},
		{"day":"2026-09-18","counts":{"app.open":8,"search":200}},
		{"day":"2026-10-15","counts":{"play.video":3}},
		{"day":"2026-10-16","counts":{"app.open":9,"upload":30}},
		{"day":"2026-10-16","counts":{"app.open":2}},
		{"day":"2026-10-17","counts":{"app.open":0,"search":3}},
		{"day":"2026-10-18","counts":{"app.open":1}},
		{"day":"2026-02-30","counts":{"app.open":1}}
	]}`
	path := filepath.Join(config, telemetryFileName)
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	u, srv, clock := newTelemetryForTest(t, config)
	srv.status = http.StatusServiceUnavailable
	if err := u.send(context.Background()); err == nil {
		t.Fatal("503 treated as delivered")
	}
	rep := srv.last(t)
	want := map[string]map[string]int{"2026-09-18": {"app.open": 1}, "2026-10-16": {"app.open": 1}}
	if rep.Install != "00000000-0000-4000-8000-000000000001" || len(rep.Days) != 2 || !equalDays(dayCounts(rep), want) {
		t.Fatalf("legacy data was not sanitized: %+v", rep)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved telemetryRecord
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Days) != 2 || !equalDays(dayCounts(telemetryReport{Days: saved.Days}), want) {
		t.Fatalf("unsafe legacy data still on disk: %+v", saved)
	}
	clock.set(clock.now().AddDate(0, 0, 31))
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(srv.reports) != 1 || len(u.rec.Days) != 0 {
		t.Fatalf("expired sparse days retained or resent: %+v", u.rec)
	}
	u.RecordActivity()
	srv.status = http.StatusNoContent
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-11-17": {"app.open": 1}}) {
		t.Fatalf("activity after long outage = %v", got)
	}
}

func TestTelemetryEnableWakesLoopWithoutWaitingForNetwork(t *testing.T) {
	u, srv, _ := newTelemetryForTest(t, t.TempDir())
	if _, err := u.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	received := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	srv.during = func() {
		close(received)
		<-release
	}
	if err := u.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = u.ServiceShutdown() })
	answered := make(chan error, 1)
	go func() {
		_, err := u.SetEnabled(true)
		answered <- err
	}()
	select {
	case err := <-answered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("enabling waited for a blocked network request")
	}
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("enabling did not wake the reporting loop")
	}
	if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-10-17": {"app.open": 1}}) {
		t.Fatalf("prompt report = %v", got)
	}
	unblock()
}

func TestTelemetryForegroundDayAddedDuringSendSurvivesAcknowledgement(t *testing.T) {
	u, srv, clock := newTelemetryForTest(t, t.TempDir())
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	srv.during = func() {
		clock.set(clock.now().Add(4 * time.Hour))
		u.RecordActivity()
	}
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.during = nil
	srv.mu.Unlock()
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dayCounts(srv.last(t)); !equalDays(got, map[string]map[string]int{"2026-10-18": {"app.open": 1}}) {
		t.Fatalf("midnight foreground activity lost: %v", got)
	}
}

func TestTelemetryOffDuringSendCannotRestoreOldIdentity(t *testing.T) {
	config := t.TempDir()
	u, srv, _ := newTelemetryForTest(t, config)
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	first := u.rec.Install
	during := make(chan error, 1)
	srv.during = func() {
		if _, err := u.SetEnabled(false); err != nil {
			during <- err
			return
		}
		u.RecordActivity()
		if _, err := os.Stat(filepath.Join(config, telemetryFileName)); !errors.Is(err, os.ErrNotExist) {
			during <- errors.New("pending file survived disable during send")
			return
		}
		_, err := u.SetEnabled(true)
		during <- err
	}
	// Cancellation cannot retract an already received request, but its completion
	// must not clear or save the new identity created by re-enabling.
	_ = u.send(context.Background())
	if err := <-during; err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.during = nil
	srv.mu.Unlock()
	second := u.rec.Install
	if second == first {
		t.Fatal("off/on reused the old installation identity")
	}
	data, err := os.ReadFile(filepath.Join(config, telemetryFileName))
	if err != nil {
		t.Fatal(err)
	}
	var saved telemetryRecord
	if err := json.Unmarshal(data, &saved); err != nil || saved.Install != second {
		t.Fatalf("old send overwrote new record: %+v, %v", saved, err)
	}
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rep := srv.last(t); rep.Install != second || !equalDays(dayCounts(rep), map[string]map[string]int{"2026-10-17": {"app.open": 1}}) {
		t.Fatalf("new installation report = %+v", rep)
	}
}

func TestTelemetryFailedSettingsSaveDoesNotPretendSuccess(t *testing.T) {
	u, _, _ := newTelemetryForTest(t, t.TempDir())
	u.RecordActivity()
	first := u.rec.Install
	failure := errors.New("settings unavailable")
	u.drive.configErr = failure
	if state, err := u.AcknowledgeNotice(); !errors.Is(err, failure) || !state.Enabled || !state.NeedsNotice || u.rec.Install != first {
		t.Fatalf("failed acknowledgement changed state: %+v, %v", state, err)
	}
	if state, err := u.SetEnabled(false); !errors.Is(err, failure) || !state.Enabled || !state.NeedsNotice || u.rec.Install != first {
		t.Fatalf("failed disable changed state: %+v, %v", state, err)
	}
	u.drive.configErr = nil
	if state, err := u.AcknowledgeNotice(); err != nil || !state.Enabled || state.NeedsNotice || u.rec.Install != first {
		t.Fatalf("acknowledgement retry = %+v, %v", state, err)
	}
	u.drive.configErr = failure
	if state, err := u.SetEnabled(false); !errors.Is(err, failure) || !state.Enabled || state.NeedsNotice || u.rec.Install != first {
		t.Fatalf("failed disable reported success: %+v, %v", state, err)
	}
	u.drive.configErr = nil
	if _, err := u.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	u.drive.configErr = failure
	if state, err := u.SetEnabled(true); !errors.Is(err, failure) || state.Enabled || state.NeedsNotice || u.rec != nil {
		t.Fatalf("failed enable reported success: %+v, %v", state, err)
	}
}

func equalDays(a, b map[string]map[string]int) bool {
	return maps.EqualFunc(a, b, maps.Equal)
}
