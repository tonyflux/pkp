package services

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/tonyflux/pkp/internal/i18n"
)

// subtitle style and A/V sync: how big, how high and in what colour subtitles are
// drawn — saved, and the same for every video — and how far the subtitles
// and the sound are shifted against the picture, for the video playing only.

// SubtitleStyle controls native subtitle tracks and the AI caption overlay.
// Text subtitles take every setting. ASS tracks and picture tracks
// (Blu-ray PGS, DVD VobSub, DVB, XSUB) take the size but keep their own colours, and only part
// of them moves up: ASS lines at the bottom without positioning tags (libass
// leaves \pos, \move, \clip, drawings and top or middle lines alone), and
// pictures in the lower half of the frame.
type SubtitleStyle struct {
	// Scale multiplies the normal size, from minSubtitleScale to
	// maxSubtitleScale.
	Scale float64 `json:"scale"`
	// Raise lifts subtitles from their place at the bottom, in percent of
	// the picture's height: 0 leaves them at the bottom, 100 puts them at
	// the top.
	Raise int `json:"raise"`
	// Color is the colour of the text, "#RRGGBB".
	Color string `json:"color"`
}

const (
	minSubtitleScale = 0.5
	maxSubtitleScale = 2.5
	maxSubtitleRaise = 100
	// maxSyncShift bounds subtitle delay and audio delay, in seconds either way.
	maxSyncShift = 600
)

var defaultSubtitleStyle = SubtitleStyle{Scale: 1, Raise: 0, Color: "#FFFFFF"}

var subtitleColor = regexp.MustCompile(`^#[0-9A-F]{6}$`)

// check refuses a style mpv could not draw as asked.
func (s SubtitleStyle) check() error {
	switch {
	case math.IsNaN(s.Scale) || s.Scale < minSubtitleScale || s.Scale > maxSubtitleScale:
		return i18n.Errorf("字幕大小需在 %v 到 %v 之间", minSubtitleScale, maxSubtitleScale)
	case s.Raise < 0 || s.Raise > maxSubtitleRaise:
		return i18n.Errorf("字幕高度需在 0 到 %d 之间", maxSubtitleRaise)
	case !subtitleColor.MatchString(s.Color):
		return i18n.Errorf("字幕颜色「%s」不是 #RRGGBB 格式", s.Color)
	}
	return nil
}

// settled holds a hand-edited style to what check accepts.
func (s SubtitleStyle) settled() SubtitleStyle {
	if math.IsNaN(s.Scale) || s.Scale == 0 {
		s.Scale = defaultSubtitleStyle.Scale
	}
	s.Scale = min(max(s.Scale, minSubtitleScale), maxSubtitleScale)
	s.Raise = min(max(s.Raise, 0), maxSubtitleRaise)
	s.Color = strings.ToUpper(s.Color)
	if !subtitleColor.MatchString(s.Color) {
		s.Color = defaultSubtitleStyle.Color
	}
	return s
}

// SetSubtitleStyle saves how subtitles look and applies it at once.
// Color is "#RRGGBB".
func (d *DriveService) SetSubtitleStyle(style SubtitleStyle) error {
	style.Color = strings.ToUpper(style.Color)
	if err := style.check(); err != nil {
		return err
	}
	if err := d.updateSettings(func(s *LocalSettings) { s.SubtitleStyle = style }); err != nil {
		return err
	}
	if d.player == nil {
		return nil
	}
	return d.player.setSubtitleStyle(style)
}

// setSubtitleStyle makes style the one in use. Before mpv runs it is only
// kept: ensureRunning applies it.
func (p *PlayerService) setSubtitleStyle(style SubtitleStyle) error {
	p.mu.Lock()
	p.state.SubtitleStyle = style
	p.dirty = true
	p.mu.Unlock()
	if !p.mpv.Running() {
		return nil
	}
	return p.withContext(func(ctx context.Context) error { return p.applySubtitleStyle(ctx, style) })
}

// applySubtitleStyle hands style to mpv. Its options outlive a file, so
// this is needed once per mpv start and per change. sub-pos is in percent
// of the picture's height, 100 being where subtitles normally sit.
func (p *PlayerService) applySubtitleStyle(ctx context.Context, style SubtitleStyle) error {
	for _, o := range []struct {
		name  string
		value any
	}{
		{"sub-scale", style.Scale},
		{"sub-pos", float64(100 - style.Raise)},
		{"sub-color", style.Color},
	} {
		if err := p.mpv.SetProperty(ctx, o.name, o.value); err != nil {
			p.log.Warn("player: subtitle style not applied", "property", o.name, "error", err)
			return i18n.New("播放器没有接受这个字幕样式")
		}
	}
	return nil
}

// SetSubtitleDelay shifts the subtitles against the picture, in seconds:
// positive shows them later, for subtitles that come too early. It holds
// for the video playing; the next one starts at 0.
func (p *PlayerService) SetSubtitleDelay(seconds float64) error {
	return p.setShift("sub-delay", seconds)
}

// SetAudioDelay shifts the sound against the picture, in seconds: positive
// plays it later, for sound that runs ahead of the picture. It holds for
// the video playing; the next one starts at 0.
func (p *PlayerService) SetAudioDelay(seconds float64) error {
	return p.setShift("audio-delay", seconds)
}

func (p *PlayerService) setShift(property string, seconds float64) error {
	if math.IsNaN(seconds) || math.Abs(seconds) > maxSyncShift {
		return i18n.Errorf("调整幅度需在 ±%d 秒之内", maxSyncShift)
	}
	return p.withContext(func(ctx context.Context) error {
		return p.mpv.SetProperty(ctx, property, seconds)
	})
}

// resetShifts puts both shifts back to 0 for a new video: mpv would carry
// them over, and an offset that fits one file is wrong for the next.
func (p *PlayerService) resetShifts(ctx context.Context) {
	for _, property := range []string{"sub-delay", "audio-delay"} {
		if err := p.mpv.SetProperty(ctx, property, 0.0); err != nil {
			p.log.Warn("player: could not reset "+property, "error", err)
		}
	}
}

// The overlay uses the same 720-unit canvas, font size and bottom margin
// as mpv's text subtitles.
const (
	aiCaptionResY   = 720
	aiCaptionFont   = 38
	aiCaptionMargin = 22
)

type captionLook struct {
	style SubtitleStyle
	resX  int
}

// A raised caption needs the surface's proportions because sub-pos does
// not affect OSD overlays.
func (p *PlayerService) captionLook(shown bool) captionLook {
	p.mu.Lock()
	look := captionLook{style: p.state.SubtitleStyle}
	p.mu.Unlock()
	if !shown || look.style.Raise == 0 {
		return look
	}
	var dims struct{ W, H int }
	err := p.withContext(func(ctx context.Context) error {
		return p.mpv.GetProperty(ctx, "osd-dimensions", &dims)
	})
	if err == nil && dims.W > 0 && dims.H > 0 {
		look.resX = int(math.Round(aiCaptionResY * float64(dims.W) / float64(dims.H)))
	}
	return look
}

// Treat service text literally, including ASS markup and escape sequences.
func assCaption(text string, look captionLook) string {
	size := math.Round(aiCaptionFont*look.style.Scale*10) / 10
	var b strings.Builder
	b.WriteString(`{\an2`)
	if look.resX > 0 {
		bottom := float64(aiCaptionResY - aiCaptionMargin)
		y := bottom - float64(look.style.Raise)/100*(bottom-size*1.25)
		fmt.Fprintf(&b, `\pos(%d,%d)`, look.resX/2, int(math.Round(y)))
	}
	b.WriteString(`\fs` + strconv.FormatFloat(size, 'f', -1, 64) + `\bord2.5\shad0`)
	if c := look.style.Color; subtitleColor.MatchString(c) && c != defaultSubtitleStyle.Color {
		b.WriteString(`\1c&H` + c[5:7] + c[3:5] + c[1:3] + `&`)
	}
	b.WriteString("}")
	lineStart := true
	for _, r := range text {
		switch {
		case r == '\n':
			b.WriteString(`\N`)
			lineStart = true
			continue
		case r == ' ' && lineStart:
			b.WriteString(`\h`)
			continue
		case r == '{':
			b.WriteString(`\{`)
		case r == '\\':
			b.WriteString("\\\u2060")
		default:
			b.WriteRune(r)
		}
		lineStart = false
	}
	return b.String()
}
