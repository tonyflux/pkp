package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeArchiveMatchesBundleAndSources(t *testing.T) {
	commit := strings.Repeat("a", 40)
	bundle := map[string][]byte{
		"runtime-packages.tsv":  []byte("lib\t1\tsrc\t1\n"),
		"runtime-source-commit": []byte(commit + "\n"),
		"runtime-build-id":      []byte(strings.Repeat("a", 64) + "\n"),
	}
	dsc := []byte("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA384\n\nFormat: 3.0 (quilt)\nSource: src\nVersion: 1\n-----BEGIN PGP SIGNATURE-----\nVersion: GnuPG v1.4.14 (MirBSD)\nsignature\n-----END PGP SIGNATURE-----\n")
	digest := sha256.Sum256(dsc)
	files := map[string][]byte{
		"runtime-packages.tsv":  bundle["runtime-packages.tsv"],
		"runtime-source-commit": bundle["runtime-source-commit"],
		"runtime-build-id":      bundle["runtime-build-id"],
		"source-packages.tsv":   []byte("src\t1"),
		"distro/src.dsc":        dsc,
		"SHA256SUMS":            []byte(fmt.Sprintf("%x  distro/src.dsc\n", digest)),
	}
	writeArchive := func() string {
		t.Helper()
		var output bytes.Buffer
		gz := gzip.NewWriter(&output)
		tw := tar.NewWriter(gz)
		for name, body := range files {
			if err := tw.WriteHeader(&tar.Header{Name: "./" + name, Mode: 0644, Size: int64(len(body))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "sources.tar.gz")
		if err := os.WriteFile(file, output.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
		return file
	}
	file := writeArchive()
	if err := verifyRuntimeSourceArchive(file, commit, bundle); err != nil {
		t.Fatal(err)
	}
	files["source-packages.tsv"] = []byte("src\tGnuPG v1.4.14 (MirBSD)")
	bundle["runtime-packages.tsv"] = []byte("lib\t1\tsrc\tGnuPG v1.4.14 (MirBSD)\n")
	files["runtime-packages.tsv"] = bundle["runtime-packages.tsv"]
	if err := verifyRuntimeSourceArchive(writeArchive(), commit, bundle); err == nil {
		t.Fatal("accepted signature armor version as source version")
	}
	files["source-packages.tsv"] = []byte("src\t1")
	bundle["runtime-packages.tsv"] = []byte("lib\t1\tsrc\t1\n")
	files["runtime-packages.tsv"] = bundle["runtime-packages.tsv"]
	if err := verifyRuntimeSourceArchive(file, strings.Repeat("b", 40), bundle); err == nil {
		t.Fatal("accepted wrong commit")
	}
	for _, name := range runtimeMetadataNames {
		original := files[name]
		files[name] = []byte("different\n")
		if err := verifyRuntimeSourceArchive(writeArchive(), commit, bundle); err == nil {
			t.Fatalf("accepted mismatched %s", name)
		}
		files[name] = original
	}
	files["distro/src.dsc"] = []byte("changed")
	if err := verifyRuntimeSourceArchive(writeArchive(), commit, bundle); err == nil {
		t.Fatal("accepted changed source")
	}
}
