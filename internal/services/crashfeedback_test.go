package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func feedbackForTest(t *testing.T, endpoint string) (*CrashFeedbackService, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "PKP-Crash-20261116-120000-123.log")
	if err := os.WriteFile(path, []byte("panic: private token\nstack"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewCrashFeedbackService(NewDriveService(DriveOptions{ConfigDir: t.TempDir(), NoIndexSync: true, Logger: quiet()}, nil), endpoint)
	s.dirs = []string{dir}
	return s, path
}

func TestCrashFeedbackConsentAndIdempotentRetry(t *testing.T) {
	var mu sync.Mutex
	var requests []struct{ ID, Report string }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/crashes" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s %s: %v", r.Method, r.URL, r.Header)
		}
		var body struct{ ID, Report string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, body)
		n := len(requests)
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s, path := feedbackForTest(t, server.URL)
	report, err := s.Pending()
	if err != nil || report == nil || report.Path != path || report.Text != "panic: private token\nstack" {
		t.Fatalf("pending = %+v, %v", report, err)
	}
	if len(requests) != 0 {
		t.Fatal("preview sent data without consent")
	}
	if err := s.Send("wrong-id", "edited"); err == nil {
		t.Fatal("accepted a different report")
	}
	if err := s.Send(report.ID, "panic: [redacted]"); err == nil {
		t.Fatal("server failure was reported as success")
	}
	if again, _ := s.Pending(); again == nil || again.ID != report.ID {
		t.Fatalf("failed submission lost the report: %+v", again)
	}
	if err := s.Send(report.ID, "panic: [redacted]"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0] != requests[1] || requests[1].Report != "panic: [redacted]" {
		t.Fatalf("sent incorrect content or retry id: %+v", requests)
	}
	if next, err := s.Pending(); err != nil || next != nil {
		t.Fatalf("sent report prompted again: %+v %v", next, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "panic: private token\nstack" {
		t.Fatalf("original was changed: %q %v", data, err)
	}
}

func TestCrashFeedbackDismissSurvivesRestartAndDoesNotSend(t *testing.T) {
	s, path := feedbackForTest(t, "https://telemetry.pkpapp.cc")
	report, err := s.Pending()
	if err != nil || report == nil {
		t.Fatalf("pending: %+v %v", report, err)
	}
	s.client = func() *http.Client { t.Fatal("dismiss attempted network"); return nil }
	if err := s.Dismiss(report.ID); err != nil {
		t.Fatal(err)
	}
	next := &CrashFeedbackService{endpoint: s.endpoint, state: s.state, dirs: s.dirs}
	if report, err := next.Pending(); err != nil || report != nil {
		t.Fatalf("dismissal not persisted: %+v %v", report, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new crash in same name"), 0600); err != nil {
		t.Fatal(err)
	}
	if report, err := next.Pending(); err != nil || report == nil || report.Text != "new crash in same name" {
		t.Fatalf("changed report missed: %+v %v", report, err)
	}
}

func TestCrashFeedbackDisabledForServer(t *testing.T) {
	s, _ := feedbackForTest(t, "https://telemetry.pkpapp.cc")
	if report, err := s.pendingReport(true); err != nil || report != nil {
		t.Fatalf("server exposed host crash report: %+v %v", report, err)
	}
}

func TestCrashFeedbackLimitsAndRedirect(t *testing.T) {
	redirected := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	s, _ := feedbackForTest(t, server.URL)
	report, err := s.Pending()
	if err != nil || report == nil {
		t.Fatalf("pending: %+v %v", report, err)
	}
	for _, text := range []string{"", strings.Repeat("中", crashPreviewBytes/3+1), strings.Repeat("\"", 70000)} {
		if err := s.Send(report.ID, text); err == nil {
			t.Fatal("accepted invalid or oversized content")
		}
	}
	if err := s.Send(report.ID, "safe"); err == nil || redirected {
		t.Fatalf("redirect leaked report: err=%v redirected=%v", err, redirected)
	}
	if report, err := s.Pending(); err != nil || report == nil {
		t.Fatalf("failure discarded report: %+v %v", report, err)
	}
}

func TestCrashFeedbackIgnoresSymlinkAndTruncatesUTF8(t *testing.T) {
	s, path := feedbackForTest(t, "https://telemetry.pkpapp.cc")
	if err := os.WriteFile(path, []byte(strings.Repeat("中", crashPreviewBytes/3+5)), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(path, filepath.Join(filepath.Dir(path), "PKP-Crash-z.log"))
	report, err := s.Pending()
	if err != nil || report == nil || !report.Truncated || len(report.Text) > crashPreviewBytes || !strings.HasPrefix(report.Text, "中") {
		t.Fatalf("truncated report: %+v %v", report, err)
	}
	if report.Path != path {
		t.Fatalf("followed symlink: %s", report.Path)
	}
	if !utf8.ValidString(report.Text) {
		t.Fatal("preview is not valid UTF-8")
	}
}
