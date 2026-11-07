package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
	"github.com/tonyflux/pkp/internal/lanshare"
)

// LANService owns an independent listener: locking or hiding the desktop does
// not revoke guests. Only explicit management actions and application exit do.
type LANService struct {
	drive   *DriveService
	mu      sync.Mutex
	manager *lanshare.Manager
	closed  bool
}

func NewLANService(d *DriveService) *LANService {
	s := &LANService{drive: d}
	d.lan = s
	return s
}

func (s *LANService) ServiceName() string { return "LANService" }

func (s *LANService) get() (*lanshare.Manager, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("service_closed")
	}
	if s.manager != nil {
		return s.manager, nil
	}
	if s.drive.configErr != nil {
		return nil, errors.New("storage_unavailable")
	}
	manager, err := lanshare.New(lanshare.Options{ConfigDir: s.drive.configDir, OriginalDir: s.drive.LocalSettings().DownloadDir, Cloud: &lanCloud{drive: s.drive}, Media: lanmedia.New()})
	if err != nil {
		return nil, err
	}
	s.manager = manager
	return manager, nil
}

func (s *LANService) Status() (lanshare.Status, error) {
	m, err := s.get()
	if err != nil {
		return lanshare.Status{}, err
	}
	return m.Status(), nil
}
func (s *LANService) Tasks() ([]lanshare.Task, error) {
	m, err := s.get()
	if err != nil {
		return nil, err
	}
	return m.Tasks(), nil
}
func (s *LANService) Interfaces() ([]lanshare.NetworkInterface, error) { return lanshare.Interfaces() }
func (s *LANService) Start(req lanshare.StartRequest) error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.Start(req)
}
func (s *LANService) Stop() error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.Stop()
}
func (s *LANService) SetPassword(password string) error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.SetPassword(password)
}
func (s *LANService) AddSpace(req lanshare.SpaceRequest) (lanshare.Space, error) {
	m, err := s.get()
	if err != nil {
		return lanshare.Space{}, err
	}
	return m.AddSpace(req)
}
func (s *LANService) UpdateSpace(req lanshare.SpaceUpdate) error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.UpdateSpace(req)
}
func (s *LANService) RemoveSpace(id string) error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.RemoveSpace(id)
}
func (s *LANService) ListPreparationEntries(spaceID, parentID string) ([]lanshare.PreparationEntry, error) {
	m, err := s.get()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return m.ListPreparationEntries(ctx, spaceID, parentID)
}
func (s *LANService) PreparationThumbnail(spaceID, fileID string) (string, error) {
	m, err := s.get()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return m.PreparationThumbnail(ctx, spaceID, fileID)
}
func (s *LANService) PrepareDesktop(spaceID, fileID, kind string) (lanshare.Task, error) {
	m, err := s.get()
	if err != nil {
		return lanshare.Task{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return m.PrepareDesktop(ctx, spaceID, fileID, kind)
}
func (s *LANService) CancelTask(id string) error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.CancelTask(id)
}
func (s *LANService) ResumeTask(id string) error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.ResumeTask(id)
}
func (s *LANService) DismissTaskHistory(spaceID, taskID string) error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.DismissTaskHistory(spaceID, taskID)
}
func (s *LANService) ClearCache() (lanshare.ClearResult, error) {
	m, err := s.get()
	if err != nil {
		return lanshare.ClearResult{}, err
	}
	return m.ClearCache()
}

func (s *LANService) SetStorageDirectory(kind, path string) error {
	m, err := s.get()
	if err != nil {
		return err
	}
	return m.SetStorageDirectory(kind, path)
}

// CloudFolders never resolves an empty account to the foreground account.
func (s *LANService) CloudFolders(accountID, parentID string) ([]lanshare.Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cloud := &lanCloud{drive: s.drive}
	identity, err := cloud.AccountIdentity(ctx, accountID)
	if err != nil {
		return nil, err
	}
	entries, err := cloud.List(ctx, accountID, "", parentID, identity)
	if err != nil {
		return nil, err
	}
	folders := make([]lanshare.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir {
			folders = append(folders, entry)
		}
	}
	return folders, nil
}

func (s *LANService) ServiceShutdown() error {
	s.mu.Lock()
	s.closed = true
	manager := s.manager
	s.mu.Unlock()
	if manager != nil {
		return manager.Close()
	}
	return nil
}
