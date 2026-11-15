package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEncoderSelectionFallback(t *testing.T) {
	for _, tc := range []struct {
		available, want string
		calls           []string
	}{
		{"nvidia", "nvidia", []string{"nvidia"}},
		{"intel", "intel", []string{"nvidia", "intel"}},
		{"cpu", "cpu", []string{"nvidia", "intel", "cpu"}},
		{"none", "", []string{"nvidia", "intel", "cpu"}},
	} {
		t.Run(tc.available, func(t *testing.T) {
			c := DefaultConfig()
			var calls []string
			got, err := selectEncoder(context.Background(), c, func(_ context.Context, _ Config, engine string) error {
				calls = append(calls, engine)
				if engine != tc.available {
					return errors.New("fixture unavailable")
				}
				return nil
			}, func(string) {})
			if got != tc.want || (err != nil) != (tc.want == "") || !reflect.DeepEqual(calls, tc.calls) {
				t.Fatalf("got=%s err=%v calls=%v", got, err, calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := selectEncoder(ctx, DefaultConfig(), func(context.Context, Config, string) error { t.Fatal("probe after cancellation"); return nil }, func(string) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// Optional content-based verification: the cap must not become a target,
// simpler scenes consume less, and higher quality preserves more data.
func TestEncoderContentRateControl(t *testing.T) {
	ff := os.Getenv("VIEWHUB_TEST_FFMPEG")
	if ff == "" {
		t.Skip("set VIEWHUB_TEST_FFMPEG")
	}
	engines := strings.Split(os.Getenv("VIEWHUB_TEST_ENCODERS"), ",")
	if engines[0] == "" {
		engines = []string{"cpu"}
	}
	for _, engine := range engines {
		for _, codec := range []string{"h264", "h265"} {
			t.Run(engine+"-"+codec, func(t *testing.T) {
				encode := func(pattern, quality string, limit, frames int) int {
					c := DefaultConfig()
					c.Width = 1280
					c.Height = 720
					c.FPS = 30
					c.Codec = codec
					c.Quality = quality
					c.MaxBitrate = limit
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i", fmt.Sprintf("%s=s=1280x720:r=30", pattern), "-frames:v", fmt.Sprint(frames), "-an"}
					args = append(args, encoderArgs(c, engine)...)
					cmd := exec.CommandContext(ctx, ff, args...)
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					out, err := cmd.Output()
					if err != nil {
						t.Fatalf("%s %s %d: %v %s", engine, codec, limit, err, stderr.String())
					}
					return len(out)
				}
				still := encode("color", "high", 20000, 60)
				complex := encode("testsrc2", "high", 20000, 60)
				standard := encode("testsrc2", "standard", 20000, 60)
				ultra := encode("testsrc2", "ultra", 20000, 60)
				limited := encode("testsrc2", "ultra", 1000, 180)
				if still*2 >= complex {
					t.Fatalf("scene complexity did not affect output: still=%d complex=%d", still, complex)
				}
				if standard >= ultra {
					t.Fatalf("quality had no effect: standard=%d ultra=%d", standard, ultra)
				}
				// Six seconds plus one 0.5s VBV buffer and implementation
				// tolerance; a short initial GOP is not a steady-state cap test.
				if float64(limited)*8/6/1000 > 1200 {
					t.Fatalf("1Mbps VBV cap not respected: bytes=%d", limited)
				}
				t.Logf("bytes/2s still=%d complex=%d standard=%d ultra=%d; limited bytes/6s=%d", still, complex, standard, ultra, limited)
			})
		}
	}
}

func TestEncoderConfigCompatibility(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"codec":"h265","encoder":"cpu"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil || c.Codec != "h265" {
		t.Fatalf("old config: %+v %v", c, err)
	}
	c.Password = "fixture-only"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(c)
	if err != nil || strings.Contains(string(b), `"encoder"`) {
		t.Fatal("internal encoder selection leaked into config")
	}
}

// Optional real FFmpeg smoke test exercises AUD/parameter-set compatibility,
// not just argument spelling. No camera, credentials, or video files needed.
func TestEncoderFFmpeg(t *testing.T) {
	ff := os.Getenv("VIEWHUB_TEST_FFMPEG")
	if ff == "" {
		t.Skip("set VIEWHUB_TEST_FFMPEG for hardware smoke test")
	}
	engines := strings.Split(os.Getenv("VIEWHUB_TEST_ENCODERS"), ",")
	if engines[0] == "" {
		engines = []string{"cpu"}
	}
	for _, codec := range []string{"h264", "h265"} {
		for _, engine := range engines {
			t.Run(codec+"-"+engine, func(t *testing.T) {
				c := DefaultConfig()
				c.FFmpeg = ff
				c.Width = 1920
				c.Height = 1080
				c.FPS = 30
				c.MaxBitrate = 20000
				c.Codec = codec
				if err := probeEncoder(context.Background(), c, engine); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
