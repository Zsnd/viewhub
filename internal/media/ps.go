package media

import (
	"bytes"
	"encoding/binary"
)

const (
	maxPSBuffer = 2 << 20
	videoAVC    = 0x1b
	videoHEVC   = 0x24
	clockWrap   = int64(1) << 33
)

var prefix = []byte{0, 0, 1}

// psReader assembles MPEG-2 PS video PES packets and groups their payloads
// into access units by the 90 kHz presentation clock.
type psReader struct {
	buffer, access []byte
	pts, dts       int64
	clock          bool
	videoID, codec byte
	frame          func(byte, [][]byte, int64, int64)
}

func (p *psReader) reset() { p.buffer = nil; p.access = nil }

func (p *psReader) input(data []byte) {
	if len(p.buffer)+len(data) > maxPSBuffer {
		p.reset()
	}
	p.buffer = append(p.buffer, data...)
	for len(p.buffer) > 3 {
		start := bytes.Index(p.buffer, prefix)
		if start < 0 {
			p.buffer = p.buffer[len(p.buffer)-2:]
			return
		}
		p.buffer = p.buffer[start:]
		if len(p.buffer) < 4 {
			return
		}
		id := p.buffer[3]
		var size int
		switch {
		case id == 0xba:
			if len(p.buffer) < 14 {
				return
			}
			if p.buffer[4]&0xc0 != 0x40 {
				p.buffer = p.buffer[4:]
				continue
			}
			size = 14 + int(p.buffer[13]&7)
		case id == 0xb9:
			size = 4
		case id < 0xb9:
			p.buffer = p.buffer[4:]
			continue
		default:
			if len(p.buffer) < 6 {
				return
			}
			size = 6 + int(binary.BigEndian.Uint16(p.buffer[4:6]))
			if size == 6 && id >= 0xe0 && id <= 0xef {
				for at := 6; at+3 < len(p.buffer); at++ {
					if p.buffer[at] == 0 && p.buffer[at+1] == 0 && p.buffer[at+2] == 1 && p.buffer[at+3] >= 0xb9 {
						size = at
						break
					}
				}
				if size == 6 {
					return
				}
			}
		}
		if len(p.buffer) < size {
			return
		}
		switch {
		case id == 0xbc:
			p.programMap(p.buffer[6:size])
		case id >= 0xe0 && id <= 0xef && (p.videoID == 0 || p.videoID == id):
			p.videoID = id
			p.video(p.buffer[:size])
		}
		p.buffer = p.buffer[size:]
	}
}

func stamp(b []byte) int64 {
	return int64(b[0]&0x0e)<<29 | int64(b[1])<<22 | int64(b[2]&0xfe)<<14 | int64(b[3])<<7 | int64(b[4]>>1)
}
func unwrap(value, previous int64) int64 {
	candidate := (previous &^ (clockWrap - 1)) + value
	if candidate-previous > clockWrap/2 {
		candidate -= clockWrap
	}
	if previous-candidate > clockWrap/2 {
		candidate += clockWrap
	}
	return candidate
}

func (p *psReader) video(pes []byte) {
	if len(pes) < 9 || pes[6]&0xc0 != 0x80 {
		return
	}
	offset := 9 + int(pes[8])
	if offset > len(pes) {
		return
	}
	flags := pes[7] >> 6
	if flags >= 2 {
		if offset < 14 {
			return
		}
		pts := stamp(pes[9:14])
		if p.clock {
			pts = unwrap(pts, p.pts)
		}
		dts := pts
		if flags == 3 {
			if offset < 19 {
				return
			}
			dts = unwrap(stamp(pes[14:19]), pts)
		}
		if p.clock && pts != p.pts && len(p.access) > 0 {
			nalus := splitAnnexB(p.access)
			if len(nalus) > 0 {
				p.frame(p.codec, nalus, p.pts, p.dts)
			}
			p.access = nil
		}
		p.pts, p.dts, p.clock = pts, dts, true
	}
	if !p.clock {
		return
	}
	if len(p.access)+len(pes)-offset > maxPSBuffer {
		p.access = nil
		return
	}
	p.access = append(p.access, pes[offset:]...)
}

// The PS map describes the codec independently of the PES video stream ID.
func (p *psReader) programMap(body []byte) {
	if len(body) < 10 || body[0]&0x80 == 0 {
		return
	}
	body = body[:len(body)-4] // CRC
	offset := 4 + int(binary.BigEndian.Uint16(body[2:4]))
	if offset+2 > len(body) {
		return
	}
	end := offset + 2 + int(binary.BigEndian.Uint16(body[offset:offset+2]))
	if end > len(body) {
		return
	}
	for offset += 2; offset+4 <= end; {
		codec, id := body[offset], body[offset+1]
		next := offset + 4 + int(binary.BigEndian.Uint16(body[offset+2:offset+4]))
		if next > end {
			return
		}
		if (codec == videoAVC || codec == videoHEVC) && id >= 0xe0 && id <= 0xef && (p.videoID == 0 || p.videoID == id) {
			p.videoID, p.codec = id, codec
			return
		}
		offset = next
	}
}
