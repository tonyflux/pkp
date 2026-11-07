package lanmedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type sparseIndexFixture struct {
	moov  int64
	size  int64
	reads int
}

func (f *sparseIndexFixture) ReadAt(p []byte, offset int64) (int, error) {
	f.reads++
	clear(p)
	if offset == 0 {
		binary.BigEndian.PutUint32(p, uint32(f.moov))
		copy(p[4:], "mdat")
	} else if offset == f.moov {
		binary.BigEndian.PutUint32(p, 24)
		copy(p[4:], "moov")
	} else {
		return 0, io.EOF
	}
	return len(p), nil
}
func TestMP4IndexSkipsMediaAndRejectsBrokenLengths(t *testing.T) {
	fixture := &sparseIndexFixture{moov: 600_000_000, size: 600_000_024}
	index, err := mp4IndexBytes(context.Background(), fixture, fixture.size)
	if err != nil || index != 24 || fixture.reads != 2 {
		t.Fatalf("index=%d reads=%d err=%v", index, fixture.reads, err)
	}
	for _, length := range []uint32{2, 17, 0xffffffff} {
		data := make([]byte, 16)
		binary.BigEndian.PutUint32(data, length)
		copy(data[4:], "moov")
		if got, err := mp4IndexBytes(context.Background(), bytes.NewReader(data), int64(len(data))); got != 0 || err != nil {
			t.Fatalf("broken box published length %d: %d %v", length, got, err)
		}
	}
	data := make([]byte, 24)
	binary.BigEndian.PutUint32(data, 1)
	copy(data[4:], "moov")
	binary.BigEndian.PutUint64(data[8:], 24)
	if got, err := mp4IndexBytes(context.Background(), bytes.NewReader(data), int64(len(data))); got != 24 || err != nil {
		t.Fatalf("64-bit moov: %d %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mp4IndexBytes(ctx, fixture, fixture.size); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLargeMP4IndexOptimizationIsExplicitAndLossless(t *testing.T) {
	base := Info{Container: "mp4", MIME: "video/mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1280, Height: 720, Duration: 6000, BitDepth: 8, PixelFormat: "yuv420p", MP4IndexBytes: 4 << 20}
	caps := Capabilities{H264: true, AAC: true, MP4: true}
	plan, err := MakePlan(base, caps)
	if err != nil || plan.Kind != "direct" || plan.CanOptimizeStartup {
		t.Fatalf("ordinary index: %+v %v", plan, err)
	}
	base.MP4IndexBytes++
	plan, err = MakePlan(base, caps)
	if err != nil || plan.Kind != "direct" || !plan.CanOptimizeStartup {
		t.Fatalf("large index blocked direct playback: %+v %v", plan, err)
	}
	plan, err = MakeStartupPlan(base, caps)
	if err != nil || plan.Kind != "remux" || plan.Reason != "startup_index" {
		t.Fatalf("explicit lossless optimization: %+v %v", plan, err)
	}
	unsupported := caps
	rejected := false
	unsupported.VideoSupported = &rejected
	if _, err = MakeStartupPlan(base, unsupported); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported codec entered lossless optimization: %v", err)
	}
	for _, change := range []func(*Info){func(i *Info) { i.VideoTracks = 2 }, func(i *Info) { i.AudioTracks = 2 }, func(i *Info) { i.Subtitles = true }, func(i *Info) { i.HDR = true }} {
		multiple := base
		change(&multiple)
		compatible := caps
		compatible.HDR = true
		plan, err = MakePlan(multiple, compatible)
		if err != nil || plan.Kind != "direct" || plan.CanOptimizeStartup {
			t.Fatalf("safe direct source incorrectly needs preparation: %+v %v", plan, err)
		}
		if _, err = MakeStartupPlan(multiple, compatible); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("optimization must not omit tracks or HDR: %v", err)
		}
	}
}
