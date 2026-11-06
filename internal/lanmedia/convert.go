package lanmedia

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type limitedBuffer struct {
	data     []byte
	max      int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > b.max-len(b.data) {
		p = p[:b.max-len(b.data)]
		b.overflow = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

func outputSize(width, height int) (int, int) {
	long, short := 1920.0, 1080.0
	if height > width {
		long, short = short, long
	}
	ratio := math.Min(1, math.Min(long/float64(width), short/float64(height)))
	return int(float64(width)*ratio) / 2 * 2, int(float64(height)*ratio) / 2 * 2
}

func conversionArgs(input, output string, p Plan, options []string) ([]string, error) {
	if p.Kind != "remux" && p.Kind != "audio" && p.Kind != "video" || p.Info.HDR {
		return nil, ErrUnsupported
	}
	if p.Info.Encrypted {
		return nil, ErrUnsupported
	}
	if p.Container == "" {
		p.Container = "mp4"
	}
	if p.Container != "mp4" && (p.Container != "webm" || p.Kind != "remux") {
		return nil, ErrUnsupported
	}
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-xerror", "-y"}, options...)
	args = append(args, "-i", input, "-map_metadata", "-1", "-map_chapters", "-1", "-sn", "-dn")
	if p.Info.VideoCodec != "" {
		args = append(args, "-map", fmt.Sprintf("0:%d", p.Info.VideoIndex))
	}
	if p.Info.AudioCodec != "" {
		args = append(args, "-map", fmt.Sprintf("0:%d", p.Info.AudioIndex))
	}
	args = append(args, "-c", "copy")
	if p.Kind == "video" {
		w, h := displaySize(p.Info)
		if w < 2 || h < 2 {
			return nil, ErrInvalid
		}
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d,setsar=1,format=yuv420p", w, h))
		switch runtime.GOOS {
		case "darwin":
			args = append(args, "-c:v", "h264_videotoolbox", "-allow_sw", "1")
		case "windows":
			args = append(args, "-c:v", "h264_mf", "-hw_encoding", "0")
		default:
			return nil, ErrUnavailable
		}
		args = append(args, "-b:v", "5000k", "-maxrate", "7500k", "-bufsize", "15000k", "-pix_fmt", "yuv420p", "-tag:v", "avc1")
	} else if p.Info.VideoCodec == "hevc" {
		args = append(args, "-tag:v", "hvc1")
	}
	if p.Info.AudioCodec != "" && (p.Kind == "audio" || p.Kind == "video" && !p.CopyAudio) {
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	}
	if p.MaxBytes > 0 {
		args = append(args, "-fs", strconv.FormatInt(p.MaxBytes, 10))
	}
	if p.Container == "mp4" {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, "-progress", "pipe:1", "-nostats", "-f", p.Container, output)
	return args, nil
}

// Convert never overwrites an existing completed result. Temporary output stays
// private until the encoder, metadata validation and full decode all succeed.
func (p *Processor) Convert(ctx context.Context, input, output string, plan Plan, progress func(seconds float64)) error {
	if p == nil || p.ffmpeg == "" || p.ffprobe == "" {
		return ErrUnavailable
	}
	in, closeInput, err := pathInput(ctx, input)
	if err != nil {
		return err
	}
	defer closeInput()
	if filepath.Clean(input) == filepath.Clean(output) {
		return ErrInvalid
	}
	return p.convert(ctx, in, output, plan, progress)
}

func (p *Processor) convert(ctx context.Context, in *mediaInput, output string, plan Plan, progress func(float64)) error {
	if !filepath.IsAbs(output) {
		return ErrInvalid
	}
	if _, err := os.Lstat(output); err == nil {
		return fmt.Errorf("output already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	actual, err := p.probe(ctx, in)
	if err != nil {
		return err
	}
	if actual != plan.Info {
		return fmt.Errorf("%w: input changed", ErrInvalid)
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".lan-media-*.mp4")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Close(); err != nil {
		return err
	}
	args, err := conversionArgs(in.location, tmpName, plan, in.options)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, p.ffmpeg, args...)
	configureMediaCommand(cmd)
	var stderr limitedBuffer
	stderr.max = 8192
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	ended := false
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		if key == "progress" && value == "end" {
			ended = true
		}
		if key == "out_time_us" && progress != nil {
			micros, err := strconv.ParseInt(value, 10, 64)
			if err == nil && micros >= 0 {
				progress(math.Min(float64(micros)/1e6, actual.Duration))
			}
		}
	}
	if scanner.Err() != nil {
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || scanner.Err() != nil || !ended {
		return fmt.Errorf("%w: conversion failed: %s", ErrUnsupported, strings.TrimSpace(strings.ReplaceAll(string(stderr.data), in.location, "[input]")))
	}
	if !in.valid() {
		return fmt.Errorf("%w: input changed", ErrInvalid)
	}
	if plan.MaxBytes > 0 {
		st, err := os.Stat(tmpName)
		if err != nil {
			return err
		}
		if st.Size() >= plan.MaxBytes {
			return fmt.Errorf("disk_full: output limit reached")
		}
	}
	result, err := p.Probe(ctx, tmpName)
	if err != nil {
		return err
	}
	container := plan.Container
	if container == "" {
		container = "mp4"
	}
	if result.Container != container || math.Abs(result.Duration-actual.Duration) > math.Max(0.25, math.Min(1, actual.Duration*0.001)) || (actual.AudioCodec != "") != (result.AudioCodec != "") || (actual.VideoCodec != "") != (result.VideoCodec != "") {
		return fmt.Errorf("%w: incomplete output", ErrInvalid)
	}
	if plan.Kind == "video" {
		w, h := displaySize(actual)
		if result.VideoCodec != "h264" || result.Width != w || result.Height != h || result.HDR {
			return fmt.Errorf("%w: unexpected video output", ErrInvalid)
		}
	} else if result.VideoCodec != actual.VideoCodec || result.Width != actual.Width || result.Height != actual.Height {
		return fmt.Errorf("%w: unexpected copied video", ErrInvalid)
	}
	if (plan.Kind == "audio" || plan.Kind == "video") && actual.AudioCodec != "" && result.AudioCodec != "aac" {
		return fmt.Errorf("%w: unexpected audio output", ErrInvalid)
	}
	if (plan.Kind == "remux" || plan.Kind == "video" && plan.CopyAudio) && result.AudioCodec != actual.AudioCodec {
		return fmt.Errorf("%w: unexpected copied audio", ErrInvalid)
	}
	// A readable header is not a complete playable output: decode every selected packet.
	checkArgs := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-xerror"}, inputOptions...)
	checkArgs = append(checkArgs, "-i", tmpName, "-map", "0:v?", "-map", "0:a?", "-f", "null", "-")
	check := exec.CommandContext(ctx, p.ffmpeg, checkArgs...)
	configureMediaCommand(check)
	check.Stdout, check.Stderr = io.Discard, io.Discard
	if err := check.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: output decode failed", ErrInvalid)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !in.valid() {
		return fmt.Errorf("%w: input changed", ErrInvalid)
	}
	f, err := os.OpenFile(tmpName, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Link publishes atomically without replacing a valid previous result.
	if err := os.Link(tmpName, output); err != nil {
		return err
	}
	if ctx.Err() != nil {
		_ = os.Remove(output)
		return ctx.Err()
	}
	return nil
}
