package capture

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func encoderName(c Config, engine string) string {
	codec := "h264"
	if c.Codec == "h265" {
		codec = "hevc"
	}
	switch engine {
	case "nvidia":
		return codec + "_nvenc"
	case "intel":
		return codec + "_qsv"
	default:
		if c.Codec == "h265" {
			return "libx265"
		}
		return "libx264"
	}
}

func encoderPixelFormat(engine string) string {
	if engine == "intel" {
		return "nv12"
	}
	return "yuv420p"
}

// Probe and capture share all output options, including framing and latency.
func encoderArgs(c Config, engine string) []string {
	args := []string{"-c:v", encoderName(c, engine), "-pix_fmt", encoderPixelFormat(engine), "-maxrate", fmt.Sprintf("%dk", c.MaxBitrate), "-bufsize", fmt.Sprintf("%dk", max(1, c.MaxBitrate/2)), "-g", strconv.Itoa(c.FPS), "-bf", "0"}
	quality := strconv.Itoa(qualityValue(c))
	switch engine {
	case "nvidia":
		args = append(args, "-preset", "p1", "-tune", "ull", "-rc", "vbr", "-b:v", "0", "-cq", quality, "-multipass", "qres", "-strict_gop", "1", "-rc-lookahead", "0", "-zerolatency", "1", "-delay", "0", "-aud", "1")
	case "intel":
		// FFmpeg chooses QVBR when global_quality, nonzero bitrate and a
		// different maxrate are present; plain ICQ would ignore the cap.
		args = append(args, "-b:v", fmt.Sprintf("%dk", max(1, c.MaxBitrate/2)), "-global_quality", quality, "-preset", "veryfast", "-async_depth", "1", "-look_ahead_depth", "0", "-aud", "1")
		if c.Codec != "h265" {
			args = append(args, "-look_ahead", "0", "-repeat_pps", "1")
		}
	default:
		args = append(args, "-crf", quality, "-preset", "ultrafast", "-tune", "zerolatency")
		if c.Codec == "h265" {
			args = append(args, "-x265-params", fmt.Sprintf("aud=1:repeat-headers=1:scenecut=0:pools=%d:frame-threads=1:log-level=error", max(1, min(8, runtime.NumCPU()))))
		} else {
			args = append(args, "-x264-params", "aud=1:repeat-headers=1:scenecut=0")
		}
	}
	format := "h264"
	if c.Codec == "h265" {
		format = "hevc"
	}
	// Repeat available codec headers at keyframes for fresh platform viewers.
	return append(args, "-bsf:v", "dump_extra=freq=keyframe", "-f", format, "pipe:1")
}

func selectEncoder(ctx context.Context, c Config, probe func(context.Context, Config, string) error, note func(string)) (string, error) {
	candidates := []string{"nvidia", "intel", "cpu"}
	var failures []string
	for _, engine := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := probe(ctx, c, engine); err == nil {
			return engine, nil
		} else {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			message := encoderName(c, engine) + " 不可用：" + err.Error()
			failures = append(failures, message)
			note(message)
		}
	}
	return "", fmt.Errorf("编码检测失败；请检查显卡驱动及 FFmpeg 是否支持硬件或 CPU 编码。%s", strings.Join(failures, "；"))
}

// Encode actual-size synthetic frames without opening the camera. Listing
// compiled encoders alone does not verify driver/device access (e.g. a VM).
func probeEncoder(ctx context.Context, c Config, engine string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d", c.Width, c.Height, c.FPS), "-frames:v", "3", "-an", "-color_range", "tv"}
	args = append(args, encoderArgs(c, engine)...)
	cmd := exec.CommandContext(ctx, c.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	frames := 0
	parseErr := readFramesCodec(stdout, c.Codec == "h265", func(frame []byte) error {
		if err := validateEncoderFrame(frame, c.Codec == "h265", frames == 0); err != nil {
			return err
		}
		frames++
		return nil
	})
	if parseErr != nil {
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 700 {
			msg = msg[:700]
		}
		return fmt.Errorf("%v %s", err, msg)
	}
	if parseErr != nil {
		return parseErr
	}
	if frames != 3 {
		return fmt.Errorf("期望 3 帧，实际 %d 帧；无法确认帧边界", frames)
	}
	return nil
}

func validateEncoderFrame(frame []byte, hevc, first bool) error {
	s := bufio.NewScanner(bytes.NewReader(frame))
	s.Buffer(make([]byte, 65536), 4<<20)
	s.Split(splitNAL)
	seen := map[byte]bool{}
	for s.Scan() {
		n := s.Bytes()
		if len(n) == 0 {
			continue
		}
		kind := n[0] & 31
		if hevc {
			kind = n[0] >> 1 & 63
		}
		seen[kind] = true
	}
	if err := s.Err(); err != nil {
		return err
	}
	aud := byte(9)
	headers := []byte{7, 8}
	if hevc {
		aud, headers = 35, []byte{32, 33, 34}
	}
	if !seen[aud] {
		return errors.New("编码输出缺少 AUD 帧边界")
	}
	if first {
		for _, kind := range headers {
			if !seen[kind] {
				return errors.New("编码输出缺少首帧参数集")
			}
		}
	}
	return nil
}
