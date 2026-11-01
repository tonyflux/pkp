package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/update"
)

func releaseFixture(t *testing.T, version string) (string, ed25519.PublicKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := []byte("complete installer")
	digest := sha256.Sum256(body)
	m := releaseManifest{Manifest: update.Manifest{Version: version, PublishedAt: "2026-10-25T00:00:00Z", Notes: "User-facing release notes", Packages: map[string]update.Package{}}}
	for _, platform := range []string{"darwin-universal", "windows-x64-arm64"} {
		name := update.PackageName(m.Version, platform)
		m.Packages[platform] = update.Package{URL: update.AssetURL(m.Version, name), Size: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), MinOS: "12.0"}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if requiresSource(version) {
		m.Source = &releaseSource{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40), URL: update.AssetURL(version, sourceName(version))}
		writeTestSource(t, filepath.Join(dir, sourceName(version)), version, *m.Source, map[string]string{"main.go": "package main\n", "third_party/manifest.json": "{}"})
		info, err := os.Stat(filepath.Join(dir, sourceName(version)))
		if err != nil {
			t.Fatal(err)
		}
		m.Source.Size = info.Size()
		m.Source.SHA256, err = fileDigest(filepath.Join(dir, sourceName(version)))
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"update.json": data, "update.json.sig": []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))), "LICENSE": []byte("binary license"), "libmpv-sources.tar.gz": []byte("corresponding source archive")}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var sums strings.Builder
	names := releaseAssets(m.Version)
	for _, name := range names[:len(names)-1] {
		digest, err := fileDigest(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sums.WriteString(digest + "  " + name + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, pub
}

func TestReleaseSourceContractByVersion(t *testing.T) {
	for _, test := range []struct {
		version string
		assets  int
	}{
		{"0.1.0", 7},
		{"0.2.0", 8},
		{"0.3.0", 8},
		{"1.0.0", 8},
	} {
		t.Run(test.version, func(t *testing.T) {
			if got := len(releaseAssets(test.version)); got != test.assets {
				t.Fatalf("release assets=%d, want %d", got, test.assets)
			}
			dir, _ := releaseFixture(t, test.version)
			data, err := os.ReadFile(filepath.Join(dir, "update.json"))
			if err != nil {
				t.Fatal(err)
			}
			var signed releaseManifest
			if err := json.Unmarshal(data, &signed); err != nil {
				t.Fatal(err)
			}
			if (signed.Source != nil) != (test.assets == 8) {
				t.Fatalf("signed source present=%v, assets=%d", signed.Source != nil, test.assets)
			}
			if _, err := verifySource(dir, signed.Manifest, data); err != nil {
				t.Fatal(err)
			}
			if test.assets == 8 {
				signed.Source = nil
			} else {
				signed.Source = &releaseSource{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40)}
			}
			data, err = json.Marshal(signed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifySource(dir, signed.Manifest, data); err == nil {
				t.Fatal("incorrect source identity presence accepted")
			}
		})
	}
}

func TestVerifyReleaseRejectsUnsafeOrIncompletePublication(t *testing.T) {
	for _, version := range []string{"0.1.0", "0.2.0", "0.3.0", "1.0.0"} {
		for _, scenario := range []string{"valid", "extra-source", "missing-source", "symlink", "corrupt-installer", "corrupt-license", "corrupt-own-source", "missing-own-source", "duplicate-sum", "traversal-sum", "incomplete-sums", "tampered-manifest", "wrong-key"} {
			t.Run(version+"/"+scenario, func(t *testing.T) {
				if !requiresSource(version) && (scenario == "corrupt-own-source" || scenario == "missing-own-source") {
					t.Skip("historical release has no own source asset")
				}
				dir, pub := releaseFixture(t, version)
				write := func(name string, data []byte) {
					t.Helper()
					if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				read := func(name string) []byte {
					t.Helper()
					data, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil {
						t.Fatal(err)
					}
					return data
				}
				switch scenario {
				case "extra-source":
					write("private.go", []byte("private source"))
				case "missing-source":
					if err := os.Remove(filepath.Join(dir, "libmpv-sources.tar.gz")); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					original := filepath.Join(t.TempDir(), "LICENSE")
					if err := os.Rename(filepath.Join(dir, "LICENSE"), original); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(original, filepath.Join(dir, "LICENSE")); err != nil {
						t.Fatal(err)
					}
				case "corrupt-own-source":
					write(sourceName(version), []byte("altered own source"))
				case "missing-own-source":
					if err := os.Remove(filepath.Join(dir, sourceName(version))); err != nil {
						t.Fatal(err)
					}
				case "corrupt-installer":
					write(update.PackageName(version, "darwin-universal"), []byte("altered installer"))
				case "corrupt-license":
					write("LICENSE", []byte("altered license"))
				case "duplicate-sum":
					write("SHA256SUMS", append(read("SHA256SUMS"), read("SHA256SUMS")...))
				case "traversal-sum":
					write("SHA256SUMS", []byte(strings.ReplaceAll(string(read("SHA256SUMS")), "  LICENSE", "  ../LICENSE")))
				case "incomplete-sums":
					write("SHA256SUMS", []byte(strings.SplitN(string(read("SHA256SUMS")), "\n", 2)[0]))
				case "tampered-manifest":
					write("update.json", []byte(strings.ReplaceAll(string(read("update.json")), version, "9.9.9")))
				case "wrong-key":
					var err error
					pub, _, err = ed25519.GenerateKey(rand.Reader)
					if err != nil {
						t.Fatal(err)
					}
				}
				m, err := verifyRelease(dir, pub)
				if scenario == "valid" {
					if err != nil || m.Version != version {
						t.Fatalf("valid signed release: %+v, %v", m, err)
					}
				} else if err == nil {
					t.Fatal("unsafe release accepted")
				}
			})
		}
	}
}

func writeTestSource(t *testing.T, file, version string, source releaseSource, files map[string]string) {
	t.Helper()
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	identity, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{"release-source.json": string(identity)}
	for name, body := range files {
		all[name] = body
	}
	for name, body := range all {
		if err := tw.WriteHeader(&tar.Header{Name: "PKP-" + version + "/" + name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
