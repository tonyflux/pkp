package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLinuxReleaseRetainsLegacyContractAndAuthenticatesPackages(t *testing.T) {
	m, _, _, _ := releaseFixture(t, []byte("linux payload"))
	p := m.Packages["darwin-universal"]
	m.LinuxPackages = make(map[string]Package, 8)
	for _, arch := range [...]string{"amd64", "arm64"} {
		for _, format := range [...]string{"deb", "rpm", "appimage", "sources"} {
			platform := "linux-" + arch + "-" + format
			p.URL, p.MinOS = AssetURL(m.Version, PackageName(m.Version, platform)), "2.39"
			m.LinuxPackages[platform] = p
		}
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)))
	got, err := Verify(data, sig, pub)
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		Packages map[string]Package `json:"packages"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.Packages) != 2 || legacy.Packages["darwin-universal"].URL != m.Packages["darwin-universal"].URL {
		t.Fatal("Linux extension changed legacy platform contract")
	}
	if _, ok := got.Package("linux-arm64-appimage"); !ok {
		t.Fatal("Linux package missing")
	}
	for _, scenario := range []string{"missing", "unknown", "foreign", "checksum", "baseline"} {
		t.Run(scenario, func(t *testing.T) {
			var bad Manifest
			if err := json.Unmarshal(data, &bad); err != nil {
				t.Fatal(err)
			}
			platform := "linux-arm64-appimage"
			p := bad.LinuxPackages[platform]
			switch scenario {
			case "missing":
				delete(bad.LinuxPackages, platform)
			case "unknown":
				delete(bad.LinuxPackages, platform)
				bad.LinuxPackages["linux-386-appimage"] = p
			case "foreign":
				p.URL = "https://example.org/PKP.AppImage"
				bad.LinuxPackages[platform] = p
			case "checksum":
				p.SHA256 = "not a digest"
				bad.LinuxPackages[platform] = p
			case "baseline":
				p.MinOS = "6.8"
				bad.LinuxPackages[platform] = p
			}
			if !errors.Is(bad.Validate(), ErrManifest) {
				t.Fatal("unsafe Linux metadata accepted")
			}
		})
	}
	assets := []map[string]string{{"name": "update.json", "browser_download_url": AssetURL(m.Version, "update.json")}, {"name": "update.json.sig", "browser_download_url": AssetURL(m.Version, "update.json.sig")}}
	for _, group := range []map[string]Package{m.Packages, m.LinuxPackages} {
		for platform, p := range group {
			assets = append(assets, map[string]string{"name": PackageName(m.Version, platform), "browser_download_url": p.URL})
		}
	}
	release, _ := json.Marshal(map[string]any{"tag_name": "v" + m.Version, "assets": assets})
	client := Client{PublicKey: pub, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		payload := data
		switch {
		case r.URL.String() == LatestURL:
			payload = release
		case strings.HasSuffix(r.URL.Path, ".sig"):
			payload = sig
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
	})}}
	if _, err := client.Latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, asset := range assets {
		if asset["name"] == PackageName(m.Version, "linux-arm64-appimage") {
			assets = append(assets[:i], assets[i+1:]...)
			break
		}
	}
	release, _ = json.Marshal(map[string]any{"tag_name": "v" + m.Version, "assets": assets})
	if _, err := client.Latest(context.Background()); !errors.Is(err, ErrManifest) {
		t.Fatalf("missing Linux release asset accepted: %v", err)
	}
}
