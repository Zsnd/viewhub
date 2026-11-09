package media

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"time"

	hls "github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gohlslib/v2/pkg/codecs"
	"github.com/pion/rtp"
	codecsRTP "github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

const pacingBuffer = 180 * time.Millisecond

type Frame struct {
	codec    byte
	NALUs    [][]byte
	PTS, DTS int64 // 90 kHz
	Arrival  time.Time
}

type Metrics struct {
	Packets         atomic.Uint64
	LastRTPUnixNano atomic.Int64
	Missing         atomic.Uint64
	Frames          atomic.Uint64
	InputGapMS      atomic.Int64
	OutputGapMS     atomic.Int64
}

type Stream struct {
	Track         *webrtc.TrackLocalStaticRTP
	Ready         chan struct{}
	Metrics       Metrics
	mux           *hls.Muxer
	hlsTrack      *hls.Track
	conn          *net.UDPConn
	decoderConfig DecoderConfig
	frames        frameFanout
}

func Open(first, last int) (*Stream, error) {
	var conn *net.UDPConn
	for port := first; port <= last; port++ {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err == nil {
			conn = c
			break
		}
	}
	if conn == nil {
		return nil, errors.New("no RTP port available")
	}
	_ = conn.SetReadBuffer(2 << 20)
	return &Stream{Ready: make(chan struct{}), conn: conn}, nil
}

func (s *Stream) initOutput(codec byte) error {
	rtcCodec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}
	ht := &hls.Track{Codec: &codecs.H264{}, ClockRate: 90000}
	if codec == videoHEVC {
		rtcCodec.MimeType = webrtc.MimeTypeH265
		rtcCodec.SDPFmtpLine = "profile-id=1;tier-flag=0;tx-mode=SRST"
		ht.Codec = &codecs.H265{}
	}
	track, err := webrtc.NewTrackLocalStaticRTP(rtcCodec, "video", "gb28181")
	if err != nil {
		return err
	}
	mux := &hls.Muxer{Tracks: []*hls.Track{ht}, Variant: hls.MuxerVariantLowLatency,
		SegmentMinDuration: time.Second, PartMinDuration: 200 * time.Millisecond, SegmentMaxSize: 8 << 20,
		OnEncodeError: func(err error) { slog.Debug("HLS encode", "error", err) },
	}
	if err = mux.Start(); err != nil {
		return err
	}
	s.Track, s.mux, s.hlsTrack = track, mux, ht
	slog.Info("media stream identified", "codec", rtcCodec.MimeType)
	return nil
}

func (s *Stream) Port() int                                       { return s.conn.LocalAddr().(*net.UDPAddr).Port }
func (s *Stream) ServeHLS(w http.ResponseWriter, r *http.Request) { s.mux.Handle(w, r) }

func (s *Stream) Run(ctx context.Context, source netip.Addr) {
	frames := make(chan Frame, 96)
	done := make(chan struct{})
	go func() { defer close(done); s.pace(ctx, frames) }()
	stop := context.AfterFunc(ctx, func() { _ = s.conn.Close() })
	defer stop()
	defer s.conn.Close()
	s.receive(ctx, source, frames)
	close(frames)
	<-done
	if s.mux != nil {
		s.mux.Close()
	}
}

func (s *Stream) receive(ctx context.Context, source netip.Addr, frames chan<- Frame) {
	var previousFrame time.Time
	d := psReader{frame: func(codec byte, nalus [][]byte, pts, dts int64) {
		now := time.Now()
		if !previousFrame.IsZero() {
			maxMetric(&s.Metrics.InputGapMS, now.Sub(previousFrame).Milliseconds())
		}
		previousFrame = now
		f := Frame{codec: codec, NALUs: nalus, PTS: pts, DTS: dts, Arrival: now}
		select {
		case frames <- f:
		case <-ctx.Done():
		default:
			s.Metrics.Missing.Add(1)
		}
	}}
	buffer := make([]byte, 65536)
	queue := make(map[uint16][]byte)
	var next uint16
	var ssrc uint32
	var initialized bool
	var gapSince time.Time
	var pendingSize int
	drain := func() {
		for {
			payload, ok := queue[next]
			if !ok {
				break
			}
			delete(queue, next)
			next++
			pendingSize -= len(payload)
			d.input(payload)
		}
		if len(queue) == 0 {
			gapSince = time.Time{}
		}
	}
	for ctx.Err() == nil {
		_ = s.conn.SetReadDeadline(time.Now().Add(35 * time.Millisecond))
		n, addr, err := s.conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				return
			}
		} else if addr.Addr().Unmap() == source.Unmap() {
			var packet rtp.Packet
			if packet.Unmarshal(buffer[:n]) != nil || packet.Version != 2 || len(packet.Payload) == 0 {
				continue
			}
			if !initialized {
				initialized = true
				next = packet.SequenceNumber
				ssrc = packet.SSRC
			}
			if packet.SSRC == ssrc {
				s.Metrics.LastRTPUnixNano.Store(time.Now().UnixNano())
			}
			distance := int16(packet.SequenceNumber - next)
			if packet.SSRC != ssrc || distance < 0 {
				continue
			}
			if distance > 512 {
				s.Metrics.Missing.Add(uint64(distance))
				clear(queue)
				pendingSize = 0
				next = packet.SequenceNumber
				d.reset()
			}
			if _, exists := queue[packet.SequenceNumber]; !exists {
				queue[packet.SequenceNumber] = bytes.Clone(packet.Payload)
				pendingSize += len(packet.Payload)
				s.Metrics.Packets.Add(1)
			}
			drain()
		}
		if len(queue) == 0 {
			continue
		}
		if gapSince.IsZero() {
			gapSince = time.Now()
		}
		if time.Since(gapSince) < 35*time.Millisecond && len(queue) < 32 && pendingSize < 512<<10 {
			continue
		}
		min := uint16(32767)
		for seq := range queue {
			min = minOf(min, seq-next)
		}
		s.Metrics.Missing.Add(uint64(min))
		next += min
		d.reset()
		drain()
		gapSince = time.Now()
	}
}

func minOf(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

func maxMetric(metric *atomic.Int64, value int64) {
	for previous := metric.Load(); value > previous; previous = metric.Load() {
		if metric.CompareAndSwap(previous, value) {
			break
		}
	}
}

func splitAnnexB(data []byte) [][]byte {
	var out [][]byte
	start := -1
	for i := 0; i+2 < len(data); i++ {
		if data[i] != 0 || data[i+1] != 0 {
			continue
		}
		size := 0
		if data[i+2] == 1 {
			size = 3
		} else if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
			size = 4
		}
		if size == 0 {
			continue
		}
		if start >= 0 && i > start {
			out = append(out, data[start:i])
		}
		start = i + size
		i = start - 1
	}
	if start >= 0 && start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

func (s *Stream) pace(ctx context.Context, frames <-chan Frame) {
	var origin time.Time
	var baseDTS, basePTS, lastDTS, lastOut int64
	var sent time.Time
	var sequence uint16
	var payloader rtp.Payloader = &codecsRTP.H264Payloader{}
	var parameters videoParameters
	var codec byte
	started := false
	for {
		var frame Frame
		select {
		case <-ctx.Done():
			return
		case f, ok := <-frames:
			if !ok {
				return
			}
			frame = f
		}
		if frame.codec != 0 && frame.codec != codec {
			if started {
				// A codec change requires a fresh SDP negotiation and HLS init segment.
				return
			}
			codec = frame.codec
			parameters = videoParameters{}
		}
		if codec == 0 {
			codec = sniffVideoCodec(frame.NALUs)
			if codec == 0 {
				continue
			}
		}
		key := parameters.inspect(codec, frame.NALUs)
		if !started {
			if !key || !parameters.complete(codec) {
				continue
			}
			if err := s.initOutput(codec); err != nil {
				slog.Error("initialize video output", "error", err)
				return
			}
			if codec == videoHEVC {
				payloader = &codecsRTP.H265Payloader{SkipAggregation: true}
				s.decoderConfig = decoderConfig(parameters)
			}
			started = true
			basePTS = frame.PTS
			baseDTS = frame.DTS
			origin = frame.Arrival.Add(pacingBuffer)
		}
		if frame.DTS < lastDTS-45000 || frame.DTS > lastDTS+450000 && !sent.IsZero() {
			baseDTS = frame.DTS - (lastOut + 3600)
			basePTS = frame.PTS - (lastOut + 3600)
		}
		elapsed := frame.DTS - baseDTS
		target := origin.Add(time.Duration(elapsed) * time.Second / 90000)
		if remaining := time.Until(target); remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		lastDTS, lastOut = frame.DTS, elapsed
		now := time.Now()
		if !sent.IsZero() {
			maxMetric(&s.Metrics.OutputGapMS, now.Sub(sent).Milliseconds())
		}
		sent = now
		if key && parameters.complete(codec) {
			frame.NALUs = parameters.prepend(codec, frame.NALUs)
		}
		pts := frame.PTS - basePTS
		if codec == videoHEVC {
			s.frames.publish(key, pts, frame.NALUs)
			_ = s.mux.WriteH265(s.hlsTrack, now, pts, frame.NALUs)
		} else {
			_ = s.mux.WriteH264(s.hlsTrack, now, pts, frame.NALUs)
		}
		var payloads [][]byte
		for _, nal := range frame.NALUs {
			if codec == videoAVC && nal[0]&31 == 9 || codec == videoHEVC && (len(nal) < 2 || nal[0]>>1&63 == 35) {
				continue
			}
			payloads = append(payloads, payloader.Payload(1150, nal)...)
		}
		for i, payload := range payloads {
			packet := rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: sequence, Timestamp: uint32(pts), Marker: i == len(payloads)-1}, Payload: payload}
			_ = s.Track.WriteRTP(&packet)
			sequence++
		}
		if s.Metrics.Frames.Add(1) == 1 {
			close(s.Ready)
		}
	}
}
