package main

import (
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tonyflux/pkp/internal/update"
)

type linuxProvenance struct {
	Commit string            `json:"commit"`
	Arch   string            `json:"arch"`
	Files  map[string]string `json:"files"`
}

func provenanceName(version, arch string) string {
	return "PKP-" + version + "-linux-" + arch + "-provenance.json"
}

// This catches accidental reuse, not maliciously forged packages or provenance.
func linuxProvenanceForBuild(dir, version, arch string) error {
	if err := checkVersion(version); err != nil {
		return err
	}
	if dir == "" {
		return errors.New("provide a Linux package directory")
	}
	if arch != "amd64" && arch != "arm64" {
		return errors.New("unsupported Linux architecture")
	}
	source, err := sourceSnapshot()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "pkp-linux-provenance-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	p := linuxProvenance{Commit: source.Commit, Arch: arch, Files: map[string]string{}}
	var runtimeMetadata map[string][]byte
	for _, format := range []string{"deb", "rpm", "appimage", "sources"} {
		name := update.PackageName(version, "linux-"+arch+"-"+format)
		file := filepath.Join(dir, name)
		info, err := os.Lstat(file)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("invalid Linux asset: %s", name)
		}
		p.Files[name], err = fileDigest(file)
		if err != nil {
			return err
		}
		if format == "sources" {
			continue
		}
		root := filepath.Join(tmp, format)
		if err := os.Mkdir(root, 0755); err != nil {
			return err
		}
		var cmd *exec.Cmd
		binary := "usr/lib/pkp/pkp"
		switch format {
		case "deb":
			cmd = exec.Command("dpkg-deb", "-x", file, root)
		case "rpm":
			cmd = exec.Command("rpm", "--root", root, "-i", "--nodeps", "--noscripts", "--notriggers", file)
			binary = "opt/pkp/runtime/usr/lib/pkp/pkp"
		case "appimage":
			cmd = exec.Command(file, "--appimage-extract", binary)
			cmd.Dir = root
			binary = "squashfs-root/" + binary
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("extract %s: %w: %s", name, err, out)
		}
		if err := checkLinuxBuildInfo(filepath.Join(root, binary), source.Commit, arch); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if format == "appimage" {
			for _, item := range runtimeMetadataNames {
				cmd := exec.Command(file, "--appimage-extract", item)
				cmd.Dir = root
				if out, err := cmd.CombinedOutput(); err != nil {
					return fmt.Errorf("extract %s from %s: %w: %s", item, name, err, out)
				}
			}
		}
		if format == "rpm" || format == "appimage" {
			bundle := filepath.Join(root, "opt/pkp/runtime")
			if format == "appimage" {
				bundle = filepath.Join(root, "squashfs-root")
			}
			metadata, err := readRuntimeMetadata(bundle)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if runtimeMetadata == nil {
				runtimeMetadata = metadata
			} else if !equalRuntimeMetadata(runtimeMetadata, metadata) {
				return errors.New("RPM and AppImage runtime metadata differ")
			}
		}
	}
	archive := filepath.Join(dir, update.PackageName(version, "linux-"+arch+"-sources"))
	if err := verifyRuntimeSourceArchive(archive, source.Commit, runtimeMetadata); err != nil {
		return err
	}
	current, err := sourceSnapshot()
	if err != nil {
		return err
	}
	if current.Commit != source.Commit || current.Tree != source.Tree {
		return errors.New("source changed during Linux provenance check")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	output := filepath.Join(dir, provenanceName(version, arch))
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("refusing to replace Linux provenance: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func checkLinuxBuildInfo(file, commit, arch string) error {
	info, err := buildinfo.ReadFile(file)
	if err != nil {
		return err
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != arch || settings["vcs"] != "git" || settings["vcs.revision"] != commit || settings["vcs.modified"] != "false" {
		return fmt.Errorf("Linux binary build identity does not match clean source %s (%s)", commit, arch)
	}
	return nil
}

func verifyLinuxProvenance(dir, version string, source *releaseSource) error {
	if source == nil {
		return errors.New("Linux signing requires source identity")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		data, err := os.ReadFile(filepath.Join(dir, provenanceName(version, arch)))
		if err != nil {
			return fmt.Errorf("Linux provenance required: %w", err)
		}
		var p linuxProvenance
		if err := json.Unmarshal(data, &p); err != nil {
			return err
		}
		if p.Commit != source.Commit || p.Arch != arch || len(p.Files) != 4 {
			return fmt.Errorf("Linux provenance source or architecture mismatch: %s", arch)
		}
		for _, format := range []string{"deb", "rpm", "appimage", "sources"} {
			name := update.PackageName(version, "linux-"+arch+"-"+format)
			file := filepath.Join(dir, name)
			info, err := os.Lstat(file)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return fmt.Errorf("invalid Linux asset: %s", name)
			}
			digest, err := fileDigest(file)
			if err != nil {
				return err
			}
			if digest != p.Files[name] {
				return fmt.Errorf("Linux provenance digest mismatch: %s", name)
			}
		}
	}
	return nil
}
