package media

import (
	"encoding/binary"
	"testing"
)

func videoPES(pts int64, nalu byte) []byte {
	out := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5,
		byte(0x21 | (pts>>29)&14), byte(pts >> 22), byte(1 | (pts>>14)&254), byte(pts >> 7), byte(1 | (pts<<1)&254),
		0, 0, 0, 1, nalu, 0x88}
	binary.BigEndian.PutUint16(out[4:6], uint16(len(out)-6))
	return out
}

func TestPSFragmentationAndClockWrap(t *testing.T) {
	var times []int64
	var kinds []byte
	reader := psReader{frame: func(_ byte, au [][]byte, pts, dts int64) {
		if pts != dts {
			t.Fatalf("unexpected DTS %d for PTS %d", dts, pts)
		}
		times = append(times, pts)
		for _, nal := range au {
			kinds = append(kinds, nal[0]&31)
		}
	}}
	payload := append(videoPES(clockWrap-3600, 0x67), videoPES(clockWrap-3600, 0x65)...)
	payload = append(payload, videoPES(0, 0x41)...)
	payload = append(payload, videoPES(3600, 0x41)...)
	for i := range payload {
		reader.input(payload[i : i+1])
	}
	if len(times) != 2 || times[0] != clockWrap-3600 || times[1] != clockWrap {
		t.Fatalf("timestamps: %v", times)
	}
	if len(kinds) != 3 || kinds[0] != 7 || kinds[1] != 5 || kinds[2] != 1 {
		t.Fatalf("access units: %v", kinds)
	}
}

func TestPSSelectsHEVCFromProgramMap(t *testing.T) {
	// The program map includes an audio stream and a descriptor on video e1.
	body := []byte{0xe1, 0xff, 0, 0, 0, 10,
		0x90, 0xc0, 0, 0, videoHEVC, 0xe1, 0, 2, 0x52, 0, 0, 0, 0, 0}
	data := append([]byte{0, 0, 1, 0xbc, 0, byte(len(body))}, body...)
	data = append(data, videoPES(0, 0x67)...) // different stream, ignored
	for i := range 2 {
		pes := videoPES(int64(i)*3600, 0x40)
		pes[3] = 0xe1
		data = append(data, pes...)
	}
	frames := 0
	reader := psReader{frame: func(codec byte, nalus [][]byte, pts, _ int64) {
		frames++
		if codec != videoHEVC || pts != 0 || len(nalus) != 1 || nalus[0][0] != 0x40 {
			t.Fatalf("unexpected HEVC access unit: codec=%x pts=%d nalus=%x", codec, pts, nalus)
		}
	}}
	for i := range data {
		reader.input(data[i : i+1])
	}
	if frames != 1 {
		t.Fatalf("got %d frames, want 1", frames)
	}
}

func TestHEVCKeyframeRequiresParameterSets(t *testing.T) {
	nalus := [][]byte{{0x40, 1, 9}, {0x42, 1, 8}, {0x44, 1, 7}, {0x2a, 1, 6}}
	var p videoParameters
	if sniffVideoCodec(nalus) != videoHEVC || p.inspect(videoHEVC, nalus[:1]) || p.complete(videoHEVC) {
		t.Fatal("VPS identifies HEVC, but is insufficient to start playback")
	}
	if !p.inspect(videoHEVC, nalus[1:]) || !p.complete(videoHEVC) {
		t.Fatal("CRA with VPS/SPS/PPS should start playback")
	}
	got := p.prepend(videoHEVC, nalus)
	if len(got) != 4 || got[0][0] != 0x40 || got[1][0] != 0x42 || got[2][0] != 0x44 || got[3][0] != 0x2a {
		t.Fatalf("unexpected HEVC access unit %x", got)
	}
}
