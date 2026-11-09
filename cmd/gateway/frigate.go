package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"viewhub/internal/frigaterelay"
	"viewhub/internal/gb"
)

const frigateDevice = "frigate"
const frigatePath = "/api/frigate"

var cameraName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var vodAsset = regexp.MustCompile(`^[a-zA-Z0-9_-]+\.(m3u8|m4s|mp4|ts)$`)

type frigateSource struct {
	origin      *url.URL
	relayOrigin *url.URL
	camera      string
	client      *http.Client
}

type frigateRecording struct {
	Start float64 `json:"start_time"`
	End   float64 `json:"end_time"`
}

func (f *frigateSource) get(ctx context.Context, path string, query url.Values, dst any) error {
	u := *f.origin
	u.Path, u.RawQuery = path, query.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return err
	}
	res, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &url.Error{Op: "Frigate", URL: path, Err: http.ErrNotSupported}
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(dst)
}

func (f *frigateSource) device(ctx context.Context) gb.Device {
	var stats struct {
		Cameras map[string]struct {
			FPS float64 `json:"camera_fps"`
		} `json:"cameras"`
	}
	online := f.get(ctx, "/api/stats", nil, &stats) == nil && stats.Cameras[f.camera].FPS > 0
	return gb.Device{
		ID: frigateDevice, Name: "Frigate 本地录像机", Kind: "frigate",
		Online: online, Channels: []gb.Channel{{ID: f.camera, Name: "海康枪机"}},
	}
}

func (a *application) frigateAllowed(r *http.Request) bool {
	return a.frigate != nil && who(r).account.SpaceID == gb.DefaultSpaceID
}

func (a *application) frigateRecords(w http.ResponseWriter, r *http.Request, start, end time.Time) {
	f := a.frigate
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var segments []frigateRecording
	query := url.Values{"after": {strconv.FormatInt(start.Unix(), 10)}, "before": {strconv.FormatInt(end.Unix(), 10)}}
	if err := f.get(ctx, "/api/"+f.camera+"/recordings", query, &segments); err != nil {
		problem(w, 502, "Frigate 未返回录像目录")
		return
	}
	jsonReply(w, 200, frigateWindows(segments, start, end))
}

func frigateWindows(segments []frigateRecording, start, end time.Time) []gb.Recording {
	slices.SortFunc(segments, func(a, b frigateRecording) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return 0
	})
	location := time.FixedZone("UTC+8", 8*3600)
	result := make([]gb.Recording, 0)
	appendWindow := func(from, to time.Time) {
		const layout = "2006-01-02T15:04:05"
		for from.Before(to) {
			next := min(to.Unix(), from.Truncate(time.Hour).Add(time.Hour).Unix())
			until := time.Unix(next, 0)
			result = append(result, gb.Recording{
				Name: "Frigate 录像", Start: from.In(location).Format(layout), End: until.In(location).Format(layout),
			})
			from = until
		}
	}
	var from, to time.Time
	for _, segment := range segments {
		s := time.Unix(max(int64(segment.Start), start.Unix()), 0)
		e := time.Unix(min(int64(segment.End+0.999), end.Unix()), 0)
		if !e.After(s) {
			continue
		}
		if from.IsZero() || s.After(to.Add(time.Second)) {
			appendWindow(from, to)
			from, to = s, e
		} else if e.After(to) {
			to = e
		}
	}
	appendWindow(from, to)
	return result
}

func (a *application) frigateVOD(w http.ResponseWriter, r *http.Request) {
	if !a.frigateAllowed(r) {
		http.NotFound(w, r)
		return
	}
	start, err1 := strconv.ParseInt(r.PathValue("start"), 10, 64)
	end, err2 := strconv.ParseInt(r.PathValue("end"), 10, 64)
	asset := r.PathValue("asset")
	if err1 != nil || err2 != nil || start < 1 || end <= start || end-start > 3600 || !vodAsset.MatchString(asset) {
		http.NotFound(w, r)
		return
	}
	path := "/vod/" + a.frigate.camera + "/start/" + r.PathValue("start") + "/end/" + r.PathValue("end") + "/" + asset
	proxy := httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(a.frigate.origin)
			req.Out.URL.Path, req.Out.URL.RawPath, req.Out.URL.RawQuery = path, "", ""
			req.Out.Header.Del("Cookie")
			req.Out.Header.Del("Authorization")
		},
		ModifyResponse: func(res *http.Response) error {
			res.Header.Del("Set-Cookie")
			res.Header.Del("Cache-Control")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			problem(w, 502, "Frigate 录像暂不可用")
		},
	}
	proxy.ServeHTTP(w, r)
}

func (a *application) frigateLive(w http.ResponseWriter, r *http.Request) {
	if !a.frigateAllowed(r) {
		http.NotFound(w, r)
		return
	}
	upgrader := websocket.Upgrader{}
	if !websocket.IsWebSocketUpgrade(r) {
		problem(w, 400, "需要 WebSocket 播放")
		return
	}
	f := a.frigate
	if f.relayOrigin == nil {
		problem(w, 503, "Frigate 中继暂不可用")
		return
	}
	u := *f.relayOrigin
	u.Scheme, u.Path = "ws", "/live"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	upstream, res, err := dialer.DialContext(r.Context(), u.String(), nil)
	if err != nil {
		if res != nil {
			res.Body.Close()
		}
		problem(w, 502, "Frigate 实时画面暂不可用")
		return
	}
	defer upstream.Close()
	browser, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer browser.Close()
	go func() {
		defer browser.Close()
		defer upstream.Close()
		frigaterelay.WatchProgress(browser, "cloud", func(progress frigaterelay.Progress) error {
			upstream.SetWriteDeadline(time.Now().Add(2 * time.Second))
			return upstream.WriteJSON(progress)
		})
	}()
	for {
		upstream.SetReadDeadline(time.Now().Add(20 * time.Second))
		kind, data, err := upstream.ReadMessage()
		if err != nil {
			return
		}
		browser.SetWriteDeadline(time.Now().Add(8 * time.Second))
		if browser.WriteMessage(kind, data) != nil {
			return
		}
	}
}

type frigateSignal struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func (a *application) frigateRTC(w http.ResponseWriter, r *http.Request) {
	if !a.frigateAllowed(r) {
		http.NotFound(w, r)
		return
	}
	upgrader := websocket.Upgrader{}
	browser, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer browser.Close()
	browser.SetReadLimit(64 << 10)

	f := a.frigate
	u := *f.origin
	u.Scheme, u.Path = "ws", "/live/webrtc/api/ws"
	u.RawQuery = url.Values{"src": {f.camera}}.Encode()
	dialer := websocket.Dialer{HandshakeTimeout: 4 * time.Second}
	upstream, res, err := dialer.DialContext(r.Context(), u.String(), nil)
	if err != nil {
		if res != nil {
			res.Body.Close()
		}
		return
	}
	defer upstream.Close()
	upstream.SetReadLimit(64 << 10)

	go func() {
		defer upstream.Close()
		offered, candidates := false, 0
		browser.SetReadDeadline(time.Now().Add(6 * time.Second))
		for {
			var msg frigateSignal
			if browser.ReadJSON(&msg) != nil {
				return
			}
			switch msg.Type {
			case "webrtc/offer":
				if offered || !strings.HasPrefix(msg.Value, "v=0\r\n") {
					return
				}
				offered = true
				browser.SetReadDeadline(time.Time{})
			case "webrtc/candidate":
				candidates++
				if !offered || candidates > 64 || len(msg.Value) > 1024 ||
					!strings.HasPrefix(msg.Value, "candidate:") {
					return
				}
			default:
				return
			}
			upstream.SetWriteDeadline(time.Now().Add(4 * time.Second))
			if upstream.WriteJSON(msg) != nil {
				return
			}
		}
	}()
	for {
		var msg frigateSignal
		if upstream.ReadJSON(&msg) != nil {
			return
		}
		switch msg.Type {
		case "webrtc/answer", "webrtc/candidate", "error":
			browser.SetWriteDeadline(time.Now().Add(4 * time.Second))
			if browser.WriteJSON(msg) != nil {
				return
			}
		}
	}
}

func frigateURL(origin string) (*url.URL, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || strings.TrimRight(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, &url.Error{Op: "validate", URL: "Frigate configuration", Err: http.ErrNotSupported}
	}
	return u, nil
}

func newFrigate(origin, camera string) (*frigateSource, error) {
	u, err := frigateURL(origin)
	if err != nil {
		return nil, err
	}
	if !cameraName.MatchString(camera) {
		return nil, &url.Error{Op: "validate", URL: "Frigate camera", Err: http.ErrNotSupported}
	}
	return &frigateSource{origin: u, camera: camera, client: &http.Client{
		Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}
