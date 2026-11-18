//go:build linux

package lanshare

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxReadProofRevokesDuringStreaming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	file, token, err := OpenLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	proof, err := newLocalReadProof(file, token)
	if err != nil {
		t.Fatal(err)
	}
	defer proof.Close()
	reader := revocableFile{File: file, ctx: context.Background(), token: token, proof: proof}
	if n, err := reader.Read(make([]byte, 4)); n != 4 || err != nil {
		t.Fatalf("unchanged read: %d %v", n, err)
	}
	if err = os.WriteFile(path, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := reader.Read(make([]byte, 4)); n != 0 || !errors.Is(err, errUnavailable) {
		t.Fatalf("rewritten bytes escaped proof: %d %v", n, err)
	}
	if err = os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = proof.Check(); !errors.Is(err, errUnavailable) {
		t.Fatalf("mutation revocation was not sticky: %v", err)
	}
	proof.Close()
	proof.Close()
}
