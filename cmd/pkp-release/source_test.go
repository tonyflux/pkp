package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sourceRepository(t *testing.T) releaseSource {
	t.Helper()
	t.Chdir(t.TempDir())
	runGit := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "--quiet")
	if err := os.WriteFile("main.go", []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".gitignore", []byte("bin/\naccounts.json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("-c", "user.name=Release Test", "-c", "user.email=release@example.invalid", "commit", "--quiet", "-m", "source")
	source, err := sourceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestSourceSnapshotRequiresCleanTrackedSource(t *testing.T) {
	for _, scenario := range []string{"clean", "ignored-output", "dirty", "untracked", "tracked-account", "export-ignore"} {
		t.Run(scenario, func(t *testing.T) {
			sourceRepository(t)
			file, body := "", ""
			switch scenario {
			case "ignored-output":
				file, body = "accounts.json", "not archived"
			case "dirty":
				file, body = "main.go", "package changed\n"
			case "untracked":
				file, body = "missing.go", "package main\n"
			case "tracked-account":
				file, body = "accounts.json", "must not ship"
			case "export-ignore":
				file, body = ".gitattributes", "main.go export-ignore\n"
			}
			if file != "" {
				if err := os.WriteFile(file, []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "tracked-account" || scenario == "export-ignore" {
				if _, err := gitOutput("add", "--force", file); err != nil {
					t.Fatal(err)
				}
				if _, err := gitOutput("-c", "user.name=Release Test", "-c", "user.email=release@example.invalid", "commit", "--quiet", "-m", scenario); err != nil {
					t.Fatal(err)
				}
			}
			_, err := sourceSnapshot()
			valid := scenario == "clean" || scenario == "ignored-output"
			if (err == nil) != valid {
				t.Fatalf("snapshot accepted=%v, want %v: %v", err == nil, valid, err)
			}
		})
	}
}

func TestSourceArchiveMatchesEveryTrackedByte(t *testing.T) {
	source := sourceRepository(t)
	dependencies := t.TempDir()
	if err := os.WriteFile(filepath.Join(dependencies, "manifest.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := writeSourceArchive(&archive, "0.3.0", source, dependencies); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), sourceName("0.3.0"))
	if err := os.WriteFile(file, archive.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshotArchive(file, "0.3.0", source); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"changed", "omitted", "extra", "traversal"} {
		t.Run(scenario, func(t *testing.T) {
			files := map[string]string{"main.go": "package main\n", ".gitignore": "bin/\naccounts.json\n", "third_party/manifest.json": "{}"}
			switch scenario {
			case "changed":
				files["main.go"] = "package different\n"
			case "omitted":
				delete(files, "main.go")
			case "extra":
				files["extra.go"] = "package main\n"
			case "traversal":
				files["../escape"] = "unsafe"
			}
			writeTestSource(t, file, "0.3.0", source, files)
			if err := verifySnapshotArchive(file, "0.3.0", source); err == nil {
				t.Fatal("noncorresponding source accepted")
			}
		})
	}
}

func TestPublicReleaseCommitUsesSignedSourceNotMainTip(t *testing.T) {
	directory := t.TempDir()
	sha, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	response, err := json.Marshal(map[string]any{"sha": sha, "commit": map[string]any{"tree": map[string]string{"sha": tree}}})
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \"$2\" in\n  */commits/" + sha + ") printf '%s' '" + string(response) + "' ;;\n  */compare/main..." + sha + ") printf '%s' \"$COMPARE_STATUS\" ;;\n  *) exit 42 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	source := &releaseSource{Commit: sha, Tree: tree}
	for _, status := range []string{"identical", "behind", "ahead", "diverged"} {
		t.Setenv("COMPARE_STATUS", status)
		commit, err := publicReleaseCommit("repos/tonyflux/pkp", source)
		valid := status == "identical" || status == "behind"
		if (err == nil) != valid || (valid && commit != sha) {
			t.Fatalf("status %s: commit=%s err=%v", status, commit, err)
		}
	}
	t.Setenv("COMPARE_STATUS", "identical")
	source.Tree = strings.Repeat("c", 40)
	if _, err := publicReleaseCommit("repos/tonyflux/pkp", source); err == nil {
		t.Fatal("different public source tree accepted")
	}
}

func TestDraftSourceIdentitySurvivesMainAdvancing(t *testing.T) {
	source := strings.Repeat("a", 40)
	for _, scenario := range []struct {
		tag, target string
		valid       bool
	}{
		{source, "main", true},
		{source, source, true},
		{"", source, true},
		{"", "main", false},
		{strings.Repeat("b", 40), source, false},
	} {
		if err := checkDraftSource(source, scenario.tag, scenario.target); (err == nil) != scenario.valid {
			t.Fatalf("draft %+v: %v", scenario, err)
		}
	}
}
