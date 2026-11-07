// Package lanmedia prepares complete local media for browser playback using bundled tools.
package lanmedia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

var (
	ErrUnavailable = errors.New("media_unavailable")
	ErrUnsupported = errors.New("unsupported_media")
	ErrInvalid     = errors.New("invalid_media")
)

type Info struct {
	Container     string  `json:"container"`
	VideoCodec    string  `json:"videoCodec"`
	AudioCodec    string  `json:"audioCodec"`
	AudioProfile  string  `json:"audioProfile"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	Duration      float64 `json:"duration"`
	HDR           bool    `json:"hdr"`
	Encrypted     bool    `json:"encrypted"`
	Subtitles     bool    `json:"subtitles"`
	BitDepth      int     `json:"bitDepth"`
	PixelFormat   string  `json:"pixelFormat"`
	Profile       string  `json:"profile"`
	Level         int     `json:"level"`
	VideoMIME     string  `json:"videoMime"`
	AudioMIME     string  `json:"audioMime"`
	FrameRate     float64 `json:"frameRate"`
	VideoBitrate  int64   `json:"videoBitrate"`
	SampleAspect  float64 `json:"sampleAspect"`
	Rotation      int     `json:"rotation"`
	VideoIndex    int     `json:"videoIndex"`
	AudioIndex    int     `json:"audioIndex"`
	AudioTracks   int     `json:"audioTracks"`
	VideoTracks   int     `json:"videoTracks"`
	MIME          string  `json:"mime"`
	MP4IndexBytes int64   `json:"mp4IndexBytes,omitempty"`
}

type Capabilities struct {
	H264           bool  `json:"h264"`
	HEVC           bool  `json:"hevc"`
	VP9            bool  `json:"vp9"`
	AV1            bool  `json:"av1"`
	AAC            bool  `json:"aac"`
	Opus           bool  `json:"opus"`
	MP4            bool  `json:"mp4"`
	WebM           bool  `json:"webm"`
	HDR            bool  `json:"hdr"`
	HEVC10         bool  `json:"hevc10"`
	VideoSupported *bool `json:"videoSupported,omitempty"`
	AudioSupported *bool `json:"audioSupported,omitempty"`
}

type Plan struct {
	Kind               string   `json:"kind"`
	Reason             string   `json:"reason"`
	MaxBytes           int64    `json:"-"`
	Info               Info     `json:"info"`
	Container          string   `json:"container"`
	MIME               string   `json:"mime"`
	CopyAudio          bool     `json:"copyAudio"`
	Warnings           []string `json:"warnings"`
	CanOptimizeStartup bool     `json:"canOptimizeStartup,omitempty"`
}

type Processor struct{ ffmpeg, ffprobe string }

func New() *Processor {
	p := &Processor{}
	exe, err := os.Executable()
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, dir := range []string{filepath.Dir(exe), filepath.Join(filepath.Dir(exe), "Helpers"), filepath.Join(filepath.Dir(exe), "..", "Helpers")} {
		ffmpeg, ffprobe := filepath.Join(dir, "ffmpeg"+suffix), filepath.Join(dir, "ffprobe"+suffix)
		if a, err := os.Stat(ffmpeg); err == nil && a.Mode().IsRegular() {
			if b, err := os.Stat(ffprobe); err == nil && b.Mode().IsRegular() {
				p.ffmpeg, p.ffprobe = ffmpeg, ffprobe
				break
			}
		}
	}
	return p
}

// Only self-contained media demuxers are allowed: playlists and concat inputs
// could cause a local file to read other files or access the network.
// The pinned MOV demuxer's enable_drefs and use_absolute_path defaults are
// false. Passing those private options globally makes other demuxers fail.
var inputOptions = []string{"-protocol_whitelist", "file", "-format_whitelist", "mov,matroska,webm,mpegts,mpeg,avi,flv,ogg,wav,flac,mp3,aac,asf,ac3,eac3"}

func localInput(path string) (string, os.FileInfo, error) {
	if !filepath.IsAbs(path) {
		return "", nil, ErrInvalid
	}
	st, err := os.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return "", nil, ErrInvalid
	}
	return path, st, nil
}

func unchanged(path string, before os.FileInfo) bool {
	after, err := os.Lstat(path)
	return err == nil && after.Mode().IsRegular() && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

// Probe accepts trusted completed local paths. Authorized roots use ProbeFile
// so a rename or path replacement cannot redirect the child to another file.
func (p *Processor) Probe(ctx context.Context, path string) (Info, error) {
	if p == nil || p.ffprobe == "" {
		return Info{}, ErrUnavailable
	}
	in, closeInput, err := pathInput(ctx, path)
	if err != nil {
		return Info{}, err
	}
	defer closeInput()
	return p.probe(ctx, in)
}

func (p *Processor) probe(ctx context.Context, in *mediaInput) (Info, error) {
	args := append([]string{"-v", "error"}, in.options...)
	args = append(args, "-show_format", "-show_streams", "-show_data", "-of", "json", in.location)
	cmd := exec.CommandContext(ctx, p.ffprobe, args...)
	configureMediaCommand(cmd)
	var stdout limitedBuffer
	stdout.max = 4 << 20
	cmd.Stdout = &stdout
	var stderr limitedBuffer
	stderr.max = 8192
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Info{}, ctx.Err()
		}
		return Info{}, fmt.Errorf("%w: probe failed", ErrInvalid)
	}
	if ctx.Err() != nil {
		return Info{}, ctx.Err()
	}
	if stdout.overflow || !in.valid() {
		return Info{}, ErrInvalid
	}
	info, err := parseProbe(stdout.data)
	if err != nil {
		return Info{}, err
	}
	if info.Container == "matroska" {
		header := make([]byte, 4096)
		n, _ := in.file.ReadAt(header, 0)
		if webmHeader(header[:n]) {
			info.Container = "webm"
		}
	}
	if info.Container == "mp4" {
		info.MP4IndexBytes, err = mp4IndexBytes(ctx, in.file, in.before.Size())
		if err != nil {
			return Info{}, err
		}
	}
	info.MIME = mediaMIME(info.Container, info.VideoCodec != "")
	if !in.valid() {
		if ctx.Err() != nil {
			return Info{}, ctx.Err()
		}
		return Info{}, ErrInvalid
	}
	return info, nil
}

type probeStream struct {
	Index        int    `json:"index"`
	Type         string `json:"codec_type"`
	Codec        string `json:"codec_name"`
	CodecTag     string `json:"codec_tag_string"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	PixelFormat  string `json:"pix_fmt"`
	Profile      string `json:"profile"`
	Level        int    `json:"level"`
	ExtraData    string `json:"extradata"`
	Bitrate      string `json:"bit_rate"`
	FrameRate    string `json:"avg_frame_rate"`
	SampleAspect string `json:"sample_aspect_ratio"`
	Bits         string `json:"bits_per_raw_sample"`
	Transfer     string `json:"color_transfer"`
	Primaries    string `json:"color_primaries"`
	Duration     string `json:"duration"`
	Disposition  struct {
		Default  int `json:"default"`
		Attached int `json:"attached_pic"`
	} `json:"disposition"`
	SideData []struct {
		Type     string `json:"side_data_type"`
		Rotation int    `json:"rotation"`
	} `json:"side_data_list"`
}

func parseProbe(data []byte) (Info, error) {
	var doc struct {
		Streams []probeStream `json:"streams"`
		Format  struct {
			Name     string            `json:"format_name"`
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return Info{}, ErrInvalid
	}
	info := Info{VideoIndex: -1, AudioIndex: -1}
	info.Duration, _ = strconv.ParseFloat(doc.Format.Duration, 64)
	name := doc.Format.Name
	switch {
	case strings.Contains(name, "mov"):
		info.Container = "mp4"
		// QuickTime is not evidence of MP4 browser support.
		if strings.TrimSpace(doc.Format.Tags["major_brand"]) == "qt" {
			info.Container = "mov"
		}
	case strings.Contains(name, "matroska"):
		info.Container = "matroska"
	default:
		info.Container = strings.Split(name, ",")[0]
	}
	videoDefault, audioDefault := false, false
	for _, s := range doc.Streams {
		if s.CodecTag == "encv" || s.CodecTag == "enca" {
			info.Encrypted = true
		}
		for _, side := range s.SideData {
			if strings.Contains(strings.ToLower(side.Type), "encryption") {
				info.Encrypted = true
			}
		}
		switch s.Type {
		case "subtitle":
			info.Subtitles = true
		case "video":
			if s.Disposition.Attached != 0 {
				continue
			}
			info.VideoTracks++
			if info.VideoIndex >= 0 && (videoDefault || s.Disposition.Default == 0) {
				continue
			}
			videoDefault = s.Disposition.Default != 0
			info.VideoIndex, info.VideoCodec, info.Width, info.Height = s.Index, s.Codec, s.Width, s.Height
			info.PixelFormat, info.Profile = s.PixelFormat, s.Profile
			info.Level = s.Level
			info.VideoBitrate, _ = strconv.ParseInt(s.Bitrate, 10, 64)
			info.FrameRate = fraction(s.FrameRate, "/")
			info.SampleAspect = fraction(s.SampleAspect, ":")
			if info.SampleAspect <= 0 {
				info.SampleAspect = 1
			}
			info.BitDepth, _ = strconv.Atoi(s.Bits)
			if info.BitDepth == 0 {
				info.BitDepth = 8
				for _, bits := range []int{9, 10, 12, 14, 16} {
					if strings.Contains(s.PixelFormat, strconv.Itoa(bits)) {
						info.BitDepth = bits
						break
					}
				}
			}
			info.VideoMIME = videoMIME(info, s.ExtraData)
			info.HDR = s.Transfer == "smpte2084" || s.Transfer == "arib-std-b67" || s.Primaries == "bt2020"
			for _, side := range s.SideData {
				if strings.Contains(side.Type, "DOVI") || strings.Contains(side.Type, "Mastering display") || strings.Contains(side.Type, "HDR") {
					info.HDR = true
				}
				if side.Type == "Display Matrix" {
					info.Rotation = side.Rotation
				}
			}
		case "audio":
			info.AudioTracks++
			if info.AudioIndex >= 0 && (audioDefault || s.Disposition.Default == 0) {
				continue
			}
			audioDefault = s.Disposition.Default != 0
			info.AudioIndex, info.AudioCodec = s.Index, s.Codec
			info.AudioProfile = s.Profile
			info.AudioMIME = ""
			switch s.Codec {
			case "aac":
				object := map[string]int{"LC": 2, "HE-AAC": 5, "HE-AACv2": 29}[s.Profile]
				if object != 0 {
					info.AudioMIME = fmt.Sprintf("audio/mp4; codecs=\"mp4a.40.%d\"", object)
				}
			case "opus":
				info.AudioMIME = "audio/webm; codecs=\"opus\""
			}
		}
	}
	if info.VideoIndex < 0 && info.AudioIndex < 0 || !finiteDuration(info.Duration) || info.VideoIndex >= 0 && (info.Width <= 0 || info.Height <= 0) {
		return Info{}, ErrInvalid
	}
	info.MIME = mediaMIME(info.Container, info.VideoCodec != "")
	return info, nil
}

func finiteDuration(n float64) bool { return n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }

func mediaMIME(container string, video bool) string {
	prefix := "audio/"
	if video {
		prefix = "video/"
	}
	switch container {
	case "mp4":
		return prefix + "mp4"
	case "webm":
		return prefix + "webm"
	}
	return "application/octet-stream"
}

func MakePlan(info Info, caps Capabilities) (Plan, error) {
	p := Plan{Info: info, Container: "mp4", MIME: mediaMIME("mp4", info.VideoCodec != ""), Warnings: []string{}}
	if info.Subtitles {
		p.Warnings = append(p.Warnings, "subtitles_unsupported")
	}
	if info.AudioTracks > 1 || info.VideoTracks > 1 {
		p.Warnings = append(p.Warnings, "extra_tracks_omitted")
	}
	unsupported := func(reason string) (Plan, error) { p.Kind, p.Reason = "unsupported", reason; return p, ErrUnsupported }
	if !finiteDuration(info.Duration) || info.VideoCodec == "" && info.AudioCodec == "" {
		return unsupported("invalid_media")
	}
	if info.Encrypted {
		return unsupported("invalid_media")
	}
	videoOK := info.VideoCodec == ""
	chromaOK := info.PixelFormat == "" || info.PixelFormat == "yuv420p" || info.PixelFormat == "yuvj420p" || info.PixelFormat == "yuv420p10le" || info.PixelFormat == "p010le"
	switch info.VideoCodec {
	case "h264":
		videoOK = caps.H264 && info.BitDepth <= 8 && chromaOK
	case "hevc":
		videoOK = caps.HEVC && chromaOK && (info.BitDepth <= 8 || info.BitDepth <= 10 && caps.HEVC10)
	case "vp9":
		videoOK = caps.VP9 && info.BitDepth <= 8 && chromaOK
	case "av1":
		videoOK = caps.AV1 && info.BitDepth <= 8 && chromaOK
	}
	audioOK := info.AudioCodec == "" || info.AudioCodec == "aac" && caps.AAC || info.AudioCodec == "opus" && caps.Opus
	if info.VideoCodec != "" && caps.VideoSupported != nil {
		videoOK = videoOK && *caps.VideoSupported
	}
	if info.AudioCodec != "" && caps.AudioSupported != nil {
		audioOK = audioOK && *caps.AudioSupported
	}
	if info.AudioCodec == "aac" && info.AudioProfile != "" && info.AudioProfile != "LC" && info.AudioProfile != "HE-AAC" && info.AudioProfile != "HE-AACv2" {
		audioOK = false
	}
	mp4Video := info.VideoCodec == "" || info.VideoCodec == "h264" || info.VideoCodec == "hevc" || info.VideoCodec == "av1"
	mp4Audio := info.AudioCodec == "" || info.AudioCodec == "aac"
	webmVideo := info.VideoCodec == "" || info.VideoCodec == "vp9" || info.VideoCodec == "av1"
	webmAudio := info.AudioCodec == "" || info.AudioCodec == "opus"
	containerOK := info.Container == "mp4" && caps.MP4 && mp4Video && mp4Audio || info.Container == "webm" && caps.WebM && webmVideo && webmAudio
	if info.HDR && !caps.HDR {
		return unsupported("hdr_unsupported")
	}
	if videoOK && audioOK && containerOK {
		p.Kind, p.Reason, p.Container, p.MIME = "direct", "compatible", info.Container, info.MIME
		// A large index is a startup cost, not an incompatibility.
		p.CanOptimizeStartup = info.Container == "mp4" && info.VideoCodec != "" && !info.HDR && info.VideoTracks <= 1 && info.AudioTracks <= 1 && !info.Subtitles && info.MP4IndexBytes > 4<<20
		return p, nil
	}
	// No validated HDR tone mapping or metadata preservation pipeline is bundled.
	if info.HDR {
		return unsupported("hdr_unsupported")
	}
	if videoOK && audioOK && caps.WebM && webmVideo && webmAudio {
		p.Kind, p.Reason, p.Container, p.MIME = "remux", "container_unsupported", "webm", mediaMIME("webm", info.VideoCodec != "")
		return p, nil
	}
	if !caps.MP4 {
		return unsupported("browser_unsupported")
	}
	if videoOK && mp4Video {
		if audioOK && mp4Audio {
			p.Kind, p.Reason = "remux", "container_unsupported"
			return p, nil
		}
		if caps.AAC {
			p.Kind, p.Reason = "audio", "audio_unsupported"
			return p, nil
		}
		return unsupported("browser_unsupported")
	}
	if !caps.H264 || info.AudioCodec != "" && !caps.AAC {
		return unsupported("browser_unsupported")
	}
	if info.Rotation%90 != 0 {
		return unsupported("video_unsupported")
	}
	p.Kind, p.Reason = "video", "video_unsupported"
	p.CopyAudio = audioOK && mp4Audio
	return p, nil
}

// MakeStartupPlan is opt-in: it never substitutes a lossy compatibility conversion.
func MakeStartupPlan(info Info, caps Capabilities) (Plan, error) {
	p, err := MakePlan(info, caps)
	if err != nil {
		return p, err
	}
	if !p.CanOptimizeStartup {
		return p, ErrUnsupported
	}
	p.Kind, p.Reason, p.CanOptimizeStartup = "remux", "startup_index", false
	return p, nil
}
