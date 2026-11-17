package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/crashlog"
	"github.com/tonyflux/pkp/internal/netproxy"
)

const (
	crashPreviewBytes = 120 * 1024
	crashRequestBytes = 128 * 1024
)

// CrashFeedbackReport is a local report awaiting an explicit user decision.
type CrashFeedbackReport struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Path      string `json:"path"`
	Truncated bool   `json:"truncated"`
}

type crashDecision struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
}

// CrashFeedbackService asks before sending any report, independent of usage statistics.
type CrashFeedbackService struct {
	mu       sync.Mutex
	endpoint string
	state    string
	stateErr error
	dirs     []string
	client   func() *http.Client
	pending  *CrashFeedbackReport
	key      string
}

// NewCrashFeedbackService scans the recorder's desktop and fallback locations.
func NewCrashFeedbackService(drive *DriveService, endpoint string) *CrashFeedbackService {
	return &CrashFeedbackService{
		endpoint: strings.TrimRight(endpoint, "/"),
		state:    filepath.Join(drive.configDir, "crash-feedback.json"),
		stateErr: drive.configErr,
		dirs:     crashlog.ReportDirs(),
		client:   netproxy.Client,
	}
}

// Pending returns one report for review without sending it.
func (s *CrashFeedbackService) Pending() (*CrashFeedbackReport, error) {
	return s.pendingReport(application.System.IsServer())
}

func (s *CrashFeedbackService) pendingReport(server bool) (*CrashFeedbackReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endpoint == "" || server {
		return nil, nil
	}
	if s.pending != nil {
		copy := *s.pending
		return &copy, nil
	}
	decisions, err := s.load()
	if err != nil {
		return nil, err
	}
	for _, dir := range s.dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		slices.Reverse(entries)
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "PKP-Crash-") || !strings.HasSuffix(entry.Name(), ".log") || !entry.Type().IsRegular() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
				continue
			}
			file, err := os.Open(path)
			if err != nil {
				continue
			}
			opened, err := file.Stat()
			if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
				file.Close()
				continue
			}
			data, err := io.ReadAll(io.LimitReader(file, crashPreviewBytes+1))
			file.Close()
			if err != nil {
				continue
			}
			after, err := os.Lstat(path)
			if err != nil || !after.Mode().IsRegular() || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
				continue
			}
			truncated := len(data) > crashPreviewBytes
			if truncated {
				data = data[:crashPreviewBytes]
				if _, width := utf8.DecodeLastRune(data); width == 1 && len(data) > 0 && data[len(data)-1] >= utf8.RuneSelf {
					start := len(data) - 1
					for start > 0 && len(data)-start < utf8.UTFMax && data[start]&0xc0 == 0x80 {
						start--
					}
					if !utf8.FullRune(data[start:]) {
						data = data[:start]
					}
				}
			}
			text := strings.ToValidUTF8(string(data), "�")
			for len(text) > crashPreviewBytes {
				_, width := utf8.DecodeLastRuneInString(text)
				text = text[:len(text)-width]
				truncated = true
			}
			// A report's fingerprint stays local; it is not sent to the server.
			hash := sha256.Sum256(append([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00", path, info.Size(), info.ModTime().UnixNano())), data...))
			key := hex.EncodeToString(hash[:])
			decision := decisions[key]
			if decision.Done {
				continue
			}
			if decision.ID == "" {
				decision.ID, err = randomCrashID()
				if err != nil {
					return nil, err
				}
				decisions[key] = decision
				if err := s.save(decisions); err != nil {
					return nil, err
				}
			}
			s.key = key
			s.pending = &CrashFeedbackReport{ID: decision.ID, Text: text, Path: path, Truncated: truncated}
			copy := *s.pending
			return &copy, nil
		}
	}
	return nil, nil
}

// Dismiss retains the local report but does not ask about this version again.
func (s *CrashFeedbackService) Dismiss(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finish(id)
}

// Send transmits exactly the reviewed text only after the user chooses to send.
func (s *CrashFeedbackService) Send(id, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil || s.pending.ID != id {
		return errors.New("crash feedback: no matching pending report")
	}
	if !utf8.ValidString(text) || len(text) == 0 || len(text) > crashPreviewBytes || strings.TrimSpace(text) == "" {
		return errors.New("crash feedback: report is empty or too large")
	}
	endpoint, err := url.Parse(s.endpoint)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("crash feedback: invalid endpoint")
	}
	ip := net.ParseIP(endpoint.Hostname())
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && (endpoint.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return errors.New("crash feedback: HTTPS required")
	}
	// The UUID changes with edits, while an unchanged retry remains idempotent.
	hash := sha256.Sum256([]byte(id + "\x00" + text))
	hash[6] = hash[6]&0x0f | 0x40
	hash[8] = hash[8]&0x3f | 0x80
	reportID := formatCrashID(hash[:16])
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(struct {
		ID     string `json:"id"`
		Report string `json:"report"`
	}{reportID, text})
	if err != nil || encoded.Len()-1 > crashRequestBytes {
		return errors.New("crash feedback: report is too large")
	}
	body := bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint+"/v1/crashes", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := *s.client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("crash feedback: send failed: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("crash feedback: server returned HTTP %d", response.StatusCode)
	}
	return s.finish(id)
}

func (s *CrashFeedbackService) finish(id string) error {
	if s.pending == nil || s.pending.ID != id {
		return errors.New("crash feedback: no matching pending report")
	}
	decisions, err := s.load()
	if err != nil {
		return err
	}
	decision := decisions[s.key]
	if decision.ID != id {
		return errors.New("crash feedback: report state changed")
	}
	decision.Done = true
	decisions[s.key] = decision
	if err := s.save(decisions); err != nil {
		return err
	}
	s.pending, s.key = nil, ""
	return nil
}

func (s *CrashFeedbackService) load() (map[string]crashDecision, error) {
	if s.stateErr != nil {
		return nil, s.stateErr
	}
	decisions := make(map[string]crashDecision)
	data, err := os.ReadFile(s.state)
	if errors.Is(err, os.ErrNotExist) {
		return decisions, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &decisions); err != nil {
		return nil, err
	}
	if decisions == nil {
		return nil, errors.New("crash feedback: invalid state")
	}
	return decisions, nil
}

func (s *CrashFeedbackService) save(decisions map[string]crashDecision) error {
	data, err := json.Marshal(decisions)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.state)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".crash-feedback-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.state)
}

func randomCrashID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return formatCrashID(b[:]), nil
}

func formatCrashID(b []byte) string {
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
