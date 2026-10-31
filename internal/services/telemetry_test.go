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

func TestTelemetryReportsDailyCountsAndKeepsTodayUntilItEnds(t *testing.T) {
	config := t.TempDir()
	u, srv, clock := newTelemetryForTest(t, config)
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	u.Count("route.files")
	u.Count("route.files")
	u.Count("play.video")
	u.Count("a file name.mkv") // not a usage name: dropped
	clock.set(clock.now().Add(4 * time.Hour))
	u.Count("upload")

	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep := srv.last(t)
	if !validInstallID(rep.Install) || rep.Version != "1.2.3" || rep.OS == "" || rep.Arch == "" || rep.Locale == "" {
		t.Fatalf("report header = %+v", rep)
	}
	want := map[string]map[string]int{
		"2026-10-17": {"route.files": 2, "play.video": 1},
		"2026-10-18": {"upload": 1},
	}
	if got := dayCounts(rep); !equalDays(got, want) {
		t.Fatalf("days sent = %v, want %v", got, want)
	}

	// The server has yesterday: only today, still growing, is sent again,
	// as a running total under the same install.
	u.Count("upload")
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := srv.last(t)
	if again.Install != rep.Install {
		t.Fatalf("install id changed: %s → %s", rep.Install, again.Install)
	}
	if got := dayCounts(again); !equalDays(got, map[string]map[string]int{"2026-10-18": {"upload": 2}}) {
		t.Fatalf("second report = %v", got)
	}
}

func TestTelemetryKeepsDaysTheServerDidNotTake(t *testing.T) {
	config := t.TempDir()
	u, srv, clock := newTelemetryForTest(t, config)
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	u.Count("search")
	clock.set(clock.now().Add(4 * time.Hour))
	srv.status = http.StatusServiceUnavailable
	if err := u.send(context.Background()); err == nil {
		t.Fatal("a 503 was taken as delivered")
	}
	// A restart reads what was kept.
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	next, srv2, clock2 := newTelemetryForTest(t, config)
	clock2.set(clock.now())
	next.Count("download")
	if err := next.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]int{"2026-10-17": {"search": 1}, "2026-10-18": {"download": 1}}
	if got := dayCounts(srv2.last(t)); !equalDays(got, want) {
		t.Fatalf("after the outage = %v, want %v", got, want)
	}
}

func TestTelemetryDoesNotDropADayCountedIntoWhileSending(t *testing.T) {
	u, srv, clock := newTelemetryForTest(t, t.TempDir())
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	u.Count("search")
	// Midnight passes while the report is on its way, after one more search
	// on the old day was counted.
	srv.during = func() {
		u.Count("search")
		clock.set(clock.now().Add(4 * time.Hour))
	}
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.during = nil
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := dayCounts(srv.last(t)); got["2026-10-17"]["search"] != 2 {
		t.Fatalf("the late count was lost: %v", got)
	}
}

func TestTelemetryOffForgetsTheInstallAndCountsNothing(t *testing.T) {
	config := t.TempDir()
	u, srv, _ := newTelemetryForTest(t, config)
	if _, err := u.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	u.Count("route.cleanup")
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, telemetryFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("counts not kept: %v", err)
	}
	first := u.rec.Install

	if st, err := u.SetEnabled(false); err != nil || st.Enabled {
		t.Fatalf("SetEnabled(false) = %+v, %v", st, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("telemetry.json survived switching off: %v", err)
	}
	u.Count("route.cleanup")
	if err := u.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(srv.reports) != 0 {
		t.Fatalf("sent while off: %+v", srv.reports)
	}

	// The choice survives a restart; on again is a new install.
	again, srv2, _ := newTelemetryForTest(t, config)
	if again.State().Enabled {
		t.Fatal("off was not saved")
	}
	if _, err := again.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	again.Count("route.cleanup")
	if err := again.send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rep := srv2.last(t); rep.Install == first {
		t.Fatal("switching off and on kept the old install id")
	}
}

func TestTelemetryConsentAndLegacyMigration(t *testing.T) {
	for _, settings := range []string{"", `{}`, `{"usageStatsOff":true}`, `{"usageStatsOff":false}`} {
		t.Run(settings, func(t *testing.T) {
			config := t.TempDir()
			if settings != "" {
				if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(config, telemetryFileName)
			if settings != "" {
				if err := os.WriteFile(path, []byte(`{"install":"old","days":[]}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			u, srv, _ := newTelemetryForTest(t, config)
			if u.State().Enabled {
				t.Fatal("statistics enabled without consent")
			}
			u.Count("search")
			if err := u.send(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(srv.reports) != 0 {
				t.Fatal("report sent without consent")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old counts remain: %v", err)
			}
			if _, err := u.SetEnabled(true); err != nil {
				t.Fatal(err)
			}
			restarted, _, _ := newTelemetryForTest(t, config)
			if !restarted.State().Enabled {
				t.Fatal("consent not persisted")
			}
			if _, err := restarted.SetEnabled(false); err != nil {
				t.Fatal(err)
			}
			off, _, _ := newTelemetryForTest(t, config)
			if off.State().Enabled {
				t.Fatal("disable not persisted")
			}
		})
	}
}

func TestTelemetryWithoutAnEndpointWritesNothing(t *testing.T) {
	config := t.TempDir()
	d := NewDriveService(DriveOptions{ConfigDir: config, NoIndexSync: true, Logger: quiet()}, nil)
	u := NewTelemetryService(d, "", "dev")
	if err := u.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	u.Count("route.files")
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config, telemetryFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a build without an endpoint kept counts: %v", err)
	}
}

func equalDays(a, b map[string]map[string]int) bool {
	return maps.EqualFunc(a, b, maps.Equal)
}
