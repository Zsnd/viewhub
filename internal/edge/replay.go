package edge

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/pion/rtp"
)

type replaySource struct {
	raw, clock string
	strict     bool
	end        int64
}

func (e *edge) locateReplay(ctx context.Context, c channel, at int64) (replaySource, int, string) {
	loc, _ := time.LoadLocation(c.Timezone)
	requested := time.Unix(at, 0).In(loc)
	start, end, _ := e.day(c, requested.Format("2006-01-02"))
	rows, _, err := e.getRecordings(ctx, c, start, end)
	if err != nil {
		return replaySource{}, 502, "未能读取录像目录"
	}
	var found *interval
	for _, row := range rows {
		if at >= row.Start && at < row.End {
			found = &row
			break
		}
	}
	if found == nil {
		return replaySource{}, 404, "该时刻没有录像"
	}
	stop := time.Unix(found.End, 0).UTC()
	var raw, username, password string
	strict := false
	if found.ProfileG {
		config := c.recordingONVIF()
		raw, err = config.replayURI(ctx)
		username, password = config.Username, config.Password
		strict = config.StrictHost
	} else {
		raw = found.URI
		username, password = c.Hikvision.Username, c.Hikvision.Password
	}
	if err != nil {
		return replaySource{}, 502, "录像机未返回回放地址"
	}
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" || (target.Scheme != "rtsp" && target.Scheme != "rtsps") {
		return replaySource{}, 502, "录像回放地址无效"
	}
	if username != "" {
		target.User = url.UserPassword(username, password)
	}
	rangeHeader := ""
	if found.ProfileG {
		rangeHeader = "clock=" + requested.UTC().Format("20060102T150405Z") + "-" + stop.Format("20060102T150405Z")
	} else {
		q := target.Query()
		for key := range q {
			if strings.EqualFold(key, "starttime") || strings.EqualFold(key, "endtime") {
				q.Del(key)
			}
		}
		q.Set("starttime", requested.UTC().Format("20060102T150405Z"))
		q.Set("endtime", stop.Format("20060102T150405Z"))
		target.RawQuery = q.Encode()
	}
	return replaySource{target.String(), rangeHeader, strict, stop.Unix()}, 0, ""
}

func sendReplayMP4(ctx context.Context, source replaySource, w http.ResponseWriter) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	input, output := io.Pipe()
	stopPipe := context.AfterFunc(ctx, func() { _ = output.CloseWithError(ctx.Err()) })
	defer stopPipe()
	defer input.Close()
	defer output.Close()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-probesize", "32768", "-analyzeduration", "300000", "-f", "mpegts", "-i", "pipe:0", "-an", "-c:v", "copy", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-frag_duration", "500000", "-flush_packets", "1", "-f", "mp4", "pipe:1")
	cmd.Stdin = input
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		failure(w, 502, "回放封装不可用")
		return
	}
	if err := cmd.Start(); err != nil {
		failure(w, 502, "回放封装不可用")
		return
	}
	upstreamDone := make(chan struct{})
	defer func() { cancel(); input.Close(); cmd.Wait(); <-upstreamDone }()
	go func() {
		defer close(upstreamDone)
		track := &mpegts.Track{Codec: &codecs.H264{}}
		writer := &mpegts.Writer{W: output, Tracks: []*mpegts.Track{track}}
		if err := writer.Initialize(); err != nil {
			_ = output.CloseWithError(err)
			return
		}
		_ = output.CloseWithError(rtspReplay(ctx, source, func(pts, dts int64, au [][]byte) {
			_ = writer.WriteH264(track, pts, dts, au)
		}))
	}()
	initial := make([]byte, 4096)
	n, err := stdout.Read(initial)
	if n == 0 || err != nil && err != io.EOF {
		failure(w, 502, "录像机未返回可播放的 H.264 录像")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("X-Accel-Buffering", "no")
	ctl := http.NewResponseController(w)
	stopWrite := context.AfterFunc(ctx, func() { _ = ctl.SetWriteDeadline(time.Now()) })
	defer stopWrite()
	if ctx.Err() != nil {
		return
	}
	w.Write(initial[:n])
	ctl.Flush()
	for {
		n, err := stdout.Read(initial)
		if n > 0 && ctx.Err() == nil {
			if _, writeErr := w.Write(initial[:n]); writeErr != nil {
				return
			}
			ctl.Flush()
		}
		if err != nil || ctx.Err() != nil {
			return
		}
	}
}

func rtspReplay(ctx context.Context, source replaySource, frameOut func(int64, int64, [][]byte)) error {
	target, err := base.ParseURL(source.raw)
	if err != nil {
		return err
	}
	transport := gortsplib.ProtocolTCP
	c := gortsplib.Client{Scheme: target.Scheme, Host: target.Host, Protocol: &transport, ReadTimeout: 8 * time.Second, WriteTimeout: 5 * time.Second,
		OnRequest: func(req *base.Request) {
			if source.clock != "" && req.Method == base.Play {
				req.Header["Require"] = base.HeaderValue{"onvif-replay"}
				req.Header["Range"] = base.HeaderValue{source.clock}
			}
		},
	}
	dialer := &net.Dialer{}
	c.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil || (source.strict && host != target.Hostname()) {
			return nil, errUnsupported
		}
		conn, err := dialer.DialContext(dialCtx, network, address)
		if err == nil {
			context.AfterFunc(ctx, func() { _ = conn.Close() })
		}
		return conn, err
	}
	if err := c.Start(); err != nil {
		return err
	}
	defer c.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-done:
		}
	}()
	desc, _, err := c.Describe(target)
	if err != nil {
		return err
	}
	if source.strict && (desc.BaseURL == nil || desc.BaseURL.Hostname() != target.Hostname()) {
		return errUnsupported
	}
	var codec *format.H264
	media := desc.FindFormat(&codec)
	if media == nil {
		return errUnsupported
	}
	decoder, err := codec.CreateDecoder()
	if err != nil {
		return err
	}
	if _, err = c.Setup(desc.BaseURL, media, 0, 0); err != nil {
		return err
	}
	extract := h264.NewDTSExtractor()
	sps, pps := codec.SPS, codec.PPS
	started := false
	var origin time.Time
	var firstDTS int64
	c.OnPacketRTP(media, codec, func(packet *rtp.Packet) {
		pts, ok := c.PacketPTS(media, packet)
		if !ok {
			return
		}
		au, err := decoder.Decode(packet)
		if err != nil {
			return
		}
		var frame bool
		for _, nalu := range au {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1f {
			case 7:
				sps = nalu
			case 8:
				pps = nalu
			case 1, 5:
				frame = true
			}
		}
		if !frame {
			return
		}
		if h264.IsRandomAccess(au) && len(sps) > 0 && len(pps) > 0 {
			au = append([][]byte{sps, pps}, au...)
			started = true
		}
		if !started {
			return
		}
		dts, err := extract.Extract(au, pts)
		if err != nil || ctx.Err() != nil {
			return
		}
		if origin.IsZero() {
			origin, firstDTS = time.Now(), dts
		}
		wait := time.Until(origin.Add(time.Duration(dts-firstDTS) * time.Second / 90000))
		if wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() == nil {
			frameOut(pts, dts, au)
		}
	})
	if _, err := c.Play(nil); err != nil {
		return err
	}
	return c.Wait()
}
