package services

import (
	"context"
	"encoding/base64"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/audiotags"
	"github.com/tonyflux/pkp/internal/i18n"
)

// Music playback: what a music file says about itself, for the music
// page. The file's own header is read once more, on a connection of its
// own to the stream proxy, never through mpv: FFmpeg has the tags and the
// embedded cover as soon as the header is parsed.

// AudioDetails is the playing music file's own description. Empty fields
// are unknown; Cover is a data: URL, "" when the file carries no picture.
type AudioDetails struct {
	FileID string `json:"fileId"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Date   string `json:"date"`
	Cover  string `json:"cover"`
}

// audioRead bounds one read of a header: a remote MP4 may need its moov
// from the end of the file.
const audioRead = 20 * time.Second

// audioMemo keeps the last file's details: the page asks again whenever it
// comes back up.
type audioMemo struct {
	mu      sync.Mutex
	details AudioDetails
}

var errNotPlaying = i18n.New("这个文件已经不在播放")

// AudioDetails reads the tags and cover of fileID, which must be the file
// playing.
func (p *PlayerService) AudioDetails(fileID string) (AudioDetails, error) {
	p.audio.mu.Lock()
	if p.audio.details.FileID == fileID && fileID != "" {
		d := p.audio.details
		p.audio.mu.Unlock()
		return d, nil
	}
	p.audio.mu.Unlock()

	p.mu.Lock()
	url, playing := p.current.URL, p.fileID
	p.mu.Unlock()
	if url == "" || fileID == "" || playing != fileID {
		return AudioDetails{}, errNotPlaying
	}
	ctx, cancel := context.WithTimeout(context.Background(), audioRead)
	defer cancel()
	tags, err := audiotags.Read(ctx, url)
	if err != nil {
		return AudioDetails{}, err
	}
	d := AudioDetails{FileID: fileID, Title: tags.Title, Artist: tags.Artist, Album: tags.Album, Date: tags.Date}
	if len(tags.Cover) > 0 {
		d.Cover = "data:" + tags.CoverType + ";base64," + base64.StdEncoding.EncodeToString(tags.Cover)
	}
	p.audio.mu.Lock()
	p.audio.details = d
	p.audio.mu.Unlock()
	return d, nil
}
