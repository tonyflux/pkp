package update

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
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func releaseFixture(t *testing.T, body []byte) (Manifest, []byte, []byte, ed25519.PublicKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	m := Manifest{Version: "0.2.0", PublishedAt: "2026-10-22T08:00:00Z", Notes: "Release notes", Packages: map[string]Package{}}
	for _, platform := range []string{"darwin-universal", "windows-x64-arm64"} {
		m.Packages[platform] = Package{URL: AssetURL(m.Version, PackageName(m.Version, platform)), Size: int64(len(body)), SHA256: hex.EncodeToString(h[:]), MinOS: "12.0"}
	}
	data, _ := json.Marshal(m)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)))
	return m, data, sig, pub
}

func TestSignedReleaseDownloadAndInstallRecheck(t *testing.T) {
	body := []byte("a complete release payload")
	m, data, sig, pub := releaseFixture(t, body)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range []string{"Authorization", "Cookie", "X-Install-ID"} {
			if r.Header.Get(header) != "" {
				t.Errorf("private header leaked: %s", header)
			}
		}
		switch {
		case r.URL.Path == "/repos/tonyflux/pkp/releases/latest":
			assets := []map[string]string{}
			for _, name := range []string{"update.json", "update.json.sig", PackageName(m.Version, "darwin-universal"), PackageName(m.Version, "windows-x64-arm64")} {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": AssetURL(m.Version, name)})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + m.Version, "draft": false, "prerelease": false, "assets": assets})
		case strings.HasSuffix(r.URL.Path, "/update.json"):
			w.Write(data)
		case strings.HasSuffix(r.URL.Path, "/update.json.sig"):
			w.Write(sig)
		case strings.HasSuffix(r.URL.Path, ".dmg"):
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	origin, _ := url.Parse(server.URL)
	transport := server.Client().Transport
	client := &Client{HTTP: &http.Client{CheckRedirect: Redirect, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *r.URL
		copy.URL = &u
		copy.URL.Scheme, copy.URL.Host = origin.Scheme, origin.Host
		return transport.RoundTrip(copy)
	})}, PublicKey: pub}
	got, err := client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != m.Version || got.Notes != m.Notes {
		t.Fatalf("wrong release: %+v", got)
	}
	p := got.Packages["darwin-universal"]
	path, err := client.Download(context.Background(), p, t.TempDir(), PackageName(m.Version, "darwin-universal"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(path, p); err != nil {
		t.Fatal(err)
	}
	corrupted := append([]byte(nil), body...)
	corrupted[0] ^= 1
	if err := os.WriteFile(path, corrupted, 0600); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(CheckFile(path, p), ErrIntegrity) {
		t.Fatal("modified cached installer accepted")
	}
}

func TestManifestRejectsTamperingAndWrongKey(t *testing.T) {
	_, data, sig, pub := releaseFixture(t, []byte("payload"))
	data[0] ^= 1
	if _, err := Verify(data, sig, pub); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered manifest: %v", err)
	}
	data[0] ^= 1
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify(data, sig, other); !errors.Is(err, ErrSignature) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestManifestRejectsUntrustedPackageAndUnsupportedFormat(t *testing.T) {
	m, _, _, _ := releaseFixture(t, []byte("payload"))
	p := m.Packages["darwin-universal"]
	p.URL = "https://evil.example/update.dmg"
	m.Packages["darwin-universal"] = p
	if !errors.Is(m.Validate(), ErrManifest) {
		t.Fatal("untrusted package URL accepted")
	}
	m, _, _, _ = releaseFixture(t, []byte("payload"))
	m.PublishedAt = "not a date"
	if !errors.Is(m.Validate(), ErrManifest) {
		t.Fatal("invalid release date accepted")
	}
}

func TestDownloadRemovesFailedAndCancelledFiles(t *testing.T) {
	body := "expected payload"
	m, _, _, _ := releaseFixture(t, []byte(body))
	for _, scenario := range []string{"checksum", "oversize", "truncated", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			payload := body
			switch scenario {
			case "checksum":
				payload = "incorrect data!!"
			case "oversize":
				payload += "extra"
			case "truncated":
				payload = payload[:3]
			}
			client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, ContentLength: -1, Body: io.NopCloser(strings.NewReader(payload)), Header: http.Header{}}, nil
			})}}
			progress := func(int64) {
				if scenario == "cancelled" {
					cancel()
				}
			}
			path, err := client.Download(ctx, m.Packages["darwin-universal"], dir, "release.dmg", progress)
			if err == nil || path != "" {
				t.Fatalf("bad download accepted: %q %v", path, err)
			}
			files, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 0 {
				t.Fatal("failed download retained")
			}
		})
	}
}

func TestRedirectRejectsDowngradeAndUnknownHosts(t *testing.T) {
	for _, target := range []string{"http://github.com/a", "https://evil.example/a", "https://github.com.evil.example/a", "https://user:secret@github.com/a", "https://github.com:444/a"} {
		u, _ := url.Parse(target)
		if Redirect(&http.Request{URL: u}, nil) == nil {
			t.Fatalf("unsafe redirect accepted: %s", target)
		}
	}
	client := &Client{HTTP: &http.Client{CheckRedirect: Redirect, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"http://evil.example/package"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Latest(ctx); err == nil {
		t.Fatal("HTTP client followed unsafe redirect")
	}
}

func TestStableVersionsAndOSCompatibility(t *testing.T) {
	for _, row := range []struct {
		candidate, current string
		newer              bool
	}{
		{"0.10.0", "0.9.9", true}, {"0.2.0", "0.2.0", false}, {"0.1.9", "0.2.0", false},
		{"1.0.0-beta.1", "0.2.0", false}, {"1.0.0", "unknown", false}, {"01.0.0", "0.2.0", false},
	} {
		if Newer(row.candidate, row.current) != row.newer {
			t.Errorf("%s > %s", row.candidate, row.current)
		}
	}
	if !SupportsOS("12.0.0", "12.0") || SupportsOS("11.9.9", "12.0") || SupportsOS("unknown", "12.0") {
		t.Fatal("incorrect OS compatibility")
	}
}
