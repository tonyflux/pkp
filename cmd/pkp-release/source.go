package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tonyflux/pkp/internal/update"
)

type releaseSource struct {
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type releaseManifest struct {
	update.Manifest
	Source *releaseSource `json:"source,omitempty"`
}

func requiresSource(version string) bool {
	return version == "0.2.0" || update.Newer(version, "0.2.0")
}

func sourceName(version string) string { return "PKP-" + version + "-source.tar.gz" }

var gitObjectID = regexp.MustCompile(`^[0-9a-f]{40}$`)

func gitOutput(args ...string) ([]byte, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Only a complete, unchanged tracked snapshot can describe the binaries we build.
func sourceSnapshot() (releaseSource, error) {
	var source releaseSource
	root, err := gitOutput("rev-parse", "--show-prefix")
	if err != nil {
		return source, err
	}
	if strings.TrimSpace(string(root)) != "" {
		return source, errors.New("release must run at the repository root")
	}
	status, err := gitOutput("status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return source, err
	}
	if len(status) != 0 {
		return source, errors.New("release source must be a clean committed snapshot (including nonignored untracked files)")
	}
	ignored, err := gitOutput("ls-files", "--cached", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return source, err
	}
	if len(ignored) != 0 {
		return source, errors.New("release source contains tracked ignored output, cache or account files")
	}
	flags, err := gitOutput("ls-files", "-v", "-z")
	if err != nil {
		return source, err
	}
	for _, file := range bytes.Split(flags, []byte{0}) {
		if len(file) != 0 && (file[0] == 'S' || file[0] >= 'a' && file[0] <= 'z') {
			return source, errors.New("release source must not use skip-worktree or assume-unchanged files")
		}
	}
	resources, err := filepath.Glob("*.syso")
	if err != nil {
		return source, err
	}
	if len(resources) != 0 {
		return source, errors.New("remove generated root .syso resources before release preflight")
	}
	files, err := gitOutput("ls-files", "--stage", "-z")
	if err != nil {
		return source, err
	}
	for _, file := range bytes.Split(files, []byte{0}) {
		if bytes.HasPrefix(file, []byte("160000 ")) {
			return source, errors.New("release source must not contain unarchived submodules")
		}
	}
	paths, err := gitOutput("ls-files", "-z")
	if err != nil {
		return source, err
	}
	for _, file := range bytes.Split(paths, []byte{0}) {
		if string(file) == "release-source.json" || strings.HasPrefix(string(file), "third_party/") {
			return source, errors.New("tracked source uses reserved release-source.json or third_party/ archive paths")
		}
	}
	cmd := exec.Command("git", "check-attr", "--cached", "-z", "--stdin", "export-ignore", "export-subst")
	cmd.Stdin = bytes.NewReader(paths)
	attrs, err := cmd.Output()
	if err != nil {
		return source, err
	}
	fields := bytes.Split(attrs, []byte{0})
	for i := 2; i < len(fields); i += 3 {
		if string(fields[i]) != "unspecified" && string(fields[i]) != "unset" {
			return source, errors.New("release source must not use export-ignore or export-subst transformations")
		}
	}
	for _, item := range []struct {
		ref   string
		value *string
	}{{"HEAD", &source.Commit}, {"HEAD^{tree}", &source.Tree}} {
		out, err := gitOutput("rev-parse", "--verify", item.ref)
		if err != nil {
			return source, err
		}
		*item.value = strings.TrimSpace(string(out))
		if !gitObjectID.MatchString(*item.value) {
			return source, errors.New("invalid release source Git identity")
		}
	}
	return source, nil
}

func writeSourceArchive(w io.Writer, version string, source releaseSource, dependencies string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	cmd := exec.Command("git", "archive", "--format=tar", "--prefix=PKP-"+version+"/", source.Commit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tr := tar.NewReader(stdout)
	var copyErr error
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			copyErr = err
			break
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if err := tw.WriteHeader(h); err != nil {
			copyErr = err
			break
		}
		if _, err := io.Copy(tw, tr); err != nil {
			copyErr = err
			break
		}
	}
	// Drain Git's tar padding before waiting for its stdout pipe to close.
	if copyErr == nil {
		_, copyErr = io.Copy(io.Discard, stdout)
	}
	if copyErr != nil {
		stdout.Close()
	}
	if err := cmd.Wait(); err != nil {
		return err
	}
	if copyErr != nil {
		return copyErr
	}
	prefix := "PKP-" + version + "/"
	metadata, err := json.Marshal(releaseSource{Commit: source.Commit, Tree: source.Tree})
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: prefix + "release-source.json", Mode: 0644, Size: int64(len(metadata)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(metadata); err != nil {
		return err
	}
	err = filepath.WalkDir(dependencies, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("dependency source is not a regular file: %s", file)
		}
		rel, err := filepath.Rel(dependencies, file)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: prefix + "third_party/" + filepath.ToSlash(rel), Mode: 0644, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func packageSource(dir, version, commit string) error {
	if !requiresSource(version) {
		return errors.New("PKP source packaging requires v0.2.0 or newer")
	}
	source, err := sourceSnapshot()
	if err != nil {
		return err
	}
	if source.Commit != commit {
		return errors.New("release source HEAD changed since build preflight")
	}
	temporary, err := os.MkdirTemp("", "pkp-source-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	dependencies := filepath.Join(temporary, "third_party")
	cmd := exec.Command("python3", "build/release-dependency-sources.py", dependencies)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("package dependency source: %w", err)
	}
	after, err := sourceSnapshot()
	if err != nil {
		return err
	}
	if after.Commit != source.Commit {
		return errors.New("release source changed while gathering dependencies")
	}
	file := filepath.Join(dir, sourceName(version))
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	err = writeSourceArchive(f, version, source, dependencies)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return verifySnapshotArchive(file, version, source)
}

// Compare every own-source byte and executable/symlink mode against Git, not a working-directory copy.
func verifySnapshotArchive(file, version string, source releaseSource) error {
	listing, err := gitOutput("ls-tree", "-r", "-z", "--full-tree", source.Commit)
	if err != nil {
		return err
	}
	type trackedFile struct{ mode, object string }
	wanted := map[string]trackedFile{}
	for _, record := range bytes.Split(listing, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		parts := bytes.SplitN(record, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return errors.New("invalid Git tree entry")
		}
		fields := strings.Fields(string(parts[0]))
		if len(fields) != 3 || fields[1] != "blob" {
			return errors.New("source snapshot must contain blobs only")
		}
		wanted[string(parts[1])] = trackedFile{fields[0], fields[2]}
	}
	err = walkSourceArchive(file, version, source, func(name string, h *tar.Header, r io.Reader) error {
		if h.Typeflag == tar.TypeDir || name == "release-source.json" || strings.HasPrefix(name, "third_party/") {
			return nil
		}
		tracked, ok := wanted[name]
		if !ok {
			return fmt.Errorf("source archive contains untracked own source: %s", name)
		}
		var input io.Reader = r
		size := h.Size
		if tracked.mode == "120000" {
			if h.Typeflag != tar.TypeSymlink {
				return errors.New("source symlink mode differs from Git")
			}
			input, size = strings.NewReader(h.Linkname), int64(len(h.Linkname))
		} else if h.Typeflag != tar.TypeReg || (tracked.mode == "100755") != (h.Mode&0111 != 0) {
			return errors.New("source file mode differs from Git")
		}
		hash := sha1.New()
		fmt.Fprintf(hash, "blob %d\x00", size)
		if _, err := io.Copy(hash, input); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != tracked.object {
			return fmt.Errorf("source archive differs from Git: %s", name)
		}
		delete(wanted, name)
		return nil
	})
	if err != nil {
		return err
	}
	if len(wanted) != 0 {
		return errors.New("source archive omits tracked source files")
	}
	return nil
}

func walkSourceArchive(file, version string, source releaseSource, visit func(string, *tar.Header, io.Reader) error) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	prefix := "PKP-" + version + "/"
	seen := map[string]bool{}
	metadata, dependencies := false, false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeDir && h.Name == strings.TrimSuffix(prefix, "/") {
			continue
		}
		name := strings.TrimPrefix(h.Name, prefix)
		clean := path.Clean(name)
		if !strings.HasPrefix(h.Name, prefix) || strings.Contains(name, "\\") || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") || (strings.TrimSuffix(name, "/") != clean && name != "") || strings.Contains("/"+clean+"/", "/.git/") || seen[clean] {
			return errors.New("unsafe or duplicate release source entry")
		}
		seen[clean] = true
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeSymlink {
			return errors.New("unsupported source archive entry")
		}
		if h.Typeflag == tar.TypeSymlink {
			target := path.Clean(path.Join(path.Dir(name), h.Linkname))
			if path.IsAbs(h.Linkname) || target == ".." || strings.HasPrefix(target, "../") || strings.Contains(h.Linkname, "\\") {
				return errors.New("escaping source symlink")
			}
		}
		if name == "release-source.json" {
			if h.Typeflag != tar.TypeReg || h.Size > 4096 {
				return errors.New("invalid source identity metadata")
			}
			var identity releaseSource
			if err := json.NewDecoder(io.LimitReader(tr, h.Size)).Decode(&identity); err != nil {
				return err
			}
			if identity.Commit != source.Commit || identity.Tree != source.Tree {
				return errors.New("archive source identity differs from signed manifest")
			}
			metadata = true
		} else {
			if strings.HasPrefix(name, "third_party/") && h.Typeflag == tar.TypeReg {
				dependencies = true
			}
			if visit != nil {
				if err := visit(name, h, tr); err != nil {
					return err
				}
			}
		}
	}
	if !metadata || !dependencies {
		return errors.New("source archive requires source identity and dependency sources")
	}
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return err
	}
	return nil
}

func prepareSource(dir, version string) (*releaseSource, error) {
	if !requiresSource(version) {
		return nil, nil
	}
	source, err := sourceSnapshot()
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, sourceName(version))
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("release source archive must be a regular file")
	}
	if err := verifySnapshotArchive(file, version, source); err != nil {
		return nil, err
	}
	digest, err := fileDigest(file)
	if err != nil {
		return nil, err
	}
	source.URL, source.Size, source.SHA256 = update.AssetURL(version, sourceName(version)), info.Size(), digest
	return &source, nil
}

func verifySource(dir string, m update.Manifest, data []byte) (*releaseSource, error) {
	var signed releaseManifest
	if err := json.Unmarshal(data, &signed); err != nil {
		return nil, err
	}
	if !requiresSource(m.Version) {
		if signed.Source != nil {
			return nil, errors.New("historical release must not claim new source metadata")
		}
		return nil, nil
	}
	source := signed.Source
	if source == nil || !gitObjectID.MatchString(source.Commit) || !gitObjectID.MatchString(source.Tree) || source.URL != update.AssetURL(m.Version, sourceName(m.Version)) {
		return nil, errors.New("release requires signed source commit, tree and official archive URL")
	}
	if err := update.CheckFile(filepath.Join(dir, sourceName(m.Version)), update.Package{Size: source.Size, SHA256: source.SHA256}); err != nil {
		return nil, fmt.Errorf("release source: %w", err)
	}
	if err := walkSourceArchive(filepath.Join(dir, sourceName(m.Version)), m.Version, *source, nil); err != nil {
		return nil, err
	}
	return source, nil
}

func publicReleaseCommit(base string, source *releaseSource) (string, error) {
	ref := "main"
	if source != nil {
		ref = source.Commit
	}
	out, err := gh(nil, "api", base+"/commits/"+ref)
	if err != nil {
		return "", err
	}
	var commit struct {
		SHA    string `json:"sha"`
		Commit struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(out, &commit); err != nil {
		return "", err
	}
	if !gitObjectID.MatchString(commit.SHA) {
		return "", errors.New("invalid public release commit")
	}
	if source != nil {
		if commit.SHA != source.Commit || commit.Commit.Tree.SHA != source.Tree {
			return "", errors.New("public commit differs from signed source identity")
		}
		out, err := gh(nil, "api", base+"/compare/main..."+source.Commit, "--jq", ".status")
		if err != nil {
			return "", err
		}
		status := strings.TrimSpace(string(out))
		if status != "identical" && status != "behind" {
			return "", errors.New("release source commit is not in public main history")
		}
	}
	return commit.SHA, nil
}
