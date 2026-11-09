package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"viewhub/internal/relay"
)

type channel struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Stream          string       `json:"stream"`
	Timezone        string       `json:"timezone"`
	Frigate         string       `json:"frigate"`
	ONVIF           *onvifConfig `json:"onvif"`
	Hikvision       *hikConfig   `json:"hikvision"`
	RecordingONVIF  *onvifConfig `json:"-"`
	ManagedStream   string       `json:"-"`
	ManagedRecorder bool         `json:"-"`
	RecorderID      string       `json:"-"`
	RecorderName    string       `json:"-"`
	DeviceKind      string       `json:"-"`
}

func (c channel) recordingONVIF() *onvifConfig {
	if c.RecordingONVIF != nil {
		return c.RecordingONVIF
	}
	return c.ONVIF
}
func (c channel) hasProfileG() bool {
	config := c.recordingONVIF()
	return config != nil && config.Recording != ""
}
func (c channel) usesFrigateVOD() bool { return c.Frigate != "" && !c.hasProfileG() }

type interval struct {
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	URI      string `json:"-"`
	ProfileG bool   `json:"-"`
}

type edge struct {
	channels     map[string]channel
	recorders    *recorderSettings
	rtc, frigate *url.URL
	secret       string
	replays      replayRegistry
	replayRTC    *webrtc.API
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var assetName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,120}\.(m3u8|ts|mp4|m4s)$`)
var upstreamClient = &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func newEdge(secret string) http.Handler {
	data, err := os.ReadFile(env("VIEWHUB_CHANNELS", "/config/channels.json"))
	if err != nil {
		log.Fatal(err)
	}
	var config struct{ Channels []channel }
	if json.Unmarshal(data, &config) != nil {
		log.Fatal("invalid channel configuration")
	}
	recorders, err := openRecorderSettings(os.Getenv("VIEWHUB_ONVIF_STATE"), os.Getenv("VIEWHUB_ONVIF_LAN"))
	if err != nil {
		log.Fatal("invalid ONVIF recorder settings")
	}
	e := &edge{secret: secret, channels: map[string]channel{}, recorders: recorders, rtc: address(env("VIEWHUB_GO2RTC", "http://go2rtc:1984"))}
	if port := os.Getenv("VIEWHUB_REPLAY_ICE_PORT"); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			log.Fatal("invalid replay ICE port")
		}
		udp, err := net.ListenUDP("udp4", &net.UDPAddr{Port: n})
		if err != nil {
			log.Fatal("cannot listen for replay ICE")
		}
		settings := webrtc.SettingEngine{}
		settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
		settings.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udp))
		if ip := os.Getenv("VIEWHUB_REPLAY_ICE_IP"); net.ParseIP(ip).To4() != nil {
			settings.SetNAT1To1IPs([]string{ip}, webrtc.ICECandidateTypeHost)
		}
		e.replayRTC = webrtc.NewAPI(webrtc.WithSettingEngine(settings))
	}
	if v := os.Getenv("VIEWHUB_FRIGATE"); v != "" {
		e.frigate = address(v)
	}
	for _, c := range config.Channels {
		if !identifier.MatchString(c.ID) || !identifier.MatchString(c.Stream) || c.Name == "" || (c.Frigate != "" && (!identifier.MatchString(c.Frigate) || e.frigate == nil)) {
			log.Fatal("invalid channel")
		}
		if _, exists := e.channels[c.ID]; exists {
			log.Fatal("duplicate channel")
		}
		if c.Timezone == "" {
			c.Timezone = "Asia/Shanghai"
		}
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			log.Fatal("invalid channel timezone")
		}
		e.channels[c.ID] = c
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /api/site/channels", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, e.channelList()) })
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		mux.HandleFunc(method+" /api/site/onvif", e.setupRecorder)
	}
	mux.HandleFunc("GET /api/site/{id}/recordings", e.recordings)
	mux.HandleFunc("GET /api/site/{id}/vod/{start}/{end}/{asset}", e.vod)
	mux.HandleFunc("GET /api/site/{id}/replay", e.replay)
	mux.HandleFunc("POST /api/site/{id}/replay-offer", e.replayOffer)
	mux.HandleFunc("POST /api/site/{id}/replay-control", e.replayControl)
	mux.HandleFunc("GET /api/site/{id}/rtc", e.live)
	mux.HandleFunc("GET /api/site/{id}/relay", e.live)
	mux.HandleFunc("POST /api/site/{id}/ptz", e.ptz)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && !tokenMatches(r, secret) {
			failure(w, 401, "现场服务鉴权失败")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (e *edge) day(c channel, date string) (time.Time, time.Time, error) {
	location, _ := time.LoadLocation(c.Timezone)
	start, err := time.ParseInLocation("2006-01-02", date, location)
	return start, start.AddDate(0, 0, 1), err
}

func (e *edge) getRecordings(ctx context.Context, c channel, start, end time.Time) ([]interval, string, error) {
	switch {
	case c.usesFrigateVOD():
		query := url.Values{"after": {strconv.FormatInt(start.Unix(), 10)}, "before": {strconv.FormatInt(end.Unix(), 10)}}
		u := e.frigate.JoinPath("api", c.Frigate, "recordings")
		u.RawQuery = query.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		res, err := upstreamClient.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, "", unavailable
		}
		var rows []struct {
			Start float64 `json:"start_time"`
			End   float64 `json:"end_time"`
		}
		if err := json.NewDecoder(res.Body).Decode(&rows); err != nil {
			return nil, "", err
		}
		result := []interval{}
		for _, row := range rows {
			result = append(result, interval{Start: max(start.Unix(), int64(row.Start)), End: min(end.Unix(), int64(row.End+0.5))})
		}
		return mergeIntervals(result), "frigate", nil
	case c.hasProfileG():
		result, err := c.recordingONVIF().recordings(ctx, start, end)
		if err == nil || !errors.Is(err, errUnsupported) || c.Hikvision == nil {
			return result, "onvif", err
		}
		rows, err := c.Hikvision.recordings(ctx, start, end)
		return rows, "isapi", err
	case c.Hikvision != nil:
		rows, err := c.Hikvision.recordings(ctx, start, end)
		return rows, "isapi", err
	default:
		return nil, "", errUnsupported
	}
}

func mergeIntervals(rows []interval) []interval {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Start < rows[j].Start })
	result := []interval{}
	for _, row := range rows {
		if row.End <= row.Start {
			continue
		}
		if n := len(result); n > 0 && row.Start <= result[n-1].End {
			result[n-1].End = max(result[n-1].End, row.End)
		} else {
			result = append(result, row)
		}
	}
	return result
}

func (e *edge) recordings(w http.ResponseWriter, r *http.Request) {
	c, ok := e.getChannel(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	start, end, err := e.day(c, r.URL.Query().Get("date"))
	if err != nil {
		failure(w, 400, "日期无效")
		return
	}
	rows, source, err := e.getRecordings(r.Context(), c, start, end)
	if errors.Is(err, errTrackRequired) {
		failure(w, 422, "录像含多条视频轨道，请在现场网关指定轨道编码")
		return
	}
	if errors.Is(err, errUnsupported) {
		failure(w, 422, "此通道未提供可用的录像检索服务")
		return
	}
	if err != nil {
		failure(w, 502, "未能读取录像目录")
		return
	}
	jsonResponse(w, map[string]any{"start": start.Unix(), "end": end.Unix(), "segments": mergeIntervals(append([]interval{}, rows...)), "mode": map[bool]string{true: "hls", false: "mp4"}[source == "frigate"], "source": source})
}

func (e *edge) vod(w http.ResponseWriter, r *http.Request) {
	c, ok := e.getChannel(r.PathValue("id"))
	start, err1 := strconv.ParseInt(r.PathValue("start"), 10, 64)
	end, err2 := strconv.ParseInt(r.PathValue("end"), 10, 64)
	asset := r.PathValue("asset")
	if !ok || !c.usesFrigateVOD() || err1 != nil || err2 != nil || start < 1 || end <= start || end-start > 3600 || !assetName.MatchString(asset) {
		http.NotFound(w, r)
		return
	}
	target := e.frigate.JoinPath("vod", c.Frigate, "start", strconv.FormatInt(start, 10), "end", strconv.FormatInt(end, 10), asset)
	proxy := &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(e.frigate)
		p.Out.URL.Path, p.Out.URL.RawPath, p.Out.URL.RawQuery = "/"+strings.TrimPrefix(target.Path, "/"), "", ""
		p.Out.Header.Del("Authorization")
	}, ModifyResponse: func(res *http.Response) error {
		res.Header.Del("Set-Cookie")
		res.Header.Set("Cache-Control", "no-store")
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			return unavailable
		}
		return nil
	}}
	proxy.ServeHTTP(w, r)
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
var wsDial = websocket.Dialer{HandshakeTimeout: 4 * time.Second}

type signal struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (e *edge) live(w http.ResponseWriter, r *http.Request) {
	c, ok := e.getChannel(r.PathValue("id"))
	if !ok || c.Stream == "" || !websocket.IsWebSocketUpgrade(r) {
		http.NotFound(w, r)
		return
	}
	if c.ManagedStream != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		err := e.configureLive(ctx, c.Stream, c.ManagedStream, true)
		cancel()
		if err != nil {
			failure(w, 502, "现场实时视频源不可用")
			return
		}
	}
	isRelay := strings.HasSuffix(r.URL.Path, "/relay")
	target := e.rtc.JoinPath("api", "ws")
	target.Scheme = map[string]string{"http": "ws", "https": "wss"}[target.Scheme]
	target.RawQuery = url.Values{"src": {c.Stream}}.Encode()
	media, res, err := wsDial.DialContext(r.Context(), target.String(), nil)
	if err != nil {
		if res != nil {
			res.Body.Close()
		}
		failure(w, 502, "实时视频源不可用")
		return
	}
	defer media.Close()
	viewer, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer viewer.Close()
	if isRelay {
		go func() { defer media.Close(); defer viewer.Close(); relay.WatchProgress(viewer, "edge", nil) }()
		media.SetWriteDeadline(time.Now().Add(4 * time.Second))
		if media.WriteJSON(signal{Type: "mse", Value: "avc1.640029,avc1.4d0029,avc1.420029,mp4a.40.2"}) != nil {
			return
		}
		media.SetReadLimit(8 << 20)
	} else {
		go func() {
			defer media.Close()
			viewer.SetReadLimit(64 << 10)
			viewer.SetReadDeadline(time.Now().Add(8 * time.Second))
			offer, candidates := false, 0
			for {
				var msg signal
				if viewer.ReadJSON(&msg) != nil {
					return
				}
				switch msg.Type {
				case "webrtc/offer":
					if offer || !strings.HasPrefix(msg.Value, "v=0") {
						return
					}
					offer = true
					viewer.SetReadDeadline(time.Time{})
				case "webrtc/candidate":
					candidates++
					if !offer || candidates > 64 || len(msg.Value) > 1024 || !strings.HasPrefix(msg.Value, "candidate:") {
						return
					}
				default:
					return
				}
				media.SetWriteDeadline(time.Now().Add(4 * time.Second))
				if media.WriteJSON(msg) != nil {
					return
				}
			}
		}()
		media.SetReadLimit(64 << 10)
	}
	for {
		if isRelay {
			media.SetReadDeadline(time.Now().Add(20 * time.Second))
		}
		kind, data, err := media.ReadMessage()
		if err != nil {
			return
		}
		viewer.SetWriteDeadline(time.Now().Add(8 * time.Second))
		if viewer.WriteMessage(kind, data) != nil {
			return
		}
	}
}

func (e *edge) ptz(w http.ResponseWriter, r *http.Request) {
	c, ok := e.getChannel(r.PathValue("id"))
	if !ok || c.ONVIF == nil || !c.ONVIF.PTZ {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		failure(w, 415, "需要 JSON 请求")
		return
	}
	var move struct {
		Action string
		Preset string
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&move) != nil {
		failure(w, 400, "无效云台命令")
		return
	}
	if err := c.ONVIF.move(r.Context(), move.Action, move.Preset); err != nil {
		failure(w, 502, "云台命令执行失败")
		return
	}
	w.WriteHeader(204)
}

var errUnsupported = fmt.Errorf("recording service unsupported")
