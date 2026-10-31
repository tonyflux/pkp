package services

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/torrent"
)

// TorrentFile is one file a picked .torrent lists.
type TorrentFile struct {
	// Index is the file's place in the torrent, which is what the server's
	// file selection counts in (verified against the live API 2026-10-17:
	// a create with files ["10","9"] saved exactly those two).
	Index int `json:"index"`
	// Path is the path inside the torrent, "/" separated.
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Category buckets the row for its icon, as on the file page.
	Category string `json:"category"`
}

// TorrentMeta is a .torrent on this computer as the Cloud Add sheet shows it.
// The file is read locally — nothing is uploaded to look inside it — and the
// path comes back so the add can read it again when the user has chosen.
type TorrentMeta struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Hash is the BitTorrent v1 info hash, 40 upper-case hex digits.
	Hash  string        `json:"hash"`
	Size  int64         `json:"size"`
	Files []TorrentFile `json:"files"`
}

// ReadTorrent reads a .torrent picked on this computer and lists what it
// contains. The drive API takes a link, so the torrent is turned into a
// magnet link from its own bytes: no upload, and the file list is the
// torrent's own, available before any server has heard of it.
func (d *DriveService) ReadTorrent(path string) (TorrentMeta, error) {
	meta, err := readTorrentFile(path)
	if err != nil {
		return TorrentMeta{}, err
	}
	out := TorrentMeta{Path: path, Name: meta.Name, Hash: meta.InfoHash, Size: meta.Size}
	out.Files = make([]TorrentFile, 0, len(meta.Files))
	for _, f := range meta.Files {
		out.Files = append(out.Files, TorrentFile{
			Index:    f.Index,
			Path:     f.Path,
			Size:     f.Size,
			Category: categoryOf(drive.File{Name: f.Path}),
		})
	}
	return out, nil
}

// AddTorrent creates one cloud-add task for a .torrent on this computer, for
// an account ("" is the account on screen). files are the indices of
// TorrentMeta.Files to fetch; none means the whole torrent. The torrent is
// read again here so the task is built from the file itself rather than from
// what the page remembers of it.
func (d *DriveService) AddTorrent(account, path string, files []int, parentID string) (OfflineResult, error) {
	meta, err := readTorrentFile(path)
	if err != nil {
		return OfflineResult{}, err
	}
	picked, err := pickedFiles(meta, files)
	if err != nil {
		return OfflineResult{}, err
	}

	s, err := d.sessionFor(account)
	if err != nil {
		return OfflineResult{}, err
	}
	client, err := s.api()
	if err != nil {
		return OfflineResult{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), driveTimeout)
	defer cancel()
	created, err := client.CreateOfflineTask(ctx, drive.OfflineTaskParams{
		URL:      meta.Magnet(),
		ParentID: parentID,
		Files:    picked,
	})

	result := OfflineResult{URL: meta.Magnet(), Name: meta.Name}
	if err != nil {
		result.Error = d.classify(s, client, err).Error()
		return result, nil
	}
	result.OK = true
	result.TaskID = created.Task.ID
	if created.Task.Name != "" {
		result.Name = created.Task.Name
	}
	requestSync(s)
	kickInbox(s)
	return result, nil
}

// readTorrentFile reads and parses one .torrent, with the reasons a person
// can act on: a file that is not a torrent, or one the API cannot address.
func readTorrentFile(path string) (torrent.Meta, error) {
	if path == "" {
		return torrent.Meta{}, i18n.New("没有选择种子文件")
	}
	info, err := os.Stat(path)
	if err != nil {
		return torrent.Meta{}, i18n.Errorf("打不开种子文件：%w", err)
	}
	if info.Size() > torrent.MaxSize {
		return torrent.Meta{}, i18n.Errorf("种子文件超过 %d MB，不像是种子", torrent.MaxSize>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return torrent.Meta{}, i18n.Errorf("读不了种子文件：%w", err)
	}
	meta, err := torrent.Parse(data)
	if err != nil {
		if errors.Is(err, torrent.ErrNoV1Hash) {
			return torrent.Meta{}, i18n.New("这是 BitTorrent v2 种子，云盘只认 v1 磁力链接")
		}
		return torrent.Meta{}, i18n.New("这个文件不是种子，或者已经损坏")
	}
	if len(meta.Files) == 0 {
		return torrent.Meta{}, i18n.New("这个种子里没有文件")
	}
	return meta, nil
}

// pickedFiles turns chosen indices into what the API's file filter takes:
// the index as a string, in torrent order. Choosing everything sends no
// filter at all, which is also what the server does by default.
func pickedFiles(meta torrent.Meta, files []int) ([]string, error) {
	if len(files) == 0 || len(files) == len(meta.Files) {
		return nil, nil
	}
	seen := make(map[int]bool, len(files))
	for _, i := range files {
		if i < 0 || i >= len(meta.Files) {
			return nil, i18n.Errorf("种子里没有第 %d 个文件", i)
		}
		seen[i] = true
	}
	picked := make([]string, 0, len(seen))
	for _, f := range meta.Files {
		if seen[f.Index] {
			picked = append(picked, strconv.Itoa(f.Index))
		}
	}
	return picked, nil
}
