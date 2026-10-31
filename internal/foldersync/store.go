package foldersync

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tonyflux/pkp/internal/syncfs"
)

type baseline struct {
	Local     *Node  `json:"local"`
	Cloud     *Node  `json:"cloud"`
	Preserved string `json:"preserved,omitempty"`
}

type snapshot struct {
	ID       string           `json:"id"`
	Root     Node             `json:"root"`
	Local    map[string]Node  `json:"local"`
	Cloud    map[string]Node  `json:"cloud"`
	Skipped  []syncfs.Skipped `json:"skipped"`
	Issues   []Issue          `json:"issues,omitempty"`
	Complete bool             `json:"complete"`
	At       string           `json:"at"`
}

type operation struct {
	ID            string          `json:"id"`
	Action        string          `json:"action"`
	Direction     string          `json:"direction"`
	Path          string          `json:"path"`
	From          string          `json:"from,omitempty"`
	Local         *Node           `json:"local,omitempty"`
	Cloud         *Node           `json:"cloud,omitempty"`
	Stage         string          `json:"stage"`
	Temp          string          `json:"temp,omitempty"`
	New           *Node           `json:"new,omitempty"`
	FileID        string          `json:"fileId,omitempty"`
	Grant         json.RawMessage `json:"grant,omitempty"`
	ConfigVersion string          `json:"configVersion"`
	Children      []*operation    `json:"children,omitempty"`
	Committed     bool            `json:"committed,omitempty"`
}

type pairRecord struct {
	Pair         Pair                `json:"pair"`
	Identity     syncfs.Identity     `json:"identity"`
	Owner        string              `json:"owner"`
	Root         Node                `json:"root"`
	Version      string              `json:"version"`
	BaselineHash string              `json:"baselineHash"`
	Baseline     map[string]baseline `json:"-"`
	Snapshot     *snapshot           `json:"snapshot,omitempty"`
	Operations   []*operation        `json:"operations,omitempty"`
	Approved     string              `json:"approved,omitempty"`
	Removing     bool                `json:"removing,omitempty"`
	Repair       bool                `json:"repair,omitempty"`
}

type manifest struct {
	Version int          `json:"version"`
	Pairs   []pairRecord `json:"pairs"`
}

type baselineFile struct {
	Version string              `json:"version"`
	Entries map[string]baseline `json:"entries"`
}

func uid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func atomicJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sync-store-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return syncfs.DurableReplace(name, path)
}

func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if err = dec.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err = dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrRepair
	}
	return nil
}

func (m *Manager) saveLocked() error {
	if m.storeErr != nil {
		return m.storeErr
	}
	v := manifest{Version: 1, Pairs: make([]pairRecord, 0, len(m.pairs))}
	for _, r := range m.pairs {
		v.Pairs = append(v.Pairs, r.record)
	}
	if err := atomicJSON(filepath.Join(m.opts.Dir, "pairs.json"), v); err != nil {
		m.storeErr = err
		return err
	}
	return nil
}

// Baseline generations are immutable: a crash before the manifest commit leaves
// the previous complete generation usable, rather than half-committing evidence.
func (m *Manager) saveBaselineLocked(r *runtimePair) error {
	if r.broken {
		return ErrRepair
	}
	version := uid()
	f := baselineFile{Version: version, Entries: r.record.Baseline}
	if err := atomicJSON(filepath.Join(m.opts.Dir, r.record.Pair.ID, "baseline-"+version+".json"), f); err != nil {
		r.broken = true
		return err
	}
	r.record.BaselineHash = digest(f)
	r.record.Version = version
	if err := m.saveLocked(); err != nil {
		r.broken = true
		return err
	}
	return nil
}

func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var c T
	_ = json.Unmarshal(b, &c)
	return c
}
