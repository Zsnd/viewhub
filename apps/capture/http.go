package capture

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func cameras(ctx context.Context, ffmpeg string) ([]string, error) {
	if runtime.GOOS == "linux" {
		return filepath.Glob("/dev/video*")
	}
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(timeout, ffmpeg, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	b, err := cmd.CombinedOutput()
	if timeout.Err() != nil {
		return nil, timeout.Err()
	}
	matches := regexp.MustCompile(`"([^"]+)" \(video\)`).FindAllStringSubmatch(string(b), -1)
	out := []string{}
	for _, m := range matches {
		out = append(out, m[1])
	}
	if len(out) == 0 && err != nil {
		return nil, err
	}
	return out, nil
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func (d *Device) Handler(web string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, d.Snapshot()) })
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, d.Config()) })
	mux.HandleFunc("GET /api/video-recommendation", func(w http.ResponseWriter, r *http.Request) {
		c := DefaultConfig()
		var err error
		for key, dst := range map[string]*int{"width": &c.Width, "height": &c.Height, "fps": &c.FPS} {
			*dst, err = strconv.Atoi(r.URL.Query().Get(key))
			if err != nil {
				http.Error(w, "视频参数必须为整数", 400)
				return
			}
		}
		c.Codec, c.Quality = r.URL.Query().Get("codec"), r.URL.Query().Get("quality")
		if err := c.validateVideo(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonResponse(w, map[string]int{"maxBitrate": recommendedMaxBitrate(c)})
	})
	mux.HandleFunc("PUT /api/config", func(w http.ResponseWriter, r *http.Request) {
		var c Config
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		dec.DisallowUnknownFields()
		if dec.Decode(&c) != nil {
			http.Error(w, "配置必须是有效 JSON，数值必须为有限整数", 400)
			return
		}
		if err := d.Save(c); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonResponse(w, d.Config())
	})
	mux.HandleFunc("GET /api/cameras", func(w http.ResponseWriter, r *http.Request) {
		names, err := cameras(r.Context(), d.Config().FFmpeg)
		if err != nil {
			http.Error(w, "无法枚举摄像头，请检查 FFmpeg 路径："+err.Error(), 500)
			return
		}
		jsonResponse(w, names)
	})
	mux.HandleFunc("GET /api/capabilities", func(w http.ResponseWriter, r *http.Request) {
		c := d.Config()
		if name := r.URL.Query().Get("camera"); name != "" {
			c.Camera = name
		}
		modes, err := cameraModes(r.Context(), c)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonResponse(w, map[string]any{"modes": modes, "codecs": []string{"h264", "h265"}})
	})
	mux.HandleFunc("POST /api/start", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Start(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonResponse(w, d.Snapshot())
	})
	mux.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) { d.Stop(); jsonResponse(w, d.Snapshot()) })
	mux.Handle("/", http.FileServer(http.Dir(web)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "仅允许本机访问", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			u, e := url.Parse(origin)
			// Some local browser clients omit Origin. A non-simple custom header
			// still requires a CORS preflight from any cross-origin website; this
			// server deliberately provides no CORS permission.
			allowed := origin == "" && r.Header.Get("X-ViewHub-Capture") == "1"
			if origin != "" && e == nil && u.Host == r.Host && u.Scheme == "http" {
				allowed = true
			}
			// The local Ego browser normalizes a loopback Origin without its port.
			// Only accept that form with the preflight-protected application header.
			if e == nil && u.Scheme == "http" && u.Hostname() == host && u.Port() == "" && r.Header.Get("X-ViewHub-Capture") == "1" {
				allowed = true
			}
			if !allowed || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "写操作要求本机同源 JSON 请求", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
