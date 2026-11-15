package media

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestPSG711AudioFragmentationAndCodecSelection(t *testing.T) {
	for _, codec := range []byte{0x90, 0x0f} {
		body := []byte{0xe1, 0xff, 0, 0, 0, 8, videoAVC, 0xe0, 0, 0, codec, 0xc0, 0, 0, 0, 0, 0, 0}
		input := append([]byte{0, 0, 1, 0xbc, 0, byte(len(body))}, body...)
		payload := bytes.Repeat([]byte{0xd5}, 160)
		pes := append([]byte{0, 0, 1, 0xc0, 0, 0, 0x80, 0x80, 5, 0x21, 0, 1, 0, 1}, payload...)
		binary.BigEndian.PutUint16(pes[4:6], uint16(len(pes)-6))
		input = append(input, pes...)
		calls := 0
		reader := psReader{audio: func(data []byte, pts int64) {
			calls++
			if !bytes.Equal(data, payload) || pts != 0 {
				t.Fatalf("audio payload/clock: %x %d", data, pts)
			}
		}}
		for _, b := range input {
			reader.input([]byte{b})
		}
		want := 0
		if codec == 0x90 {
			want = 1
		}
		if calls != want {
			t.Fatalf("codec=%x calls=%d want=%d", codec, calls, want)
		}
	}
}

func TestPSAudioRejectsTruncatedPES(t *testing.T) {
	reader := psReader{audio: func([]byte, int64) { t.Fatal("malformed audio reached decoder") }}
	for _, data := range [][]byte{{0, 0, 1, 0xc0}, {0, 0, 1, 0xc0, 0, 3, 0, 0, 0}, {0, 0, 1, 0xc0, 0, 3, 0x80, 0x80, 10}, {0, 0, 1, 0xc0, 0, 3, 0x80, 0x80, 0}} {
		reader.audioPES(data)
	}
}
