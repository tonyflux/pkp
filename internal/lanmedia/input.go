package lanmedia

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type mediaInput struct {
	location string
	options  []string
	file     *os.File
	before   os.FileInfo
	path     string
	ctx      context.Context
}

func (in *mediaInput) valid() bool {
	if in.ctx.Err() != nil {
		return false
	}
	after, err := in.file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(in.before, after) || in.before.Size() != after.Size() || !in.before.ModTime().Equal(after.ModTime()) {
		return false
	}
	return in.path == "" || unchanged(in.path, in.before)
}

func pathInput(ctx context.Context, path string) (*mediaInput, func(), error) {
	path, before, err := localInput(path)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	in := &mediaInput{location: path, options: inputOptions, file: f, before: before, path: path, ctx: ctx}
	if !in.valid() {
		_ = f.Close()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, ErrInvalid
	}
	return in, func() { _ = f.Close() }, nil
}

type checkedReader struct{ input *mediaInput }

func (r checkedReader) ReadAt(p []byte, offset int64) (int, error) {
	if !r.input.valid() {
		return 0, ErrInvalid
	}
	return r.input.file.ReadAt(p, offset)
}

const inputIdleTimeout = 30 * time.Second

type deadlineResponseWriter struct{ http.ResponseWriter }

func (w deadlineResponseWriter) Write(p []byte) (int, error) {
	// Bound stalled clients without timing out a long, actively consumed movie.
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(inputIdleTimeout)); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}

// fileInput exposes only the already-authorized handle, never its current path.
// The private endpoint permits seeking without copying large originals to cache.
func fileInput(ctx context.Context, file *os.File) (*mediaInput, func(), error) {
	if file == nil {
		return nil, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	before, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 {
		return nil, nil, ErrInvalid
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, nil, err
	}
	path := "/" + hex.EncodeToString(secret[:])
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	host := listener.Addr().String()
	options := append([]string(nil), inputOptions...)
	options[1] = "http,tcp"
	options = append(options, "-http_proxy", "", "-rw_timeout", strconv.FormatInt(inputIdleTimeout.Microseconds(), 10))
	in := &mediaInput{location: "http://" + host + path, options: options, file: file, before: before, ctx: ctx}
	var mu sync.Mutex
	var handlers sync.WaitGroup
	closed := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		handlers.Add(1)
		mu.Unlock()
		defer handlers.Done()
		if r.Host != host || r.RequestURI != path || r.URL.Path != path || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.Header.Get("Origin") != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !in.valid() {
			http.Error(w, "input changed", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(deadlineResponseWriter{w}, r, "media", before.ModTime(), io.NewSectionReader(checkedReader{in}, 0, before.Size()))
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: inputIdleTimeout, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	stopped := make(chan struct{})
	go func() { defer close(stopped); _ = server.Serve(listener) }()
	var once sync.Once
	closeServer := func() {
		once.Do(func() {
			mu.Lock()
			closed = true
			mu.Unlock()
			_ = server.Close()
			_ = listener.Close()
			<-stopped
			handlers.Wait()
		})
	}
	stop := context.AfterFunc(ctx, closeServer)
	return in, func() { stop(); closeServer() }, nil
}

func configureMediaCommand(cmd *exec.Cmd) {
	configureCommand(cmd)
	cmd.WaitDelay = 5 * time.Second
	// An inherited proxy must never see the endpoint token or file bytes.
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy":
			continue
		}
		cmd.Env = append(cmd.Env, item)
	}
}

// ProbeFile borrows a complete, authorized handle without closing, seeking,
// reopening or copying it. The caller retains source-version/revocation checks.
func (p *Processor) ProbeFile(ctx context.Context, file *os.File) (Info, error) {
	if p == nil || p.ffprobe == "" {
		return Info{}, ErrUnavailable
	}
	in, closeInput, err := fileInput(ctx, file)
	if err != nil {
		return Info{}, err
	}
	defer closeInput()
	return p.probe(ctx, in)
}

// ConvertFile borrows its complete, authorized input until the child and private
// endpoint stop. Only the trusted private output is reopened for validation.
func (p *Processor) ConvertFile(ctx context.Context, file *os.File, output string, plan Plan, progress func(float64)) error {
	if p == nil || p.ffmpeg == "" || p.ffprobe == "" {
		return ErrUnavailable
	}
	in, closeInput, err := fileInput(ctx, file)
	if err != nil {
		return err
	}
	defer closeInput()
	return p.convert(ctx, in, output, plan, progress)
}
