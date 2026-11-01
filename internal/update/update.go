// Package update implements the signed, stable GitHub release protocol.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const Repository = "tonyflux/pkp"
const LatestURL = "https://api.github.com/repos/" + Repository + "/releases/latest"
const MaxManifest = 128 << 10
const MaxPackage = 2 << 30

var (
	ErrSignature         = errors.New("update: invalid signature")
	ErrManifest          = errors.New("update: invalid manifest")
	ErrIntegrity         = errors.New("update: package checksum or size mismatch")
	ErrNoRelease         = errors.New("update: no published release")
	ErrRateLimit         = errors.New("update: GitHub rate limit")
	ErrInstallLocation   = errors.New("update: app must run from an installed writable volume")
	ErrInstallPermission = errors.New("update: replacement authorization denied")
	ErrInstallBusy       = errors.New("update: another app instance is using this installation")
	ErrInstallRollback   = errors.New("update: could not restore the previous application")
)

type Package struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	MinOS  string `json:"minOS"`
}

type Manifest struct {
	Version     string             `json:"version"`
	PublishedAt string             `json:"publishedAt"`
	Notes       string             `json:"notes"`
	Packages    map[string]Package `json:"packages"`
}

// Version accepts stable three-part versions only. Unknown and development builds
// cannot accidentally compare as an old stable release.
func Version(s string) ([3]uint64, error) {
	var v [3]uint64
	parts := strings.Split(s, ".")
	if len(parts) != len(v) {
		return v, ErrManifest
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return v, ErrManifest
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return v, ErrManifest
			}
		}
		n, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return v, ErrManifest
		}
		v[i] = n
	}
	return v, nil
}

func Newer(candidate, current string) bool {
	a, ea := Version(candidate)
	b, eb := Version(current)
	if ea != nil || eb != nil {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func SupportsOS(current, minimum string) bool {
	parse := func(s string) ([]uint64, bool) {
		parts := strings.Split(s, ".")
		if len(parts) < 2 || len(parts) > 4 {
			return nil, false
		}
		out := make([]uint64, 4)
		for i, p := range parts {
			if p == "" {
				return nil, false
			}
			for _, c := range p {
				if c < '0' || c > '9' {
					return nil, false
				}
			}
			n, err := strconv.ParseUint(p, 10, 32)
			if err != nil {
				return nil, false
			}
			out[i] = n
		}
		return out, true
	}
	a, ok := parse(current)
	if !ok {
		return false
	}
	b, ok := parse(minimum)
	if !ok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

func AssetURL(version, name string) string {
	return "https://github.com/" + Repository + "/releases/download/v" + version + "/" + name
}

func PackageName(version, platform string) string {
	switch platform {
	case "darwin-universal":
		return "PKP-" + version + "-macos-universal.dmg"
	case "windows-x64-arm64":
		return "PKP-" + version + "-windows-x64-arm64-setup.exe"
	default:
		return ""
	}
}

func (m Manifest) Validate() error {
	if _, err := Version(m.Version); err != nil {
		return ErrManifest
	}
	if _, err := time.Parse(time.RFC3339, m.PublishedAt); err != nil {
		return ErrManifest
	}
	if strings.TrimSpace(m.Notes) == "" || len(m.Notes) > 64<<10 || len(m.Packages) != 2 {
		return ErrManifest
	}
	for _, platform := range []string{"darwin-universal", "windows-x64-arm64"} {
		p, ok := m.Packages[platform]
		digest, err := hex.DecodeString(p.SHA256)
		if !ok || p.Size <= 0 || p.Size > MaxPackage || err != nil || len(digest) != sha256.Size || !SupportsOS(p.MinOS, p.MinOS) || p.URL != AssetURL(m.Version, PackageName(m.Version, platform)) {
			return ErrManifest
		}
	}
	return nil
}

// Verify authenticates the original bytes, not a re-encoded JSON object.
func Verify(data, signature []byte, public ed25519.PublicKey) (Manifest, error) {
	var m Manifest
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if len(data) > MaxManifest || err != nil || len(public) != ed25519.PublicKeySize || !ed25519.Verify(public, data, sig) {
		return m, ErrSignature
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, ErrManifest
	}
	if err := m.Validate(); err != nil {
		return m, err
	}
	return m, nil
}

// Client uses the application's proxy transport without cookies or account headers.
// A separate client also gives large packages a deadline independent of API calls.
type Client struct {
	HTTP      *http.Client
	PublicKey ed25519.PublicKey
}

func AllowedURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	switch u.Host {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}

func Redirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 || !AllowedURL(req.URL) {
		return errors.New("update: forbidden redirect")
	}
	return nil
}

func (c *Client) get(ctx context.Context, address string) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil || !AllowedURL(u) {
		return nil, ErrManifest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "PKP-Updater")
	req.Header.Set("Accept", "application/octet-stream")
	if u.Host == "api.github.com" {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, ErrNoRelease
		case http.StatusForbidden, http.StatusTooManyRequests:
			return nil, ErrRateLimit
		}
		return nil, fmt.Errorf("update: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func (c *Client) read(ctx context.Context, address string, limit int64) ([]byte, error) {
	resp, err := c.get(ctx, address)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrManifest
	}
	return data, nil
}

func (c *Client) Latest(ctx context.Context) (Manifest, error) {
	var empty Manifest
	data, err := c.read(ctx, LatestURL, 256<<10)
	if err != nil {
		return empty, err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.Unmarshal(data, &release) != nil || release.Draft || release.Prerelease || !strings.HasPrefix(release.Tag, "v") {
		return empty, ErrManifest
	}
	version := strings.TrimPrefix(release.Tag, "v")
	if _, err := Version(version); err != nil {
		return empty, err
	}
	assets := map[string]string{}
	for _, a := range release.Assets {
		if _, exists := assets[a.Name]; exists {
			return empty, ErrManifest
		}
		assets[a.Name] = a.URL
	}
	for _, name := range []string{"update.json", "update.json.sig"} {
		if assets[name] != AssetURL(version, name) {
			return empty, ErrManifest
		}
	}
	data, err = c.read(ctx, assets["update.json"], MaxManifest)
	if err != nil {
		return empty, err
	}
	sig, err := c.read(ctx, assets["update.json.sig"], 256)
	if err != nil {
		return empty, err
	}
	m, err := Verify(data, sig, c.PublicKey)
	if err != nil {
		return empty, err
	}
	if m.Version != version {
		return empty, ErrManifest
	}
	for platform, p := range m.Packages {
		if assets[PackageName(version, platform)] != p.URL {
			return empty, ErrManifest
		}
	}
	return m, nil
}

// Download creates a private random file and exposes it only after verification.
// Cancelled or failed downloads are removed; callers never execute a .part file.
func (c *Client) Download(ctx context.Context, p Package, dir, name string, progress func(int64)) (path string, err error) {
	if p.Size <= 0 || p.Size > MaxPackage {
		return "", ErrManifest
	}
	if filepath.Base(name) != name || name == "." {
		return "", ErrManifest
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".update-*.part")
	if err != nil {
		return "", err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(f.Name())
		}
	}()
	resp, err := c.get(ctx, p.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength >= 0 && resp.ContentLength != p.Size {
		return "", ErrIntegrity
	}
	hash := sha256.New()
	reader := io.LimitReader(resp.Body, p.Size+1)
	buf := make([]byte, 128<<10)
	var total int64
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > p.Size {
				return "", ErrIntegrity
			}
			if _, err = f.Write(buf[:n]); err != nil {
				return "", err
			}
			hash.Write(buf[:n])
			if progress != nil {
				progress(total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
		if err = ctx.Err(); err != nil {
			return "", err
		}
	}
	if total != p.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), p.SHA256) {
		return "", ErrIntegrity
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	// Keep the random directory, but a platform-recognisable extension for open/ShellExecute.
	path = strings.TrimSuffix(f.Name(), ".part") + filepath.Ext(name)
	if err = os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func CheckFile(path string, p Package) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != p.Size {
		return ErrIntegrity
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, p.Size+1)); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), p.SHA256) {
		return ErrIntegrity
	}
	return nil
}
