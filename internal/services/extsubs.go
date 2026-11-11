package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// Transcode Subtitles: PKP requests subtitle entries by the original file's content id
// using GET /oracle/subtitle with the account's token. It fetches the selected
// text entries once per play and adds them to mpv as external tracks during
// transcode playback, without adding them to the original rendition.

const (
	// subtitleFetchers is how many subtitle files download at once: a
	// series episode can list thirty languages.
	subtitleFetchers = 4
	// maxSubtitleBytes skips anything bigger: no text subtitle for a film
	// comes near it.
	maxSubtitleBytes = 4 << 20
	subtitleTimeout  = 30 * time.Second
)

// textSubtitle lists the formats mpv reads as text; anything else the list
// returns (a picture format, an unknown one) is left out.
var textSubtitle = map[string]bool{"srt": true, "ass": true, "ssa": true, "vtt": true, "webvtt": true}

// extSub is one subtitle ready for mpv: its text and how the track is named.
type extSub struct {
	// lang is the language as the source named it ("chiSimplified",
	// "Simplified Chinese subtitles"); the UI turns it into words.
	lang string
	data string
}

// extSubs are the extracted subtitles of the video playing. attached is the
// load (PlayerService.loads) they were last added to: mpv drops external
// tracks whenever a file is loaded again, a quality switch included.
type extSubs struct {
	account, fileID string
	items           []extSub
	attached        uint64
}

// hasTranscode reports whether any rendition is a transcode: only then can
// the extracted subtitles be needed.
func hasTranscode(options []rendition) bool {
	for _, r := range options {
		if !r.Origin {
			return true
		}
	}
	return false
}

// loadExtracted fetches file's extracted subtitles and gives them to the
// player for that play. Nothing found, or a failure, leaves the transcodes
// as they were: without subtitles.
func (d *DriveService) loadExtracted(s *accountSession, fileID string, file drive.File) {
	gcid := strings.ToUpper(file.Hash)
	if len(gcid) != 40 {
		return
	}
	list, err := callIn(d, s, func(ctx context.Context, client *drive.Client) ([]drive.Subtitle, error) {
		return client.Subtitles(ctx, gcid, int64(mediaDuration(file)))
	})
	if err != nil {
		d.log.Warn("drive: extracted subtitles unavailable", "file", fileID, "error", err)
		return
	}
	items := d.fetchSubtitles(extractedOnly(list))
	if len(items) > 0 {
		d.player.setExtracted(s.key, fileID, items)
	}
}

// extractedOnly keeps source = 1 entries with a URL and a supported text
// format, preserving response order.
func extractedOnly(list []drive.Subtitle) []drive.Subtitle {
	var out []drive.Subtitle
	for _, sub := range list {
		if sub.Source == drive.SubtitleExtracted && sub.URL != "" && textSubtitle[strings.ToLower(strings.TrimPrefix(sub.Ext, "."))] {
			out = append(out, sub)
		}
	}
	return out
}

// fetchSubtitles downloads the files, a few at a time, through the proxy
// setting like every other outbound request, and keeps the order. One that
// fails is left out.
func (d *DriveService) fetchSubtitles(list []drive.Subtitle) []extSub {
	got := make([]*extSub, len(list))
	slots := make(chan struct{}, subtitleFetchers)
	var wg sync.WaitGroup
	for i, sub := range list {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			text, err := d.fetchSubtitle(sub.URL)
			if err != nil {
				d.log.Warn("drive: subtitle not downloaded", "name", sub.Name, "error", err)
				return
			}
			lang := ""
			if len(sub.Languages) > 0 {
				lang = strings.TrimSpace(sub.Languages[0])
			}
			got[i] = &extSub{lang: lang, data: text}
		}()
	}
	wg.Wait()
	var out []extSub
	for _, sub := range got {
		if sub != nil {
			out = append(out, *sub)
		}
	}
	return out
}

func (d *DriveService) fetchSubtitle(link string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), subtitleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", d.profile().UserAgent)
	resp, err := netproxy.Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSubtitleBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxSubtitleBytes {
		return "", fmt.Errorf("larger than %d bytes", maxSubtitleBytes)
	}
	return subtitleText(raw)
}

// subtitleText makes the bytes safe to pass to mpv as a memory:// URL, a C
// string: UTF-16 (which has NUL bytes) becomes UTF-8. Other 8-bit text
// (GBK, Big5) stays as it is; mpv detects its encoding itself.
func subtitleText(raw []byte) (string, error) {
	var units []uint16
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}):
		for i := 2; i+1 < len(raw); i += 2 {
			units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
	case bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		for i := 2; i+1 < len(raw); i += 2 {
			units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
	}
	if units != nil {
		raw = []byte(string(utf16.Decode(units)))
	}
	raw = bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF"))
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", fmt.Errorf("not text")
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("empty")
	}
	return string(raw), nil
}

/* ---------------- the player's side ---------------- */

// setExtracted gives the player the extracted subtitles of the file it
// opened for account; a file that has been replaced meanwhile ignores them.
func (p *PlayerService) setExtracted(account, fileID string, items []extSub) {
	p.mu.Lock()
	if p.fileID != fileID || p.account != account {
		p.mu.Unlock()
		return
	}
	p.subs = extSubs{account: account, fileID: fileID, items: items}
	p.mu.Unlock()
	p.attachExtracted()
}

// attachExtracted adds the extracted subtitles to the file mpv has loaded
// when it is a transcode, once per load. The first returned entry is selected;
// a quality switch then puts back what was chosen before it (see
// restoreTracks, which runs
// after this). Never call it from mpv's event goroutine.
func (p *PlayerService) attachExtracted() {
	p.mu.Lock()
	subs := p.subs
	load := p.loads
	q := p.state.Quality.Current
	if subs.fileID == "" || subs.fileID != p.fileID || subs.account != p.account || len(subs.items) == 0 ||
		q == "" || q == OriginKey || subs.attached == load {
		p.mu.Unlock()
		return
	}
	p.subs.attached = load
	show := !p.state.AISubtitles.On
	p.mu.Unlock()

	for i, sub := range subs.items {
		p.mu.Lock()
		stale := p.loads != load
		p.mu.Unlock()
		if stale {
			return
		}
		flag := "auto"
		if i == 0 && show {
			flag = "select"
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		err := p.mpv.Command(ctx, "sub-add", "memory://"+sub.data, flag, "", sub.lang)
		cancel()
		if err != nil {
			p.log.Warn("player: extracted subtitle not added", "lang", sub.lang, "error", err)
		}
	}
}
