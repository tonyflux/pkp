package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/update"
)

func TestLinuxProvenanceBindsAllPackagesAndCommit(t *testing.T) {
	const version = "0.2.0"
	dir := t.TempDir()
	source := &releaseSource{Commit: strings.Repeat("a", 40)}
	for _, arch := range []string{"amd64", "arm64"} {
		p := linuxProvenance{Commit: source.Commit, Arch: arch, Files: map[string]string{}}
		for _, format := range []string{"deb", "rpm", "appimage", "sources"} {
			name := update.PackageName(version, "linux-"+arch+"-"+format)
			file := filepath.Join(dir, name)
			if err := os.WriteFile(file, []byte(name), 0644); err != nil {
				t.Fatal(err)
			}
			var err error
			p.Files[name], err = fileDigest(file)
			if err != nil {
				t.Fatal(err)
			}
		}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, provenanceName(version, arch)), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyLinuxProvenance(dir, version, source); err != nil {
		t.Fatal(err)
	}
	if err := verifyLinuxProvenance(dir, version, &releaseSource{Commit: strings.Repeat("b", 40)}); err == nil {
		t.Fatal("accepted wrong source commit")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, format := range []string{"deb", "rpm", "appimage", "sources"} {
			name := update.PackageName(version, "linux-"+arch+"-"+format)
			file := filepath.Join(dir, name)
			if err := os.WriteFile(file, []byte("stale"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := verifyLinuxProvenance(dir, version, source); err == nil {
				t.Fatalf("accepted changed %s", name)
			}
			if err := os.WriteFile(file, []byte(name), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Remove(filepath.Join(dir, provenanceName(version, "arm64"))); err != nil {
		t.Fatal(err)
	}
	if err := verifyLinuxProvenance(dir, version, source); err == nil {
		t.Fatal("accepted missing provenance")
	}
}

func TestLinuxBuildInfoRejectsInvalidBinary(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pkp")
	if err := os.WriteFile(file, []byte("not a Go executable"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkLinuxBuildInfo(file, strings.Repeat("a", 40), "amd64"); err == nil {
		t.Fatal("accepted invalid executable")
	}
}

func TestLinuxProvenanceRejectsInvalidVersionBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	if err := linuxProvenanceForBuild(dir, "../bad", "amd64"); err == nil {
		t.Fatal("accepted invalid version")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("invalid version wrote output")
	}
}

func TestLinuxBuildInfoRequiresCleanMatchingRevision(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", "amd64")
	t.Setenv("CGO_ENABLED", "0")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", name, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write("go.mod", "module example.test/provenance\n\ngo 1.24\n")
	write("main.go", "package main\nfunc main() {}\n")
	run("git", "init", "--quiet")
	run("git", "add", ".")
	run("git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "test")
	commit := run("git", "rev-parse", "HEAD")
	binary := filepath.Join(t.TempDir(), "pkp")
	run("go", "build", "-buildvcs=true", "-o", binary, ".")
	arch := run("go", "env", "GOARCH")
	if err := checkLinuxBuildInfo(binary, commit, arch); err != nil {
		t.Fatal(err)
	}
	if err := checkLinuxBuildInfo(binary, strings.Repeat("b", 40), arch); err == nil {
		t.Fatal("accepted old revision")
	}
	write("main.go", "package main\nfunc main() { println(1) }\n")
	run("go", "build", "-buildvcs=true", "-o", binary, ".")
	if err := checkLinuxBuildInfo(binary, commit, arch); err == nil {
		t.Fatal("accepted dirty build")
	}
}
