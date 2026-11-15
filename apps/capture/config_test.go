package capture

import (
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserWriteAndNumericBoundaries(t *testing.T) {
	d, _ := NewDevice(filepath.Join(t.TempDir(), "config.json"))
	c := DefaultConfig()
	c.Password = "fixture-only"
	if err := d.Save(c); err != nil {
		t.Fatal(err)
	}
	h := d.Handler(t.TempDir())
	for _, tc := range []struct {
		origin, header string
		want           int
	}{
		{"http://127.0.0.1:18491", "1", 200},
		{"http://127.0.0.1", "1", 200},
		{"http://127.0.0.1", "", 403},
		{"https://evil.example", "1", 403},
		{"null", "1", 403},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:18491/api/stop", strings.NewReader("{}"))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-ViewHub-Capture", tc.header)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("origin=%s header=%s status=%d", tc.origin, tc.header, w.Code)
		}
	}
	b, _ := json.Marshal(c)
	for _, port := range []string{"1e999", "65536", "1.5", "null", "-1"} {
		body := strings.Replace(string(b), `"port":15060`, `"port":`+port, 1)
		r := httptest.NewRequest("PUT", "http://127.0.0.1:18491/api/config", strings.NewReader(body))
		r.Header.Set("Origin", "http://127.0.0.1:18491")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("port %s accepted: %d", port, w.Code)
		}
	}
}

func TestConfigBoundaries(t *testing.T) {
	c := DefaultConfig()
	c.Password = "fixture-only"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.Port = 65536 }, func(c *Config) { c.FPS = 0 }, func(c *Config) { c.Width = 1279 }, func(c *Config) { c.MaxBitrate = 20001 }, func(c *Config) { c.DeviceID = "1" }, func(c *Config) { c.LocalIP = "0.0.0.0" }} {
		bad := c
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid config accepted")
		}
	}
}

func TestHEVCFramingAndCameraModes(t *testing.T) {
	input := []byte{0, 0, 0, 1, 70, 1, 0x50, 0, 0, 1, 38, 1, 0xaa, 0, 0, 0, 1, 70, 1, 0x50, 0, 0, 1, 2, 1, 0xbb}
	var frames [][]byte
	if err := readFramesCodec(strings.NewReader(string(input)), true, func(frame []byte) error { frames = append(frames, append([]byte{}, frame...)); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("HEVC access units=%d", len(frames))
	}
	ps := programStreamCodec(frames[0], 90000, 0x24)
	at := strings.Index(string(ps), string([]byte{0, 0, 1, 0xbc}))
	if ps[at+12] != 0x24 || crcMPEG(ps[at:at+20]) != 0 {
		t.Fatal("invalid HEVC program map")
	}
	modes := parseCameraModes("vcodec=mjpeg  min s=1280x720 fps=30 max s=1280x720 fps=30\nvcodec=mjpeg min s=1280x720 fps=30 max s=1280x720 fps=30\npixel_format=yuyv422 min s=1280x720 fps=10 max s=1280x720 fps=10")
	if len(modes) != 1 || modes[0].FPS != 30 {
		t.Fatalf("modes=%+v", modes)
	}
	c := DefaultConfig()
	c.Password = "fixture-only"
	c.Codec = "h265"
	if c.Validate() != nil {
		t.Fatal("HEVC rejected")
	}
	c.Codec = "av1"
	if c.Validate() == nil {
		t.Fatal("unsupported AV1 accepted")
	}
}
func TestHTTPConfigSecretAndOrigin(t *testing.T) {
	d, _ := NewDevice(filepath.Join(t.TempDir(), "config.json"))
	c := DefaultConfig()
	c.Password = "fixture-only"
	if err := d.Save(c); err != nil {
		t.Fatal(err)
	}
	h := d.Handler(t.TempDir())
	r := httptest.NewRequest("GET", "http://127.0.0.1:18491/api/config", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), c.Password) {
		t.Fatal("password exposed or config unavailable")
	}
	r = httptest.NewRequest("POST", "http://127.0.0.1:18491/api/start", strings.NewReader("{}"))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin start accepted")
	}
	r = httptest.NewRequest("GET", "http://evil.example/api/config", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("host rebinding accepted")
	}
}
func TestOfferValidation(t *testing.T) {
	good := "v=0\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nm=video 10000 RTP/AVP 96\r\na=rtpmap:96 PS/90000\r\ny=0200000001\r\n"
	o, e := parseOffer(good)
	if e != nil || o.SSRC != 200000001 || o.PT != 96 {
		t.Fatal(o, e)
	}
	for _, bad := range []string{strings.ReplaceAll(good, "RTP/AVP", "TCP/RTP/AVP"), strings.ReplaceAll(good, "10000", "0"), strings.ReplaceAll(good, "PS/90000", "H264/90000"), strings.ReplaceAll(good, "127.0.0.1", "0.0.0.0"), strings.ReplaceAll(good, "0200000001", "99999999999")} {
		if _, e := parseOffer(bad); e == nil {
			t.Fatal("bad SDP accepted")
		}
	}
}
func TestAnnexBAndProgramStream(t *testing.T) {
	input := []byte{0, 0, 0, 1, 9, 0xf0, 0, 0, 1, 0x65, 0xaa, 0, 0, 0, 1, 9, 0xf0, 0, 0, 1, 0x41, 0xbb}
	var frames [][]byte
	if e := readFrames(strings.NewReader(string(input)), func(b []byte) error { frames = append(frames, append([]byte{}, b...)); return nil }); e != nil {
		t.Fatal(e)
	}
	if len(frames) != 2 {
		t.Fatalf("frames=%d", len(frames))
	}
	ps := programStream(frames[0], 90000)
	if binary.BigEndian.Uint32(ps[:4]) != 0x1ba {
		t.Fatal("missing pack")
	}
	at := strings.Index(string(ps), string([]byte{0, 0, 1, 0xbc}))
	size := 6 + int(binary.BigEndian.Uint16(ps[at+4:at+6]))
	if crcMPEG(ps[at:at+size]) != 0 {
		t.Fatal("program map CRC")
	}
	if !strings.Contains(string(ps), string(frames[0])) {
		t.Fatal("lost frame")
	}
}
