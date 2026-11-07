package lanshare

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

//go:embed web/*.html web/*.js web/*.css
var embeddedWeb embed.FS

type connectionKey struct{}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, lanmedia.ErrInvalid) {
		return "unsupported_media"
	}
	if isDiskFull(err) {
		return "disk_full"
	}
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		return coded.Code()
	}
	text := err.Error()
	for _, code := range []string{"unauthorized", "invalid_password", "rate_limited", "forbidden", "source_unavailable", "not_ready", "prepare_disabled", "invalid_request", "disk_full", "media_unavailable", "unsupported_media"} {
		if text == code || strings.HasPrefix(text, code+":") {
			return code
		}
	}
	if errors.Is(err, os.ErrPermission) {
		return "source_unavailable"
	}
	return "internal"
}

func jsonError(w http.ResponseWriter, err error) {
	code := errorCode(err)
	status := http.StatusBadRequest
	switch code {
	case "unauthorized", "invalid_password":
		status = http.StatusUnauthorized
	case "forbidden", "prepare_disabled":
		status = http.StatusForbidden
	case "rate_limited":
		status = http.StatusTooManyRequests
	case "source_unavailable", "not_ready":
		status = http.StatusConflict
	case "disk_full":
		status = http.StatusInsufficientStorage
	case "media_unavailable":
		status = http.StatusServiceUnavailable
	case "internal":
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func jsonResponse(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errInvalid
	}
	return nil
}

func (m *Manager) sessionLocked(r *http.Request) string {
	cookie, err := r.Cookie("pkp_lan")
	if err != nil || len(cookie.Value) != 48 {
		return ""
	}
	s := m.sessions[cookie.Value]
	if s == nil || s.Generation != m.generation || time.Since(s.LastSeen) > 30*time.Minute {
		return ""
	}
	s.LastSeen = time.Now()
	return cookie.Value
}

func (m *Manager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	m.mu.Lock()
	host, running := m.host, m.running
	m.mu.Unlock()
	if !running || r.Host != host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+host) {
		jsonError(w, errForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		jsonError(w, errInvalid)
		return
	}
	if r.Method == http.MethodPost {
		typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || typ != "application/json" || r.Header.Get("X-PKP-Request") != "1" || r.Header.Get("Origin") != "http://"+host {
			jsonError(w, errForbidden)
			return
		}
	}
	if r.URL.Path == "/api/login" {
		if r.Method != http.MethodPost {
			jsonError(w, errInvalid)
			return
		}
		m.login(w, r)
		return
	}
	m.mu.Lock()
	sessionID := m.sessionLocked(r)
	m.mu.Unlock()
	if r.URL.Path == "/api/session" && r.Method == http.MethodGet {
		jsonResponse(w, map[string]bool{"authenticated": sessionID != ""})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			jsonError(w, errInvalid)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		switch name {
		case "index.html", "app.js", "player.js", "select.js", "style.css", "locales.js":
		default:
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, m.web, name)
		return
	}
	if sessionID == "" {
		jsonError(w, errors.New("unauthorized"))
		return
	}
	select {
	case m.apiSlots <- struct{}{}:
		defer func() { <-m.apiSlots }()
	default:
		jsonError(w, errors.New("rate_limited"))
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	if r.URL.Path != "/api/content" {
		cancel()
		timeout := 60 * time.Second
		if r.URL.Path == "/api/file" || r.URL.Path == "/api/playback" {
			timeout = 10 * time.Minute
		}
		ctx, cancel = context.WithTimeout(r.Context(), timeout)
	}
	defer cancel()
	ctx = m.sourceProofContext(ctx)
	r = r.WithContext(ctx)
	connection, _ := ctx.Value(connectionKey{}).(net.Conn)
	response := &activeResponse{Session: sessionID, Conn: connection, Cancel: cancel, Context: ctx}
	m.mu.Lock()
	if m.sessions[sessionID] == nil || !m.running || m.closed {
		m.mu.Unlock()
		jsonError(w, errors.New("unauthorized"))
		return
	}
	m.responses[response] = struct{}{}
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		cancel()
		response.Watch.Wait()
		m.mu.Lock()
		delete(m.responses, response)
		m.mu.Unlock()
		m.wg.Done()
	}()
	writer := &revocableWriter{ResponseWriter: w, ctx: ctx}
	m.api(writer, r, response)
}

type revocableWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w *revocableWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(30 * time.Second))
	return w.ResponseWriter.Write(p)
}
func (w *revocableWriter) WriteHeader(code int) {
	if w.ctx.Err() == nil {
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *revocableWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (m *Manager) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &request); err != nil {
		jsonError(w, err)
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	m.mu.Lock()
	rate := m.rates[ip]
	if time.Since(rate.Start) > time.Minute {
		rate = loginRate{Start: time.Now()}
	}
	if rate.Attempts >= 8 || len(m.sessions) >= 128 || len(m.rates) >= 1024 && rate.Attempts == 0 {
		m.mu.Unlock()
		jsonError(w, errors.New("rate_limited"))
		return
	}
	rate.Attempts++
	m.rates[ip] = rate
	record, generation := m.password, m.generation
	m.mu.Unlock()
	select {
	case m.loginSlot <- struct{}{}:
		defer func() { <-m.loginSlot }()
	default:
		jsonError(w, errors.New("rate_limited"))
		return
	}
	if !passwordMatches(request.Password, record) {
		jsonError(w, errors.New("invalid_password"))
		return
	}
	id := randomID()
	m.mu.Lock()
	if !m.running || m.generation != generation {
		m.mu.Unlock()
		jsonError(w, errors.New("unauthorized"))
		return
	}
	m.sessions[id] = &session{LastSeen: time.Now(), Generation: generation}
	delete(m.rates, ip)
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "pkp_lan", Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 1800})
	jsonResponse(w, map[string]bool{"authenticated": true})
}

func (m *Manager) bindResponse(response *activeResponse, sid, fid, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[response.Session] == nil || !m.running {
		return errors.New("unauthorized")
	}
	s := m.spaces[sid]
	if s == nil || !s.Enabled {
		return errForbidden
	}
	response.Space, response.File, response.Version = sid, fid, version
	if !response.Watching {
		response.Watching = true
		response.Watch.Add(1)
		go m.watchResponse(response)
	}
	return nil
}

func (m *Manager) api(w http.ResponseWriter, r *http.Request, response *activeResponse) {
	ctx := r.Context()
	sid, fid := r.URL.Query().Get("space"), r.URL.Query().Get("id")
	isGET := r.Method == http.MethodGet
	switch r.URL.Path {
	case "/api/logout":
		if r.Method != http.MethodPost {
			jsonError(w, errInvalid)
			return
		}
		m.mu.Lock()
		// A logout response may finish, but every other active request for this session is closed.
		delete(m.responses, response)
		m.revokeLocked(response.Session, "")
		m.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "pkp_lan", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		jsonResponse(w, map[string]any{})
	case "/api/spaces":
		if !isGET {
			jsonError(w, errInvalid)
			return
		}
		jsonResponse(w, m.visitorSpaces(ctx))
	case "/api/state":
		if !isGET {
			jsonError(w, errInvalid)
			return
		}
		m.visitorSnapshot(w, r, response)
	case "/api/playback":
		if r.Method != http.MethodPost {
			jsonError(w, errInvalid)
			return
		}
		m.visitorPlayback(w, r, response)
	case "/api/files":
		if !isGET {
			jsonError(w, errInvalid)
			return
		}
		if err := m.bindResponse(response, sid, "", ""); err != nil {
			jsonError(w, err)
			return
		}
		parent := r.URL.Query().Get("parent")
		entries, err := m.list(ctx, sid, parent)
		if err != nil {
			jsonError(w, err)
			return
		}
		jsonResponse(w, map[string]any{"entries": entries, "parentId": parent})
	case "/api/file":
		if !isGET {
			jsonError(w, errInvalid)
			return
		}
		source, err := m.resolve(ctx, sid, fid)
		if err != nil {
			jsonError(w, err)
			return
		}
		if err = m.bindResponse(response, sid, fid, source.Entry.Version); err != nil {
			jsonError(w, err)
			return
		}
		m.decorate(&source.Entry, sid)
		jsonResponse(w, source.Entry)
	case "/api/prepare":
		if r.Method != http.MethodPost {
			jsonError(w, errInvalid)
			return
		}
		var request prepareRequest
		if err := readJSON(w, r, &request); err != nil {
			jsonError(w, err)
			return
		}
		if err := m.bindResponse(response, request.SpaceID, "", ""); err != nil {
			jsonError(w, err)
			return
		}
		task, err := m.prepare(ctx, request, response.Session)
		if err != nil {
			jsonError(w, err)
			return
		}
		jsonResponse(w, task)
	case "/api/tasks":
		if !isGET {
			jsonError(w, errInvalid)
			return
		}
		if err := m.bindResponse(response, sid, "", ""); err != nil {
			jsonError(w, err)
			return
		}
		tasks, err := m.taskList(ctx, sid, response.Session, false)
		if err != nil {
			jsonError(w, err)
			return
		}
		jsonResponse(w, tasks)
	case "/api/tasks/remove", "/api/tasks/clear":
		if r.Method != http.MethodPost {
			jsonError(w, errInvalid)
			return
		}
		var request struct {
			SpaceID string `json:"spaceId"`
			TaskID  string `json:"taskId"`
		}
		if err := readJSON(w, r, &request); err != nil {
			jsonError(w, err)
			return
		}
		if request.SpaceID == "" || (r.URL.Path == "/api/tasks/remove" && request.TaskID == "") || (r.URL.Path == "/api/tasks/clear" && request.TaskID != "") {
			jsonError(w, errInvalid)
			return
		}
		if err := m.bindResponse(response, request.SpaceID, "", ""); err != nil {
			jsonError(w, err)
			return
		}
		removed, err := m.dismissTasks(ctx, request.SpaceID, request.TaskID, response.Session)
		if err != nil {
			jsonError(w, err)
			return
		}
		jsonResponse(w, map[string]any{"removed": removed})
	case "/api/cancel":
		if r.Method != http.MethodPost {
			jsonError(w, errInvalid)
			return
		}
		var request struct {
			SpaceID string `json:"spaceId"`
			TaskID  string `json:"taskId"`
		}
		if err := readJSON(w, r, &request); err != nil {
			jsonError(w, err)
			return
		}
		if err := m.cancelForSession(ctx, request.SpaceID, request.TaskID, response.Session); err != nil {
			jsonError(w, err)
			return
		}
		jsonResponse(w, map[string]any{})
	case "/api/content":
		if !isGET && r.Method != http.MethodHead {
			jsonError(w, errInvalid)
			return
		}
		m.content(w, r, response, sid, fid)
	default:
		http.NotFound(w, r)
	}
}

func (m *Manager) probe(ctx context.Context, sid string, source Source, variant string) (lanmedia.Info, error) {
	select {
	case m.probeSlot <- struct{}{}:
		defer func() { <-m.probeSlot }()
	case <-ctx.Done():
		return lanmedia.Info{}, ctx.Err()
	default:
		return lanmedia.Info{}, errors.New("rate_limited")
	}
	var f *os.File
	var entry Entry
	var release func()
	var err error
	if variant == "compatible" {
		f, entry, release, err = m.openCompatible(sid, source)
	} else if variant == "" || variant == "original" {
		f, entry, err = m.openOriginal(sid, source)
		if f != nil {
			release = func() { f.Close() }
		}
	} else {
		return lanmedia.Info{}, errInvalid
	}
	if err != nil {
		return lanmedia.Info{}, err
	}
	defer release()
	return m.probeOpened(ctx, f, sid, source.Entry.ID, source.Entry.Version, variant, entry.localToken)
}

func safeInline(mimeType string) bool {
	typ, _, _ := mime.ParseMediaType(mimeType)
	switch typ {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/avif", "image/bmp", "video/mp4", "video/webm", "video/quicktime", "audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav", "audio/webm", "audio/flac":
		return true
	}
	return false
}

type revocableFile struct {
	*os.File
	ctx   context.Context
	token string
}

func (f revocableFile) Read(p []byte) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	_, before, _, err := handleIdentity(f.File)
	if err != nil || before != f.token {
		return 0, errUnavailable
	}
	n, readErr := f.File.Read(p)
	_, after, _, err := handleIdentity(f.File)
	if err != nil || after != f.token {
		return 0, errUnavailable
	}
	return n, readErr
}

func (m *Manager) content(w http.ResponseWriter, r *http.Request, response *activeResponse, sid, fid string) {
	select {
	case m.transferSlots <- struct{}{}:
		defer func() { <-m.transferSlots }()
	default:
		jsonError(w, errors.New("rate_limited"))
		return
	}
	source, err := m.resolve(r.Context(), sid, fid)
	if err != nil {
		jsonError(w, err)
		return
	}
	var file *os.File
	var entry Entry
	var release func()
	variant := r.URL.Query().Get("variant")
	if variant == "compatible" {
		file, entry, release, err = m.openCompatible(sid, source)
	} else if variant == "" || variant == "original" {
		file, entry, err = m.openOriginal(sid, source)
		if file != nil {
			release = func() { file.Close() }
		}
	} else {
		err = errInvalid
	}
	if err != nil {
		jsonError(w, err)
		return
	}
	defer release()
	if err = m.bindResponse(response, sid, fid, entry.Version); err != nil {
		jsonError(w, err)
		return
	}
	// Avoid multi-range amplification; single and suffix byte ranges retain normal ServeContent semantics.
	if strings.Contains(r.Header.Get("Range"), ",") {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	contentType := entry.MIME
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" || !safeInline(contentType) {
		disposition, contentType = "attachment", "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": entry.Name}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("ETag", `"`+sourceVersion(entry.Version+"/"+variant+"/"+entry.localToken)+`"`)
	stat, err := file.Stat()
	if err != nil {
		jsonError(w, errUnavailable)
		return
	}
	_, token, _, err := handleIdentity(file)
	if err != nil || token != entry.localToken {
		jsonError(w, errUnavailable)
		return
	}
	http.ServeContent(w, r, entry.Name, stat.ModTime(), revocableFile{File: file, ctx: r.Context(), token: entry.localToken})
}

func (m *Manager) watchResponse(response *activeResponse) {
	defer response.Watch.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-response.Context.Done():
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		sid, fid, version := response.Space, response.File, response.Version
		m.mu.Unlock()
		ctx, cancel := context.WithTimeout(response.Context, sourceRecheckTimeout)
		ctx = m.sourceProofContext(ctx)
		var err error
		if fid != "" {
			var src Source
			src, err = m.resolve(ctx, sid, fid)
			if err == nil && src.Entry.Version != version {
				err = errUnavailable
			}
		} else {
			var s spaceRecord
			s, err = m.space(sid, false)
			if err == nil {
				err = m.validateSpace(ctx, s)
			}
		}
		cancel()
		if response.Context.Err() != nil {
			return
		}
		if err != nil {
			m.mu.Lock()
			response.Cancel()
			if response.Conn != nil {
				_ = response.Conn.Close()
			}
			m.mu.Unlock()
			return
		}
	}
}

func (m *Manager) probeOpened(ctx context.Context, f *os.File, sid, fid, version, variant, expectedToken string) (lanmedia.Info, error) {
	_, token, _, err := handleIdentity(f)
	if err != nil || token != expectedToken {
		return lanmedia.Info{}, errUnavailable
	}
	if variant == "" {
		variant = "original"
	}
	key := resourceKey(sid, fid, version) + "/" + variant + "/" + token
	m.mu.Lock()
	info, cached := m.mediaInfo[key]
	m.mu.Unlock()
	if cached {
		return info, nil
	}
	info, err = m.media.ProbeFile(ctx, f)
	if err != nil {
		return info, err
	}
	_, after, _, err := handleIdentity(f)
	if err != nil || after != token {
		return lanmedia.Info{}, errUnavailable
	}
	current, err := m.resolve(m.sourceProofContext(ctx), sid, fid)
	if err != nil || current.Entry.Version != version {
		return lanmedia.Info{}, errUnavailable
	}
	if variant == "original" && !current.Entry.Ready {
		return lanmedia.Info{}, errNotReady
	}
	m.mu.Lock()
	if len(m.mediaInfo) >= 128 {
		clear(m.mediaInfo)
	}
	m.mediaInfo[key] = info
	m.mu.Unlock()
	return info, nil
}
