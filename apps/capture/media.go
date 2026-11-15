package capture

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func command(ctx context.Context, c Config, engine string) *exec.Cmd {
	args := []string{"-hide_banner", "-loglevel", "warning", "-nostdin"}
	inputFPS := c.inputFPS
	if inputFPS == 0 {
		inputFPS = c.FPS
	}
	if runtime.GOOS == "windows" {
		args = append(args, "-f", "dshow", "-rtbufsize", "64M", "-vcodec", "mjpeg", "-video_size", fmt.Sprintf("%dx%d", c.Width, c.Height), "-framerate", strconv.Itoa(inputFPS), "-i", "video="+c.Camera)
	} else {
		args = append(args, "-f", "v4l2", "-input_format", "mjpeg", "-video_size", fmt.Sprintf("%dx%d", c.Width, c.Height), "-framerate", strconv.Itoa(c.FPS), "-i", c.Camera)
	}
	// MJPEG uses JPEG/full range. Convert samples as well as setting the output
	// metadata; changing the range flag alone would make levels incorrect.
	args = append(args, "-an", "-r", strconv.Itoa(c.FPS), "-vf", "scale=in_range=full:out_range=limited,format="+encoderPixelFormat(engine), "-color_range", "tv")
	args = append(args, encoderArgs(c, engine)...)
	return exec.CommandContext(ctx, c.FFmpeg, args...)
}

// splitNAL accepts both Annex-B prefixes. Scanner bounds prevent unbounded memory.
func splitNAL(data []byte, eof bool) (int, []byte, error) {
	start := bytes.Index(data, []byte{0, 0, 1})
	if start < 0 {
		if eof {
			return len(data), nil, nil
		}
		return 0, nil, nil
	}
	next := bytes.Index(data[start+3:], []byte{0, 0, 1})
	if next >= 0 {
		end := start + 3 + next
		return end, bytes.TrimRight(data[start+3:end], "\x00"), nil
	}
	if eof {
		return len(data), bytes.TrimRight(data[start+3:], "\x00"), nil
	}
	return 0, nil, nil
}
func readFrames(r io.Reader, send func([]byte) error) error {
	return readFramesCodec(r, false, send)
}
func readFramesCodec(r io.Reader, hevc bool, send func([]byte) error) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 65536), 4<<20)
	s.Split(splitNAL)
	var frame []byte
	for s.Scan() {
		n := s.Bytes()
		if len(n) == 0 {
			continue
		}
		aud := n[0]&31 == 9
		if hevc {
			aud = n[0]>>1&63 == 35
		}
		if aud && len(frame) > 0 {
			if err := send(frame); err != nil {
				return err
			}
			frame = nil
		}
		if len(frame)+len(n) > 8<<20 {
			return errors.New("视频帧超过 8 MiB")
		}
		frame = append(frame, 0, 0, 0, 1)
		frame = append(frame, n...)
	}
	if err := s.Err(); err != nil {
		return err
	}
	if len(frame) > 0 {
		return send(frame)
	}
	return nil
}

type bitWriter struct {
	data []byte
	n    int
}

func (b *bitWriter) put(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		if b.n%8 == 0 {
			b.data = append(b.data, 0)
		}
		b.data[b.n/8] |= byte((v>>i)&1) << uint(7-b.n%8)
		b.n++
	}
}
func crcMPEG(data []byte) uint32 {
	crc := uint32(0xffffffff)
	for _, v := range data {
		crc ^= uint32(v) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
func programStream(frame []byte, pts uint64) []byte {
	return programStreamCodec(frame, pts, 0x1b)
}
func programStreamCodec(frame []byte, pts uint64, codec byte) []byte {
	pts &= (1 << 33) - 1
	b := bitWriter{data: []byte{0, 0, 1, 0xba}, n: 32}
	b.put(1, 2)
	b.put(pts>>30, 3)
	b.put(1, 1)
	b.put(pts>>15, 15)
	b.put(1, 1)
	b.put(pts, 15)
	b.put(1, 1)
	b.put(0, 9)
	b.put(1, 1)
	b.put(10000, 22)
	b.put(3, 2)
	b.put(31, 5)
	b.put(0, 3)
	out := b.data
	// System header declares a single bounded video stream.
	out = append(out, 0, 0, 1, 0xbb, 0, 9, 0x80, 0x4e, 0x21, 0, 0xe1, 0x7f, 0xe0, 0xe2, 0)
	psm := []byte{0, 0, 1, 0xbc, 0, 14, 0xe0, 0xff, 0, 0, 0, 4, codec, 0xe0, 0, 0}
	crc := crcMPEG(psm)
	psm = binary.BigEndian.AppendUint32(psm, crc)
	out = append(out, psm...)
	for len(frame) > 0 {
		n := min(len(frame), 65000)
		pes := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5, byte(0x21 | (pts>>29)&14), byte(pts >> 22), byte(1 | (pts>>14)&254), byte(pts >> 7), byte(1 | (pts<<1)&254)}
		binary.BigEndian.PutUint16(pes[4:6], uint16(n+8))
		out = append(out, pes...)
		out = append(out, frame[:n]...)
		frame = frame[n:]
	}
	return out
}

type offer struct {
	IP   string
	Port int
	SSRC uint32
	PT   byte
}

func parseOffer(body string) (offer, error) {
	var o offer
	validPS := false
	var offered []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		switch {
		case strings.HasPrefix(line, "c=IN IP4 "):
			o.IP = strings.TrimSpace(strings.TrimPrefix(line, "c=IN IP4 "))
		case strings.HasPrefix(line, "m=video "):
			f := strings.Fields(strings.TrimPrefix(line, "m="))
			if len(f) < 4 || f[2] != "RTP/AVP" {
				return o, errors.New("仅支持 UDP 视频")
			}
			o.Port, _ = strconv.Atoi(f[1])
			offered = f[3:]
		case strings.HasPrefix(line, "a=rtpmap:"):
			f := strings.Fields(strings.TrimPrefix(line, "a=rtpmap:"))
			if len(f) == 2 && strings.EqualFold(f[1], "PS/90000") {
				for _, v := range offered {
					if v == f[0] {
						p, e := strconv.ParseUint(v, 10, 7)
						if e == nil {
							o.PT = byte(p)
							validPS = true
						}
					}
				}
			}
		case strings.HasPrefix(line, "y="):
			n, e := strconv.ParseUint(strings.TrimSpace(line[2:]), 10, 32)
			if e != nil {
				return o, errors.New("无效 SSRC")
			}
			o.SSRC = uint32(n)
		case line == "s=Playback" || strings.HasPrefix(line, "m=audio"):
			return o, errors.New("首版仅支持实时视频")
		}
	}
	ip := net.ParseIP(o.IP)
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() || o.Port < 1 || o.Port > 65535 || !validPS || o.SSRC == 0 {
		return o, errors.New("无效 SDP 视频目标、PS 格式或 SSRC")
	}
	return o, nil
}
func stream(ctx context.Context, c Config, o offer, frameSent func(int), selected func(string), note func(string)) error {
	engine, err := selectEncoder(ctx, c, probeEncoder, note)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		modes, err := cameraModes(ctx, c)
		if err != nil {
			return err
		}
		for _, mode := range modes {
			if mode.Width == c.Width && mode.Height == c.Height {
				c.inputFPS = max(c.inputFPS, mode.FPS)
			}
		}
		if c.inputFPS == 0 {
			return errors.New("摄像头不支持所选 MJPEG 分辨率，请检测视频能力后重新选择")
		}
		if c.FPS > c.inputFPS {
			return fmt.Errorf("所选分辨率最大 %d fps，请降低输出帧率", c.inputFPS)
		}
	}
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(o.IP), Port: o.Port})
	if err != nil {
		return err
	}
	defer conn.Close()
	cmd := command(ctx, c, engine)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	selected(encoderName(c, engine))
	note("正在使用编码器：" + encoderName(c, engine))
	go func() {
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			note(s.Text())
		}
	}()
	var seq uint16
	var count uint64
	err = readFramesCodec(stdout, c.Codec == "h265", func(frame []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pts := count * 90000 / uint64(c.FPS)
		codec := byte(0x1b)
		if c.Codec == "h265" {
			codec = 0x24
		}
		ps := programStreamCodec(frame, pts, codec)
		for at := 0; at < len(ps); {
			n := min(1200, len(ps)-at)
			packet := make([]byte, 12+n)
			packet[0] = 0x80
			packet[1] = o.PT
			if at+n == len(ps) {
				packet[1] |= 0x80
			}
			binary.BigEndian.PutUint16(packet[2:4], seq)
			binary.BigEndian.PutUint32(packet[4:8], uint32(pts))
			binary.BigEndian.PutUint32(packet[8:12], o.SSRC)
			copy(packet[12:], ps[at:at+n])
			conn.SetWriteDeadline(time.Now().Add(time.Second))
			if _, e := conn.Write(packet); e != nil {
				return e
			}
			seq++
			at += n
		}
		count++
		frameSent(len(ps))
		return nil
	})
	// A parser/socket error must also terminate FFmpeg before waiting.
	if err != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err != nil {
		return err
	}
	return waitErr
}

type CameraMode struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	FPS    int `json:"fps"`
}

var modePattern = regexp.MustCompile(`vcodec=mjpeg\s+min s=(\d+)x(\d+) fps=([0-9.]+) max s=(\d+)x(\d+) fps=([0-9.]+)`)

func parseCameraModes(text string) []CameraMode {
	out := []CameraMode{}
	seen := map[CameraMode]bool{}
	for _, m := range modePattern.FindAllStringSubmatch(text, -1) {
		w, _ := strconv.Atoi(m[4])
		h, _ := strconv.Atoi(m[5])
		f, _ := strconv.ParseFloat(m[6], 64)
		mode := CameraMode{w, h, int(f)}
		if !seen[mode] {
			out = append(out, mode)
			seen[mode] = true
		}
	}
	return out
}
func cameraModes(ctx context.Context, c Config) ([]CameraMode, error) {
	if runtime.GOOS != "windows" {
		return []CameraMode{}, nil
	}
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(timeout, c.FFmpeg, "-hide_banner", "-list_options", "true", "-f", "dshow", "-i", "video="+c.Camera).CombinedOutput()
	modes := parseCameraModes(string(b))
	if timeout.Err() != nil {
		return nil, timeout.Err()
	}
	if len(modes) == 0 {
		return nil, fmt.Errorf("未检测到 MJPEG 采集模式，请检查设备与 FFmpeg：%v", err)
	}
	return modes, nil
}
