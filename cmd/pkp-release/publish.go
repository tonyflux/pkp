package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tonyflux/pkp/internal/update"
)

func releaseAssets(version string) []string {
	names := []string{update.PackageName(version, "darwin-universal"), update.PackageName(version, "windows-x64-arm64"), "update.json", "update.json.sig", "libmpv-sources.tar.gz", "LICENSE"}
	if requiresSource(version) {
		names = append(names, sourceName(version))
	}
	return append(names, "SHA256SUMS")
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// The directory itself is the upload allowlist, not a wildcard over build output.
func verifyRelease(dir string, public ed25519.PublicKey) (update.Manifest, error) {
	var empty update.Manifest
	data, err := os.ReadFile(filepath.Join(dir, "update.json"))
	if err != nil {
		return empty, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, "update.json.sig"))
	if err != nil {
		return empty, err
	}
	m, err := update.Verify(data, sig, public)
	if err != nil {
		return empty, err
	}
	names := releaseAssets(m.Version)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return empty, err
	}
	if len(entries) != len(names) {
		return empty, fmt.Errorf("release directory must contain exactly %d allowlisted assets", len(names))
	}
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			return empty, err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return empty, fmt.Errorf("release asset must be a nonempty regular file: %s", name)
		}
	}
	for platform, p := range m.Packages {
		if err := update.CheckFile(filepath.Join(dir, update.PackageName(m.Version, platform)), p); err != nil {
			return empty, err
		}
	}
	checksums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return empty, err
	}
	wanted := make(map[string]bool, len(names)-1)
	for _, name := range names[:len(names)-1] {
		wanted[name] = true
	}
	for _, line := range strings.Split(strings.TrimSpace(string(checksums)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || !wanted[parts[1]] {
			return empty, errors.New("SHA256SUMS must list every allowlisted asset exactly once")
		}
		actual, err := fileDigest(filepath.Join(dir, parts[1]))
		if err != nil {
			return empty, err
		}
		if parts[0] != actual {
			return empty, fmt.Errorf("checksum mismatch: %s", parts[1])
		}
		delete(wanted, parts[1])
	}
	if len(wanted) != 0 {
		return empty, errors.New("incomplete SHA256SUMS")
	}
	if _, err := verifySource(dir, m, data); err != nil {
		return empty, err
	}
	return m, nil
}

func gh(input []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	cmd.Env = append(os.Environ(), "GH_HOST=github.com")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func ghJSON(value any, args ...string) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return gh(data, append(args, "--input", "-")...)
}

type githubRelease struct {
	ID         int64  `json:"id"`
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Body       string `json:"body"`
	Target     string `json:"target_commitish"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

func publishRelease(dir string, publish bool) error {
	m, err := verifyRelease(dir, update.PublicKey())
	if err != nil {
		return err
	}
	base := "repos/" + update.Repository
	out, err := gh(nil, "api", base)
	if err != nil {
		return err
	}
	var repo struct {
		FullName      string `json:"full_name"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(out, &repo); err != nil {
		return err
	}
	if repo.FullName != update.Repository || repo.Private || repo.DefaultBranch != "main" {
		return errors.New("release target must be the public tonyflux/pkp repository with main as default branch")
	}
	tag := "v" + m.Version
	var source *releaseSource
	if requiresSource(m.Version) {
		data, err := os.ReadFile(filepath.Join(dir, "update.json"))
		if err != nil {
			return err
		}
		source, err = verifySource(dir, m, data)
		if err != nil {
			return err
		}
	}
	publicCommit, err := publicReleaseCommit(base, source)
	if err != nil {
		return err
	}
	out, err = gh(nil, "api", "--paginate", "--slurp", base+"/releases?per_page=100")
	if err != nil {
		return err
	}
	var pages [][]githubRelease
	if err := json.Unmarshal(out, &pages); err != nil {
		return err
	}
	var release githubRelease
	for _, page := range pages {
		for _, candidate := range page {
			if candidate.Tag == tag {
				release = candidate
			}
			if publish && !candidate.Draft && !candidate.Prerelease && !update.Newer(m.Version, strings.TrimPrefix(candidate.Tag, "v")) {
				if _, err := update.Version(strings.TrimPrefix(candidate.Tag, "v")); err == nil {
					return errors.New("Latest must advance the stable version")
				}
			}
		}
	}
	if release.ID != 0 && (!release.Draft || release.Prerelease) {
		return errors.New("refusing to modify a published or prerelease version; release a new version instead")
	}
	if release.ID != 0 && release.Body != m.Notes {
		return errors.New("existing draft notes differ from signed manifest")
	}
	// An existing tag must be public history, even when resuming a draft.
	out, err = gh(nil, "api", base+"/git/matching-refs/tags/"+tag)
	if err != nil {
		return err
	}
	var refs []struct {
		Ref    string `json:"ref"`
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(out, &refs); err != nil {
		return err
	}
	tagCommit := ""
	for _, ref := range refs {
		if ref.Ref != "refs/tags/"+tag {
			continue
		}
		if ref.Object.Type != "commit" {
			return errors.New("release tag must point directly to a public commit")
		}
		tagCommit = ref.Object.SHA
		if source != nil && ref.Object.SHA != source.Commit {
			return errors.New("release tag differs from signed release source commit")
		}
		out, err = gh(nil, "api", base+"/compare/main..."+ref.Object.SHA, "--jq", ".status")
		if err != nil {
			return err
		}
		status := strings.TrimSpace(string(out))
		if status != "identical" && status != "behind" {
			return errors.New("release tag is not in public main history")
		}
	}
	if source != nil && release.ID != 0 {
		if err := checkDraftSource(source.Commit, tagCommit, release.Target); err != nil {
			return err
		}
	}
	if release.ID == 0 {
		out, err = ghJSON(map[string]any{"tag_name": tag, "target_commitish": publicCommit, "name": "PKP " + m.Version, "body": m.Notes, "draft": true, "prerelease": false}, "api", "--method", "POST", base+"/releases")
		if err != nil {
			return err
		}
		if err := json.Unmarshal(out, &release); err != nil {
			return err
		}
		if release.ID == 0 || !release.Draft {
			return errors.New("GitHub did not create a draft")
		}
	}
	names := releaseAssets(m.Version)
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	existing := map[string]bool{}
	for _, asset := range release.Assets {
		if !allowed[asset.Name] || existing[asset.Name] {
			return errors.New("existing draft contains unexpected or duplicate assets")
		}
		existing[asset.Name] = true
	}
	for _, name := range names {
		if existing[name] {
			continue
		}
		if _, err := gh(nil, "release", "upload", tag, filepath.Join(dir, name), "--repo", update.Repository); err != nil {
			return err
		}
	}
	download, err := os.MkdirTemp("", "pkp-release-readback-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(download)
	if _, err := gh(nil, "release", "download", tag, "--repo", update.Repository, "--dir", download); err != nil {
		return err
	}
	if _, err := verifyRelease(download, update.PublicKey()); err != nil {
		return fmt.Errorf("uploaded draft failed verification: %w", err)
	}
	for _, name := range names {
		local, err := fileDigest(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		remote, err := fileDigest(filepath.Join(download, name))
		if err != nil {
			return err
		}
		if local != remote {
			return fmt.Errorf("existing draft asset differs; refusing overwrite: %s", name)
		}
	}
	if publish {
		// Publishing, not uploading, is the explicit stable-channel cutover.
		if _, err := ghJSON(map[string]any{"draft": false, "make_latest": "true"}, "api", "--method", "PATCH", fmt.Sprintf("%s/releases/%d", base, release.ID)); err != nil {
			return err
		}
		fmt.Println("Published Latest:", "https://github.com/"+update.Repository+"/releases/tag/"+tag)
	} else {
		fmt.Println("Verified draft ready for approval:", tag, "(not visible to the updater)")
	}
	return nil
}

func checkDraftSource(sourceCommit, tagCommit, target string) error {
	if tagCommit != "" {
		if tagCommit != sourceCommit {
			return errors.New("release tag differs from signed release source commit")
		}
		return nil
	}
	if target != sourceCommit {
		return errors.New("untagged draft target differs from signed release source commit")
	}
	return nil
}
