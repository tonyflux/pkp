package services

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/tonyflux/pkp/internal/ai"
	"github.com/tonyflux/pkp/internal/download"
	"github.com/tonyflux/pkp/internal/i18n"
)

// The range Concurrent Uploads may be set to. Each file already sends four parts at
// once, so five files is twenty connections — enough to fill a home uplink;
// more would mostly invite throttling.
const (
	minParallelUploads = 1
	maxParallelUploads = 5
)

// The range Concurrent Downloads may be set to. An account's downloads share
// download.AccountConnections connections, so more files at once would
// mostly wait for one.
const (
	minParallelDownloads = 1
	maxParallelDownloads = 5
)

// LocalSettings are this computer's preferences that the Go side acts on;
// the webview only shows and changes them. One file beside the credentials
// holds them for every account signed in here.
type LocalSettings struct {
	// Update checks are opt-out, independent of anonymous usage statistics.
	UpdateChecksOff   bool   `json:"updateChecksOff"`
	UpdateLastCheck   string `json:"updateLastCheck,omitempty"`
	UpdateIgnored     string `json:"updateIgnored,omitempty"`
	UpdateRemindAfter string `json:"updateRemindAfter,omitempty"`
	// ResumableUploads is Resume Uploads: an upload that fails or that a quit
	// cuts short keeps its placeholder and the parts already sent, and
	// Continue picks it up from there.
	ResumableUploads bool `json:"resumableUploads"`
	// ParallelUploads is how many files upload at once, from
	// minParallelUploads to maxParallelUploads, per account.
	ParallelUploads int `json:"parallelUploads"`
	// PlaybackQuality is the rendition preference the player menu saves:
	// "auto", "origin" or a tier such as "720". Every video starts in it.
	PlaybackQuality string `json:"playbackQuality"`
	// SubtitleStyle is how subtitles are drawn: size, height and colour.
	// Every video uses it.
	SubtitleStyle SubtitleStyle `json:"subtitleStyle"`
	// KeepPlayingOnSwitch is Keep Playing When Switching Accounts. Off, switching accounts
	// pauses the video, keeps its position and puts the player away; on,
	// the video keeps playing and always says whose it is.
	KeepPlayingOnSwitch bool `json:"keepPlayingOnSwitch"`
	// SignInAccount is the one account an earlier, single-account build
	// remembered; its password is in the system keychain. The sign-in page
	// offers it until an account signs in, which carries its own choice.
	SignInAccount string `json:"signInAccount,omitempty"`
	// NoRememberSignIn and NoAutoSignIn record that Remember Account Password or Auto Sign-In
	// was unticked at the last sign-in: the next form starts from them. Both
	// start ticked, so they are kept as opt-outs: a file that never named
	// them reads as ticked.
	NoRememberSignIn bool `json:"noRememberSignIn,omitempty"`
	NoAutoSignIn     bool `json:"noAutoSignIn,omitempty"`
	// DownloadDir is the folder Download to Computer saves into; a file that does not
	// name one means ~/Downloads.
	DownloadDir string `json:"downloadDir"`
	// ParallelDownloads is how many files of an account download at once,
	// from minParallelDownloads to maxParallelDownloads.
	ParallelDownloads int `json:"parallelDownloads"`
	// DownloadThreads is how many connections each downloading file opens,
	// from 1 to download.MaxThreads.
	DownloadThreads int `json:"downloadThreads"`
	// AIProvider is AI Assistant's model service: "anthropic" or "openai"
	// (OpenAI Compatible). AIBaseURL is the OpenAI-compatible endpoint, kept
	// while Anthropic is chosen; AIModel "" means not configured yet.
	AIProvider string `json:"aiProvider"`
	AIBaseURL  string `json:"aiBaseUrl"`
	AIModel    string `json:"aiModel"`
	// AITokenLimit is Per-Conversation Limit: a conversation stops sending once its
	// turns have used this many tokens. From minAITokenLimit to
	// maxAITokenLimit, or unlimitedAITokens (0) for Unlimited.
	AITokenLimit int `json:"aiTokenLimit"`
	// AIKeyFor is the provider and base URL ("openai https://…/v1") the
	// API key in the keychain was entered for. The key itself is never
	// here; it is only sent where this names.
	AIKeyFor string `json:"aiKeyFor,omitempty"`
	// AINaming is the Media Organization naming style: "plex", "chinese" or
	// "custom", which uses AISeriesTemplate and AIMovieTemplate (the
	// webview checks templates before saving them).
	AINaming         string `json:"aiNaming"`
	AISeriesTemplate string `json:"aiSeriesTemplate"`
	AIMovieTemplate  string `json:"aiMovieTemplate"`
	// AICapabilities is what Test Connection (or a refusal in use) found each
	// model service does, by aiCapabilityKey: whether it takes tools and
	// streams. A service not in it is unknown.
	AICapabilities map[string]AICapability `json:"aiCapabilities,omitempty"`
	// ProxyMode is Network & Proxy's Connection Method: "system" follows this computer's
	// own settings and the environment, "direct" adds no proxy of PKP's
	// own, "custom" uses the endpoint below. A file that names none means
	// "system", which is what every build before this one did.
	ProxyMode string `json:"proxyMode"`
	// The custom endpoint: scheme "socks5", "http" or "https". The fields
	// are kept while another mode is chosen, so switching back brings the
	// address the user typed rather than an empty form.
	ProxyScheme string `json:"proxyScheme,omitempty"`
	ProxyHost   string `json:"proxyHost,omitempty"`
	ProxyPort   int    `json:"proxyPort,omitempty"`
	ProxyUser   string `json:"proxyUser,omitempty"`
	// ProxyBypass is Addresses That Do Not Use the Proxy as the user typed it, one per line:
	// hosts, *.suffix forms and networks that never go through the proxy.
	ProxyBypass string `json:"proxyBypass,omitempty"`
	// ProxyPasswordFor is the endpoint and user name
	// ("socks5://127.0.0.1:7890 alice") the password in the keychain was
	// entered for. The password itself is never here; it is only sent to
	// the proxy this names.
	ProxyPasswordFor string `json:"proxyPasswordFor,omitempty"`
	// AppLock is App Lock: PKP opens only after the system
	// confirms this computer's owner. It is this computer's, for every
	// account, and starts off.
	AppLock bool `json:"appLock"`
	// AppLockDelay is how many minutes after leaving the app it locks by
	// itself, 0 (Immediately) to maxAppLockDelay. The screen locking or the window
	// closing always locks at once.
	AppLockDelay int `json:"appLockDelay"`
	// AppLockMethod is the way in: lockMethodSystem (the default) or
	// lockMethodPassword. AppLockPassword records that a Separate Password is set; its
	// verifier is in the system's credential store, never here.
	AppLockMethod   string `json:"appLockMethod"`
	AppLockPassword bool   `json:"appLockPassword,omitempty"`
	// AppLockThrottle counts wrong Separate Password attempts across restarts.
	AppLockThrottle LockThrottle `json:"appLockThrottle"`
	// NotificationsOff switches System Notifications off; it starts on, so it is kept as
	// an opt-out. NotifyInForeground also posts them while PKP is the app in
	// front with its window open, where the page already shows the same.
	NotificationsOff   bool `json:"notificationsOff,omitempty"`
	NotifyInForeground bool `json:"notifyInForeground,omitempty"`
	// UsageStatsEnabled controls daily active installation statistics; it defaults on.
	UsageStatsEnabled bool `json:"usageStatsEnabled"`
	// Keep false on disk so unrelated saves do not look like an older explicit answer.
	UsageStatsNoticeSeen bool `json:"usageStatsNoticeSeen"`
	// Language is Interface Language: one of i18n.Langs, or "" to follow this
	// computer's language list (language.go).
	Language string `json:"language,omitempty"`
}

// autoSignIn is Auto Sign-In as it stands; it needs Remember Account Password.
func (s LocalSettings) autoSignIn() bool {
	return !s.NoRememberSignIn && !s.NoAutoSignIn
}

// defaultLocalSettings is what a computer without a settings file gets.
var defaultLocalSettings = LocalSettings{
	ResumableUploads:  true,
	ParallelUploads:   2,
	PlaybackQuality:   AutoQuality,
	SubtitleStyle:     defaultSubtitleStyle,
	ParallelDownloads: 2,
	DownloadThreads:   download.MaxThreads,
	AIProvider:        string(ai.Anthropic),
	AIBaseURL:         ai.DefaultOpenAIBaseURL,
	AITokenLimit:      defaultAITokenLimit,
	AINaming:          namingPlex,
	ProxyMode:         proxyModeSystem,
	AppLockDelay:      defaultAppLockDelay,
	AppLockMethod:     lockMethodSystem,
	UsageStatsEnabled: true,
}

// defaultDownloadDir is ~/Downloads, or nothing without a home folder.
func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Downloads")
}

func (d *DriveService) settingsPath() string {
	return filepath.Join(d.configDir, "settings.json")
}

// LocalSettings reads this computer's settings. A missing file, or one that
// cannot be read, reads as the defaults; a setting the file does not name
// keeps its default.
func (d *DriveService) LocalSettings() LocalSettings {
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	return d.readSettings()
}

// SetResumableUploads switches Resume Uploads for every account and saves the
// choice. It decides what the next stop keeps and whether Continue resumes;
// uploads running now finish under the setting they started with.
func (d *DriveService) SetResumableUploads(on bool) error {
	if err := d.updateSettings(func(s *LocalSettings) { s.ResumableUploads = on }); err != nil {
		return err
	}
	for _, s := range d.sessions() {
		if q := s.queue(); q != nil {
			q.SetResumable(on)
		}
	}
	return nil
}

// SetParallelUploads sets how many files of each account upload at once and
// saves it. Files already moving keep going; queued ones start as the count
// drops under it.
func (d *DriveService) SetParallelUploads(n int) error {
	if n < minParallelUploads || n > maxParallelUploads {
		return i18n.Errorf("同时上传的文件数需在 %d 到 %d 之间", minParallelUploads, maxParallelUploads)
	}
	if err := d.updateSettings(func(s *LocalSettings) { s.ParallelUploads = n }); err != nil {
		return err
	}
	for _, s := range d.sessions() {
		if q := s.queue(); q != nil {
			q.SetParallel(n)
		}
	}
	return nil
}

// SetParallelDownloads sets how many files of each account download at once
// and saves it. Files already downloading keep going; queued ones start as
// the count drops under it.
func (d *DriveService) SetParallelDownloads(n int) error {
	if n < minParallelDownloads || n > maxParallelDownloads {
		return i18n.Errorf("同时下载的文件数需在 %d 到 %d 之间", minParallelDownloads, maxParallelDownloads)
	}
	if err := d.updateSettings(func(s *LocalSettings) { s.ParallelDownloads = n }); err != nil {
		return err
	}
	for _, s := range d.sessions() {
		if q := s.downloadQueue(); q != nil {
			q.SetParallel(n)
		}
	}
	return nil
}

// SetDownloadThreads sets how many connections each downloading file opens
// and saves it. Files already downloading keep the number they started with.
func (d *DriveService) SetDownloadThreads(n int) error {
	if n < 1 || n > download.MaxThreads {
		return i18n.Errorf("每个文件的下载线程数需在 1 到 %d 之间", download.MaxThreads)
	}
	if err := d.updateSettings(func(s *LocalSettings) { s.DownloadThreads = n }); err != nil {
		return err
	}
	for _, s := range d.sessions() {
		if q := s.downloadQueue(); q != nil {
			q.SetThreads(n)
		}
	}
	return nil
}

// SetDownloadDir sets the folder Download to Computer saves into and saves it. It must
// be an existing folder this user can write in; downloads already in the
// list keep the folder they were queued for.
func (d *DriveService) SetDownloadDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return i18n.New("保存位置必须是完整的文件夹路径")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return i18n.Errorf("「%s」不是文件夹", dir)
	}
	probe, err := os.CreateTemp(dir, ".pkp-write-*")
	if err != nil {
		d.log.Warn("download: folder not writable", "dir", dir, "error", err)
		return i18n.Errorf("无法写入「%s」，请换一个文件夹或检查权限", dir)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return d.updateSettings(func(s *LocalSettings) { s.DownloadDir = dir })
}

// SetKeepPlayingOnSwitch saves Keep Playing When Switching Accounts. What it means for a video
// playing now is the webview's to carry out: turning it off while another
// account's video plays pauses it and keeps its position.
func (d *DriveService) SetKeepPlayingOnSwitch(on bool) error {
	return d.updateSettings(func(s *LocalSettings) { s.KeepPlayingOnSwitch = on })
}

// updateSettings changes one setting and saves the file.
func (d *DriveService) updateSettings(change func(*LocalSettings)) error {
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	s := d.readSettings()
	change(&s)
	return d.writeSettings(s)
}

// readSettings loads the file over the defaults; settingsMu must be held.
func (d *DriveService) readSettings() LocalSettings {
	s := defaultLocalSettings
	if d.configErr != nil {
		return settled(s)
	}
	data, err := os.ReadFile(d.settingsPath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			d.log.Warn("drive: settings unreadable, using defaults", "error", err)
		}
		return settled(s)
	}
	stored := struct {
		*LocalSettings
		UsageStatsEnabled    *bool `json:"usageStatsEnabled"`
		UsageStatsNoticeSeen *bool `json:"usageStatsNoticeSeen"`
		UsageStatsChoiceMade bool  `json:"usageStatsChoiceMade"`
		UsageStatsOff        bool  `json:"usageStatsOff"`
	}{LocalSettings: &s}
	if err := json.Unmarshal(data, &stored); err != nil {
		d.log.Warn("drive: settings unreadable, using defaults", "error", err)
		return settled(defaultLocalSettings)
	}
	if stored.UsageStatsEnabled != nil {
		s.UsageStatsEnabled = *stored.UsageStatsEnabled
	} else if stored.UsageStatsChoiceMade || stored.UsageStatsOff {
		// Older omitempty saves represented a refusal by choiceMade alone.
		s.UsageStatsEnabled = false
	}
	if stored.UsageStatsNoticeSeen != nil {
		s.UsageStatsNoticeSeen = *stored.UsageStatsNoticeSeen
	} else {
		s.UsageStatsNoticeSeen = stored.UsageStatsChoiceMade || stored.UsageStatsEnabled != nil
	}
	return settled(s)
}

// settled holds settings to what the app can act on: hand-edited values to
// their range, a missing download folder to the default.
func settled(s LocalSettings) LocalSettings {
	s.ParallelUploads = min(max(s.ParallelUploads, minParallelUploads), maxParallelUploads)
	if !validChoice(s.PlaybackQuality) {
		s.PlaybackQuality = AutoQuality
	}
	s.SubtitleStyle = s.SubtitleStyle.settled()
	s.ParallelDownloads = min(max(s.ParallelDownloads, minParallelDownloads), maxParallelDownloads)
	s.DownloadThreads = min(max(s.DownloadThreads, 1), download.MaxThreads)
	if s.DownloadDir == "" {
		s.DownloadDir = defaultDownloadDir()
	}
	if s.AIProvider != string(ai.Anthropic) && s.AIProvider != string(ai.OpenAI) {
		s.AIProvider = string(ai.Anthropic)
	}
	if s.AIBaseURL == "" {
		s.AIBaseURL = ai.DefaultOpenAIBaseURL
	}
	if s.AITokenLimit != unlimitedAITokens {
		s.AITokenLimit = min(max(s.AITokenLimit, minAITokenLimit), maxAITokenLimit)
	}
	if !validNaming(s.AINaming) {
		s.AINaming = namingPlex
	}
	if s.ProxyMode == "" {
		s.ProxyMode = proxyModeSystem
	}
	// A hand-edited proxy block the app cannot act on would leave it with
	// no way out but the file itself — a wrong port is enough to take every
	// request offline — so an unusable one falls back to Follow System Settings.
	if proxyConfigOf(s).Check() != nil {
		s.ProxyMode = proxyModeSystem
		s.ProxyScheme, s.ProxyHost, s.ProxyPort = "", "", 0
		s.ProxyUser, s.ProxyPasswordFor = "", ""
	}
	if !validAppLockDelay(s.AppLockDelay) {
		s.AppLockDelay = defaultAppLockDelay
	}
	if s.AppLockMethod != lockMethodPassword {
		s.AppLockMethod = lockMethodSystem
	}
	return s
}

// writeSettings replaces the file atomically; settingsMu must be held.
func (d *DriveService) writeSettings(s LocalSettings) error {
	if d.configErr != nil {
		return d.configErr
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.configDir, 0o700); err != nil {
		return err
	}
	tmp := d.settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.settingsPath())
}
