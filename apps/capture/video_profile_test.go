package capture

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoRecommendationsAndManualPersistence(t *testing.T) {
	c := DefaultConfig()
	c.Password = "fixture-only"
	c.Width = 1920
	c.Height = 1080
	if recommendedMaxBitrate(c) != 6000 {
		t.Fatal("unexpected 1080p30 H264 recommendation")
	}
	c.Codec = "h265"
	if recommendedMaxBitrate(c) != 4000 {
		t.Fatal("unexpected H265 recommendation")
	}
	c.Quality = "ultra"
	if recommendedMaxBitrate(c) <= 4000 {
		t.Fatal("higher quality should have a higher allowance")
	}
	c.FPS = 15
	if recommendedMaxBitrate(c) >= 6000 {
		t.Fatal("lower fps should reduce recommendation")
	}
	d, _ := NewDevice(filepath.Join(t.TempDir(), "config.json"))
	c.MaxBitrate = 1234
	c.BitrateMode = "manual"
	if err := d.Save(c); err != nil {
		t.Fatal(err)
	}
	c.FPS = 30
	c.Quality = "standard"
	if err := d.Save(c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(d.path)
	if err != nil || loaded.MaxBitrate != 1234 || loaded.BitrateMode != "manual" {
		t.Fatalf("manual cap overwritten: %+v %v", loaded, err)
	}
	c.BitrateMode = "auto"
	if err := d.Save(c); err != nil {
		t.Fatal(err)
	}
	if got := d.Config().MaxBitrate; got != recommendedMaxBitrate(c) {
		t.Fatalf("auto cap=%d", got)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.MaxBitrate = 0 }, func(c *Config) { c.MaxBitrate = 20001 }, func(c *Config) { c.Quality = "unknown" }, func(c *Config) { c.BitrateMode = "unknown" }} {
		bad := c
		mutate(&bad)
		if d.Save(bad) == nil {
			t.Fatal("invalid video profile accepted")
		}
	}
}

func TestLegacyTargetBitrateMigration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"width":1920,"height":1080,"fps":30,"codec":"h265","bitrate":12000,"password":"fixture-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil || c.MaxBitrate != 12000 || c.BitrateMode != "manual" || c.Quality != "high" {
		t.Fatalf("legacy migration failed: %+v %v", c, err)
	}
	if err := SaveConfig(p, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), `"bitrate"`) {
		t.Fatal("obsolete target bitrate persisted")
	}
}

func TestVideoRecommendationHTTP(t *testing.T) {
	d, _ := NewDevice(filepath.Join(t.TempDir(), "config.json"))
	h := d.Handler(t.TempDir())
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"width=1920&height=1080&fps=30&codec=h265&quality=high", 200},
		{"width=0&height=1080&fps=30&codec=h265&quality=high", 400},
		{"width=1920&height=1080&fps=1.5&codec=h265&quality=high", 400},
		{"width=1920&height=1080&fps=30&codec=av1&quality=high", 400},
		{"width=1920&height=1080&fps=30&codec=h265&quality=unknown", 400},
	} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:18491/api/video-recommendation?"+tc.query, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("query=%s status=%d", tc.query, w.Code)
		}
		if tc.status == 200 {
			var v map[string]int
			json.Unmarshal(w.Body.Bytes(), &v)
			if v["maxBitrate"] != 4000 {
				t.Fatal(v)
			}
		}
	}
}

func TestVideoProfileHTTPRejectsInvalidMaxBitrate(t *testing.T) {
	d, _ := NewDevice(filepath.Join(t.TempDir(), "config.json"))
	c := DefaultConfig()
	c.Password = "fixture-only"
	if err := d.Save(c); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	h := d.Handler(t.TempDir())
	for _, bad := range []string{"null", "0", "20001", "1.5", "1e999", "-1"} {
		fields["maxBitrate"] = json.RawMessage(bad)
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("PUT", "http://127.0.0.1:18491/api/config", strings.NewReader(string(body)))
		r.Header.Set("Origin", "http://127.0.0.1:18491")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-ViewHub-Capture", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("maxBitrate=%s status=%d", bad, w.Code)
		}
	}
}
