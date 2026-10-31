// Command pkp-release prepares, verifies and publishes signed public release assets.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tonyflux/pkp/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultKey() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".ssh", "pkp-release", "update-ed25519.key")
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pkp-release version|keygen|check|snapshot|source|prepare|verify|publish [flags]")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	keyPath := fs.String("key", defaultKey(), "Ed25519 private key outside the repository")
	version := fs.String("version", "", "stable app version")
	dir := fs.String("dir", "", "release asset directory")
	sourceCommit := fs.String("source-commit", "", "clean source commit captured before building")
	notesPath := fs.String("notes", "", "UTF-8 release notes file")
	metadataOnly := fs.Bool("metadata-only", false, "check version metadata without a signing key")
	publish := fs.Bool("publish", false, "publish a verified draft as Latest (publish command only)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "version":
		data, err := os.ReadFile("build/darwin/Info.plist")
		if err != nil {
			return err
		}
		match := regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`).FindSubmatch(data)
		if len(match) != 2 {
			return errors.New("missing app version")
		}
		if _, err := update.Version(string(match[1])); err != nil {
			return err
		}
		fmt.Println(string(match[1]))
		return nil
	case "keygen":
		if *keyPath == "" {
			return errors.New("provide -key outside the repository")
		}
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*keyPath), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(*keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.WriteString(base64.StdEncoding.EncodeToString(private.Seed()) + "\n")
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Println("Private key saved to:", *keyPath)
		fmt.Println("Public key:", base64.StdEncoding.EncodeToString(pub))
		return nil
	case "snapshot":
		source, err := sourceSnapshot()
		if err != nil {
			return err
		}
		fmt.Println(source.Commit)
		return nil
	case "source":
		if *dir == "" || *sourceCommit == "" {
			return errors.New("provide -dir and -source-commit captured before building")
		}
		return packageSource(*dir, *version, *sourceCommit)
	case "check":
		if err := checkVersion(*version); err != nil {
			return err
		}
		if *notesPath != "" {
			if _, err := notes(*notesPath); err != nil {
				return err
			}
		}
		if *metadataOnly {
			fmt.Println("Version metadata verified:", *version)
			return nil
		}
		if requiresSource(*version) {
			if _, err := sourceSnapshot(); err != nil {
				return err
			}
		}
		if _, err := loadKey(*keyPath); err != nil {
			return err
		}
		fmt.Println("Version metadata and signing key verified:", *version)
		return nil
	case "prepare":
		if err := checkVersion(*version); err != nil {
			return err
		}
		key, err := loadKey(*keyPath)
		if err != nil {
			return err
		}
		notes, err := notes(*notesPath)
		if err != nil {
			return err
		}
		if *dir == "" {
			return errors.New("provide -dir")
		}
		return prepare(*dir, *version, notes, key)
	case "verify":
		if *dir == "" {
			return errors.New("provide -dir")
		}
		m, err := verifyRelease(*dir, update.PublicKey())
		if err != nil {
			return err
		}
		fmt.Printf("All %d release assets verified: %s\n", len(releaseAssets(m.Version)), m.Version)
		return nil
	case "publish":
		if *dir == "" {
			return errors.New("provide -dir")
		}
		return publishRelease(*dir, *publish)
	default:
		return errors.New("unknown command: " + args[0])
	}
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w; run pkp-release keygen once", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("signing key must be a private regular file (chmod 600)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid signing seed")
	}
	key := ed25519.NewKeyFromSeed(seed)
	if !key.Public().(ed25519.PublicKey).Equal(update.PublicKey()) {
		return nil, errors.New("signing key does not match the public key embedded in PKP")
	}
	return key, nil
}

func notes(path string) (string, error) {
	if path == "" {
		return "", errors.New("provide RELEASE_NOTES=<file> (or -notes <file>) with user-facing release notes")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(data))
	if s == "" || len(s) > 64<<10 {
		return "", errors.New("release notes must contain 1–65536 bytes")
	}
	return s, nil
}

func checkVersion(version string) error {
	if _, err := update.Version(version); err != nil {
		return errors.New("provide a stable version with three numeric components")
	}
	// Preserve the existing packaging files; fail before building if any drift.
	checks := []struct{ path, pattern, expected string }{
		{"build/darwin/Info.plist", `<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`, version},
		{"build/darwin/Info.plist", `<key>CFBundleVersion</key>\s*<string>([^<]+)</string>`, version},
		{"build/darwin/Info.dev.plist", `<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`, version},
		{"build/darwin/Info.dev.plist", `<key>CFBundleVersion</key>\s*<string>([^<]+)</string>`, version},
		{"build/config.yml", `(?m)^  version: "([^"]+)"`, version},
		{"build/windows/info.json", `"file_version":\s*"([^"]+)"`, version},
		{"build/windows/info.json", `"ProductVersion":\s*"([^"]+)"`, version},
		{"build/windows/wails.exe.manifest", `name="com.pkp.desktop" version="([^"]+)"`, version},
		{"build/windows/nsis/wails_tools.nsh", `!define INFO_PRODUCTVERSION "([^"]+)"`, version},
		{"build/windows/msix/app_manifest.xml", `Version="([^"]+)"`, version + ".0"},
	}
	for _, c := range checks {
		data, err := os.ReadFile(c.path)
		if err != nil {
			return err
		}
		m := regexp.MustCompile(c.pattern).FindSubmatch(data)
		if len(m) != 2 || string(m[1]) != c.expected {
			return fmt.Errorf("version mismatch: %s; expected %s", c.path, c.expected)
		}
	}
	return nil
}

func macMinimum() (string, error) {
	data, err := os.ReadFile("build/darwin/Info.plist")
	if err != nil {
		return "", err
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	want := false
	for {
		tok, err := decoder.Token()
		if err != nil {
			return "", errors.New("missing macOS minimum version")
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "key" {
			var key string
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return "", err
			}
			want = key == "LSMinimumSystemVersion"
		} else if start.Name.Local == "string" && want {
			var minimum string
			err := decoder.DecodeElement(&minimum, &start)
			return minimum, err
		}
	}
}

func prepare(dir, version, notes string, key ed25519.PrivateKey) error {
	minimum, err := macMinimum()
	if err != nil {
		return err
	}
	source, err := prepareSource(dir, version)
	if err != nil {
		return err
	}
	m := releaseManifest{Manifest: update.Manifest{Version: version, PublishedAt: time.Now().UTC().Format(time.RFC3339), Notes: notes, Packages: map[string]update.Package{}}, Source: source}
	for _, platform := range []string{"darwin-universal", "windows-x64-arm64"} {
		name := update.PackageName(version, platform)
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		h := sha256.New()
		size, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		minOS := minimum
		if platform == "windows-x64-arm64" {
			minOS = "10.0"
		}
		m.Packages[platform] = update.Package{URL: update.AssetURL(version, name), Size: size, SHA256: hex.EncodeToString(h.Sum(nil)), MinOS: minOS}
	}
	if err := m.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)) + "\n")
	for _, name := range []string{"update.json", "update.json.sig"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("refusing to replace %s", name)
		}
	}
	for _, asset := range []struct {
		name string
		data []byte
	}{{"update.json", data}, {"update.json.sig", sig}} {
		if err := os.WriteFile(filepath.Join(dir, asset.name), asset.data, 0644); err != nil {
			return err
		}
	}
	fmt.Println("Signed release prepared:", dir, "(not uploaded)")
	return nil
}
