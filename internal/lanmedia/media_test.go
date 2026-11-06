package lanmedia

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMakePlanLowestCost(t *testing.T) {
	base := Info{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 3840, Height: 2160, Duration: 60, BitDepth: 8, PixelFormat: "yuv420p", MIME: "video/mp4"}
	caps := Capabilities{H264: true, AAC: true, MP4: true, VP9: true, Opus: true, WebM: true}
	cases := []struct {
		name   string
		change func(*Info, *Capabilities)
		kind   string
	}{
		{"compatible 4K remains original", func(i *Info, c *Capabilities) {}, "direct"},
		{"container only", func(i *Info, c *Capabilities) { i.Container = "matroska" }, "remux"},
		{"audio only", func(i *Info, c *Capabilities) { i.AudioCodec = "dts" }, "audio"},
		{"unsupported video", func(i *Info, c *Capabilities) { i.VideoCodec = "mpeg4" }, "video"},
		{"high bitdepth h264", func(i *Info, c *Capabilities) { i.BitDepth = 10 }, "video"},
		{"chroma unsupported", func(i *Info, c *Capabilities) { i.PixelFormat = "yuv444p" }, "video"},
		{"HDR not claimed", func(i *Info, c *Capabilities) { i.HDR = true }, "unsupported"},
		{"HDR transformation refused", func(i *Info, c *Capabilities) { i.HDR = true; i.Container = "matroska"; c.HDR = true }, "unsupported"},
		{"HDR direct explicitly supported", func(i *Info, c *Capabilities) { i.HDR = true; c.HDR = true }, "direct"},
		{"no browser encoder target", func(i *Info, c *Capabilities) { i.Container = "avi"; c.MP4 = false }, "unsupported"},
		{"webm direct", func(i *Info, c *Capabilities) { i.Container = "webm"; i.VideoCodec = "vp9"; i.AudioCodec = "opus" }, "direct"},
		{"webm remux", func(i *Info, c *Capabilities) { i.Container = "matroska"; i.VideoCodec = "vp9"; i.AudioCodec = "opus" }, "remux"},
		{"actual source parameters unsupported", func(i *Info, c *Capabilities) { supported := false; c.VideoSupported = &supported }, "video"},
		{"audio file", func(i *Info, c *Capabilities) { i.VideoCodec = ""; i.AudioCodec = "flac"; i.Container = "flac" }, "audio"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			i, c := base, caps
			tt.change(&i, &c)
			plan, err := MakePlan(i, c)
			if plan.Kind != tt.kind {
				t.Fatalf("kind=%s want=%s, err=%v", plan.Kind, tt.kind, err)
			}
			if (err != nil) != (tt.kind == "unsupported") {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
	base.Subtitles, base.AudioTracks = true, 2
	p, _ := MakePlan(base, caps)
	if len(p.Warnings) != 2 {
		t.Fatalf("missing subtitle/track warnings: %+v", p)
	}
}

func TestOutputSize(t *testing.T) {
	for _, tc := range [][4]int{{3840, 2160, 1920, 1080}, {2160, 3840, 1080, 1920}, {640, 360, 640, 360}, {641, 361, 640, 360}, {4000, 2000, 1920, 960}} {
		w, h := outputSize(tc[0], tc[1])
		if w != tc[2] || h != tc[3] {
			t.Fatalf("%v => %d,%d", tc, w, h)
		}
	}
	w, h := displaySize(Info{Width: 1920, Height: 1080, SampleAspect: 1, Rotation: 90})
	if w != 1080 || h != 1920 {
		t.Fatalf("rotation: %dx%d", w, h)
	}
}

func TestProbeMetadataAndCodec(t *testing.T) {
	data := []byte(`{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"10"},"streams":[{"index":0,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}},{"index":1,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"pix_fmt":"yuv420p","profile":"High","level":40,"extradata":"\n00000000: 0164 0028 ffe1  a\n"},{"index":2,"codec_type":"audio","codec_name":"aac","profile":"LC"},{"index":3,"codec_type":"subtitle","codec_name":"ass"}]}`)
	i, err := parseProbe(data)
	if err != nil {
		t.Fatal(err)
	}
	if i.VideoIndex != 1 || i.AudioIndex != 2 || !i.Subtitles || i.VideoMIME != `video/mp4; codecs="avc1.640028"` {
		t.Fatalf("bad probe: %+v", i)
	}
	if _, err := parseProbe([]byte(`{"format":{"duration":"NaN"}}`)); err == nil {
		t.Fatal("invalid duration accepted")
	}
	if !webmHeader([]byte{0x1a, 0x45, 0xdf, 0xa3, 0x87, 0x42, 0x82, 0x84, 'w', 'e', 'b', 'm'}) {
		t.Fatal("WebM not detected")
	}
	if webmHeader([]byte("not a WebM file")) {
		t.Fatal("invalid header accepted")
	}
}

func TestConversionArguments(t *testing.T) {
	i := Info{VideoCodec: "h264", AudioCodec: "dts", VideoIndex: 1, AudioIndex: 2, Width: 3840, Height: 2160}
	args, err := conversionArgs("/private/input", "/private/output", Plan{Kind: "audio", Info: i, MaxBytes: 100000}, inputOptions)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Join(args, " ")
	if strings.Contains(s, "-vf") || !strings.Contains(s, "-c copy") || !strings.Contains(s, "-c:a aac") || !strings.Contains(s, "-fs 100000") || !strings.Contains(s, "-map 0:1") {
		t.Fatal(s)
	}
	i.HDR = true
	if _, err := conversionArgs("/in", "/out", Plan{Kind: "video", Info: i}, inputOptions); !errors.Is(err, ErrUnsupported) {
		t.Fatal("HDR conversion accepted")
	}
}

// Explicit opt-in: point only at the freshly built bundled tools. This smoke
// exercises real native encoding, probe, publication, preservation and cancel.
func TestBundledMediaSmoke(t *testing.T) {
	dir := os.Getenv("PKP_MEDIA_SMOKE_DIR")
	if dir == "" {
		t.Skip("set PKP_MEDIA_SMOKE_DIR to bundled tools directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	p := &Processor{ffmpeg: filepath.Join(dir, "ffmpeg"+suffix), ffprobe: filepath.Join(dir, "ffprobe"+suffix)}
	root := t.TempDir()
	input, output := filepath.Join(root, "input.avi"), filepath.Join(root, "output.mp4")
	raw := filepath.Join(root, "frames.yuv")
	frame := make([]byte, 320*240*3/2)
	for j := range frame {
		frame[j] = 128
	}
	f, err := os.Create(raw)
	if err != nil {
		t.Fatal(err)
	}
	for range 24 {
		if _, err := f.Write(frame); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(p.ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "rawvideo", "-pixel_format", "yuv420p", "-video_size", "320x240", "-framerate", "24", "-i", raw, "-c:v", "mpeg4", input)
	configureCommand(cmd)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, b)
	}
	pinned, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	i, err := p.ProbeFile(context.Background(), pinned)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := MakePlan(i, Capabilities{H264: true, AAC: true, MP4: true})
	if err != nil || plan.Kind != "video" {
		t.Fatalf("plan %+v: %v", plan, err)
	}
	if err := p.ConvertFile(context.Background(), pinned, output, plan, nil); err != nil {
		t.Fatal(err)
	}
	out, err := p.Probe(context.Background(), output)
	if err != nil || out.VideoCodec != "h264" || out.Width != 320 || out.Height != 240 {
		t.Fatalf("output %+v: %v", out, err)
	}
	if err := p.Convert(context.Background(), input, output, plan, nil); err == nil {
		t.Fatal("existing output overwritten")
	}
	for _, kind := range []string{"remux", "audio"} {
		t.Run(kind, func(t *testing.T) {
			source, result := filepath.Join(root, kind+".mkv"), filepath.Join(root, kind+".mp4")
			args := []string{"-hide_banner", "-loglevel", "error", "-i", output}
			if kind == "audio" {
				pcm := filepath.Join(root, "silence.pcm")
				if err := os.WriteFile(pcm, make([]byte, 48000*2), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-f", "s16le", "-ar", "48000", "-ac", "1", "-i", pcm, "-c:a", "pcm_s16le")
			}
			args = append(args, "-c:v", "copy", source)
			cmd := exec.Command(p.ffmpeg, args...)
			configureCommand(cmd)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v: %s", err, b)
			}
			info, err := p.Probe(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := MakePlan(info, Capabilities{H264: true, AAC: true, MP4: true})
			if err != nil || plan.Kind != kind {
				t.Fatalf("plan %+v: %v", plan, err)
			}
			if err := p.Convert(context.Background(), source, result, plan, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := filepath.Join(root, "cancelled.mp4")
	if err := p.Convert(ctx, input, cancelled, plan, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := os.Stat(cancelled); !os.IsNotExist(err) {
		t.Fatal("cancelled output published")
	}
	limited := filepath.Join(root, "limited.mp4")
	plan.MaxBytes = 256
	if err := p.Convert(context.Background(), input, limited, plan, nil); err == nil {
		t.Fatal("truncated output accepted")
	}
	if _, err := os.Stat(limited); !os.IsNotExist(err) {
		t.Fatal("limited output published")
	}
	playlist := filepath.Join(root, "hostile.m3u8")
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nfile:///etc/passwd\n#EXT-X-ENDLIST\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Probe(context.Background(), playlist); err == nil {
		t.Fatal("playlist input accepted")
	}
	hostile, err := os.Open(playlist)
	if err != nil {
		t.Fatal(err)
	}
	defer hostile.Close()
	if _, err := p.ProbeFile(context.Background(), hostile); err == nil {
		t.Fatal("playlist accepted through pinned HTTP input")
	}
}
