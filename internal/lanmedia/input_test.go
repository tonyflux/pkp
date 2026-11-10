package lanmedia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPinnedInputRangesAndBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	content := bytes.Repeat([]byte("0123456789"), 1000)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	in, closeInput, err := fileInput(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInput()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second * 5}
	defer client.CloseIdleConnections()
	u, _ := url.Parse(in.location)
	if u.Hostname() != "127.0.0.1" || in.options[1] != "http,tcp" {
		t.Fatalf("unrestricted endpoint: %s %v", in.location, in.options)
	}
	if !strings.Contains(strings.Join(in.options, " "), "-rw_timeout 30000000") {
		t.Fatalf("missing bounded child I/O: %v", in.options)
	}
	req, _ := http.NewRequest(http.MethodGet, in.location, nil)
	req.Header.Set("Range", "bytes=123-145")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusPartialContent || !bytes.Equal(body, content[123:146]) {
		t.Fatalf("range: status=%d body=%q err=%v", response.StatusCode, body, err)
	}
	for _, tc := range []struct {
		name, method, suffix, host, origin string
		status                             int
	}{
		{"head", http.MethodHead, "", "", "", http.StatusOK},
		{"wrong path", http.MethodGet, "x", "", "", http.StatusNotFound},
		{"query", http.MethodGet, "?leak=1", "", "", http.StatusNotFound},
		{"wrong host", http.MethodGet, "", "localhost", "", http.StatusNotFound},
		{"browser origin", http.MethodGet, "", "", "https://example.com", http.StatusNotFound},
		{"write method", http.MethodPost, "", "", "", http.StatusMethodNotAllowed},
		{"options", http.MethodOptions, "", "", "", http.StatusMethodNotAllowed},
		{"put", http.MethodPut, "", "", "", http.StatusMethodNotAllowed},
		{"trace", http.MethodTrace, "", "", "", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, in.location+tc.suffix, nil)
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", response.StatusCode, tc.status)
			}
		})
	}
	encoded := *u
	encoded.RawPath = "/%" + fmt.Sprintf("%02x", u.Path[1]) + u.Path[2:]
	response, err = client.Get(encoded.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("encoded token path accepted")
	}
	if err := os.Rename(path, path+".old"); err == nil {
		if err := os.WriteFile(path, []byte("replacement secret"), 0600); err != nil {
			t.Fatal(err)
		}
		response, err := client.Get(in.location)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || !bytes.Equal(body, content) {
			t.Fatal("path replacement redirected pinned handle")
		}
	}
	closeInput()
	if _, err := client.Get(in.location); err == nil {
		t.Fatal("endpoint remains reachable after operation")
	}
	var one [1]byte
	if _, err := f.ReadAt(one[:], 0); err != nil {
		t.Fatal("borrowed input handle closed")
	}
}

func TestPinnedInputMutationAndCancellation(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("completed")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in, closeInput, err := fileInput(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInput()
	if err := f.Truncate(3); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second * 5}
	defer client.CloseIdleConnections()
	response, err := client.Get(in.location)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("changed handle served: %d", response.StatusCode)
	}
	cancel()
	closeInput()
	if _, err := client.Get(in.location); err == nil {
		t.Fatal("cancelled endpoint remains reachable")
	}
}

func TestMediaCommandDoesNotInheritProxies(t *testing.T) {
	t.Setenv("http_proxy", "http://untrusted.invalid")
	t.Setenv("HTTPS_PROXY", "http://untrusted.invalid")
	cmd := exec.Command("unused")
	configureMediaCommand(cmd)
	if cmd.WaitDelay != 5*time.Second {
		t.Fatalf("missing bounded subprocess wait: %v", cmd.WaitDelay)
	}
	for _, item := range cmd.Env {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy":
			t.Fatalf("proxy inherited: %s", key)
		}
	}
}

func TestPinnedLargeInputConcurrentRangesWithoutReopening(t *testing.T) {
	// Windows needs FSCTL_SET_SPARSE before extending a file; never risk allocating
	// 20 GiB there. The ordinary handle/Range tests above run on both platforms.
	if runtime.GOOS == "windows" {
		t.Skip("large sparse fixture requires Unix sparse-file semantics")
	}
	const size int64 = 20 << 30
	root := t.TempDir()
	path := filepath.Join(root, "large")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	offsets := []int64{123, 9<<30 + 777, size - 128}
	markers := [][]byte{bytes.Repeat([]byte{0x37}, 64), bytes.Repeat([]byte{0x92}, 64), bytes.Repeat([]byte{0xe4}, 64)}
	for i, offset := range offsets {
		if _, err := file.WriteAt(markers[i], offset); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := file.Seek(42, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement must never be read"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".old"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in, closeInput, err := fileInput(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInput()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var readers sync.WaitGroup
	for i := range 18 {
		readers.Add(1)
		go func(index int) {
			defer readers.Done()
			index %= len(offsets)
			offset := offsets[index]
			req, _ := http.NewRequest(http.MethodGet, in.location, nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+63))
			response, err := client.Do(req)
			if err != nil {
				t.Errorf("range request: %v", err)
				return
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 65))
			if err != nil || response.StatusCode != http.StatusPartialContent || !bytes.Equal(body, markers[index]) {
				t.Errorf("range at %d: status=%d bytes=%x err=%v", offset, response.StatusCode, body, err)
			}
			want := fmt.Sprintf("bytes %d-%d/%d", offset, offset+63, size)
			if response.Header.Get("Content-Range") != want {
				t.Errorf("range length=%q want=%q", response.Header.Get("Content-Range"), want)
			}
		}(i)
	}
	readers.Wait()
	if offset, err := file.Seek(0, io.SeekCurrent); err != nil || offset != 42 {
		t.Fatalf("shared handle cursor changed: %d %v", offset, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "large" {
		t.Fatalf("unexpected input copy/artifact: %v %v", entries, err)
	}
	// Do not consume this 20-GiB response: cancellation must interrupt its writer.
	response, err := client.Get(in.location)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	cancel()
	stopped := make(chan struct{})
	go func() { closeInput(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled input server did not release active response")
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("operation closed caller's pinned handle")
	}
}

func TestCancelledFileAPIsDoNotLaunchOrCloseInput(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write([]byte("completed input")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	processor := &Processor{ffmpeg: "must-not-launch", ffprobe: "must-not-launch"}
	if _, err := processor.ProbeFile(ctx, file); !errors.Is(err, context.Canceled) {
		t.Fatalf("probe cancellation: %v", err)
	}
	output := filepath.Join(t.TempDir(), "output.mp4")
	if err := processor.ConvertFile(ctx, file, output, Plan{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("convert cancellation: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("cancelled output published: %v", err)
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("cancelled API closed borrowed handle")
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	fail      error
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return w.fail
}

func TestDeadlineResponseWriterRefreshesAndFailsClosed(t *testing.T) {
	record := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := deadlineResponseWriter{record}
	before := time.Now()
	if n, err := writer.Write([]byte("first")); err != nil || n != 5 {
		t.Fatalf("first write: %d %v", n, err)
	}
	after := time.Now()
	if len(record.deadlines) != 1 || record.deadlines[0].Before(before.Add(inputIdleTimeout)) || record.deadlines[0].After(after.Add(inputIdleTimeout)) {
		t.Fatalf("unexpected deadline: %v", record.deadlines)
	}
	if n, err := writer.Write([]byte("second")); err != nil || n != 6 {
		t.Fatalf("second write: %d %v", n, err)
	}
	if len(record.deadlines) != 2 || record.deadlines[1].Before(record.deadlines[0]) {
		t.Fatalf("write deadline not refreshed: %v", record.deadlines)
	}
	failure := errors.New("cannot set write deadline")
	record.fail = failure
	if n, err := writer.Write([]byte("must not be written")); n != 0 || !errors.Is(err, failure) {
		t.Fatalf("deadline failure ignored: %d %v", n, err)
	}
	if record.Body.String() != "firstsecond" {
		t.Fatalf("bytes written after deadline failure: %q", record.Body.String())
	}
}
