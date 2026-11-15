package media

import (
	"encoding/binary"
	"sync"

	"github.com/bluenviron/gohlslib/v2/pkg/codecparams"
	"github.com/bluenviron/gohlslib/v2/pkg/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
)

type DecoderConfig struct {
	Codec       string `json:"codec"`
	CodedWidth  int    `json:"codedWidth"`
	CodedHeight int    `json:"codedHeight"`
}

type frameFanout struct {
	mu      sync.Mutex
	viewers map[chan []byte]bool
}

func decoderConfig(params videoParameters) DecoderConfig {
	var sps h265.SPS
	if sps.Unmarshal(params.sps) != nil {
		return DecoderConfig{}
	}
	return DecoderConfig{
		Codec:      codecparams.Marshal(&codecs.H265{VPS: params.vps, SPS: params.sps, PPS: params.pps}),
		CodedWidth: sps.Width(), CodedHeight: sps.Height(),
	}
}

// DecoderConfig is available after Ready is closed.
func (s *Stream) DecoderConfig() DecoderConfig {
	return s.decoderConfig
}

func (s *Stream) SubscribeFrames() (<-chan []byte, func()) {
	f := &s.frames
	queue := make(chan []byte, 8)
	f.mu.Lock()
	if f.viewers == nil {
		f.viewers = make(map[chan []byte]bool)
	}
	f.viewers[queue] = false
	f.mu.Unlock()
	return queue, func() {
		f.mu.Lock()
		delete(f.viewers, queue)
		f.mu.Unlock()
	}
}

func (f *frameFanout) publish(key bool, pts int64, nalus [][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var message []byte
	for queue, started := range f.viewers {
		if !started && !key {
			continue
		}
		if message == nil {
			message = frameMessage(key, pts, nalus)
		}
		select {
		case queue <- message:
			f.viewers[queue] = true
		default:
			// Avoid growing a queue of stale video when a client cannot keep up.
			delete(f.viewers, queue)
			close(queue)
		}
	}
}

func frameMessage(key bool, pts int64, nalus [][]byte) []byte {
	// Keyframe flag and signed presentation timestamp in microseconds,
	// followed by exactly one H.265 Annex B access unit.
	size := 9
	for _, nal := range nalus {
		size += 4 + len(nal)
	}
	message := make([]byte, 9, size)
	if key {
		message[0] = 1
	}
	binary.BigEndian.PutUint64(message[1:9], uint64(pts*1_000_000/90_000))
	for _, nal := range nalus {
		message = append(message, 0, 0, 0, 1)
		message = append(message, nal...)
	}
	return message
}
