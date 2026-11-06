// Package lanshare serves explicitly authorized, read-only sources on one LAN address.
package lanshare

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

const DefaultPort = 17891
const defaultCacheLimit int64 = 8 << 30
const defaultMinFree int64 = 512 << 20

// A live cloud proof walks ancestors and can refresh authentication. Give
// rechecks the same budget as a cloud API call, not a three-second LAN probe.
const sourceRecheckTimeout = 45 * time.Second

var (
	errUnavailable     = errors.New("source_unavailable")
	errForbidden       = errors.New("forbidden")
	errNotReady        = errors.New("not_ready")
	errPrepareDisabled = errors.New("prepare_disabled")
	errInvalid         = errors.New("invalid_request")
	errDiskFull        = errors.New("disk_full")
	errHeld            = errors.New("held")
)

type Options struct {
	ConfigDir   string
	OriginalDir string
	Cloud       Cloud
	Media       *lanmedia.Processor
	Web         fs.FS
	// AllowLoopback is for local integration tests; production discovery never offers it.
	AllowLoopback bool
	CacheLimit    int64
	MinFreeBytes  int64
}

type Cloud interface {
	ValidateRoot(context.Context, string, string, string) error
	AccountIdentity(context.Context, string) (string, error)
	List(context.Context, string, string, string, string) ([]Entry, error)
	Resolve(context.Context, string, string, string, string) (Source, error)
	Prepare(context.Context, string, string, string, string, string, string, string, func(int64, int64)) (string, error)
}

type Source struct {
	Entry     Entry
	LocalPath string
	// LocalVersion proves that LocalPath still identifies the verified complete bytes.
	LocalVersion string
}

type Entry struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	ParentID          string `json:"parentId"`
	Version           string `json:"version"`
	MIME              string `json:"mime"`
	IsDir             bool   `json:"isDir"`
	Size              int64  `json:"size"`
	Modified          string `json:"modified"`
	Ready             bool   `json:"ready"`
	NeedsVerification bool   `json:"needsVerification,omitempty"`
	CompatibleReady   bool   `json:"compatibleReady"`
	CompatibleKind    string `json:"compatibleKind,omitempty"`
	localToken        string
}

type SpaceRequest struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	AccountID    string `json:"accountId"`
	RootID       string `json:"rootId"`
	LocalPath    string `json:"localPath"`
	AllowPrepare bool   `json:"allowPrepare"`
}

type Space struct {
	SpaceRequest
	ID        string `json:"id"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Error     string `json:"error"`
}

type SpaceUpdate struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	AllowPrepare bool   `json:"allowPrepare"`
}

type StartRequest struct {
	InterfaceID string `json:"interfaceId"`
	Port        int    `json:"port"`
}

type NetworkInterface struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

type Task struct {
	ID      string `json:"id"`
	SpaceID string `json:"spaceId"`
	FileID  string `json:"fileId"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Error   string `json:"error"`
}

type Status struct {
	Running     bool    `json:"running"`
	Address     string  `json:"address"`
	InterfaceID string  `json:"interfaceId"`
	HasPassword bool    `json:"hasPassword"`
	Spaces      []Space `json:"spaces"`
	Tasks       []Task  `json:"tasks"`
	CacheBytes  int64   `json:"cacheBytes"`
	CacheLimit  int64   `json:"cacheLimit"`
	OriginalDir string  `json:"originalDir"`
	Error       string  `json:"error"`
}

type ClearResult struct {
	RemovedBytes int64 `json:"removedBytes"`
	Skipped      int   `json:"skipped"`
}

type prepareRequest struct {
	SpaceID      string                `json:"spaceId"`
	FileID       string                `json:"fileId"`
	Kind         string                `json:"kind"`
	Capabilities lanmedia.Capabilities `json:"capabilities"`
}

func sourceError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch err.Error() {
	case "forbidden":
		return errForbidden
	case "not_ready":
		return errNotReady
	}
	return errUnavailable
}
