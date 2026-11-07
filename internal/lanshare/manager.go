package lanshare

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
	"github.com/tonyflux/pkp/internal/syncfs"
	"golang.org/x/crypto/pbkdf2"
)

type passwordRecord struct {
	Salt []byte `json:"salt"`
	Hash []byte `json:"hash"`
}

type preparedFile struct {
	Path  string          `json:"path"`
	Token string          `json:"token"`
	Root  syncfs.Identity `json:"root,omitempty"`
}

type cacheFile struct {
	Path      string          `json:"path"`
	Token     string          `json:"token"`
	Size      int64           `json:"size"`
	Kind      string          `json:"kind"`
	MIME      string          `json:"mime"`
	PlanKey   string          `json:"planKey"`
	Used      time.Time       `json:"used"`
	Readers   int             `json:"-"`
	Directory syncfs.Identity `json:"directory,omitempty"`
}

type persisted struct {
	Version           int                        `json:"version"`
	Secret            []byte                     `json:"secret"`
	Password          passwordRecord             `json:"password"`
	Spaces            map[string]*spaceRecord    `json:"spaces"`
	Tasks             map[string]*taskRecord     `json:"tasks"`
	Originals         map[string]*preparedFile   `json:"originals"`
	Cache             map[string]*cacheFile      `json:"cache"`
	OriginalIdentity  syncfs.Identity            `json:"originalIdentity"`
	Pending           map[string]string          `json:"pending"`
	CopyIdentity      syncfs.Identity            `json:"copyIdentity"`
	PendingIdentities map[string]syncfs.Identity `json:"pendingIdentities"`
	PreventSleep      bool                       `json:"preventSleep"`
}

type session struct {
	LastSeen       time.Time
	Generation     uint64
	DismissedTasks map[string]bool
}

type activeResponse struct {
	Session, Space, File, Version string
	Conn                          net.Conn
	Cancel                        context.CancelFunc
	Context                       context.Context
	Watching                      bool
	Watch                         sync.WaitGroup
}

type loginRate struct {
	Attempts int
	Start    time.Time
}

type Manager struct {
	mu                                    sync.Mutex
	lifecycle                             sync.Mutex
	dir                                   string
	originalsDir                          string
	originalIdentity                      syncfs.Identity
	copyIdentity                          syncfs.Identity
	pendingIdentities                     map[string]syncfs.Identity
	storage                               *os.Root
	cloud                                 Cloud
	media                                 *lanmedia.Processor
	web                                   fs.FS
	secret                                []byte
	password                              passwordRecord
	spaces                                map[string]*spaceRecord
	tasks                                 map[string]*taskRecord
	originals                             map[string]*preparedFile
	cache                                 map[string]*cacheFile
	pending                               map[string]string
	mediaInfo                             map[string]lanmedia.Info
	sessions                              map[string]*session
	responses                             map[*activeResponse]struct{}
	rates                                 map[string]loginRate
	loginSlot                             chan struct{}
	transferSlots                         chan struct{}
	probeSlot                             chan struct{}
	apiSlots                              chan struct{}
	reserved                              int64
	minFree                               int64
	allowLoopback                         bool
	generation                            uint64
	running                               bool
	preventSleep                          bool
	sleepBlock                            io.Closer
	sleepBlockActive                      bool
	acquireSleep                          func() (io.Closer, error)
	address, interfaceID, host, lastError string
	server                                *http.Server
	listener                              net.Listener
	wake                                  chan struct{}
	ctx                                   context.Context
	cancel                                context.CancelFunc
	wg                                    sync.WaitGroup
	closed                                bool
}

func randomID() string {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("cryptographic randomness unavailable")
	}
	return hex.EncodeToString(buf[:])
}

func New(opts Options) (*Manager, error) {
	if opts.ConfigDir == "" {
		return nil, errInvalid
	}
	if err := os.MkdirAll(opts.ConfigDir, 0700); err != nil {
		return nil, err
	}
	config, err := filepath.EvalSymlinks(opts.ConfigDir)
	if err != nil {
		return nil, err
	}
	config, err = filepath.Abs(config)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(config, "lan-share")
	if err = os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	anchor, err := openAbsolute(dir, true)
	if err != nil {
		return nil, err
	}
	anchor.Close()
	storage, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{dir: dir, storage: storage, cloud: opts.Cloud, media: opts.Media, web: opts.Web,
		spaces: make(map[string]*spaceRecord), tasks: make(map[string]*taskRecord), originals: make(map[string]*preparedFile), cache: make(map[string]*cacheFile),
		sessions: make(map[string]*session), responses: make(map[*activeResponse]struct{}), rates: make(map[string]loginRate),
		loginSlot: make(chan struct{}, 1), transferSlots: make(chan struct{}, 4), probeSlot: make(chan struct{}, 1),
		minFree: opts.MinFreeBytes, allowLoopback: opts.AllowLoopback, wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	m.acquireSleep = acquireSleepBlock
	m.originalsDir = filepath.Join(dir, "originals")
	if opts.OriginalDir != "" {
		base, e := filepath.Abs(opts.OriginalDir)
		if e != nil {
			storage.Close()
			cancel()
			return nil, e
		}
		m.originalsDir = filepath.Join(base, "PKP-LAN-Originals")
	}
	if m.minFree <= 0 {
		m.minFree = defaultMinFree
	}
	if m.media == nil {
		m.media = lanmedia.New()
	}
	if m.web == nil {
		m.web, err = fs.Sub(embeddedWeb, "web")
		if err != nil {
			storage.Close()
			cancel()
			return nil, err
		}
	}
	f, _, readErr := OpenLocal(filepath.Join(dir, "state.json"))
	if readErr == nil {
		var saved persisted
		decoder := json.NewDecoder(io.LimitReader(f, 32<<20))
		err = decoder.Decode(&saved)
		if err == nil {
			var extra any
			if trailing := decoder.Decode(&extra); trailing != io.EOF {
				err = errUnavailable
			}
		}
		f.Close()
		if err != nil || saved.Version != 1 || len(saved.Secret) != 32 {
			storage.Close()
			cancel()
			return nil, errUnavailable
		}
		if len(saved.Password.Hash) != 0 && (len(saved.Password.Hash) != 32 || len(saved.Password.Salt) != 16) {
			storage.Close()
			cancel()
			return nil, errUnavailable
		}
		m.secret, m.password = saved.Secret, saved.Password
		m.preventSleep = saved.PreventSleep
		if saved.Spaces != nil {
			m.spaces = saved.Spaces
		}
		if saved.Tasks != nil {
			m.tasks = saved.Tasks
		}
		if saved.Originals != nil {
			m.originals = saved.Originals
		}
		if saved.Cache != nil {
			m.cache = saved.Cache
		}
		m.pending = saved.Pending
		m.originalIdentity = saved.OriginalIdentity
		if m.originalIdentity.Path != "" {
			m.originalsDir = m.originalIdentity.Path
		}
		m.copyIdentity, m.pendingIdentities = saved.CopyIdentity, saved.PendingIdentities
		// Legacy task paths belong only to the recorded root, never today's setting.
		for _, task := range m.tasks {
			if task.Original != "" && task.OriginalRoot.Path == "" {
				root := saved.OriginalIdentity
				if root.Path == "" && filepath.Dir(task.Original) == filepath.Join(dir, "originals") {
					root, _ = syncfs.Identify(filepath.Join(dir, "originals"))
				}
				if root.Path == filepath.Dir(task.Original) {
					task.OriginalRoot = root
				}
			}
		}
		for _, s := range m.spaces {
			if s.References == nil {
				s.References = make(map[string]localReference)
			}
		}
		for _, t := range m.tasks {
			t.Sessions = make(map[string]bool)
			if t.State == "queued" || t.State == "downloading" || t.State == "converting" {
				t.State, t.Error, t.Hold = "held", "interrupted", true
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		storage.Close()
		cancel()
		return nil, readErr
	}
	if m.secret == nil {
		m.secret = make([]byte, 32)
		if _, err = rand.Read(m.secret); err != nil {
			storage.Close()
			cancel()
			return nil, err
		}
	}
	if m.pending == nil {
		m.pending = make(map[string]string)
	}
	if m.pendingIdentities == nil {
		m.pendingIdentities = make(map[string]syncfs.Identity)
	}
	if m.copyIdentity.Path == "" {
		if err = storage.Mkdir("cache", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			storage.Close()
			cancel()
			return nil, errStorageUnavailable
		}
		m.copyIdentity, err = syncfs.Identify(filepath.Join(dir, "cache"))
		if err != nil {
			storage.Close()
			cancel()
			return nil, errStorageUnavailable
		}
	}
	m.apiSlots = make(chan struct{}, 16)
	m.mediaInfo = make(map[string]lanmedia.Info)
	m.recoverPending()
	if err = m.saveLocked(); err != nil {
		storage.Close()
		cancel()
		return nil, err
	}
	m.wg.Add(2)
	go m.worker()
	go m.monitor()
	return m, nil
}

func (m *Manager) saveLocked() error {
	saved := persisted{Version: 1, Secret: m.secret, Password: m.password, Spaces: m.spaces, Tasks: m.tasks, Originals: m.originals, Cache: m.cache, OriginalIdentity: m.originalIdentity, Pending: m.pending, CopyIdentity: m.copyIdentity, PendingIdentities: m.pendingIdentities, PreventSleep: m.preventSleep}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if len(data) > 32<<20 {
		return errInvalid
	}
	name := "state-" + randomID() + ".tmp"
	f, err := m.storage.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = m.storage.Rename(name, "state.json")
	}
	if err != nil {
		_ = m.storage.Remove(name)
		m.lastError = "persistence_failed"
	}
	return err
}

func Interfaces() ([]NetworkInterface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := []NetworkInterface{}
	for _, iface := range all {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "utun") || strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "tap") || strings.HasPrefix(name, "wg") || strings.Contains(name, "vpn") {
			continue
		}
		addresses, e := iface.Addrs()
		if e != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, e := net.ParseCIDR(address.String())
			if e != nil || ip.To4() == nil || !ip.IsPrivate() {
				continue
			}
			text := ip.String()
			result = append(result, NetworkInterface{ID: strconv.Itoa(iface.Index) + "@" + text, Name: iface.Name, Address: text})
		}
	}
	return result, nil
}

func (m *Manager) selectedInterface(id string) (NetworkInterface, error) {
	if m.allowLoopback && id == "lo@127.0.0.1" {
		return NetworkInterface{ID: id, Name: "loopback", Address: "127.0.0.1"}, nil
	}
	interfaces, err := Interfaces()
	if err != nil {
		return NetworkInterface{}, err
	}
	for _, iface := range interfaces {
		if iface.ID == id {
			return iface, nil
		}
	}
	return NetworkInterface{}, errUnavailable
}

func (m *Manager) Start(req StartRequest) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	iface, err := m.selectedInterface(req.InterfaceID)
	if err != nil {
		return err
	}
	if req.Port == 0 {
		req.Port = DefaultPort
	}
	if req.Port < 1 || req.Port > 65535 {
		return errInvalid
	}
	m.mu.Lock()
	if m.closed || len(m.password.Hash) == 0 {
		m.mu.Unlock()
		return errForbidden
	}
	if m.running {
		m.mu.Unlock()
		return errInvalid
	}
	ids := make([]string, 0, len(m.spaces))
	for id, space := range m.spaces {
		if space.Enabled {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	valid := false
	for _, id := range ids {
		s, e := m.space(id, false)
		if e != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		e = m.validateSpace(ctx, s)
		cancel()
		if e == nil {
			valid = true
			break
		}
	}
	if !valid {
		return errUnavailable
	}
	host := net.JoinHostPort(iface.Address, strconv.Itoa(req.Port))
	listener, err := net.Listen("tcp4", host)
	if err != nil {
		return err
	}
	if req.Port == 80 {
		host = iface.Address
	}
	server := &http.Server{Handler: http.HandlerFunc(m.serveHTTP), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 45 * time.Second, MaxHeaderBytes: 16 << 10,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connectionKey{}, c)
		}}
	m.mu.Lock()
	if !m.preventSleep {
		if err = m.releaseSleepLocked(); err != nil {
			m.mu.Unlock()
			_ = listener.Close()
			return err
		}
	} else if !m.sleepBlockActive {
		if err = m.acquireSleepLocked(); err != nil {
			m.mu.Unlock()
			_ = listener.Close()
			return err
		}
	}
	m.running, m.address, m.host, m.interfaceID, m.lastError = true, "http://"+host, host, req.InterfaceID, ""
	m.server, m.listener = server, listener
	m.generation++
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.mu.Lock()
			if m.server == server {
				m.running = false
				m.address = ""
				m.lastError = "network_unavailable"
				m.revokeLocked("", "")
				m.holdLocked("", true)
				_ = m.releaseSleepLocked()
			}
			m.mu.Unlock()
		}
	}()
	return nil
}

func (m *Manager) Stop() error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	return m.stop()
}

func (m *Manager) stop() error {
	m.mu.Lock()
	server := m.server
	m.server, m.listener = nil, nil
	m.running, m.address, m.host = false, "", ""
	m.revokeLocked("", "")
	m.holdLocked("", true)
	err := errors.Join(m.saveLocked(), m.releaseSleepLocked())
	m.mu.Unlock()
	if server != nil {
		if closeErr := server.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func (m *Manager) Close() error {
	m.lifecycle.Lock()
	m.mu.Lock()
	if m.closed {
		err := m.releaseSleepLocked()
		m.mu.Unlock()
		m.lifecycle.Unlock()
		return err
	}
	m.closed = true
	m.mu.Unlock()
	err := m.stop()
	m.cancel()
	m.lifecycle.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	if saveErr := m.saveLocked(); err == nil {
		err = saveErr
	}
	m.mu.Unlock()
	if closeErr := m.storage.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (m *Manager) SetPassword(password string) error {
	if password == "" || len(password) > 1024 {
		return errInvalid
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash := pbkdf2.Key([]byte(password), salt, 210000, 32, sha256.New)
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.password
	m.password = passwordRecord{Salt: salt, Hash: hash}
	if err := m.saveLocked(); err != nil {
		m.password = previous
		return err
	}
	m.revokeLocked("", "")
	m.holdLocked("", true)
	return m.saveLocked()
}

func passwordMatches(password string, record passwordRecord) bool {
	if len(record.Hash) != 32 || len(record.Salt) != 16 || len(password) > 1024 {
		return false
	}
	computed := pbkdf2.Key([]byte(password), record.Salt, 210000, 32, sha256.New)
	return subtle.ConstantTimeCompare(computed, record.Hash) == 1
}

func (m *Manager) AddSpace(req SpaceRequest) (Space, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 240 {
		return Space{}, errInvalid
	}
	s := &spaceRecord{Space: Space{SpaceRequest: req, ID: randomID(), Enabled: true, Available: true}, References: make(map[string]localReference)}
	if req.Kind == "local" {
		if req.AccountID != "" || req.RootID != "" || req.LocalPath == "" {
			return Space{}, errInvalid
		}
		identity, err := syncfs.Identify(req.LocalPath)
		if err != nil || syncfs.IsStagePath(identity.Path, identity) {
			return Space{}, errUnavailable
		}
		f, err := openAbsolute(identity.Path, true)
		if err != nil {
			return Space{}, errUnavailable
		}
		key, _, volume, err := handleIdentity(f)
		pathErr := validateLocalHandle(f, identity)
		f.Close()
		if err != nil || pathErr != nil {
			return Space{}, errUnavailable
		}
		s.Identity, s.HandleKey, s.Volume, s.LocalPath = identity, key, volume, identity.Path
	} else if req.Kind == "cloud" {
		if req.AccountID == "" || req.LocalPath != "" || m.cloud == nil {
			return Space{}, errInvalid
		}
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
		defer cancel()
		identity, err := m.cloud.AccountIdentity(ctx, req.AccountID)
		if err != nil || identity == "" {
			return Space{}, errUnavailable
		}
		if err = m.cloud.ValidateRoot(ctx, req.AccountID, req.RootID, identity); err != nil {
			return Space{}, sourceError(err)
		}
		s.AccountIdentity = identity
	} else {
		return Space{}, errInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || len(m.spaces) >= 128 {
		return Space{}, errInvalid
	}
	m.spaces[s.ID] = s
	if err := m.saveLocked(); err != nil {
		delete(m.spaces, s.ID)
		return Space{}, err
	}
	return s.Space, nil
}

func (m *Manager) UpdateSpace(req SpaceUpdate) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 240 {
		return errInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[req.ID]
	if s == nil {
		return errInvalid
	}
	previous := s.Space
	s.Name, s.Enabled, s.AllowPrepare = req.Name, req.Enabled, req.AllowPrepare
	if err := m.saveLocked(); err != nil {
		s.Space = previous
		return err
	}
	if !s.Enabled {
		m.revokeLocked("", s.ID)
	}
	if !s.Enabled || !s.AllowPrepare {
		m.holdLocked(s.ID, s.Enabled)
		for _, t := range m.tasks {
			if t.SpaceID == s.ID && !s.AllowPrepare {
				clear(t.Sessions)
			}
			if t.SpaceID == s.ID && (!s.Enabled || !t.Desktop) && t.cancel != nil {
				t.cancel()
			}
		}
	}
	return m.saveLocked()
}

func (m *Manager) RemoveSpace(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.spaces[id]
	if old == nil {
		return errInvalid
	}
	delete(m.spaces, id)
	if err := m.saveLocked(); err != nil {
		m.spaces[id] = old
		return err
	}
	m.revokeLocked("", id)
	m.holdLocked(id, false)
	for _, t := range m.tasks {
		if t.SpaceID == id && t.cancel != nil {
			t.cancel()
		}
	}
	return m.saveLocked()
}

// Tasks returns the desktop task snapshot without revalidating every source.
func (m *Manager) Tasks() []Task {
	m.mu.Lock()
	result := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		if !t.DesktopHidden || !terminalTask(t.State) {
			result = append(result, t.Task)
		}
	}
	m.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	result := Status{Running: m.running, Address: m.address, InterfaceID: m.interfaceID, HasPassword: len(m.password.Hash) > 0, CacheBytes: m.cacheBytesLocked() + m.pendingBytesLocked(), OriginalDir: m.originalsDir, Error: m.lastError, Spaces: []Space{}}
	result.CopyDir, result.StorageBusy = m.copyIdentity.Path, m.storageBusyLocked()
	result.PreventSleep, result.PreventSleepActive, result.PreventSleepSupported = m.preventSleep, m.sleepBlockActive, sleepPreventionSupported
	snapshots := make([]spaceRecord, 0, len(m.spaces))
	for _, s := range m.spaces {
		copy := *s
		copy.References = nil
		snapshots = append(snapshots, copy)
	}
	m.mu.Unlock()
	for _, s := range snapshots {
		s.Available, s.Error = false, ""
		if s.Enabled {
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
			err := m.validateSpace(ctx, s)
			cancel()
			s.Available = err == nil
			if err != nil {
				s.Error = "source_unavailable"
			}
		}
		result.Spaces = append(result.Spaces, s.Space)
	}
	sort.Slice(result.Spaces, func(i, j int) bool { return result.Spaces[i].Name < result.Spaces[j].Name })
	result.Tasks = m.Tasks()
	return result
}

func (m *Manager) revokeLocked(sessionID, spaceID string) {
	if spaceID == "" {
		if sessionID == "" {
			clear(m.sessions)
			m.generation++
		} else {
			delete(m.sessions, sessionID)
		}
	}
	for response := range m.responses {
		if (sessionID == "" || response.Session == sessionID) && (spaceID == "" || response.Space == spaceID || response.Space == "") {
			response.Cancel()
			if response.Conn != nil {
				_ = response.Conn.Close()
			}
		}
	}
}

func (m *Manager) monitor() {
	defer m.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
		}
		m.mu.Lock()
		running, iface := m.running, m.interfaceID
		for id, s := range m.sessions {
			if time.Since(s.LastSeen) > 30*time.Minute {
				m.revokeLocked(id, "")
			}
		}
		for ip, rate := range m.rates {
			if time.Since(rate.Start) > 5*time.Minute {
				delete(m.rates, ip)
			}
		}
		m.mu.Unlock()
		if running {
			if _, err := m.selectedInterface(iface); err != nil {
				_ = m.Stop()
				m.mu.Lock()
				m.lastError = "network_unavailable"
				m.mu.Unlock()
			}
		}
	}
}
