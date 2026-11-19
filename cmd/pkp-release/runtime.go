package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var runtimeMetadataNames = []string{"runtime-packages.tsv", "runtime-source-commit", "runtime-build-id"}

func readRuntimeMetadata(dir string) (map[string][]byte, error) {
	result := make(map[string][]byte, len(runtimeMetadataNames))
	for _, name := range runtimeMetadataNames {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 || len(data) > 1<<20 {
			return nil, fmt.Errorf("invalid runtime metadata: %s", name)
		}
		result[name] = data
	}
	return result, nil
}

func equalRuntimeMetadata(a, b map[string][]byte) bool {
	for _, name := range runtimeMetadataNames {
		if len(a[name]) == 0 || !bytes.Equal(a[name], b[name]) {
			return false
		}
	}
	return true
}

func verifyRuntimeSourceArchive(file, commit string, bundle map[string][]byte) error {
	if string(bundle["runtime-source-commit"]) != commit+"\n" || !regexp.MustCompile(`^[0-9a-f]{64}\n$`).Match(bundle["runtime-build-id"]) {
		return errors.New("runtime source commit or build identity is missing or incorrect")
	}
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
	dsc := map[string]bool{}
	tr := tar.NewReader(gz)
	metadata := map[string][]byte{}
	digests := map[string]string{}
	var checksums []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		if name == "SHA256SUMS" || name == "runtime-packages.tsv" || name == "runtime-source-commit" || name == "runtime-build-id" || name == "source-packages.tsv" {
			if h.Size < 1 || h.Size > 1<<20 {
				return fmt.Errorf("invalid runtime archive metadata: %s", name)
			}
			data, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			if name == "SHA256SUMS" {
				if checksums != nil {
					return errors.New("duplicate runtime checksums")
				}
				checksums = data
			} else {
				if metadata[name] != nil {
					return fmt.Errorf("duplicate runtime metadata: %s", name)
				}
				metadata[name] = data
			}
		} else if strings.HasPrefix(name, "distro/") {
			if _, ok := digests[name]; ok {
				return fmt.Errorf("duplicate runtime source: %s", name)
			}
			hash := sha256.New()
			if strings.HasSuffix(name, ".dsc") {
				if h.Size > 1<<20 {
					return errors.New("oversized source descriptor")
				}
				data, err := io.ReadAll(tr)
				if err != nil {
					return err
				}
				hash.Write(data)
				var source, version string
				for _, line := range strings.Split(string(data), "\n") {
					if line == "-----BEGIN PGP SIGNATURE-----" {
						break
					}
					if strings.HasPrefix(line, "Source: ") {
						source = strings.TrimPrefix(line, "Source: ")
					}
					if strings.HasPrefix(line, "Version: ") {
						version = strings.TrimPrefix(line, "Version: ")
					}
				}
				dsc[source+"\t"+version] = true
			} else if _, err := io.Copy(hash, tr); err != nil {
				return err
			}
			digests[name] = hex.EncodeToString(hash.Sum(nil))
		}
	}
	if !equalRuntimeMetadata(bundle, metadata) || len(metadata["source-packages.tsv"]) == 0 {
		return errors.New("runtime source archive does not match bundled runtime metadata")
	}
	sourcePackages := map[string]bool{}
	for _, row := range strings.Split(strings.TrimSpace(string(metadata["source-packages.tsv"])), "\n") {
		sourcePackages[row] = true
		if !dsc[row] {
			return fmt.Errorf("missing source descriptor for %s", row)
		}
	}
	for _, row := range strings.Split(strings.TrimSpace(string(bundle["runtime-packages.tsv"])), "\n") {
		fields := strings.Split(row, "\t")
		if len(fields) != 4 || !sourcePackages[fields[2]+"\t"+fields[3]] {
			return errors.New("runtime source package manifest is incomplete")
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(checksums)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || digests[parts[1]] == "" || digests[parts[1]] != parts[0] || seen[parts[1]] {
			return errors.New("runtime source archive checksum mismatch")
		}
		seen[parts[1]] = true
	}
	if len(seen) == 0 || len(seen) != len(digests) {
		return errors.New("runtime source archive has missing source files")
	}
	return nil
}
