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

type lanProofKey struct {
	session *accountSession
	client  *drive.Client
	user    string
	id      string
}

type lanProofScope struct {
	mu         sync.Mutex
	files      map[lanProofKey]*drive.File
	roots      map[lanProofKey]bool
	generation uint64
}

type lanProofContextKey struct{}

// SourceProofContext starts a bounded operation, never a cross-request cache.
// Call again after long work or on every watcher tick to obtain live evidence.
func (c *lanCloud) SourceProofContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, lanProofContextKey{}, &lanProofScope{
		files: make(map[lanProofKey]*drive.File),
		roots: make(map[lanProofKey]bool),
	})
}

func (c *lanCloud) ensureProofContext(ctx context.Context) context.Context {
	if _, ok := ctx.Value(lanProofContextKey{}).(*lanProofScope); ok {
		return ctx
	}
	return c.SourceProofContext(ctx)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" || id == drive.WholeTree {
		return nil, errLANScope
	}
	if err := c.current(a); err != nil {
		return nil, err
	}
	key := lanProofKey{a.session, a.client, a.user, id}
	scope, _ := ctx.Value(lanProofContextKey{}).(*lanProofScope)
	if scope != nil {
		scope.mu.Lock()
		f := scope.files[key]
		scope.mu.Unlock()
		if f != nil {
			return f, nil
		}
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
	if scope != nil {
		scope.mu.Lock()
		if len(scope.files) < 4096 {
			scope.files[key] = f
		}
		scope.mu.Unlock()
	}
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.current(a); err != nil {
		return err
	}
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
	key := lanProofKey{session: a.session, client: a.client, user: a.user}
	scope, _ := ctx.Value(lanProofContextKey{}).(*lanProofScope)
	if scope != nil {
		scope.mu.Lock()
		known := scope.roots[key]
		scope.mu.Unlock()
		if known {
			return nil
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, driveTimeout)
	defer cancel()
	_, err := a.client.ListFiles(callCtx, drive.ListFilesParams{Limit: 1, Filters: drive.OnlyLive()})
	if err != nil {
		_ = c.drive.classify(a.session, a.client, err)
		return errLANSource
	}
	if err := c.current(a); err != nil {
		return err
	}
	if scope != nil {
		scope.mu.Lock()
		if len(scope.roots) < 4096 {
			scope.roots[key] = true
		}
		scope.mu.Unlock()
	}
	return nil
}

func (c *lanCloud) ValidateRoot(ctx context.Context, accountID, rootID, identity string) error {
	ctx = c.ensureProofContext(ctx)
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
	entries, _, err := c.list(ctx, accountID, rootID, parentID, identity, "", false)
	return entries, err
}

// VisitorPage proves each page independently; the upstream token grants nothing.
func (c *lanCloud) VisitorPage(ctx context.Context, accountID, rootID, parentID, identity, token string) ([]lanshare.Entry, string, error) {
	ctx = c.ensureProofContext(ctx)
	if err := c.ValidateRoot(ctx, accountID, rootID, identity); err != nil {
		return nil, "", err
	}
	return c.list(ctx, accountID, rootID, parentID, identity, token, true)
}

func (c *lanCloud) list(ctx context.Context, accountID, rootID, parentID, identity, token string, singlePage bool) ([]lanshare.Entry, string, error) {
	ctx = c.ensureProofContext(ctx)
	a, err := c.account(accountID)
	if err != nil {
		return nil, "", err
	}
	if identity == "" || a.user != identity {
		return nil, "", errLANSource
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
		return nil, "", err
	}
	params := drive.ListFilesParams{ParentID: parentID, Filters: drive.OnlyLive(), Limit: drive.MaxListLimit, Order: drive.OrderNameAsc, ThumbnailSize: drive.ThumbnailMedium, PageToken: token}
	entries := []lanshare.Entry{}
	seen, tokens := map[string]bool{}, map[string]bool{}
	var downloads map[string][]download.Task
	next := ""
	for {
		callCtx, cancel := context.WithTimeout(ctx, driveTimeout)
		page, err := a.client.ListFiles(callCtx, params)
		cancel()
		if err != nil {
			_ = c.drive.classify(a.session, a.client, err)
			return nil, "", errLANSource
		}
		if err := c.current(a); err != nil {
			return nil, "", err
		}
		for _, f := range page.Files {
			if f.ID == "" || f.ParentID != parentID || f.Trashed || f.DeleteTime != "" || seen[f.ID] || f.UserID != "" && f.UserID != a.user {
				return nil, "", errLANSource
			}
			f.UserID = a.user
			seen[f.ID] = true
			entry := lanEntry(&f)
			entry.Thumbnail = f.ThumbnailLink
			if !entry.IsDir && f.Phase == drive.PhaseComplete {
				if downloads == nil {
					downloads = lanCompletedDownloads(a)
				}
				path, _, pending, err := c.completed(ctx, a, &f, false, downloads)
				if err != nil {
					return nil, "", err
				}
				entry.Ready = path != ""
				entry.NeedsVerification = pending
			}
			entries = append(entries, entry)
		}
		next = page.NextPageToken
		if singlePage {
			if next != "" && next == token {
				return nil, "", errLANSource
			}
			break
		}
		if page.NextPageToken == "" {
			break
		}
		if tokens[page.NextPageToken] || len(entries) > 100000 {
			return nil, "", errLANSource
		}
		tokens[page.NextPageToken] = true
		params.PageToken = page.NextPageToken
	}
	fresh := c.SourceProofContext(ctx)
	if singlePage {
		if err := c.validateRoot(fresh, a, rootID); err != nil {
			return nil, "", err
		}
	}
	if parentID != "" {
		if _, err := c.within(fresh, a, rootID, parentID); err != nil {
			return nil, "", err
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
	return entries, next, nil
}

func (c *lanCloud) Resolve(ctx context.Context, accountID, rootID, fileID, identity string) (lanshare.Source, error) {
	ctx = c.ensureProofContext(ctx)
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
	scope := ctx.Value(lanProofContextKey{}).(*lanProofScope)
	scope.mu.Lock()
	generation := scope.generation
	scope.mu.Unlock()
	source.LocalPath, source.LocalVersion, _, err = c.completed(ctx, a, f, true, nil)
	// Waiting for or computing a checksum can outlive the original source proof.
	// completedAt drops that proof after such work; known checksums stay fast.
	scope.mu.Lock()
	stale := scope.generation != generation
	scope.mu.Unlock()
	if err == nil && stale {
		current, proofErr := c.within(ctx, a, rootID, fileID)
		if proofErr != nil {
			return lanshare.Source{}, proofErr
		}
		if lanVersion(current) != entry.Version || current.Phase != drive.PhaseComplete {
			return lanshare.Source{}, errLANSource
		}
	}
	if err == nil {
		err = c.current(a)
	}
	source.Entry.Ready = err == nil && source.LocalPath != ""
	return source, err
}

// Snapshot only completed candidates once per listing, not every download task
// once per row. Local identity and checksums are still checked for each use.
func lanCompletedDownloads(a lanAccount) map[string][]download.Task {
	byID := make(map[string][]download.Task)
	if q := a.session.downloadQueue(); q != nil {
		for _, task := range q.List() {
			if task.State == download.StateDone {
				byID[task.FileID] = append(byID[task.FileID], task)
			}
		}
	}
	return byID
}

// Only complete downloads or committed sync originals with a strong content
// proof are reusable. Neither paths nor same-name/same-size files authorize reuse.
func (c *lanCloud) completed(ctx context.Context, a lanAccount, cloud *drive.File, verify bool, downloads map[string][]download.Task) (string, string, bool, error) {
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
	want, err := hex.DecodeString(cloud.MD5Checksum)
	if err != nil || len(want) != md5.Size {
		return "", "", pending, nil
	}
	if downloads == nil {
		downloads = lanCompletedDownloads(a)
	}
	for _, task := range downloads[cloud.ID] {
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
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
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
		if scope, ok := ctx.Value(lanProofContextKey{}).(*lanProofScope); ok {
			defer func() {
				scope.mu.Lock()
				clear(scope.files)
				clear(scope.roots)
				scope.generation++
				scope.mu.Unlock()
			}()
		}
		c.mu.Lock()
		if c.proofGate == nil {
			c.proofGate = make(chan struct{}, 1)
		}
		gate := c.proofGate
		c.mu.Unlock()
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
		defer func() { <-gate }()
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		// A preceding verifier may have proved this exact local identity.
		c.mu.Lock()
		known, checked = c.verified[key]
		c.mu.Unlock()
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
		ctx = c.SourceProofContext(ctx)
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
	f, err := c.get(c.SourceProofContext(ctx), a, fileID)
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
