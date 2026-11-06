package services

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/lanshare"
	"github.com/tonyflux/pkp/internal/syncfs"
)

var errLANSource = errors.New("source_unavailable")
var errLANScope = errors.New("forbidden")
var errLANNotReady = errors.New("not_ready")

// lanCloud uses live account-specific metadata, never the browsing index or
// the foreground account. Cached checksum evidence cannot authorize a source.
type lanCloud struct {
	drive     *DriveService
	mu        sync.Mutex
	verified  map[string]bool
	proofGate chan struct{}
}

type lanAccount struct {
	session *accountSession
	client  *drive.Client
	user    string
}

func (c *lanCloud) account(id string) (lanAccount, error) {
	if id == "" {
		return lanAccount{}, errLANSource
	}
	s := c.drive.byKey(id)
	if s == nil {
		return lanAccount{}, errLANSource
	}
	user := s.record().UserID
	client, err := s.api()
	if err != nil || user == "" {
		return lanAccount{}, errLANSource
	}
	return lanAccount{s, client, user}, nil
}

func (c *lanCloud) current(a lanAccount) error {
	if c.drive.byKey(a.session.key) != a.session || a.session.record().UserID != a.user {
		return errLANSource
	}
	client, err := a.session.api()
	if err != nil || client != a.client {
		return errLANSource
	}
	return nil
}

func (c *lanCloud) AccountIdentity(ctx context.Context, accountID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a, err := c.account(accountID)
	if err != nil {
		return "", err
	}
	return a.user, nil
}

func (c *lanCloud) get(ctx context.Context, a lanAccount, id string) (*drive.File, error) {
	if id == "" || id == drive.WholeTree {
		return nil, errLANScope
	}
	if err := c.current(a); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()
	f, err := a.client.GetFile(callCtx, id, drive.GetFileParams{})
	if err != nil {
		_ = c.drive.classify(a.session, a.client, err)
		return nil, errLANSource
	}
	if err := c.current(a); err != nil {
		return nil, err
	}
	if f.ID != id || f.Trashed || f.DeleteTime != "" || f.UserID != "" && f.UserID != a.user {
		return nil, errLANSource
	}
	f.UserID = a.user
	return f, nil
}

// within proves the complete current ancestor chain to the explicitly granted
// root. A cloud ID is not itself a capability, and prefixes are never compared.
func (c *lanCloud) within(ctx context.Context, a lanAccount, rootID, id string) (*drive.File, error) {
	f, err := c.get(ctx, a, id)
	if err != nil {
		return nil, err
	}
	node := f
	seen := map[string]bool{}
	for range 512 {
		if seen[node.ID] {
			return nil, errLANScope
		}
		seen[node.ID] = true
		if node.ID == rootID {
			if !node.IsFolder() {
				return nil, errLANScope
			}
			return f, nil
		}
		if node.ParentID == "" {
			if rootID == "" {
				return f, nil
			}
			return nil, errLANScope
		}
		node, err = c.get(ctx, a, node.ParentID)
		if err != nil {
			return nil, err
		}
		if !node.IsFolder() {
			return nil, errLANScope
		}
	}
	return nil, errLANScope
}

func (c *lanCloud) validateRoot(ctx context.Context, a lanAccount, rootID string) error {
	if rootID != "" {
		f, err := c.within(ctx, a, "", rootID)
		if err != nil {
			return err
		}
		if !f.IsFolder() {
			return errLANScope
		}
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()
	_, err := a.client.ListFiles(callCtx, drive.ListFilesParams{Limit: 1, Filters: drive.OnlyLive()})
	if err != nil {
		_ = c.drive.classify(a.session, a.client, err)
		return errLANSource
	}
	return c.current(a)
}

func (c *lanCloud) ValidateRoot(ctx context.Context, accountID, rootID, identity string) error {
	a, err := c.account(accountID)
	if err != nil {
		return err
	}
	if identity == "" || a.user != identity {
		return errLANSource
	}
	return c.validateRoot(ctx, a, rootID)
}

func lanVersion(f *drive.File) string {
	if f.ModifiedTime == "" && f.Hash == "" && f.MD5Checksum == "" {
		return ""
	}
	raw, _ := json.Marshal([]any{f.ID, f.UserID, f.CreatedTime, f.ModifiedTime, f.Hash, f.MD5Checksum, int64(f.Size), f.Phase})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func lanEntry(f *drive.File) lanshare.Entry {
	return lanshare.Entry{ID: f.ID, Name: f.Name, ParentID: f.ParentID, Version: lanVersion(f), MIME: f.MimeType, IsDir: f.IsFolder(), Size: int64(f.Size), Modified: f.ModifiedTime}
}

func (c *lanCloud) List(ctx context.Context, accountID, rootID, parentID, identity string) ([]lanshare.Entry, error) {
	a, err := c.account(accountID)
	if err != nil {
		return nil, err
	}
	if identity == "" || a.user != identity {
		return nil, errLANSource
	}
	if parentID == "" {
		parentID = rootID
	}
	if parentID == "" {
		err = c.validateRoot(ctx, a, rootID)
	} else {
		var parent *drive.File
		parent, err = c.within(ctx, a, rootID, parentID)
		if err == nil && !parent.IsFolder() {
			err = errLANScope
		}
	}
	if err != nil {
		return nil, err
	}
	params := drive.ListFilesParams{ParentID: parentID, Filters: drive.OnlyLive(), Limit: drive.MaxListLimit, Order: drive.OrderNameAsc}
	entries := []lanshare.Entry{}
	seen, tokens := map[string]bool{}, map[string]bool{}
	for {
		callCtx, cancel := context.WithTimeout(ctx, driveTimeout)
		page, err := a.client.ListFiles(callCtx, params)
		cancel()
		if err != nil {
			_ = c.drive.classify(a.session, a.client, err)
			return nil, errLANSource
		}
		if err := c.current(a); err != nil {
			return nil, err
		}
		for _, f := range page.Files {
			if f.ID == "" || f.ParentID != parentID || f.Trashed || f.DeleteTime != "" || seen[f.ID] || f.UserID != "" && f.UserID != a.user {
				return nil, errLANSource
			}
			f.UserID = a.user
			seen[f.ID] = true
			entry := lanEntry(&f)
			if !entry.IsDir && f.Phase == drive.PhaseComplete {
				path, _, pending, err := c.completed(ctx, a, &f, false)
				if err != nil {
					return nil, err
				}
				entry.Ready = path != ""
				entry.NeedsVerification = pending
			}
			entries = append(entries, entry)
		}
		if page.NextPageToken == "" {
			break
		}
		if tokens[page.NextPageToken] || len(entries) > 100000 {
			return nil, errLANSource
		}
		tokens[page.NextPageToken] = true
		params.PageToken = page.NextPageToken
	}
	if parentID != "" {
		if _, err := c.within(ctx, a, rootID, parentID); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(entries, func(a, b lanshare.Entry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return entries, nil
}

func (c *lanCloud) Resolve(ctx context.Context, accountID, rootID, fileID, identity string) (lanshare.Source, error) {
	a, err := c.account(accountID)
	if err != nil {
		return lanshare.Source{}, err
	}
	if identity == "" || a.user != identity {
		return lanshare.Source{}, errLANSource
	}
	f, err := c.within(ctx, a, rootID, fileID)
	if err != nil {
		return lanshare.Source{}, err
	}
	entry := lanEntry(f)
	source := lanshare.Source{Entry: entry}
	if f.IsFolder() {
		return source, nil
	}
	if f.Phase != drive.PhaseComplete || entry.Version == "" {
		return source, errLANNotReady
	}
	source.LocalPath, source.LocalVersion, _, err = c.completed(ctx, a, f, true)
	if err == nil {
		err = c.current(a)
	}
	source.Entry.Ready = err == nil && source.LocalPath != ""
	return source, err
}

// Only complete downloads or committed sync originals with a strong content
// proof are reusable. Neither paths nor same-name/same-size files authorize reuse.
func (c *lanCloud) completed(ctx context.Context, a lanAccount, cloud *drive.File, verify bool) (string, string, bool, error) {
	if verify {
		c.mu.Lock()
		if c.proofGate == nil {
			c.proofGate = make(chan struct{}, 1)
		}
		gate := c.proofGate
		c.mu.Unlock()
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			return "", "", false, ctx.Err()
		}
		defer func() { <-gate }()
	}
	pending := false
	if manager, err := c.drive.syncManager(); err == nil {
		for _, candidate := range manager.CompletedFiles(a.session.key, a.user, syncNode(*cloud)) {
			path, err := syncfs.ValidateRelative(candidate.Root, candidate.RelativePath)
			if err != nil {
				continue
			}
			token, unverified, err := c.completedAt(ctx, a, cloud, path, candidate.Hash, true, verify)
			if err != nil {
				return "", "", false, err
			}
			if syncfs.Validate(candidate.Root) != nil {
				continue
			}
			pending = pending || unverified
			if token != "" {
				return path, token, false, nil
			}
		}
	}
	q := a.session.downloadQueue()
	if q == nil {
		return "", "", pending, nil
	}
	want, err := hex.DecodeString(cloud.MD5Checksum)
	if err != nil || len(want) != md5.Size {
		return "", "", pending, nil
	}
	for _, task := range q.List() {
		if task.FileID != cloud.ID || task.State != download.StateDone || task.Size != int64(cloud.Size) {
			continue
		}
		token, unverified, err := c.completedAt(ctx, a, cloud, task.Path, cloud.MD5Checksum, false, verify)
		if err != nil {
			return "", "", false, err
		}
		pending = pending || unverified
		if token != "" {
			return task.Path, token, false, nil
		}
	}
	return "", "", pending, nil
}

func (c *lanCloud) completedAt(ctx context.Context, a lanAccount, cloud *drive.File, path, expected string, useSHA256, verify bool) (string, bool, error) {
	file, token, err := lanshare.OpenLocal(path)
	if err != nil {
		return "", false, nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != int64(cloud.Size) {
		return "", false, nil
	}
	algorithm := "md5"
	if useSHA256 {
		algorithm = "sha256"
	}
	key := a.user + "/" + lanVersion(cloud) + "/" + token + "/" + algorithm + "/" + expected
	c.mu.Lock()
	known, checked := c.verified[key]
	c.mu.Unlock()
	if !checked && !verify {
		return "", true, nil
	}
	if !checked {
		var digest hash.Hash
		if useSHA256 {
			digest = sha256.New()
		} else {
			digest = md5.New()
		}
		if _, err = io.Copy(digest, lanContextReader{ctx, file}); err != nil {
			if ctx.Err() != nil {
				return "", false, ctx.Err()
			}
			return "", false, nil
		}
		known = strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), expected)
	}
	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	after, err := lanshare.LocalVersion(path)
	if err != nil || after != token {
		return "", false, nil
	}
	c.mu.Lock()
	if c.verified == nil || len(c.verified) >= 1024 {
		c.verified = make(map[string]bool)
	}
	c.verified[key] = known
	c.mu.Unlock()
	if !known {
		return "", false, nil
	}
	return token, false, nil
}

type lanContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r lanContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func (c *lanCloud) Prepare(ctx context.Context, accountID, rootID, fileID, version, destination, checkpoint, identity string, progress func(int64, int64)) (string, error) {
	a, err := c.account(accountID)
	if err != nil {
		return "", err
	}
	if identity == "" || a.user != identity {
		return "", errLANSource
	}
	validate := func(ctx context.Context) error {
		f, err := c.within(ctx, a, rootID, fileID)
		if err != nil {
			return err
		}
		if f.IsFolder() || f.Phase != drive.PhaseComplete || version == "" || lanVersion(f) != version {
			return download.ErrChanged
		}
		return nil
	}
	if err := validate(ctx); err != nil {
		return "", err
	}
	f, err := c.get(ctx, a, fileID)
	if err != nil {
		return "", err
	}
	q, err := a.session.liveDownloads()
	if err != nil {
		return "", errLANSource
	}
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-ticker.C:
				if err := validate(run); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	id := sha256.Sum256([]byte(checkpoint))
	checksum, checksumErr := hex.DecodeString(f.MD5Checksum)
	hasMD5 := checksumErr == nil && len(checksum) == md5.Size
	expected := strings.ToLower(f.MD5Checksum)
	var verify func(*os.File) error
	if !hasMD5 {
		verify = func(file *os.File) error {
			digest := sha256.New()
			if _, err := io.Copy(digest, lanContextReader{run, io.NewSectionReader(file, 0, int64(f.Size))}); err != nil {
				return err
			}
			expected = hex.EncodeToString(digest.Sum(nil))
			return nil
		}
	}
	err = q.Receive(run, download.DirectRequest{ID: "lan-" + hex.EncodeToString(id[:16]), FileID: fileID, Size: int64(f.Size), Path: destination, Checkpoint: checkpoint, VerifyComplete: verify}, func(task download.Task) {
		if progress != nil {
			progress(task.Done, task.Size)
		}
	})
	cause := context.Cause(run)
	cancel(nil)
	<-stopped
	if cause != nil {
		return "", cause
	}
	if err != nil {
		return "", err
	}
	if err := validate(ctx); err != nil {
		return "", err
	}
	// Receive publishes only after every byte range completes and its source
	// version is checked; never infer completion from a sparse file's length.
	file, token, err := lanshare.OpenLocal(destination)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() != int64(f.Size) {
		return "", download.ErrChanged
	}
	var digest hash.Hash = sha256.New()
	if hasMD5 {
		digest = md5.New()
	}
	if _, err := io.Copy(digest, lanContextReader{ctx, file}); err != nil {
		return "", err
	}
	if expected == "" || hex.EncodeToString(digest.Sum(nil)) != expected {
		return "", download.ErrChanged
	}
	if after, err := lanshare.LocalVersion(destination); err != nil || after != token {
		return "", download.ErrChanged
	}
	if err := validate(ctx); err != nil {
		return "", err
	}
	return token, nil
}
