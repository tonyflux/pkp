package services

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/update"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func isolatedUpdater(t *testing.T) *UpdateService {
	t.Helper()
	d := lockDrive(t, t.TempDir())
	u := NewUpdateService(d, nil, nil, "0.1.0", true)
	u.platform, u.osVersion, u.state.Supported, u.cacheDir = "darwin-universal", "12.0", true, t.TempDir()
	t.Cleanup(func() { _ = u.ServiceShutdown() })
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := "release package"
	h := sha256.Sum256([]byte(body))
	m := update.Manifest{Version: "0.2.0", PublishedAt: "2026-10-22T08:00:00Z", Notes: "Release notes", Packages: map[string]update.Package{}}
	assets := []map[string]string{}
	for _, name := range []string{"update.json", "update.json.sig", update.PackageName(m.Version, "darwin-universal"), update.PackageName(m.Version, "windows-x64-arm64")} {
		assets = append(assets, map[string]string{"name": name, "browser_download_url": update.AssetURL(m.Version, name)})
	}
	for _, platform := range []string{"darwin-universal", "windows-x64-arm64"} {
		m.Packages[platform] = update.Package{URL: update.AssetURL(m.Version, update.PackageName(m.Version, platform)), Size: int64(len(body)), SHA256: hex.EncodeToString(h[:]), MinOS: "12.0"}
	}
	data, _ := json.Marshal(m)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))
	release, _ := json.Marshal(map[string]any{"tag_name": "v0.2.0", "assets": assets})
	u.client = &update.Client{PublicKey: pub, HTTP: &http.Client{CheckRedirect: update.Redirect, Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		payload := body
		switch {
		case r.URL.String() == update.LatestURL:
			payload = string(release)
		case strings.HasSuffix(r.URL.Path, "/update.json"):
			payload = string(data)
		case strings.HasSuffix(r.URL.Path, "/update.json.sig"):
			payload = sig
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: -1, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}}
	return u
}

func TestUpdatePreferencesManualCheckAndTamperedInstaller(t *testing.T) {
	u := isolatedUpdater(t)
	if _, err := u.SetAutomatic(false); err != nil {
		t.Fatal(err)
	}
	if err := u.Check(); err != nil {
		t.Fatal(err)
	}
	u.wg.Wait()
	state := u.State()
	if state.Automatic || state.Phase != "available" || state.Info == nil || state.Info.Version != "0.2.0" {
		t.Fatalf("manual check with auto off: %+v", state)
	}
	originalTransport := u.client.HTTP.Transport
	u.client.HTTP.Transport = updateTransport(func(*http.Request) (*http.Response, error) {
		t.Error("automatic check sent a request while disabled")
		return nil, errors.New("unexpected request")
	})
	if err := u.check(false); err != nil {
		t.Fatal(err)
	}
	u.wg.Wait()
	u.client.HTTP.Transport = originalTransport
	if err := u.Ignore(); err != nil {
		t.Fatal(err)
	}
	if _, err := u.SetAutomatic(true); err != nil {
		t.Fatal(err)
	}
	if err := u.check(false); err != nil {
		t.Fatal(err)
	}
	u.wg.Wait()
	if u.State().Notice {
		t.Fatal("ignored release notified automatically")
	}
	if err := u.Check(); err != nil {
		t.Fatal(err)
	}
	u.wg.Wait()
	if u.State().Info == nil {
		t.Fatal("manual check hid ignored version")
	}
	if _, err := u.SetAutomatic(false); err != nil {
		t.Fatal(err)
	}
	if err := u.Download(); err != nil {
		t.Fatal(err)
	}
	u.wg.Wait()
	if u.State().Phase != "ready" {
		t.Fatalf("download failed: %+v", u.State())
	}
	staged := u.path
	if _, err := u.SetAutomatic(true); err != nil {
		t.Fatal(err)
	}
	if err := u.check(false); err != nil {
		t.Fatal(err)
	}
	u.wg.Wait()
	if u.path != staged || u.State().Phase != "ready" {
		t.Fatal("background check discarded staged installer")
	}
	if _, err := u.SetAutomatic(false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.path, []byte("corrupt package"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(); !errors.Is(err, update.ErrIntegrity) {
		t.Fatalf("tampered installer handoff: %v", err)
	}
	if u.path != "" || u.State().Phase != "error" {
		t.Fatal("tampered package remains installable")
	}
	if err := u.Download(); err != nil {
		t.Fatal(err)
	}
	u.wg.Wait()
	if u.State().Phase != "ready" {
		t.Fatal("cannot redownload after integrity failure")
	}
	if s := u.drive.LocalSettings(); !s.UpdateChecksOff || s.UpdateIgnored != "0.2.0" || s.UpdateLastCheck == "" {
		t.Fatalf("preferences not retained: %+v", s)
	}
}

func TestUpdateCancellationAndShutdownStopInFlightRequests(t *testing.T) {
	u := isolatedUpdater(t)
	entered := make(chan struct{})
	u.client.HTTP.Transport = updateTransport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	if err := u.Check(); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := u.Download(); err == nil {
		t.Fatal("overlapping operation allowed")
	}
	u.Cancel()
	u.wg.Wait()
	if s := u.State(); s.Phase != "idle" || s.Error != "" {
		t.Fatalf("cancel reported failure: %+v", s)
	}
	entered = make(chan struct{})
	if err := u.Check(); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := u.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(u.ctx.Err(), context.Canceled) {
		t.Fatal("shutdown left request running")
	}
	if err := u.Check(); err == nil {
		t.Fatal("closed updater accepted another check")
	}
}

func TestQuitCancellationCannotInstallOnLaterQuit(t *testing.T) {
	m := &MenuBarService{}
	started, cancelled := false, false
	m.updateQuit = &updateQuit{start: func() error { started = true; return nil }, cancel: func() { cancelled = true }}
	m.cancelUpdateInstall()
	if !m.approveQuit() || started || !cancelled {
		t.Fatal("cancelled update leaked into a later quit")
	}
}

func TestInstallerLaunchFailureKeepsApplicationRunning(t *testing.T) {
	m := &MenuBarService{updateQuit: &updateQuit{start: func() error { return errors.New("UAC cancelled") }, cancel: func() {}}}
	if m.approveQuit() || m.quitting.Load() {
		t.Fatal("installer launch failure forced quit")
	}
	if m.updateQuit != nil {
		t.Fatal("failed handoff retained")
	}
}

func TestUpdateResultIsShownOnceForTheInstalledVersion(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, updateReceiptName)
	if err := os.WriteFile(path, []byte(`{"version":"0.2.0","error":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if phase, code := consumeUpdateResult(cache, "0.2.0"); phase != "updated" || code != "" {
		t.Fatalf("installed update = %q, %q", phase, code)
	}
	if phase, code := consumeUpdateResult(cache, "0.2.0"); phase != "" || code != "" {
		t.Fatalf("update completion repeated = %q, %q", phase, code)
	}
	if err := os.WriteFile(path, []byte(`{"version":"0.2.0","error":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if phase, code := consumeUpdateResult(cache, "0.1.0"); phase != "" || code != "" {
		t.Fatalf("old version claimed successful update = %q, %q", phase, code)
	}
	if err := os.WriteFile(path, []byte(`{"version":"0.2.0","error":"installRollback"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if phase, code := consumeUpdateResult(cache, "0.1.0"); phase != "error" || code != "installRollback" {
		t.Fatalf("recovery failure hidden on old version = %q, %q", phase, code)
	}
}
